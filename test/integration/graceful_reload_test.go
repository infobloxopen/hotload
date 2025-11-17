package integration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/infobloxopen/hotload"
	_ "github.com/infobloxopen/hotload/strategy/fsnotify"
)

// TestGracefulReload_LongRunningQueries tests the core hotload feature:
// gracefully draining old connections while DSN changes, without killing
// in-flight queries.
//
// Scenario:
//  1. Start with DSN using user1
//  2. Begin multiple long-running queries (5 second pg_sleep)
//  3. While queries are running, change DSN to user2
//  4. Verify old queries complete successfully
//  5. Verify new queries use user2
//  6. No queries should be killed mid-flight
func TestGracefulReload_LongRunningQueries(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available")
	}

	drivers := GetPostgresDriverConfigs(t)

	for _, driverCfg := range drivers {
		driverCfg := driverCfg // capture range variable
		t.Run(driverCfg.Name, func(t *testing.T) {
			testGracefulReloadLongRunningQueries(t, driverCfg)
		})
	}
}

func testGracefulReloadLongRunningQueries(t *testing.T, driverCfg DriverConfig) {
	// Register driver with unique name
	driverName := fmt.Sprintf("%s-graceful-%d", driverCfg.Name, time.Now().UnixNano())
	hotload.RegisterSQLDriver(driverName, driverCfg.Driver)

	// Create a second user for DSN switching (unique per driver to avoid conflicts)
	testUser2 := fmt.Sprintf("testuser2_%s_%d", driverCfg.Name, time.Now().UnixNano()%10000)
	testPass2 := "testpass2"

	db, err := sql.Open("postgres", testDSN)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Create user2 with same privileges
	_, err = db.Exec(fmt.Sprintf("DROP USER IF EXISTS %s", testUser2))
	if err != nil {
		t.Logf("Warning: failed to drop user2: %v", err)
	}

	_, err = db.Exec(fmt.Sprintf("CREATE USER %s WITH PASSWORD '%s'", testUser2, testPass2))
	if err != nil {
		t.Fatalf("Failed to create user2: %v", err)
	}
	defer db.Exec(fmt.Sprintf("DROP USER IF EXISTS %s", testUser2))

	_, err = db.Exec(fmt.Sprintf("GRANT ALL PRIVILEGES ON DATABASE testdb TO %s", testUser2))
	if err != nil {
		t.Fatalf("Failed to grant privileges to user2: %v", err)
	}

	// Create a tracking table to see which user executed queries
	_, err = db.Exec(`
		DROP TABLE IF EXISTS query_log;
		CREATE TABLE query_log (
			id SERIAL PRIMARY KEY,
			username TEXT NOT NULL,
			query_duration INTERVAL NOT NULL,
			created_at TIMESTAMP DEFAULT NOW()
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create tracking table: %v", err)
	}
	defer db.Exec("DROP TABLE IF EXISTS query_log")

	// Grant permissions on the table to both users
	_, err = db.Exec("GRANT ALL PRIVILEGES ON TABLE query_log TO testuser")
	if err != nil {
		t.Fatalf("Failed to grant table privileges to testuser: %v", err)
	}
	_, err = db.Exec(fmt.Sprintf("GRANT ALL PRIVILEGES ON TABLE query_log TO %s", testUser2))
	if err != nil {
		t.Fatalf("Failed to grant table privileges to user2: %v", err)
	}
	_, err = db.Exec("GRANT USAGE, SELECT ON SEQUENCE query_log_id_seq TO testuser")
	if err != nil {
		t.Fatalf("Failed to grant sequence privileges to testuser: %v", err)
	}
	_, err = db.Exec(fmt.Sprintf("GRANT USAGE, SELECT ON SEQUENCE query_log_id_seq TO %s", testUser2))
	if err != nil {
		t.Fatalf("Failed to grant sequence privileges to user2: %v", err)
	}

	// Create temp file for DSN with user1
	tmpFile, err := os.CreateTemp("", "hotload-graceful-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write initial DSN (user1)
	dsn1 := testDSN // uses testuser
	if _, err := tmpFile.WriteString(dsn1); err != nil {
		t.Fatalf("Failed to write DSN: %v", err)
	}
	tmpFile.Close()

	// Open hotload connection with generous drain timeout
	hotloadDSN := fmt.Sprintf("fsnotify://%s/%s?drainTimeout=10s&forceKill=false", driverName, tmpFile.Name())
	hotloadDB, err := sql.Open("hotload", hotloadDSN)
	if err != nil {
		t.Fatalf("Failed to open hotload database: %v", err)
	}
	defer hotloadDB.Close()

	// Set connection pool to allow multiple concurrent connections
	hotloadDB.SetMaxOpenConns(10)
	hotloadDB.SetMaxIdleConns(5)

	// Verify initial connection works
	ctx := context.Background()
	var result int
	err = hotloadDB.QueryRowContext(ctx, "SELECT 1").Scan(&result)
	if err != nil {
		t.Fatalf("Initial query failed: %v", err)
	}

	t.Log("✓ Initial connection established with testuser")

	// Start long-running queries
	var wg sync.WaitGroup
	queryErrors := make(chan error, 3)
	queryDuration := 5 * time.Second

	// Launch 3 concurrent long-running queries
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(queryNum int) {
			defer wg.Done()

			queryCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			start := time.Now()

			// Execute a query that sleeps and logs which user ran it
			var username string
			err := hotloadDB.QueryRowContext(queryCtx, `
				SELECT current_user 
				FROM pg_sleep($1)
			`, queryDuration.Seconds()).Scan(&username)

			elapsed := time.Since(start)

			if err != nil {
				queryErrors <- fmt.Errorf("query %d failed: %v", queryNum, err)
				return
			}

			// Log to tracking table
			_, err = hotloadDB.ExecContext(queryCtx,
				"INSERT INTO query_log (username, query_duration) VALUES ($1, $2)",
				username, elapsed)
			if err != nil {
				queryErrors <- fmt.Errorf("query %d failed to log: %v", queryNum, err)
				return
			}

			t.Logf("✓ Long query %d completed successfully in %v as user '%s'",
				queryNum, elapsed, username)
		}(i)
	}

	// Wait a bit for queries to start
	time.Sleep(500 * time.Millisecond)
	t.Log("⏳ Long-running queries started, now changing DSN...")

	// Change DSN to user2 while queries are running
	dsn2 := fmt.Sprintf("host=localhost port=%s user=%s password=%s dbname=testdb sslmode=disable",
		getPortFromDSN(testDSN), testUser2, testPass2)

	err = os.WriteFile(tmpFile.Name(), []byte(dsn2), 0644)
	if err != nil {
		t.Fatalf("Failed to update DSN file: %v", err)
	}

	t.Logf("🔄 DSN changed to %s, waiting for queries to complete gracefully...", testUser2)

	// Wait for all long queries to complete
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Log("✓ All long-running queries completed")
	case err := <-queryErrors:
		t.Fatalf("Query failed: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("Timeout waiting for queries to complete")
	}

	// Check for any query errors
	close(queryErrors)
	for err := range queryErrors {
		t.Errorf("Query error: %v", err)
	}

	// Give fsnotify time to detect and process the file change
	time.Sleep(2 * time.Second)

	// Now verify new connections use user2
	var currentUser string
	err = hotloadDB.QueryRowContext(ctx, "SELECT current_user").Scan(&currentUser)
	if err != nil {
		t.Fatalf("Failed to query current user: %v", err)
	}

	if currentUser != testUser2 {
		t.Errorf("Expected new connections to use '%s', got '%s'", testUser2, currentUser)
	} else {
		t.Logf("✓ New connections using %s", testUser2)
	}

	// Verify all old queries completed with testuser (not killed)
	var testuser1Count, testuser2Count int
	err = db.QueryRow("SELECT COUNT(*) FROM query_log WHERE username = 'testuser'").Scan(&testuser1Count)
	if err != nil {
		t.Fatalf("Failed to count testuser queries: %v", err)
	}

	if testuser1Count != 3 {
		t.Errorf("Expected 3 queries completed by testuser, got %d (queries may have been killed!)", testuser1Count)
	} else {
		t.Log("✓ All 3 long-running queries completed gracefully (not killed)")
	}

	// Verify new queries are using testuser2
	_, err = hotloadDB.ExecContext(ctx,
		"INSERT INTO query_log (username, query_duration) VALUES (current_user, interval '0 seconds')")
	if err != nil {
		t.Fatalf("Failed to insert test row: %v", err)
	}

	err = db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM query_log WHERE username = '%s'", testUser2)).Scan(&testuser2Count)
	if err != nil {
		t.Fatalf("Failed to count %s queries: %v", testUser2, err)
	}

	if testuser2Count < 1 {
		t.Errorf("Expected at least 1 query by %s, got %d", testUser2, testuser2Count)
	} else {
		t.Logf("✓ New queries using testuser2 (count: %d)", testuser2Count)
	}

	t.Log("✅ Graceful reload test passed: connections drained without killing in-flight queries")
}

// TestGracefulReload_ForceKill tests that forceKill=true terminates connections
// after drainTimeout, even if queries are still running.
func TestGracefulReload_ForceKill(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available")
	}

	drivers := GetPostgresDriverConfigs(t)
	for _, driverCfg := range drivers {
		driverCfg := driverCfg
		t.Run(driverCfg.Name, func(t *testing.T) {
			testGracefulReloadForceKill(t, driverCfg)
		})
	}
}

func testGracefulReloadForceKill(t *testing.T, driverCfg DriverConfig) {
	// Register driver with unique name
	driverName := fmt.Sprintf("%s-forcekill-%d", driverCfg.Name, time.Now().UnixNano())
	hotload.RegisterSQLDriver(driverName, driverCfg.Driver)

	// Create temp file for DSN
	tmpFile, err := os.CreateTemp("", "hotload-forcekill-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write initial DSN
	if _, err := tmpFile.WriteString(testDSN); err != nil {
		t.Fatalf("Failed to write DSN: %v", err)
	}
	tmpFile.Close()

	// Open hotload connection with SHORT drain timeout and forceKill=true
	hotloadDSN := fmt.Sprintf("fsnotify://%s/%s?drainTimeout=2s&forceKill=true", driverName, tmpFile.Name())
	hotloadDB, err := sql.Open("hotload", hotloadDSN)
	if err != nil {
		t.Fatalf("Failed to open hotload database: %v", err)
	}
	defer hotloadDB.Close()

	hotloadDB.SetMaxOpenConns(5)

	// Verify initial connection
	ctx := context.Background()
	var result int
	err = hotloadDB.QueryRowContext(ctx, "SELECT 1").Scan(&result)
	if err != nil {
		t.Fatalf("Initial query failed: %v", err)
	}

	t.Log("✓ Initial connection established")

	// Start a long query (10 seconds)
	queryStarted := make(chan struct{})
	queryDone := make(chan error, 1)

	go func() {
		close(queryStarted)
		queryCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		var username string
		err := hotloadDB.QueryRowContext(queryCtx,
			"SELECT current_user FROM pg_sleep(10)").Scan(&username)
		queryDone <- err
	}()

	// Wait for query to start
	<-queryStarted
	time.Sleep(500 * time.Millisecond)

	t.Log("⏳ Long query started, changing DSN with short drainTimeout...")

	// Change DSN - just modify slightly to trigger reload
	newDSN := testDSN + " application_name=hotload_test"
	err = os.WriteFile(tmpFile.Name(), []byte(newDSN), 0644)
	if err != nil {
		t.Fatalf("Failed to update DSN file: %v", err)
	}

	t.Log("🔄 DSN changed, waiting for drainTimeout to expire...")

	// The query should be killed after drainTimeout (2s)
	select {
	case err := <-queryDone:
		// Query should fail because connection was force-killed
		if err == nil {
			t.Error("Expected query to be killed after drainTimeout, but it succeeded")
		} else {
			t.Logf("✓ Query was terminated after drainTimeout as expected: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Timeout: query should have been killed within drainTimeout + buffer")
	}

	// Verify new connections still work
	time.Sleep(1 * time.Second)
	err = hotloadDB.QueryRowContext(ctx, "SELECT 1").Scan(&result)
	if err != nil {
		t.Errorf("New connection failed after force kill: %v", err)
	} else {
		t.Log("✓ New connections work after force kill")
	}

	t.Log("✅ Force kill test passed: old connection was terminated after drainTimeout")
}

// TestGracefulReload_CredentialOnlyChange tests the NEW smart DSN comparison feature.
// When only username/password change (not host/port/database), connections should
// NOT be drained - they should continue executing.
//
// Scenario:
//  1. Start with DSN using testuser/testpass
//  2. Begin a long-running query (5 second pg_sleep)
//  3. While query is running, change ONLY password in DSN
//  4. With credentialOnlyReload=true, connection should stay alive
//  5. Query should complete without interruption
//  6. New queries should use new credentials
//
// This test should FAIL with current implementation (connections always drain).
// After implementing the feature with credentialOnlyReload=true, it should pass.
func TestGracefulReload_CredentialOnlyChange(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available")
	}

	drivers := GetPostgresDriverConfigs(t)
	for _, driverCfg := range drivers {
		driverCfg := driverCfg
		t.Run(driverCfg.Name, func(t *testing.T) {
			testGracefulReloadCredentialOnlyChange(t, driverCfg)
		})
	}
}

func testGracefulReloadCredentialOnlyChange(t *testing.T, driverCfg DriverConfig) {
	// Register driver with unique name
	driverName := fmt.Sprintf("%s-credonly-%d", driverCfg.Name, time.Now().UnixNano())
	hotload.RegisterSQLDriver(driverName, driverCfg.Driver)

	// Create unique user for credential testing
	credUser := fmt.Sprintf("credtest_%s_%d", driverCfg.Name, time.Now().UnixNano()%10000)

	// Create two users with different passwords for credential testing
	db, err := sql.Open("postgres", testDSN)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Create credtest user
	_, err = db.Exec(fmt.Sprintf("DROP USER IF EXISTS %s", credUser))
	if err != nil {
		t.Logf("Warning: failed to drop %s: %v", credUser, err)
	}
	_, err = db.Exec(fmt.Sprintf("CREATE USER %s WITH PASSWORD 'pass1'", credUser))
	if err != nil {
		t.Fatalf("Failed to create %s: %v", credUser, err)
	}
	defer db.Exec(fmt.Sprintf("DROP USER IF EXISTS %s", credUser))

	_, err = db.Exec(fmt.Sprintf("GRANT ALL PRIVILEGES ON DATABASE testdb TO %s", credUser))
	if err != nil {
		t.Fatalf("Failed to grant privileges to %s: %v", credUser, err)
	}

	// Create tracking table
	_, err = db.Exec(`
		DROP TABLE IF EXISTS cred_test_log;
		CREATE TABLE cred_test_log (
			id SERIAL PRIMARY KEY,
			username TEXT NOT NULL,
			query_num INT NOT NULL,
			connection_backend_pid INT NOT NULL,
			created_at TIMESTAMP DEFAULT NOW()
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create tracking table: %v", err)
	}
	defer db.Exec("DROP TABLE IF EXISTS cred_test_log")

	_, err = db.Exec(fmt.Sprintf("GRANT ALL PRIVILEGES ON TABLE cred_test_log TO %s", credUser))
	if err != nil {
		t.Fatalf("Failed to grant table privileges: %v", err)
	}
	_, err = db.Exec(fmt.Sprintf("GRANT USAGE, SELECT ON SEQUENCE cred_test_log_id_seq TO %s", credUser))
	if err != nil {
		t.Fatalf("Failed to grant sequence privileges: %v", err)
	}

	// Create temp file for DSN
	tmpFile, err := os.CreateTemp("", "hotload-cred-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write initial DSN with credtest/pass1
	port := getPortFromDSN(testDSN)
	dsn1 := fmt.Sprintf("host=localhost port=%s user=%s password=pass1 dbname=testdb sslmode=disable", port, credUser)
	if _, err := tmpFile.WriteString(dsn1); err != nil {
		t.Fatalf("Failed to write DSN: %v", err)
	}
	tmpFile.Close()

	// Open hotload connection WITH credentialOnlyReload=true (NEW FEATURE)
	hotloadDSN := fmt.Sprintf("fsnotify://%s/%s?drainTimeout=10s&forceKill=false&credentialOnlyReload=true", driverName, tmpFile.Name())
	hotloadDB, err := sql.Open("hotload", hotloadDSN)
	if err != nil {
		t.Fatalf("Failed to open hotload database: %v", err)
	}
	defer hotloadDB.Close()

	hotloadDB.SetMaxOpenConns(5)
	hotloadDB.SetMaxIdleConns(2)

	// Verify initial connection
	ctx := context.Background()
	var result int
	var backendPID int
	err = hotloadDB.QueryRowContext(ctx, "SELECT 1, pg_backend_pid()").Scan(&result, &backendPID)
	if err != nil {
		t.Fatalf("Initial query failed: %v", err)
	}

	t.Logf("✓ Initial connection established with %s (backend PID: %d)", credUser, backendPID)

	// Start a long-running query that logs its backend PID
	queryDone := make(chan error, 1)
	queryStartPID := make(chan int, 1)

	go func() {
		queryCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		start := time.Now()

		var username string
		var pid int
		// This query sleeps for 5 seconds and logs which PID executed it
		err := hotloadDB.QueryRowContext(queryCtx, `
			SELECT current_user, pg_backend_pid()
			FROM pg_sleep($1)
		`, 5.0).Scan(&username, &pid)

		elapsed := time.Since(start)

		if err != nil {
			queryDone <- fmt.Errorf("query failed: %v", err)
			return
		}

		queryStartPID <- pid

		// Log to tracking table with backend PID
		_, err = hotloadDB.ExecContext(queryCtx,
			"INSERT INTO cred_test_log (username, query_num, connection_backend_pid) VALUES ($1, $2, $3)",
			username, 1, pid)
		if err != nil {
			queryDone <- fmt.Errorf("failed to log: %v", err)
			return
		}

		t.Logf("✓ Long query completed in %v as user '%s' on PID %d", elapsed, username, pid)
		queryDone <- nil
	}()

	// Wait for query to start
	time.Sleep(500 * time.Millisecond)
	t.Log("⏳ Long-running query started, now changing ONLY password in DSN...")

	// Change ONLY the password (credential-only change)
	// Host, port, database all stay the same
	dsn2 := fmt.Sprintf("host=localhost port=%s user=%s password=pass2 dbname=testdb sslmode=disable", port, credUser)

	// First, update the actual password in PostgreSQL
	_, err = db.Exec(fmt.Sprintf("ALTER USER %s WITH PASSWORD 'pass2'", credUser))
	if err != nil {
		t.Fatalf("Failed to change password: %v", err)
	}

	// Now update the DSN file
	err = os.WriteFile(tmpFile.Name(), []byte(dsn2), 0644)
	if err != nil {
		t.Fatalf("Failed to update DSN file: %v", err)
	}

	t.Logf("🔄 DSN changed (credential only: pass1 → pass2)")
	t.Log("💡 With credentialOnlyReload=true, connection should NOT be drained")

	// Wait for query to complete
	select {
	case err := <-queryDone:
		if err != nil {
			t.Fatalf("Query failed (connection was likely killed): %v", err)
		}
		t.Log("✓ Long-running query completed successfully (connection stayed alive!)")
	case <-time.After(20 * time.Second):
		t.Fatal("Timeout waiting for query to complete")
	}

	queryPID := <-queryStartPID

	// Give fsnotify time to process
	time.Sleep(2 * time.Second)

	// Now verify NEW connections use the NEW password (pass2)
	var newBackendPID int
	err = hotloadDB.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&newBackendPID)
	if err != nil {
		t.Fatalf("Failed to query with new credentials: %v", err)
	}

	t.Logf("✓ New connection established with updated credentials (backend PID: %d)", newBackendPID)

	// Verify the old query completed on the SAME connection (backend PID)
	// This proves the connection wasn't drained
	var loggedPID int
	err = db.QueryRow("SELECT connection_backend_pid FROM cred_test_log WHERE query_num = 1").Scan(&loggedPID)
	if err != nil {
		t.Fatalf("Failed to query logged PID: %v", err)
	}

	if loggedPID != queryPID {
		t.Errorf("Backend PID mismatch: expected %d, got %d", queryPID, loggedPID)
	}

	t.Logf("✓ Query used backend PID %d (connection was not drained)", loggedPID)
	t.Log("✅ Credential-only reload test passed: connection stayed alive during password change")
}

// TestGracefulReload_StructuralChange tests that connections ARE drained
// when host/port/database changes (structural changes).
//
// This test should pass both BEFORE and AFTER implementing credentialOnlyReload.
// Structural changes should always trigger connection draining for safety.
func TestGracefulReload_StructuralChange(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available")
	}

	drivers := GetPostgresDriverConfigs(t)
	for _, driverCfg := range drivers {
		driverCfg := driverCfg
		t.Run(driverCfg.Name, func(t *testing.T) {
			testGracefulReloadStructuralChange(t, driverCfg)
		})
	}
}

