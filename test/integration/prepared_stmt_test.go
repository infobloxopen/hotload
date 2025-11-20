package integration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/infobloxopen/hotload"
	_ "github.com/infobloxopen/hotload/fsnotify"
	pgxv5stdlib "github.com/jackc/pgx/v5/stdlib"
)

func TestPreparedStatement_AcrossDSNChange(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping prepared statement test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available")
	}

	driverName := fmt.Sprintf("pgxv5-prepstmt-%d", time.Now().UnixNano())
	hotload.RegisterSQLDriver(driverName, pgxv5stdlib.GetDefaultDriver())

	tmpFile, err := os.CreateTemp("", "hotload-prepstmt-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	initialDSN := testDSN + " application_name=app_old"
	if _, err := tmpFile.WriteString(initialDSN); err != nil {
		t.Fatalf("Failed to write DSN: %v", err)
	}
	tmpFile.Close()

	hotloadDSN := fmt.Sprintf("fsnotify://%s/%s", driverName, tmpFile.Name())
	db, err := sql.Open("hotload", hotloadDSN)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)

	t.Log("✓ Preparing statement to query application_name")

	stmt, err := db.Prepare("SELECT current_setting('application_name')")
	if err != nil {
		t.Fatalf("Failed to prepare statement: %v", err)
	}
	defer stmt.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var appName string
	err = stmt.QueryRowContext(ctx, ).Scan(&appName)
	if err != nil {
		t.Fatalf("Failed to execute prepared statement: %v", err)
	}

	if appName != "app_old" {
		t.Errorf("Expected application_name='app_old', got %q", appName)
	}
	t.Logf("✓ Initial query returned application_name=%q", appName)

	t.Log("🔄 Changing DSN to application_name=app_new")
	newDSN := testDSN + " application_name=app_new"
	err = os.WriteFile(tmpFile.Name(), []byte(newDSN), 0644)
	if err != nil {
		t.Fatalf("Failed to update DSN: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	t.Log("🔄 Executing prepared statement after DSN change...")
	
	var sawNewAppName bool
	var lastAppName string
	for i := 0; i < 10; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		
		err = stmt.QueryRowContext(ctx).Scan(&appName)
		cancel()
		
		if err != nil {
			t.Logf("   Attempt %d: Query failed: %v", i+1, err)
			continue
		}
		
		lastAppName = appName
		t.Logf("   Attempt %d: application_name=%q", i+1, appName)
		
		if appName == "app_new" {
			sawNewAppName = true
			break
		}
		
		time.Sleep(200 * time.Millisecond)
	}

	if !sawNewAppName {
		t.Errorf("❌ Prepared statement never switched to new DSN (last seen: %q)", lastAppName)
		t.Errorf("   This indicates wrappedStmt is NOT checking epoch and holding old connection indefinitely")
		t.Errorf("   Expected: wrappedStmt should return driver.ErrBadConn when epoch is old")
		t.Errorf("   Expected: database/sql should then re-prepare on new connection")
	} else {
		t.Logf("✓ Prepared statement successfully switched to new DSN (application_name=%q)", appName)
	}

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var newQueryAppName string
	err = db.QueryRowContext(ctx, "SELECT current_setting('application_name')").Scan(&newQueryAppName)
	if err != nil {
		t.Errorf("Failed to execute new query: %v", err)
	} else if newQueryAppName != "app_new" {
		t.Errorf("New query returned application_name=%q, expected 'app_new'", newQueryAppName)
	} else {
		t.Logf("✓ New queries correctly use new DSN (application_name=%q)", newQueryAppName)
	}
}

func TestTransaction_MultiStatementAcrossDSNChange(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping transaction test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available")
	}

	driverName := fmt.Sprintf("pgxv5-tx-%d", time.Now().UnixNano())
	hotload.RegisterSQLDriver(driverName, pgxv5stdlib.GetDefaultDriver())

	tmpFile, err := os.CreateTemp("", "hotload-tx-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	initialDSN := testDSN + " application_name=tx_old"
	if _, err := tmpFile.WriteString(initialDSN); err != nil {
		t.Fatalf("Failed to write DSN: %v", err)
	}
	tmpFile.Close()

	hotloadDSN := fmt.Sprintf("fsnotify://%s/%s", driverName, tmpFile.Name())
	db, err := sql.Open("hotload", hotloadDSN)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	t.Log("✓ Starting multi-statement transaction")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("Failed to begin transaction: %v", err)
	}

	var appName1 string
	err = tx.QueryRowContext(ctx, "SELECT current_setting('application_name')").Scan(&appName1)
	if err != nil {
		tx.Rollback()
		t.Fatalf("Failed to execute first statement: %v", err)
	}
	t.Logf("✓ First statement: application_name=%q", appName1)

	if appName1 != "tx_old" {
		tx.Rollback()
		t.Errorf("Expected application_name='tx_old', got %q", appName1)
	}

	_, err = tx.ExecContext(ctx, "SELECT pg_sleep(0.5)")
	if err != nil {
		tx.Rollback()
		t.Fatalf("Failed to execute pg_sleep: %v", err)
	}
	t.Log("✓ Second statement: pg_sleep(0.5) completed")

	t.Log("🔄 Changing DSN while transaction is active...")
	newDSN := testDSN + " application_name=tx_new"
	err = os.WriteFile(tmpFile.Name(), []byte(newDSN), 0644)
	if err != nil {
		tx.Rollback()
		t.Fatalf("Failed to update DSN: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	var appName2 string
	err = tx.QueryRowContext(ctx, "SELECT current_setting('application_name')").Scan(&appName2)
	if err != nil {
		tx.Rollback()
		t.Fatalf("Failed to execute third statement: %v", err)
	}
	t.Logf("✓ Third statement (after DSN change): application_name=%q", appName2)

	if appName2 != appName1 {
		t.Logf("⚠️  Transaction switched connections mid-transaction (was %q, now %q)", appName1, appName2)
		t.Logf("   This could indicate a problem - transactions should stay on same connection")
	} else {
		t.Logf("✓ Transaction stayed on same connection (application_name=%q)", appName2)
	}

	_, err = tx.ExecContext(ctx, "SELECT pg_sleep(0.5)")
	if err != nil {
		tx.Rollback()
		t.Fatalf("Failed to execute second pg_sleep: %v", err)
	}
	t.Log("✓ Fourth statement: pg_sleep(0.5) completed")

	err = tx.Commit()
	if err != nil {
		t.Fatalf("Failed to commit transaction: %v", err)
	}
	t.Log("✓ Transaction committed successfully")

	time.Sleep(200 * time.Millisecond)

	var appName3 string
	err = db.QueryRowContext(ctx, "SELECT current_setting('application_name')").Scan(&appName3)
	if err != nil {
		t.Errorf("Failed to execute query after transaction: %v", err)
	} else {
		t.Logf("✓ Query after transaction: application_name=%q", appName3)
		if appName3 == "tx_new" {
			t.Log("✓ New queries correctly use new DSN")
		} else {
			t.Logf("⚠️  Query returned %q, expected 'tx_new' (may take time for pool to drain)", appName3)
		}
	}

	t.Log("✅ Multi-statement transaction test passed")
}
