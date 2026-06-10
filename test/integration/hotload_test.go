package integration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestSwitchDatabase: changing the watched DSN reroutes new connections to
// another database, observable via current_database().
func TestSwitchDatabase(t *testing.T) {
	requirePostgres(t)

	f := newDsnFile(t, adminDsn("hotload_test"))
	db := openHotload(t, f, "")

	waitForQueryValue(t, db, "SELECT current_database()", "hotload_test", 5*time.Second)

	f.set(adminDsn("hotload_test1"))
	waitForQueryValue(t, db, "SELECT current_database()", "hotload_test1", 10*time.Second)
}

// TestPasswordRotationGraceful: rotating the database password and the DSN
// must transparently move the pool to fresh credentials; in-flight long
// operations on old connections complete undisturbed.
func TestPasswordRotationGraceful(t *testing.T) {
	requirePostgres(t)

	ops := map[string]func(db *sql.DB) error{
		"Exec": func(db *sql.DB) error {
			_, err := db.Exec("SELECT pg_sleep(2)")
			return err
		},
		"ExecContext": func(db *sql.DB) error {
			_, err := db.ExecContext(context.Background(), "SELECT pg_sleep(2)")
			return err
		},
		"Query": func(db *sql.DB) error {
			rows, err := db.Query("SELECT pg_sleep(2)")
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
			}
			return rows.Err()
		},
		"QueryContext": func(db *sql.DB) error {
			rows, err := db.QueryContext(context.Background(), "SELECT pg_sleep(2)")
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
			}
			return rows.Err()
		},
	}

	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			pass := rotatePassword(t)
			f := newDsnFile(t, userDsn(pass))
			db := openHotload(t, f, "")

			errCh := make(chan error, 1)
			go func() { errCh <- op(db) }()
			time.Sleep(300 * time.Millisecond) // let the long op reach the server

			newPass := rotatePassword(t)
			f.set(userDsn(newPass))

			select {
			case err := <-errCh:
				if err != nil {
					t.Fatalf("graceful mode must let the in-flight %s finish, got: %v", name, err)
				}
			case <-time.After(15 * time.Second):
				t.Fatal("long operation did not return")
			}

			// New connections must authenticate with the new password.
			waitForQueryValue(t, db, "SELECT current_user", testDbUser, 10*time.Second)
		})
	}
}

// TestPasswordRotationForceKill: with forceKill the in-flight operation is
// cut when the DSN changes, and the pool recovers on the new credentials.
func TestPasswordRotationForceKill(t *testing.T) {
	requirePostgres(t)

	pass := rotatePassword(t)
	f := newDsnFile(t, userDsn(pass))
	db := openHotload(t, f, "forceKill=true")

	errCh := make(chan error, 1)
	go func() {
		_, err := db.Exec("SELECT pg_sleep(30)")
		errCh <- err
	}()
	time.Sleep(300 * time.Millisecond)

	newPass := rotatePassword(t)
	f.set(userDsn(newPass))

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("forceKill mode must cancel the in-flight exec, got nil error")
		}
		t.Logf("in-flight exec canceled with: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("forceKill did not cancel the in-flight exec (30s sleep still running)")
	}

	waitForQueryValue(t, db, "SELECT current_user", testDbUser, 10*time.Second)
}

// TestLongTransactionAcrossChangeGraceful: a transaction spanning a config
// change completes on its original connection.
func TestLongTransactionAcrossChangeGraceful(t *testing.T) {
	requirePostgres(t)

	pass := rotatePassword(t)
	f := newDsnFile(t, userDsn(pass))
	db := openHotload(t, f, "")

	tag := fmt.Sprintf("txn-%d", time.Now().UnixNano())
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO test (cnum, csource) VALUES (1, $1)", tag); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("SELECT pg_sleep(1)"); err != nil {
		t.Fatal(err)
	}

	f.set(userDsn(rotatePassword(t)))
	time.Sleep(300 * time.Millisecond) // let the change land mid-transaction

	if _, err := tx.Exec("INSERT INTO test (cnum, csource) VALUES (2, $1)", tag); err != nil {
		t.Fatalf("exec after change in graceful txn: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit after change in graceful txn: %v", err)
	}

	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM test WHERE csource = $1", tag).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("committed rows = %d, want 2", n)
	}
}

