package hotload_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/infobloxopen/hotload/v3/internal/dbfake"
)

// methodsCalled returns the set of methods recorded by the fake driver.
func methodsCalled(drv *dbfake.Driver) map[string]int {
	out := map[string]int{}
	for _, c := range drv.Log.Calls() {
		out[c.Method]++
	}
	return out
}

func assertCalled(t *testing.T, drv *dbfake.Driver, method string) {
	t.Helper()
	if drv.Log.Count(method) == 0 {
		t.Errorf("expected %s to be called; calls: %v", method, methodsCalled(drv))
	}
}

func assertNotCalled(t *testing.T, drv *dbfake.Driver, method string) {
	t.Helper()
	if n := drv.Log.Count(method); n > 0 {
		t.Errorf("expected %s not to be called, got %d calls; calls: %v", method, n, methodsCalled(drv))
	}
}

// TestExecDispatch verifies that db.Exec reaches the underlying conn through
// the path matching its capabilities — and, crucially, that a conn with no
// exec support makes database/sql fall back to a prepared statement instead
// of bouncing off driver.ErrSkip as hotload v1 did.
func TestExecDispatch(t *testing.T) {
	t.Run("ExecerContext", func(t *testing.T) {
		fx := newFixture(t, fxCfg{caps: dbfake.CapExecerContext, rawCaps: true})
		if _, err := fx.db.Exec("UPDATE x"); err != nil {
			t.Fatal(err)
		}
		assertCalled(t, fx.drv, "ExecContext")
		assertNotCalled(t, fx.drv, "Exec")
		assertNotCalled(t, fx.drv, "Prepare")
	})

	t.Run("legacy Execer", func(t *testing.T) {
		fx := newFixture(t, fxCfg{caps: dbfake.CapExecer, rawCaps: true})
		if _, err := fx.db.Exec("UPDATE x"); err != nil {
			t.Fatal(err)
		}
		assertCalled(t, fx.drv, "Exec")
		assertNotCalled(t, fx.drv, "ExecContext")
		assertNotCalled(t, fx.drv, "Prepare")
	})

	t.Run("no exec support falls back to prepared stmt", func(t *testing.T) {
		fx := newFixture(t, fxCfg{caps: 0, rawCaps: true})
		if _, err := fx.db.Exec("UPDATE x"); err != nil {
			t.Fatal(err)
		}
		assertCalled(t, fx.drv, "Prepare")
		assertCalled(t, fx.drv, "StmtExec")
		assertNotCalled(t, fx.drv, "Exec")
		assertNotCalled(t, fx.drv, "ExecContext")
	})
}

// TestQueryDispatch is the query-side analog of TestExecDispatch.
func TestQueryDispatch(t *testing.T) {
	t.Run("QueryerContext", func(t *testing.T) {
		fx := newFixture(t, fxCfg{caps: dbfake.CapQueryerContext, rawCaps: true})
		if got := fx.queryDSN(); got != "dsn-1" {
			t.Fatalf("queryDSN = %q, want dsn-1", got)
		}
		assertCalled(t, fx.drv, "QueryContext")
		assertNotCalled(t, fx.drv, "Query")
		assertNotCalled(t, fx.drv, "Prepare")
	})

	t.Run("legacy Queryer", func(t *testing.T) {
		fx := newFixture(t, fxCfg{caps: dbfake.CapQueryer, rawCaps: true})
		if got := fx.queryDSN(); got != "dsn-1" {
			t.Fatalf("queryDSN = %q, want dsn-1", got)
		}
		assertCalled(t, fx.drv, "Query")
		assertNotCalled(t, fx.drv, "QueryContext")
		assertNotCalled(t, fx.drv, "Prepare")
	})

	t.Run("no query support falls back to prepared stmt", func(t *testing.T) {
		fx := newFixture(t, fxCfg{caps: 0, rawCaps: true})
		if got := fx.queryDSN(); got != "dsn-1" {
			t.Fatalf("queryDSN = %q, want dsn-1", got)
		}
		assertCalled(t, fx.drv, "Prepare")
		assertCalled(t, fx.drv, "StmtQuery")
		assertNotCalled(t, fx.drv, "Query")
		assertNotCalled(t, fx.drv, "QueryContext")
	})
}

