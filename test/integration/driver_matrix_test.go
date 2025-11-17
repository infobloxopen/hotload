package integration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/infobloxopen/hotload"
	_ "github.com/infobloxopen/hotload/strategy/fsnotify"
	pgxv4stdlib "github.com/jackc/pgx/v4/stdlib"
	pgxv5stdlib "github.com/jackc/pgx/v5/stdlib"
	"github.com/lib/pq"
	sqlite3 "github.com/mattn/go-sqlite3"
)

// DriverConfig defines a driver to test with hotload
type DriverConfig struct {
	Name          string        // Name to register with hotload
	Driver        driver.Driver // The actual driver instance
	NeedsPostgres bool          // Whether this driver requires PostgreSQL
	SkipReason    string        // Reason to skip if applicable
}

// GetDriverConfigs returns all drivers to test
func GetDriverConfigs(t *testing.T) []DriverConfig {
	configs := []DriverConfig{
		{
			Name:          "libpq",
			Driver:        &pq.Driver{},
			NeedsPostgres: true,
		},
		{
			Name:          "pgxv4",
			Driver:        pgxv4stdlib.GetDefaultDriver(),
			NeedsPostgres: true,
		},
		{
			Name:          "pgxv5",
			Driver:        pgxv5stdlib.GetDefaultDriver(),
			NeedsPostgres: true,
		},
		{
			Name:          "sqlite3",
			Driver:        &sqlite3.SQLiteDriver{},
			NeedsPostgres: false,
		},
	}

	return configs
}

// GetPostgresDriverConfigs returns only PostgreSQL-compatible drivers for complex tests
func GetPostgresDriverConfigs(t *testing.T) []DriverConfig {
	all := GetDriverConfigs(t)
	postgres := make([]DriverConfig, 0, len(all))
	for _, cfg := range all {
		if cfg.NeedsPostgres {
			postgres = append(postgres, cfg)
		}
	}
	return postgres
}

// TestDriverMatrix_BasicConnection tests basic connectivity across all drivers
func TestDriverMatrix_BasicConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	drivers := GetDriverConfigs(t)

	for _, driverCfg := range drivers {
		driverCfg := driverCfg // capture range variable
		t.Run(driverCfg.Name, func(t *testing.T) {
			if driverCfg.SkipReason != "" {
				t.Skip(driverCfg.SkipReason)
			}

			if driverCfg.NeedsPostgres && testDSN == "" {
				t.Skip("No PostgreSQL instance available")
			}

			// Register driver with hotload using unique name for this test
			driverName := fmt.Sprintf("%s-basic-%d", driverCfg.Name, time.Now().UnixNano())
			hotload.RegisterSQLDriver(driverName, driverCfg.Driver)

			// Create DSN and config file
			var dsn string
			var tmpFile *os.File
			var err error

			if driverCfg.NeedsPostgres {
				dsn = testDSN
				tmpFile, err = os.CreateTemp("", "hotload-matrix-*.txt")
			} else {
				// SQLite uses file path
				tmpDir := t.TempDir()
				dbPath := filepath.Join(tmpDir, "test.db")
				dsn = dbPath
				tmpFile, err = os.CreateTemp("", "hotload-matrix-*.txt")
			}

			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tmpFile.Name())

			if _, err := tmpFile.WriteString(dsn); err != nil {
				t.Fatalf("Failed to write DSN: %v", err)
			}
			tmpFile.Close()

			// Open via hotload
			hotloadDSN := fmt.Sprintf("fsnotify://%s/%s", driverName, tmpFile.Name())
			db, err := sql.Open("hotload", hotloadDSN)
			if err != nil {
				t.Fatalf("Failed to open database: %v", err)
			}
			defer db.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			// Test simple query
			var result int
			err = db.QueryRowContext(ctx, "SELECT 1").Scan(&result)
			if err != nil {
				t.Fatalf("Query failed: %v", err)
			}

			if result != 1 {
				t.Errorf("Expected 1, got %d", result)
			}

			t.Logf("✓ %s: Basic connection successful", driverCfg.Name)
		})
	}
}

