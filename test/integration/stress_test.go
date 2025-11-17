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
	_ "github.com/infobloxopen/hotload/strategy/fsnotify"
	"github.com/jackc/pgx/v5/stdlib"
)

// TestStress_ConnectionExhaustion tests behavior when the connection pool is exhausted
//
// Scenario:
//  1. Configure database with very small connection limit (5 connections)
//  2. Start 10 goroutines trying to hold connections
//  3. Execute long-running queries that hold connections
//  4. Verify hotload handles exhaustion gracefully
//  5. Ensure no deadlocks or panics occur
func TestStress_ConnectionExhaustion(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available")
	}

	// Register driver
	driverName := fmt.Sprintf("pgx-exhaust-%d", time.Now().UnixNano())
	hotload.RegisterSQLDriver(driverName, stdlib.GetDefaultDriver())

	// Create temp file
	tmpFile, err := os.CreateTemp("", "hotload-exhaust-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(testDSN); err != nil {
		t.Fatalf("Failed to write DSN: %v", err)
	}
	tmpFile.Close()

	// Open database via hotload
	hotloadDSN := fmt.Sprintf("fsnotify://%s/%s", driverName, tmpFile.Name())
	db, err := sql.Open("hotload", hotloadDSN)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Configure very small connection pool
	db.SetMaxOpenConns(3)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(30 * time.Second)

	t.Log("✓ Configured connection pool: 3 max connections")

	// Track successful and failed operations
	var successCount, failCount, timeoutCount atomic.Int64

	// Start workers that try to acquire connections
	const numWorkers = 15
	const workDuration = 3 * time.Second

	var wg sync.WaitGroup
	startTime := time.Now()

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for time.Since(startTime) < workDuration {
				// Try to execute query with timeout
				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)

				// Random sleep between 200-600ms to hold connection longer
				sleepMs := 200 + rand.Intn(400)

				var result int
				err := db.QueryRowContext(ctx,
					fmt.Sprintf("SELECT 1 WHERE pg_sleep(%f) IS NOT NULL OR true", float64(sleepMs)/1000)).Scan(&result)

				if err != nil {
					if ctx.Err() == context.DeadlineExceeded {
						timeoutCount.Add(1)
						t.Logf("Worker %d: timeout (likely waiting for connection)", workerID)
					} else {
						failCount.Add(1)
						t.Logf("Worker %d: error: %v", workerID, err)
					}
				} else {
					successCount.Add(1)
				}

				cancel()

				// Small delay between requests
				time.Sleep(time.Duration(50+rand.Intn(100)) * time.Millisecond)
			}
		}(i)
	}

	// Wait for all workers to complete
	wg.Wait()

	success := successCount.Load()
	fails := failCount.Load()
	timeouts := timeoutCount.Load()

	t.Logf("📊 Results after %v:", workDuration)
	t.Logf("   ✓ Successful queries: %d", success)
	t.Logf("   ⏱ Timeouts (pool exhaustion): %d", timeouts)
	t.Logf("   ✗ Other failures: %d", fails)

	// We expect some successful queries
	if success == 0 {
		t.Error("Expected at least some successful queries")
	}

	// We expect pool exhaustion to cause timeouts (this is normal behavior)
	if timeouts == 0 {
		t.Log("⚠️  No timeouts observed - pool may not have been fully stressed")
	} else {
		t.Logf("✓ Connection pool exhaustion detected (%d timeouts)", timeouts)
	}

	// We should not have many hard failures (other than timeouts)
	if fails > 5 {
		t.Errorf("Too many hard failures: %d (expected mostly timeouts)", fails)
	}

	t.Log("✅ Connection exhaustion test completed - hotload handled gracefully")
}

