package integration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/infobloxopen/hotload"
	_ "github.com/infobloxopen/hotload/fsnotify"
	pgxv4stdlib "github.com/jackc/pgx/v4/stdlib"
	pgxv5stdlib "github.com/jackc/pgx/v5/stdlib"
	"github.com/lib/pq"
)

type DriverConfig struct {
	Name       string
	Driver     driver.Driver
	SkipReason string
}

func GetDriverConfigs(t *testing.T) []DriverConfig {
	return []DriverConfig{
		{
			Name:   "libpq",
			Driver: &pq.Driver{},
		},
		{
			Name:   "pgxv4",
			Driver: pgxv4stdlib.GetDefaultDriver(),
		},
		{
			Name:   "pgxv5",
			Driver: pgxv5stdlib.GetDefaultDriver(),
		},
	}
}

func TestDriverMatrix_BasicConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available")
	}

	drivers := GetDriverConfigs(t)

	for _, driverCfg := range drivers {
		driverCfg := driverCfg
		t.Run(driverCfg.Name, func(t *testing.T) {
			if driverCfg.SkipReason != "" {
				t.Skip(driverCfg.SkipReason)
			}

			driverName := fmt.Sprintf("%s-basic-%d", driverCfg.Name, time.Now().UnixNano())
			hotload.RegisterSQLDriver(driverName, driverCfg.Driver)

			tmpFile, err := os.CreateTemp("", "hotload-matrix-*.txt")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tmpFile.Name())

			if _, err := tmpFile.WriteString(testDSN); err != nil {
				t.Fatalf("Failed to write DSN: %v", err)
			}
			tmpFile.Close()

			hotloadDSN := fmt.Sprintf("fsnotify://%s/%s", driverName, tmpFile.Name())
			db, err := sql.Open("hotload", hotloadDSN)
			if err != nil {
				t.Fatalf("Failed to open database: %v", err)
			}
			defer db.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

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

func TestDriverMatrix_Ping(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available")
	}

	drivers := GetDriverConfigs(t)

	for _, driverCfg := range drivers {
		driverCfg := driverCfg
		t.Run(driverCfg.Name, func(t *testing.T) {
			if driverCfg.SkipReason != "" {
				t.Skip(driverCfg.SkipReason)
			}

			driverName := fmt.Sprintf("%s-ping-%d", driverCfg.Name, time.Now().UnixNano())
			hotload.RegisterSQLDriver(driverName, driverCfg.Driver)

			tmpFile, err := os.CreateTemp("", "hotload-ping-*.txt")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tmpFile.Name())

			if _, err := tmpFile.WriteString(testDSN); err != nil {
				t.Fatalf("Failed to write DSN: %v", err)
			}
			tmpFile.Close()

			hotloadDSN := fmt.Sprintf("fsnotify://%s/%s", driverName, tmpFile.Name())
			db, err := sql.Open("hotload", hotloadDSN)
			if err != nil {
				t.Fatalf("Failed to open database: %v", err)
			}
			defer db.Close()

			err = db.Ping()
			if err != nil {
				t.Fatalf("Ping() failed: %v", err)
			}
			t.Logf("✓ %s: Ping() succeeded", driverCfg.Name)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			err = db.PingContext(ctx)
			if err != nil {
				t.Fatalf("PingContext() failed: %v", err)
			}
			t.Logf("✓ %s: PingContext() succeeded", driverCfg.Name)

			var result int
			err = db.QueryRowContext(ctx, "SELECT 1").Scan(&result)
			if err != nil {
				t.Fatalf("Query after ping failed: %v", err)
			}

			t.Logf("✓ %s: Ping reaches underlying driver", driverCfg.Name)
		})
	}
}

func TestDriverMatrix_PreparedStatements(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available")
	}

	drivers := GetDriverConfigs(t)

	for _, driverCfg := range drivers {
		driverCfg := driverCfg
		t.Run(driverCfg.Name, func(t *testing.T) {
			if driverCfg.SkipReason != "" {
				t.Skip(driverCfg.SkipReason)
			}

			driverName := fmt.Sprintf("%s-prep-%d", driverCfg.Name, time.Now().UnixNano())
			hotload.RegisterSQLDriver(driverName, driverCfg.Driver)

			tmpFile, err := os.CreateTemp("", "hotload-prep-*.txt")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tmpFile.Name())

			if _, err := tmpFile.WriteString(testDSN); err != nil {
				t.Fatalf("Failed to write DSN: %v", err)
			}
			tmpFile.Close()

			hotloadDSN := fmt.Sprintf("fsnotify://%s/%s", driverName, tmpFile.Name())
			db, err := sql.Open("hotload", hotloadDSN)
			if err != nil {
				t.Fatalf("Failed to open database: %v", err)
			}
			defer db.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			stmt, err := db.PrepareContext(ctx, "SELECT $1::int + $2::int")
			if err != nil {
				t.Fatalf("Failed to prepare statement: %v", err)
			}
			defer stmt.Close()

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

func TestDriverMatrix_Transactions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available")
	}

	drivers := GetDriverConfigs(t)

	for _, driverCfg := range drivers {
		driverCfg := driverCfg
		t.Run(driverCfg.Name, func(t *testing.T) {
			if driverCfg.SkipReason != "" {
				t.Skip(driverCfg.SkipReason)
			}

			driverName := fmt.Sprintf("%s-tx-%d", driverCfg.Name, time.Now().UnixNano())
			hotload.RegisterSQLDriver(driverName, driverCfg.Driver)

			tmpFile, err := os.CreateTemp("", "hotload-tx-*.txt")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tmpFile.Name())

			if _, err := tmpFile.WriteString(testDSN); err != nil {
				t.Fatalf("Failed to write DSN: %v", err)
			}
			tmpFile.Close()

			hotloadDSN := fmt.Sprintf("fsnotify://%s/%s", driverName, tmpFile.Name())
			db, err := sql.Open("hotload", hotloadDSN)
			if err != nil {
				t.Fatalf("Failed to open database: %v", err)
			}
			defer db.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			_, err = db.ExecContext(ctx, "CREATE TEMPORARY TABLE test_tx (id INT, value TEXT)")
			if err != nil {
				t.Fatalf("Failed to create table: %v", err)
			}

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

			var value string
			err = db.QueryRowContext(ctx, "SELECT value FROM test_tx WHERE id = 1").Scan(&value)
			if err != nil {
				t.Fatalf("Failed to query after commit: %v", err)
			}
			if value != "committed" {
				t.Errorf("Expected 'committed', got '%s'", value)
			}

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