// TestDriverMatrix_Ping tests Ping/PingContext across all drivers
func TestDriverMatrix_Ping(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	drivers := GetDriverConfigs(t)

	for _, driverCfg := range drivers {
		driverCfg := driverCfg // capture range variable
		t.Run(driverCfg.Name, func(t *testing.T) {
			if driverCfg.SkipReason != "" {
				t.Skip(driverCfg.SkipReason)
			}

			if driverCfg.NeedsPostgres && testDSN == "" {
				t.Skip("No PostgreSQL instance available")
			}

			// Register driver with unique name
			driverName := fmt.Sprintf("%s-ping-%d", driverCfg.Name, time.Now().UnixNano())
			hotload.RegisterSQLDriver(driverName, driverCfg.Driver)

			// Create DSN and config file
			var dsn string
			var tmpFile *os.File
			var err error

			if driverCfg.NeedsPostgres {
				dsn = testDSN
				tmpFile, err = os.CreateTemp("", "hotload-ping-*.txt")
			} else {
				tmpDir := t.TempDir()
				dbPath := filepath.Join(tmpDir, "test.db")
				dsn = dbPath
				tmpFile, err = os.CreateTemp("", "hotload-ping-*.txt")
			}

			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tmpFile.Name())

			if _, err := tmpFile.WriteString(dsn); err != nil {
				t.Fatalf("Failed to write DSN: %v", err)
			}
			tmpFile.Close()

			// Open via hotload
			hotloadDSN := fmt.Sprintf("fsnotify://%s/%s", driverName, tmpFile.Name())
			db, err := sql.Open("hotload", hotloadDSN)
			if err != nil {
				t.Fatalf("Failed to open database: %v", err)
			}
			defer db.Close()

			// Test Ping
			err = db.Ping()
			if err != nil {
				t.Fatalf("Ping() failed: %v", err)
			}
			t.Logf("✓ %s: Ping() succeeded", driverCfg.Name)

			// Test PingContext
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			err = db.PingContext(ctx)
			if err != nil {
				t.Fatalf("PingContext() failed: %v", err)
			}
			t.Logf("✓ %s: PingContext() succeeded", driverCfg.Name)

			// Verify with a query
			var result int
			err = db.QueryRowContext(ctx, "SELECT 1").Scan(&result)
			if err != nil {
				t.Fatalf("Query after ping failed: %v", err)
			}

			t.Logf("✓ %s: Ping reaches underlying driver", driverCfg.Name)
		})
	}
}

// TestDriverMatrix_PreparedStatements tests prepared statements across all drivers
func TestDriverMatrix_PreparedStatements(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	drivers := GetDriverConfigs(t)

	for _, driverCfg := range drivers {
		driverCfg := driverCfg // capture range variable
		t.Run(driverCfg.Name, func(t *testing.T) {
			if driverCfg.SkipReason != "" {
				t.Skip(driverCfg.SkipReason)
			}

			if driverCfg.NeedsPostgres && testDSN == "" {
				t.Skip("No PostgreSQL instance available")
			}

			// Register driver with unique name
			driverName := fmt.Sprintf("%s-prep-%d", driverCfg.Name, time.Now().UnixNano())
			hotload.RegisterSQLDriver(driverName, driverCfg.Driver)

			// Create DSN and config file
			var dsn string
			var tmpFile *os.File
			var err error

			if driverCfg.NeedsPostgres {
				dsn = testDSN
				tmpFile, err = os.CreateTemp("", "hotload-prep-*.txt")
			} else {
				tmpDir := t.TempDir()
				dbPath := filepath.Join(tmpDir, "test.db")
				dsn = dbPath
				tmpFile, err = os.CreateTemp("", "hotload-prep-*.txt")
			}

			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tmpFile.Name())

			if _, err := tmpFile.WriteString(dsn); err != nil {
				t.Fatalf("Failed to write DSN: %v", err)
			}
			tmpFile.Close()

			// Open via hotload
			hotloadDSN := fmt.Sprintf("fsnotify://%s/%s", driverName, tmpFile.Name())
			db, err := sql.Open("hotload", hotloadDSN)
			if err != nil {
				t.Fatalf("Failed to open database: %v", err)
			}
			defer db.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			// Prepare statement - use simple addition that works across all DBs
			var stmt *sql.Stmt
			if driverCfg.NeedsPostgres {
				stmt, err = db.PrepareContext(ctx, "SELECT $1::int + $2::int")
			} else {
				stmt, err = db.PrepareContext(ctx, "SELECT ? + ?")
			}
			if err != nil {
				t.Fatalf("Failed to prepare statement: %v", err)
			}
			defer stmt.Close()

			// Execute prepared statement
			var result int
			err = stmt.QueryRowContext(ctx, 10, 20).Scan(&result)
			if err != nil {
				t.Fatalf("Failed to execute prepared statement: %v", err)
			}

			if result != 30 {
				t.Errorf("Expected 30, got %d", result)
			}

			t.Logf("✓ %s: Prepared statements working", driverCfg.Name)
		})
	}
}

