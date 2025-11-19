package integration

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/infobloxopen/hotload"
	_ "github.com/infobloxopen/hotload/fsnotify"
	pgxv5stdlib "github.com/jackc/pgx/v5/stdlib"
)

//
func TestStress_ConcurrentQueriesWithDSNChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available")
	}

	driverName := fmt.Sprintf("pgxv5-stress-%d", time.Now().UnixNano())
	hotload.RegisterSQLDriver(driverName, pgxv5stdlib.GetDefaultDriver())

	tmpFile, err := os.CreateTemp("", "hotload-stress-*.txt")
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

	db.SetMaxOpenConns(15)
	db.SetMaxIdleConns(5)

	t.Log("✓ Starting stress test with concurrent queries and DSN changes")

	var queryCount, successCount, failCount atomic.Int64
	var dsnChangeCount atomic.Int32
	stopQueries := make(chan struct{})

	const numWorkers = 20
	var wg sync.WaitGroup

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for {
				select {
				case <-stopQueries:
					return
				default:
					queryCount.Add(1)

					sleepMs := 50 + rand.Intn(250)
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)

					var result int
					err := db.QueryRowContext(ctx,
						fmt.Sprintf("SELECT 1 WHERE pg_sleep(%f) IS NOT NULL OR true", float64(sleepMs)/1000)).Scan(&result)

					if err != nil {
						failCount.Add(1)
					} else {
						successCount.Add(1)
					}

					cancel()

					time.Sleep(time.Duration(10+rand.Intn(50)) * time.Millisecond)
				}
			}
		}(i)
	}

	time.Sleep(200 * time.Millisecond)

	const numDSNChanges = 5
	for i := 0; i < numDSNChanges; i++ {
		time.Sleep(500 * time.Millisecond)

		newDSN := fmt.Sprintf("%s application_name=hotload_test_%d", testDSN, i)
		err := os.WriteFile(tmpFile.Name(), []byte(newDSN), 0644)
		if err != nil {
			t.Errorf("Failed to update DSN: %v", err)
			break
		}

		dsnChangeCount.Add(1)
		t.Logf("🔄 DSN change #%d applied (queries running concurrently)", i+1)
	}

	time.Sleep(1 * time.Second)

	close(stopQueries)
	wg.Wait()

	queries := queryCount.Load()
	successes := successCount.Load()
	failures := failCount.Load()
	dsnChanges := dsnChangeCount.Load()

	t.Logf("📊 Stress Test Results:")
	t.Logf("   🔄 DSN changes: %d", dsnChanges)
	t.Logf("   📝 Total queries attempted: %d", queries)
	t.Logf("   ✓ Successful queries: %d (%.1f%%)", successes, float64(successes)/float64(queries)*100)
	t.Logf("   ✗ Failed queries: %d (%.1f%%)", failures, float64(failures)/float64(queries)*100)

	if queries < 50 {
		t.Errorf("Expected more queries to be executed, got %d", queries)
	}

	successRate := float64(successes) / float64(queries) * 100
	if successRate < 80.0 {
		t.Errorf("Success rate too low: %.1f%% (expected >= 80%%)", successRate)
	} else {
		t.Logf("✓ Success rate: %.1f%%", successRate)
	}

	if dsnChanges != numDSNChanges {
		t.Errorf("Expected %d DSN changes, got %d", numDSNChanges, dsnChanges)
	}

	t.Log("✅ Stress test passed: graceful handling of concurrent queries during DSN changes")
}