// TestLongTransactionAcrossChangeForceKill: with forceKill a transaction
// holding a connection across a change is rolled back by the kill.
func TestLongTransactionAcrossChangeForceKill(t *testing.T) {
	requirePostgres(t)

	pass := rotatePassword(t)
	f := newDsnFile(t, userDsn(pass))
	db := openHotload(t, f, "forceKill=true")

	tag := fmt.Sprintf("txnfk-%d", time.Now().UnixNano())
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO test (cnum, csource) VALUES (1, $1)", tag); err != nil {
		t.Fatal(err)
	}

	f.set(userDsn(rotatePassword(t)))
	time.Sleep(500 * time.Millisecond) // change lands; connection is killed

	err = func() error {
		if _, err := tx.Exec("INSERT INTO test (cnum, csource) VALUES (2, $1)", tag); err != nil {
			return err
		}
		return tx.Commit()
	}()
	if err == nil {
		t.Fatal("transaction across a forceKill change should fail")
	}
	t.Logf("transaction failed as expected: %v", err)

	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM test WHERE csource = $1", tag).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("rows from killed transaction = %d, want 0 (rolled back)", n)
	}
}

// TestContextCancellation: caller-driven cancellation behaves identically
// in both modes and leaves the pool healthy.
func TestContextCancellation(t *testing.T) {
	requirePostgres(t)

	for _, params := range []string{"", "forceKill=true"} {
		name := "graceful"
		if params != "" {
			name = "forceKill"
		}
		t.Run(name, func(t *testing.T) {
			pass := rotatePassword(t)
			f := newDsnFile(t, userDsn(pass))
			db := openHotload(t, f, params)

			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			_, err := db.ExecContext(ctx, "SELECT pg_sleep(10)")
			if err == nil {
				t.Fatal("expected context cancellation error")
			}
			if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "cancel") {
				t.Fatalf("err = %v, want context cancellation", err)
			}

			// The pool must remain healthy after the cancellation.
			waitForQueryValue(t, db, "SELECT current_user", testDbUser, 10*time.Second)
		})
	}
}

// TestPreparedStatementAcrossChange: a prepared statement survives a
// graceful change — database/sql re-prepares it on a new connection.
func TestPreparedStatementAcrossChange(t *testing.T) {
	requirePostgres(t)

	f := newDsnFile(t, adminDsn("hotload_test"))
	db := openHotload(t, f, "")

	stmt, err := db.Prepare("SELECT current_database()")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()

	var got string
	if err := stmt.QueryRow().Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "hotload_test" {
		t.Fatalf("before change: %q, want hotload_test", got)
	}

	f.set(adminDsn("hotload_test1"))
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := stmt.QueryRow().Scan(&got); err != nil {
			t.Fatalf("prepared stmt after change: %v", err)
		}
		if got == "hotload_test1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("prepared stmt still routed to %q", got)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestMultipleSequentialLongExecs: a sequence of long operations, each
// overlapping one password rotation, all complete in graceful mode (each
// in-flight operation stays within its generation's grace period; only the
// generation before the previous one is killed).
func TestMultipleSequentialLongExecs(t *testing.T) {
	requirePostgres(t)

	pass := rotatePassword(t)
	f := newDsnFile(t, userDsn(pass))
	db := openHotload(t, f, "")
	db.SetMaxOpenConns(10)

	for i := 0; i < 5; i++ {
		errCh := make(chan error, 1)
		go func() {
			_, err := db.Exec("SELECT pg_sleep(1)")
			errCh <- err
		}()
		time.Sleep(150 * time.Millisecond) // let the exec reach the server

		f.set(userDsn(rotatePassword(t)))

		if err := <-errCh; err != nil {
			t.Fatalf("round %d: long exec failed in graceful mode: %v", i, err)
		}
		// Confirm the swap landed before the next round dials; transient
		// auth failures while the new DSN propagates are retried here.
		waitForQueryValue(t, db, "SELECT current_user", testDbUser, 10*time.Second)
	}
}