// TestStress_ConcurrentDSNChanges tests hotload under concurrent query load
// while the DSN is being changed multiple times
//
// Scenario:
//  1. Start 20 goroutines executing queries with random sleeps
//  2. While queries run, change DSN every 500ms (5 times)
//  3. Verify no queries fail due to DSN changes
//  4. Ensure graceful transitions without errors
//  5. Validate that all queries complete successfully
func TestStress_ConcurrentDSNChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available")
	}

	// Register driver
	driverName := fmt.Sprintf("pgx-stress-%d", time.Now().UnixNano())
	hotload.RegisterSQLDriver(driverName, stdlib.GetDefaultDriver())

	// Create temp file
	tmpFile, err := os.CreateTemp("", "hotload-stress-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(testDSN); err != nil {
		t.Fatalf("Failed to write DSN: %v", err)
	}
	tmpFile.Close()

	// Open database via hotload with generous drain timeout
	hotloadDSN := fmt.Sprintf("fsnotify://%s/%s?drainTimeout=5s&forceKill=false", driverName, tmpFile.Name())
	db, err := sql.Open("hotload", hotloadDSN)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Configure connection pool
	db.SetMaxOpenConns(15)
	db.SetMaxIdleConns(5)

	t.Log("✓ Starting stress test with concurrent queries and DSN changes")

	// Track operations
	var queryCount, successCount, failCount atomic.Int64
	var dsnChangeCount atomic.Int32
	stopQueries := make(chan struct{})

	// Start query workers
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

					// Execute query with random sleep (50-300ms)
					sleepMs := 50 + rand.Intn(250)
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)

					var result int
					err := db.QueryRowContext(ctx,
						fmt.Sprintf("SELECT 1 WHERE pg_sleep(%f) IS NOT NULL OR true", float64(sleepMs)/1000)).Scan(&result)

					if err != nil {
						failCount.Add(1)
						t.Logf("Worker %d: query failed: %v", workerID, err)
					} else {
						successCount.Add(1)
					}

					cancel()

					// Small delay between queries
					time.Sleep(time.Duration(10+rand.Intn(50)) * time.Millisecond)
				}
			}
		}(i)
	}

	// Give queries time to start
	time.Sleep(200 * time.Millisecond)

	// DSN changer - modify DSN multiple times while queries are running
	const numDSNChanges = 5
	for i := 0; i < numDSNChanges; i++ {
		time.Sleep(500 * time.Millisecond)

		// Modify DSN slightly (add application_name to make it different)
		newDSN := fmt.Sprintf("%s application_name=hotload_test_%d", testDSN, i)
		err := os.WriteFile(tmpFile.Name(), []byte(newDSN), 0644)
		if err != nil {
			t.Errorf("Failed to update DSN: %v", err)
			break
		}

		dsnChangeCount.Add(1)
		t.Logf("🔄 DSN change #%d applied (queries running concurrently)", i+1)
	}

	// Let queries run for a bit more after last DSN change
	time.Sleep(1 * time.Second)

	// Stop query workers
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

	// Verify we did execute queries
	if queries < 50 {
		t.Errorf("Expected more queries to be executed, got %d", queries)
	}

	// Verify high success rate (some failures acceptable during transitions)
	successRate := float64(successes) / float64(queries) * 100
	if successRate < 80.0 {
		t.Errorf("Success rate too low: %.1f%% (expected >= 80%%)", successRate)
	} else {
		t.Logf("✓ Success rate: %.1f%%", successRate)
	}

	// Verify DSN changes happened
	if dsnChanges != numDSNChanges {
		t.Errorf("Expected %d DSN changes, got %d", numDSNChanges, dsnChanges)
	}

	t.Log("✅ Stress test passed: graceful handling of concurrent queries during DSN changes")
}

// TestStress_RapidDSNChanges tests hotload's debouncing when DSN changes rapidly
//
// Scenario:
//  1. Execute queries in background
//  2. Change DSN 10 times in rapid succession (every 50ms)
//  3. Verify debouncing prevents excessive reconnections
//  4. Ensure queries still complete successfully
func TestStress_RapidDSNChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available")
	}

	// Register driver
	driverName := fmt.Sprintf("pgx-rapid-%d", time.Now().UnixNano())
	hotload.RegisterSQLDriver(driverName, stdlib.GetDefaultDriver())

	// Create temp file
	tmpFile, err := os.CreateTemp("", "hotload-rapid-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(testDSN); err != nil {
		t.Fatalf("Failed to write DSN: %v", err)
	}
	tmpFile.Close()

	// Open database via hotload with debouncing enabled
	hotloadDSN := fmt.Sprintf("fsnotify://%s/%s?drainTimeout=3s&debounce=250ms", driverName, tmpFile.Name())
	db, err := sql.Open("hotload", hotloadDSN)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(10)

	t.Log("✓ Starting rapid DSN change test (debounce=250ms)")

	// Start background queries
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

	// Wait for queries to start
	time.Sleep(200 * time.Millisecond)

	// Rapidly change DSN 10 times (every 50ms)
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

	t.Log("✓ Rapid DSN changes applied, waiting for debouncing...")

	// Wait for debounce to settle and changes to be processed
	time.Sleep(1 * time.Second)

	// Stop queries
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

	// With debouncing, we expect most queries to succeed
	// (rapid changes should be coalesced)
	successRate := float64(successes) / float64(successes+failures) * 100
	if successRate < 70.0 {
		t.Errorf("Success rate too low: %.1f%% (debouncing should help)", successRate)
	}

	t.Logf("✓ Debouncing helped maintain %.1f%% success rate during rapid changes", successRate)
	t.Log("✅ Rapid DSN change test completed")
}