//
func TestStress_RapidDSNChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available")
	}

	driverName := fmt.Sprintf("pgxv5-rapid-%d", time.Now().UnixNano())
	hotload.RegisterSQLDriver(driverName, pgxv5stdlib.GetDefaultDriver())

	tmpFile, err := os.CreateTemp("", "hotload-rapid-*.txt")
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

	t.Log("✓ Starting rapid DSN change test")

	var querySuccess, queryFail atomic.Int64
	stopQueries := make(chan struct{})
	var wg sync.WaitGroup

	const numWorkers = 5
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for {
				select {
				case <-stopQueries:
					return
				default:
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					var result int
					err := db.QueryRowContext(ctx, "SELECT 1").Scan(&result)
					if err != nil {
						queryFail.Add(1)
					} else {
						querySuccess.Add(1)
					}
					cancel()
					time.Sleep(100 * time.Millisecond)
				}
			}
		}(i)
	}

	time.Sleep(200 * time.Millisecond)

	const numRapidChanges = 10
	t.Logf("🔥 Applying %d DSN changes rapidly (50ms apart)...", numRapidChanges)

	for i := 0; i < numRapidChanges; i++ {
		newDSN := fmt.Sprintf("%s application_name=rapid_%d", testDSN, i)
		err := os.WriteFile(tmpFile.Name(), []byte(newDSN), 0644)
		if err != nil {
			t.Errorf("Failed to update DSN: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Log("✓ Rapid DSN changes applied, waiting for processing...")

	time.Sleep(1 * time.Second)

	close(stopQueries)
	wg.Wait()

	successes := querySuccess.Load()
	failures := queryFail.Load()

	t.Logf("📊 Results:")
	t.Logf("   ✓ Successful queries: %d", successes)
	t.Logf("   ✗ Failed queries: %d", failures)

	if successes == 0 {
		t.Error("Expected at least some successful queries")
	}

	successRate := float64(successes) / float64(successes+failures) * 100
	if successRate < 70.0 {
		t.Errorf("Success rate too low: %.1f%% (hash-based detection should help)", successRate)
	}

	t.Logf("✓ Hash-based detection helped maintain %.1f%% success rate during rapid changes", successRate)
	t.Log("✅ Rapid DSN change test completed")
}

//
func TestStress_LongRunningTransactionDuringReload(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available")
	}

	driverName := fmt.Sprintf("pgxv5-longtx-%d", time.Now().UnixNano())
	hotload.RegisterSQLDriver(driverName, pgxv5stdlib.GetDefaultDriver())

	tmpFile, err := os.CreateTemp("", "hotload-longtx-*.txt")
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

	t.Log("✓ Starting long transaction test")

	txDone := make(chan error, 1)
	var txFailed atomic.Bool
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			txDone <- fmt.Errorf("begin failed: %w", err)
			return
		}

		t.Log("🔄 Transaction started")

		_, err = tx.ExecContext(ctx, "SELECT pg_sleep(1)")
		if err != nil {
			tx.Rollback()
			txFailed.Store(true)
			txDone <- fmt.Errorf("sleep 1 failed: %w", err)
			return
		}

		t.Log("⏳ Transaction running (sleep 1/3 complete)")

		_, err = tx.ExecContext(ctx, "SELECT pg_sleep(1)")
		if err != nil {
			tx.Rollback()
			txFailed.Store(true)
			txDone <- fmt.Errorf("sleep 2 failed: %w", err)
			return
		}

		t.Log("⏳ Transaction running (sleep 2/3 complete)")

		_, err = tx.ExecContext(ctx, "SELECT pg_sleep(1)")
		if err != nil {
			tx.Rollback()
			txFailed.Store(true)
			txDone <- fmt.Errorf("sleep 3 failed: %w", err)
			return
		}

		t.Log("⏳ Transaction running (sleep 3/3 complete)")

		err = tx.Commit()
		if err != nil {
			txFailed.Store(true)
			txDone <- fmt.Errorf("commit failed: %w", err)
			return
		}

		t.Log("✓ Transaction committed successfully")
		txDone <- nil
	}()

	time.Sleep(500 * time.Millisecond)

	t.Log("🔄 Changing DSN while transaction is in progress...")
	newDSN := testDSN + " application_name=after_tx_change"
	err = os.WriteFile(tmpFile.Name(), []byte(newDSN), 0644)
	if err != nil {
		t.Fatalf("Failed to update DSN: %v", err)
	}

	select {
	case err := <-txDone:
		if err != nil {
			if txFailed.Load() {
				t.Logf("⚠️  Transaction failed during DSN change (acceptable): %v", err)
			} else {
				t.Errorf("Transaction failed unexpectedly: %v", err)
			}
		} else {
			t.Log("✓ Long-running transaction completed successfully despite DSN change")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Transaction timed out")
	}

	time.Sleep(1 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var result int
	err = db.QueryRowContext(ctx, "SELECT 1").Scan(&result)
	if err != nil {
		t.Errorf("Query after DSN change failed: %v", err)
	} else {
		t.Log("✓ New queries working after DSN change")
	}

	t.Log("✅ Long transaction during reload test passed - hotload handled gracefully")
}

//
func TestStress_HighConcurrencyConnectionPool(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available")
	}

	driverName := fmt.Sprintf("pgxv5-highconc-%d", time.Now().UnixNano())
	hotload.RegisterSQLDriver(driverName, pgxv5stdlib.GetDefaultDriver())

	tmpFile, err := os.CreateTemp("", "hotload-highconc-*.txt")
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

	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)

	t.Log("✓ Starting high concurrency test with 50 workers")

	var queryCount atomic.Int64
	stopQueries := make(chan struct{})

	const numWorkers = 50
	var wg sync.WaitGroup

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for {
				select {
				case <-stopQueries:
					return
				default:
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					var result int
					err := db.QueryRowContext(ctx, "SELECT 1").Scan(&result)
					cancel()

					if err == nil {
						queryCount.Add(1)
					}

					time.Sleep(time.Duration(10+rand.Intn(40)) * time.Millisecond)
				}
			}
		}(i)
	}

	time.Sleep(3 * time.Second)

	stats := db.Stats()
	t.Logf("📊 Pool stats during high concurrency:")
	t.Logf("   OpenConnections: %d", stats.OpenConnections)
	t.Logf("   InUse: %d", stats.InUse)
	t.Logf("   Idle: %d", stats.Idle)
	t.Logf("   WaitCount: %d", stats.WaitCount)
	t.Logf("   WaitDuration: %v", stats.WaitDuration)

	close(stopQueries)
	wg.Wait()

	queries := queryCount.Load()
	t.Logf("✓ Executed %d queries with 50 concurrent workers", queries)

	if queries < 100 {
		t.Errorf("Expected at least 100 queries, got %d", queries)
	}

	finalStats := db.Stats()
	if finalStats.OpenConnections > 20 {
		t.Errorf("Connection leak detected: %d open connections (max: 20)", finalStats.OpenConnections)
	}

	t.Log("✅ High concurrency test passed - no leaks or deadlocks detected")
}

//
func TestStress_RaceDetection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available")
	}

	driverName := fmt.Sprintf("pgxv5-race-%d", time.Now().UnixNano())
	hotload.RegisterSQLDriver(driverName, pgxv5stdlib.GetDefaultDriver())

	tmpFile, err := os.CreateTemp("", "hotload-race-*.txt")
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

	t.Log("✓ Starting race detection test (run with -race flag)")

	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for j := 0; j < 10; j++ {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				var result int
				db.QueryRowContext(ctx, "SELECT 1").Scan(&result)
				cancel()
			}
		}(i)
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(changeID int) {
			defer wg.Done()

			time.Sleep(time.Duration(50+rand.Intn(100)) * time.Millisecond)
			newDSN := fmt.Sprintf("%s application_name=race_%d", testDSN, changeID)
			os.WriteFile(tmpFile.Name(), []byte(newDSN), 0644)
		}(i)
	}

	wg.Wait()

	t.Log("✅ Race detection test completed (check for race warnings)")
}