// TestPrepareDispatch verifies PrepareContext synthesis: with underlying
// support it is delegated; without it hotload falls back to Prepare exactly
// like database/sql would.
func TestPrepareDispatch(t *testing.T) {
	t.Run("with ConnPrepareContext", func(t *testing.T) {
		fx := newFixture(t, fxCfg{caps: dbfake.CapsModern})
		stmt, err := fx.db.Prepare("SELECT dsn")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		assertCalled(t, fx.drv, "PrepareContext")
		assertNotCalled(t, fx.drv, "Prepare")
	})

	t.Run("without ConnPrepareContext", func(t *testing.T) {
		fx := newFixture(t, fxCfg{caps: 0, rawCaps: true})
		stmt, err := fx.db.Prepare("SELECT dsn")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		assertCalled(t, fx.drv, "Prepare")
		assertNotCalled(t, fx.drv, "PrepareContext")
	})
}

// TestStmtDispatch verifies prepared statements run through the stmt
// wrappers' context methods when supported and the legacy methods when not.
func TestStmtDispatch(t *testing.T) {
	t.Run("modern stmt", func(t *testing.T) {
		fx := newFixture(t, fxCfg{caps: dbfake.CapsModern})
		stmt, err := fx.db.Prepare("UPDATE x")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		if _, err := stmt.Exec(); err != nil {
			t.Fatal(err)
		}
		assertCalled(t, fx.drv, "StmtExecContext")
		assertNotCalled(t, fx.drv, "StmtExec")
	})

	t.Run("legacy stmt", func(t *testing.T) {
		fx := newFixture(t, fxCfg{caps: 0, rawCaps: true})
		stmt, err := fx.db.Prepare("UPDATE x")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		if _, err := stmt.Exec(); err != nil {
			t.Fatal(err)
		}
		assertCalled(t, fx.drv, "StmtExec")
		assertNotCalled(t, fx.drv, "StmtExecContext")
	})
}

// TestPingDispatch: with no underlying Pinger, db.Ping must succeed without
// reaching the driver (database/sql treats absence as "no health check").
func TestPingDispatch(t *testing.T) {
	t.Run("with Pinger", func(t *testing.T) {
		fx := newFixture(t, fxCfg{caps: dbfake.CapPinger, rawCaps: true})
		if err := fx.db.Ping(); err != nil {
			t.Fatal(err)
		}
		assertCalled(t, fx.drv, "Ping")
	})

	t.Run("without Pinger", func(t *testing.T) {
		fx := newFixture(t, fxCfg{caps: 0, rawCaps: true})
		if err := fx.db.Ping(); err != nil {
			t.Fatal(err)
		}
		assertNotCalled(t, fx.drv, "Ping")
	})
}

// TestBeginTxFallback verifies the ConnBeginTx synthesis: delegation when
// supported, the exact database/sql fallback errors when not.
func TestBeginTxFallback(t *testing.T) {
	ctx := context.Background()

	t.Run("with ConnBeginTx", func(t *testing.T) {
		fx := newFixture(t, fxCfg{caps: dbfake.CapsModern})
		tx, err := fx.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		assertCalled(t, fx.drv, "BeginTx")
	})

	t.Run("without ConnBeginTx default options", func(t *testing.T) {
		fx := newFixture(t, fxCfg{caps: 0, rawCaps: true})
		tx, err := fx.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		assertCalled(t, fx.drv, "Begin")
	})

	t.Run("without ConnBeginTx non-default isolation", func(t *testing.T) {
		fx := newFixture(t, fxCfg{caps: 0, rawCaps: true})
		_, err := fx.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err == nil || !strings.Contains(err.Error(), "non-default isolation level") {
			t.Fatalf("BeginTx error = %v, want non-default isolation level error", err)
		}
	})

	t.Run("without ConnBeginTx read-only", func(t *testing.T) {
		fx := newFixture(t, fxCfg{caps: 0, rawCaps: true})
		_, err := fx.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Fatalf("BeginTx error = %v, want read-only error", err)
		}
	})
}