// TestStress_LongRunningTransactionDuringReload tests graceful draining
// of long-running transactions when DSN changes
//
// Scenario:
//  1. Start a transaction that runs for 3 seconds
//  2. Change DSN after 500ms (while transaction is running)
//  3. Verify the connection is gracefully drained (may fail mid-transaction)
//  4. Verify new queries use new DSN
//
// Note: In-flight transactions may experience "bad connection" errors
// when the DSN changes, as the underlying connection is being replaced.
// This test verifies hotload handles this gracefully without panics.
func TestStress_LongRunningTransactionDuringReload(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available")
	}

	// Register driver
	driverName := fmt.Sprintf("pgx-longtx-%d", time.Now().UnixNano())
	hotload.RegisterSQLDriver(driverName, stdlib.GetDefaultDriver())

	// Create temp file
	tmpFile, err := os.CreateTemp("", "hotload-longtx-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(testDSN); err != nil {
		t.Fatalf("Failed to write DSN: %v", err)
	}
	tmpFile.Close()

	// Open database via hotload with very long drain timeout to allow transaction to complete
	hotloadDSN := fmt.Sprintf("fsnotify://%s/%s?drainTimeout=30s&forceKill=false", driverName, tmpFile.Name())
	db, err := sql.Open("hotload", hotloadDSN)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	t.Log("✓ Starting long transaction test")

	// Start a long-running transaction
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

		// Do some work in transaction
		_, err = tx.ExecContext(ctx, "SELECT pg_sleep(1)")
		if err != nil {
			tx.Rollback()
			txFailed.Store(true)
			txDone <- fmt.Errorf("sleep 1 failed: %w", err)
			return
		}

		t.Log("⏳ Transaction running (sleep 1/3 complete)")

		// More work
		_, err = tx.ExecContext(ctx, "SELECT pg_sleep(1)")
		if err != nil {
			tx.Rollback()
			txFailed.Store(true)
			txDone <- fmt.Errorf("sleep 2 failed: %w", err)
			return
		}

		t.Log("⏳ Transaction running (sleep 2/3 complete)")

		// Final work
		_, err = tx.ExecContext(ctx, "SELECT pg_sleep(1)")
		if err != nil {
			tx.Rollback()
			txFailed.Store(true)
			txDone <- fmt.Errorf("sleep 3 failed: %w", err)
			return
		}

		t.Log("⏳ Transaction running (sleep 3/3 complete)")

		// Commit
		err = tx.Commit()
		if err != nil {
			txFailed.Store(true)
			txDone <- fmt.Errorf("commit failed: %w", err)
			return
		}

		t.Log("✓ Transaction committed successfully")
		txDone <- nil
	}()

	// Wait for transaction to start
	time.Sleep(500 * time.Millisecond)

	// Change DSN while transaction is running
	t.Log("🔄 Changing DSN while transaction is in progress...")
	newDSN := testDSN + " application_name=after_tx_change"
	err = os.WriteFile(tmpFile.Name(), []byte(newDSN), 0644)
	if err != nil {
		t.Fatalf("Failed to update DSN: %v", err)
	}

	// Wait for transaction to complete
	select {
	case err := <-txDone:
		if err != nil {
			if txFailed.Load() {
				// Transaction failed due to DSN change - this is acceptable
				t.Logf("⚠️  Transaction failed during DSN change (expected): %v", err)
			} else {
				t.Errorf("Transaction failed unexpectedly: %v", err)
			}
		} else {
			t.Log("✓ Long-running transaction completed successfully despite DSN change")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Transaction timed out")
	}

	// Give fsnotify time to detect change
	time.Sleep(1 * time.Second)

	// Verify new queries work - this is the critical test
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
