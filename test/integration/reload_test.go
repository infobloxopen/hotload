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

func TestReload_BasicDSNChange(t *testing.T) {
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
			driverName := fmt.Sprintf("%s-reload-%d", driverCfg.Name, time.Now().UnixNano())
			hotload.RegisterSQLDriver(driverName, driverCfg.Driver)

			tmpFile, err := os.CreateTemp("", "hotload-reload-*.txt")
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
				t.Fatalf("Initial query failed: %v", err)
			}

			t.Log("✓ Initial connection successful")

			newDSN := testDSN + " application_name=hotload_test_reload"
			err = os.WriteFile(tmpFile.Name(), []byte(newDSN), 0644)
			if err != nil {
				t.Fatalf("Failed to update DSN: %v", err)
			}

			t.Log("🔄 DSN updated, waiting for fsnotify to detect change...")
			time.Sleep(2 * time.Second)

			err = db.QueryRowContext(ctx, "SELECT 1").Scan(&result)
			if err != nil {
				t.Fatalf("Query after DSN change failed: %v", err)
			}

			t.Logf("✓ %s: DSN reload successful", driverCfg.Name)
		})
	}
}

func TestReload_LongRunningQuery(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available")
	}

	driverName := fmt.Sprintf("pgxv5-longquery-%d", time.Now().UnixNano())
	hotload.RegisterSQLDriver(driverName, pgxv5stdlib.GetDefaultDriver())

	tmpFile, err := os.CreateTemp("", "hotload-longquery-*.txt")
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

	db.SetMaxOpenConns(5)

	t.Log("✓ Starting long-running query test")

	queryDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		var result int
		err := db.QueryRowContext(ctx, "SELECT 1 FROM pg_sleep(3)").Scan(&result)
		queryDone <- err
	}()

	time.Sleep(500 * time.Millisecond)

	t.Log("🔄 Changing DSN while query is running...")
	newDSN := testDSN + " application_name=during_long_query"
	err = os.WriteFile(tmpFile.Name(), []byte(newDSN), 0644)
	if err != nil {
		t.Fatalf("Failed to update DSN: %v", err)
	}

	select {
	case err := <-queryDone:
		if err != nil {
			t.Logf("⚠️  Query failed during DSN change (acceptable): %v", err)
		} else {
			t.Log("✓ Long-running query completed successfully")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Timeout waiting for query to complete")
	}

	time.Sleep(2 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var result int
	err = db.QueryRowContext(ctx, "SELECT 1").Scan(&result)
	if err != nil {
		t.Fatalf("Query after DSN change failed: %v", err)
	}

	t.Log("✓ New queries working after DSN change")
	t.Log("✅ Long-running query test passed")
}

func TestReload_MultipleConsecutiveChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available")
	}

	driverName := fmt.Sprintf("pgxv5-multichange-%d", time.Now().UnixNano())
	hotload.RegisterSQLDriver(driverName, pgxv5stdlib.GetDefaultDriver())

	tmpFile, err := os.CreateTemp("", "hotload-multichange-*.txt")
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
		t.Fatalf("Initial query failed: %v", err)
	}

	t.Log("✓ Initial connection successful")

	const numChanges = 5
	for i := 0; i < numChanges; i++ {
		newDSN := fmt.Sprintf("%s application_name=change_%d", testDSN, i)
		err = os.WriteFile(tmpFile.Name(), []byte(newDSN), 0644)
		if err != nil {
			t.Fatalf("Failed to update DSN: %v", err)
		}

		t.Logf("🔄 DSN change #%d applied", i+1)
		time.Sleep(1 * time.Second)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = db.QueryRowContext(ctx, "SELECT 1").Scan(&result)
		cancel()

		if err != nil {
			t.Fatalf("Query after change #%d failed: %v", i+1, err)
		}
	}

	t.Logf("✓ All %d DSN changes handled successfully", numChanges)
	t.Log("✅ Multiple consecutive changes test passed")
}

func TestReload_ConnectionPoolBehavior(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available")
	}

	driverName := fmt.Sprintf("pgxv5-pool-%d", time.Now().UnixNano())
	hotload.RegisterSQLDriver(driverName, pgxv5stdlib.GetDefaultDriver())

	tmpFile, err := os.CreateTemp("", "hotload-pool-*.txt")
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

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)

	ctx := context.Background()

	for i := 0; i < 10; i++ {
		var result int
		err = db.QueryRowContext(ctx, "SELECT 1").Scan(&result)
		if err != nil {
			t.Fatalf("Query %d failed: %v", i, err)
		}
	}

	t.Log("✓ Connection pool established with 10 queries")

	stats := db.Stats()
	t.Logf("📊 Pool stats before DSN change: OpenConnections=%d, InUse=%d, Idle=%d",
		stats.OpenConnections, stats.InUse, stats.Idle)

	newDSN := testDSN + " application_name=pool_test"
	err = os.WriteFile(tmpFile.Name(), []byte(newDSN), 0644)
	if err != nil {
		t.Fatalf("Failed to update DSN: %v", err)
	}

	t.Log("🔄 DSN changed, waiting for epoch transition...")
	time.Sleep(2 * time.Second)

	for i := 0; i < 10; i++ {
		var result int
		err = db.QueryRowContext(ctx, "SELECT 1").Scan(&result)
		if err != nil {
			t.Fatalf("Query %d after DSN change failed: %v", i, err)
		}
	}

	stats = db.Stats()
	t.Logf("📊 Pool stats after DSN change: OpenConnections=%d, InUse=%d, Idle=%d",
		stats.OpenConnections, stats.InUse, stats.Idle)

	t.Log("✓ Connection pool working correctly after DSN change")
	t.Log("✅ Connection pool behavior test passed")
}