// TestDriverMatrix_Transactions tests transaction support across all drivers
func TestDriverMatrix_Transactions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	drivers := GetDriverConfigs(t)

	for _, driverCfg := range drivers {
		driverCfg := driverCfg // capture range variable
		t.Run(driverCfg.Name, func(t *testing.T) {
			if driverCfg.SkipReason != "" {
				t.Skip(driverCfg.SkipReason)
			}

			if driverCfg.NeedsPostgres && testDSN == "" {
				t.Skip("No PostgreSQL instance available")
			}

			// Register driver with unique name
			driverName := fmt.Sprintf("%s-tx-%d", driverCfg.Name, time.Now().UnixNano())
			hotload.RegisterSQLDriver(driverName, driverCfg.Driver)

			// Create DSN and config file
			var dsn string
			var tmpFile *os.File
			var err error

			if driverCfg.NeedsPostgres {
				dsn = testDSN
				tmpFile, err = os.CreateTemp("", "hotload-tx-*.txt")
			} else {
				tmpDir := t.TempDir()
				dbPath := filepath.Join(tmpDir, "test.db")
				dsn = dbPath
				tmpFile, err = os.CreateTemp("", "hotload-tx-*.txt")
			}

			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tmpFile.Name())

			if _, err := tmpFile.WriteString(dsn); err != nil {
				t.Fatalf("Failed to write DSN: %v", err)
			}
			tmpFile.Close()

			// Open via hotload
			hotloadDSN := fmt.Sprintf("fsnotify://%s/%s", driverName, tmpFile.Name())
			db, err := sql.Open("hotload", hotloadDSN)
			if err != nil {
				t.Fatalf("Failed to open database: %v", err)
			}
			defer db.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			// Create test table
			if driverCfg.NeedsPostgres {
				_, err = db.ExecContext(ctx, "CREATE TEMPORARY TABLE test_tx (id INT, value TEXT)")
			} else {
				_, err = db.ExecContext(ctx, "CREATE TABLE test_tx (id INTEGER, value TEXT)")
			}
			if err != nil {
				t.Fatalf("Failed to create table: %v", err)
			}

			// Test commit
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatalf("Failed to begin transaction: %v", err)
			}

			_, err = tx.ExecContext(ctx, "INSERT INTO test_tx (id, value) VALUES (1, 'committed')")
			if err != nil {
				tx.Rollback()
				t.Fatalf("Failed to insert: %v", err)
			}

			err = tx.Commit()
			if err != nil {
				t.Fatalf("Failed to commit: %v", err)
			}

			// Verify commit worked
			var value string
			err = db.QueryRowContext(ctx, "SELECT value FROM test_tx WHERE id = 1").Scan(&value)
			if err != nil {
				t.Fatalf("Failed to query after commit: %v", err)
			}
			if value != "committed" {
				t.Errorf("Expected 'committed', got '%s'", value)
			}

			// Test rollback
			tx, err = db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatalf("Failed to begin transaction: %v", err)
			}

			_, err = tx.ExecContext(ctx, "INSERT INTO test_tx (id, value) VALUES (2, 'rolled back')")
			if err != nil {
				tx.Rollback()
				t.Fatalf("Failed to insert: %v", err)
			}

			err = tx.Rollback()
			if err != nil {
				t.Fatalf("Failed to rollback: %v", err)
			}

			// Verify rollback worked
			var count int
			err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM test_tx WHERE id = 2").Scan(&count)
			if err != nil {
				t.Fatalf("Failed to query after rollback: %v", err)
			}
			if count != 0 {
				t.Errorf("Expected 0 rows after rollback, got %d", count)
			}

			t.Logf("✓ %s: Transactions working (commit and rollback)", driverCfg.Name)
		})
	}
}
