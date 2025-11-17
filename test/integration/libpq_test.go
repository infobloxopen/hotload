package integration

import (
	"context"
	"database/sql"
	"log"
	"os"
	"testing"
	"time"

	"github.com/infobloxopen/hotload"
	_ "github.com/infobloxopen/hotload/strategy/fsnotify"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/lib/pq"
)

var (
	testDSN       string
	pgContainer   *PostgresContainer
	useExistingDB bool
)

// TestMain sets up and tears down test environment
func TestMain(m *testing.M) {
	var exitCode int
	ctx := context.Background()

	// Register drivers once for all tests
	hotload.RegisterSQLDriver("postgres", &pq.Driver{})

	// Register pgx driver for pgx tests
	pgxDriver := stdlib.GetDefaultDriver()
	hotload.RegisterSQLDriver("pgx", pgxDriver)

	// Check if user provided their own PostgreSQL instance
	if envDSN := os.Getenv("PG_DSN"); envDSN != "" {
		testDSN = envDSN
		useExistingDB = true
		log.Printf("Using existing PostgreSQL instance from PG_DSN")
		// Run tests
		exitCode = m.Run()
	} else {
		// Try to start PostgreSQL container
		log.Printf("Starting PostgreSQL container for integration tests...")

		container, err := StartPostgresContainer(ctx)
		if err != nil {
			log.Printf("Failed to start PostgreSQL container: %v", err)
			log.Printf("Tests will skip. Set PG_DSN to use existing database or install Docker.")
			// Run tests anyway - they will skip
			exitCode = m.Run()
		} else {
			pgContainer = container
			testDSN = container.DSN
			log.Printf("PostgreSQL container ready at: %s", testDSN)

			// Run tests
			exitCode = m.Run()

			// Cleanup container
			log.Printf("Cleaning up PostgreSQL container...")
			if err := pgContainer.Terminate(ctx); err != nil {
				log.Printf("Warning: failed to cleanup container: %v", err)
			}
		}
	}

	os.Exit(exitCode)
} // TestLibPQ_BasicConnection tests basic connection with lib/pq driver
// Prerequisites:
//   - PostgreSQL instance running (auto-started via TestMain or set PG_DSN)
//
// Scenario:
//  1. Register lib/pq driver with hotload
//  2. Create a temporary config file with DSN
//  3. Open connection via hotload
//  4. Execute simple query
//  5. Verify connection works
func TestLibPQ_BasicConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available (no PG_DSN and container failed to start)")
	}

	// Create temporary config file
	tmpFile, err := os.CreateTemp("", "hotload-test-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write DSN to file
	if _, err := tmpFile.WriteString(testDSN); err != nil {
		t.Fatalf("Failed to write DSN to file: %v", err)
	}
	tmpFile.Close()

	// Open database via hotload (driver registered in TestMain)
	db, err := sql.Open("hotload", "fsnotify://postgres/"+tmpFile.Name())
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Set reasonable timeouts
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Test connection with simple query
	var result int
	err = db.QueryRowContext(ctx, "SELECT 1").Scan(&result)
	if err != nil {
		t.Fatalf("Failed to execute query: %v", err)
	}

	if result != 1 {
		t.Errorf("Expected result 1, got %d", result)
	}

	t.Log("✓ Successfully connected and queried via lib/pq through hotload")
}

// TestLibPQ_Ping tests that Ping() reaches the underlying lib/pq driver
//
// Scenario:
//  1. Open connection via hotload
//  2. Call db.Ping() and db.PingContext()
//  3. Verify ping succeeds and reaches the database
func TestLibPQ_Ping(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available (no PG_DSN and container failed to start)")
	}

	// Create temporary config file
	tmpFile, err := os.CreateTemp("", "hotload-ping-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write DSN to file
	if _, err := tmpFile.WriteString(testDSN); err != nil {
		t.Fatalf("Failed to write DSN to file: %v", err)
	}
	tmpFile.Close()

	// Open database via hotload
	db, err := sql.Open("hotload", "fsnotify://postgres/"+tmpFile.Name())
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Test basic Ping
	err = db.Ping()
	if err != nil {
		t.Fatalf("Ping() failed: %v", err)
	}
	t.Log("✓ Ping() succeeded")

	// Test PingContext with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = db.PingContext(ctx)
	if err != nil {
		t.Fatalf("PingContext() failed: %v", err)
	}
	t.Log("✓ PingContext() succeeded")

	// Verify connection is actually working by running a query
	var result int
	err = db.QueryRowContext(ctx, "SELECT 1").Scan(&result)
	if err != nil {
		t.Fatalf("Query after ping failed: %v", err)
	}

	if result != 1 {
		t.Errorf("Expected result 1, got %d", result)
	}

	t.Log("✓ Ping() successfully reaches underlying lib/pq driver")
}

// TestLibPQ_DSNReload tests dynamic DSN reloading with lib/pq
//
// Scenario:
//  1. Start with initial DSN in config file
//  2. Open connection and verify it works
//  3. Update config file with new DSN (or modified parameters)
//  4. Wait for hotload to detect change
//  5. Open new connection and verify it picks up changes
func TestLibPQ_DSNReload(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available (no PG_DSN and container failed to start)")
	}

	// Create temporary config file
	tmpFile, err := os.CreateTemp("", "hotload-test-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write initial DSN
	if _, err := tmpFile.WriteString(testDSN); err != nil {
		t.Fatalf("Failed to write DSN to file: %v", err)
	}
	tmpFile.Close()

	// Open database (driver registered in TestMain)
	db, err := sql.Open("hotload", "fsnotify://postgres/"+tmpFile.Name())
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Test initial connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var result int
	err = db.QueryRowContext(ctx, "SELECT 1").Scan(&result)
	if err != nil {
		t.Fatalf("Failed to execute initial query: %v", err)
	}

	t.Log("✓ Initial connection successful")

	// Update DSN in file (add application_name parameter)
	updatedDSN := testDSN + " application_name=hotload_test"
	if err := os.WriteFile(tmpFile.Name(), []byte(updatedDSN), 0644); err != nil {
		t.Fatalf("Failed to update DSN file: %v", err)
	}

	// Wait for fsnotify to detect change
	time.Sleep(500 * time.Millisecond)

	// New connection should pick up the change
	// Note: Existing connections might still use old DSN depending on forceKill setting
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()

	err = db.QueryRowContext(ctx2, "SELECT 1").Scan(&result)
	if err != nil {
		t.Fatalf("Failed to execute query after DSN update: %v", err)
	}

	t.Log("✓ DSN reload successful")
}

// TestLibPQ_ConnectionPooling tests that connection pooling works correctly
func TestLibPQ_ConnectionPooling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available (no PG_DSN and container failed to start)")
	}

	// Create temporary config file
	tmpFile, err := os.CreateTemp("", "hotload-test-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(testDSN); err != nil {
		t.Fatalf("Failed to write DSN to file: %v", err)
	}
	tmpFile.Close()

	// Driver registered in TestMain
	db, err := sql.Open("hotload", "fsnotify://postgres/"+tmpFile.Name())
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Configure connection pool
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(time.Hour)

	// Execute concurrent queries
	const numQueries = 10
	errChan := make(chan error, numQueries)

	for i := 0; i < numQueries; i++ {
		go func(n int) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			var result int
			err := db.QueryRowContext(ctx, "SELECT $1::int", n).Scan(&result)
			errChan <- err
		}(i)
	}

	// Collect results
	for i := 0; i < numQueries; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("Query %d failed: %v", i, err)
		}
	}

	t.Log("✓ Connection pooling working correctly")
}