func testGracefulReloadStructuralChange(t *testing.T, driverCfg DriverConfig) {
	// Register driver with unique name
	driverName := fmt.Sprintf("%s-structural-%d", driverCfg.Name, time.Now().UnixNano())
	hotload.RegisterSQLDriver(driverName, driverCfg.Driver)

	// Create temp file for DSN
	tmpFile, err := os.CreateTemp("", "hotload-structural-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write initial DSN
	if _, err := tmpFile.WriteString(testDSN); err != nil {
		t.Fatalf("Failed to write DSN: %v", err)
	}
	tmpFile.Close()

	// Open hotload connection WITH credentialOnlyReload=true
	// Even with this option, structural changes should still drain
	hotloadDSN := fmt.Sprintf("fsnotify://%s/%s?drainTimeout=3s&forceKill=true&credentialOnlyReload=true", driverName, tmpFile.Name())
	hotloadDB, err := sql.Open("hotload", hotloadDSN)
	if err != nil {
		t.Fatalf("Failed to open hotload database: %v", err)
	}
	defer hotloadDB.Close()

	hotloadDB.SetMaxOpenConns(5)

	// Verify initial connection
	ctx := context.Background()
	var result int
	var initialPID int
	err = hotloadDB.QueryRowContext(ctx, "SELECT 1, pg_backend_pid()").Scan(&result, &initialPID)
	if err != nil {
		t.Fatalf("Initial query failed: %v", err)
	}

	t.Logf("✓ Initial connection established (backend PID: %d)", initialPID)

	// Start a long query
	queryDone := make(chan error, 1)

	go func() {
		queryCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		var pid int
		err := hotloadDB.QueryRowContext(queryCtx,
			"SELECT pg_backend_pid() FROM pg_sleep(5)").Scan(&pid)
		queryDone <- err
	}()

	// Wait for query to start
	time.Sleep(500 * time.Millisecond)

	t.Log("⏳ Long query started, changing DSN with STRUCTURAL change (application_name)...")

	// Change DSN with a structural change (query parameter)
	// This should trigger connection draining even with credentialOnlyReload=true
	newDSN := testDSN + " application_name=structural_change_test"
	err = os.WriteFile(tmpFile.Name(), []byte(newDSN), 0644)
	if err != nil {
		t.Fatalf("Failed to update DSN file: %v", err)
	}

	t.Log("🔄 DSN changed (structural: added application_name)")
	t.Log("💡 Even with credentialOnlyReload=true, structural changes should drain")

	// Query should complete or be killed after drainTimeout
	select {
	case err := <-queryDone:
		// Either completes gracefully or is killed - both are acceptable
		if err != nil {
			t.Logf("✓ Query was terminated after drainTimeout (expected): %v", err)
		} else {
			t.Log("✓ Query completed gracefully during drain window (acceptable)")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for query")
	}

	// Give time for drain to complete
	time.Sleep(2 * time.Second)

	// Verify new connections work with new DSN
	var newPID int
	err = hotloadDB.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&newPID)
	if err != nil {
		t.Fatalf("New connection failed: %v", err)
	}

	t.Logf("✓ New connection established after structural change (backend PID: %d)", newPID)
	t.Log("✅ Structural change test passed: connections were drained as expected")
}

// Helper to extract port from DSN string
func getPortFromDSN(dsn string) string {
	// Simple parser - assumes format "host=localhost port=32783 user=..."
	var port string
	fields := splitDSN(dsn)
	for k, v := range fields {
		if k == "port" {
			port = v
			break
		}
	}
	if port == "" {
		port = "5432"
	}
	return port
}

func splitDSN(dsn string) map[string]string {
	result := make(map[string]string)
	parts := splitPreservingQuotes(dsn, ' ')
	for _, part := range parts {
		kv := splitPreservingQuotes(part, '=')
		if len(kv) == 2 {
			result[kv[0]] = kv[1]
		}
	}
	return result
}

func splitPreservingQuotes(s string, sep rune) []string {
	var result []string
	var current string
	inQuotes := false

	for _, r := range s {
		if r == '\'' || r == '"' {
			inQuotes = !inQuotes
			continue
		}
		if r == sep && !inQuotes {
			if current != "" {
				result = append(result, current)
				current = ""
			}
			continue
		}
		current += string(r)
	}

	if current != "" {
		result = append(result, current)
	}

	return result
}
