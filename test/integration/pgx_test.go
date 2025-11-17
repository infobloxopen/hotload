package integration

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/infobloxopen/hotload/strategy/fsnotify"
)

// TestPGX_BasicConnection tests basic connection with pgx driver
//
// Prerequisites:
//   - PostgreSQL instance running (auto-started via TestMain or set PG_DSN)
//
// Scenario:
//  1. Register pgx driver with hotload
//  2. Create a temporary config file with DSN
//  3. Open connection via hotload
//  4. Execute simple query
//  5. Verify connection works
func TestPGX_BasicConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available (no PG_DSN and container failed to start)")
	}

	// Create temporary config file
	tmpFile, err := os.CreateTemp("", "hotload-pgx-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write DSN to file
	if _, err := tmpFile.WriteString(testDSN); err != nil {
		t.Fatalf("Failed to write DSN to file: %v", err)
	}
	tmpFile.Close()

	// Register pgx driver with hotload

	// Open database via hotload
	db, err := sql.Open("hotload", "fsnotify://pgx/"+tmpFile.Name())
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

	t.Log("✓ Successfully connected and queried via pgx through hotload")
}

// TestPGX_Ping tests that Ping() reaches the underlying pgx driver
//
// Scenario:
//  1. Open connection via hotload using pgx driver
//  2. Call db.Ping() and db.PingContext()
//  3. Verify ping succeeds and reaches the database
func TestPGX_Ping(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available (no PG_DSN and container failed to start)")
	}

	// Create temporary config file
	tmpFile, err := os.CreateTemp("", "hotload-pgx-ping-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write DSN to file
	if _, err := tmpFile.WriteString(testDSN); err != nil {
		t.Fatalf("Failed to write DSN to file: %v", err)
	}
	tmpFile.Close()

	// Open database via hotload using pgx driver
	db, err := sql.Open("hotload", "fsnotify://pgx/"+tmpFile.Name())
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

	t.Log("✓ Ping() successfully reaches underlying pgx driver")
}

// TestPGX_PreparedStatements tests prepared statement support with pgx
func TestPGX_PreparedStatements(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available (no PG_DSN and container failed to start)")
	}

	tmpFile, err := os.CreateTemp("", "hotload-pgx-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(testDSN); err != nil {
		t.Fatalf("Failed to write DSN to file: %v", err)
	}
	tmpFile.Close()

	db, err := sql.Open("hotload", "fsnotify://pgx/"+tmpFile.Name())
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Test prepared statement
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
		t.Errorf("Expected result 30, got %d", result)
	}

	t.Log("✓ Prepared statements working correctly with pgx")
}

// TestPGX_Transactions tests transaction support with pgx
func TestPGX_Transactions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available (no PG_DSN and container failed to start)")
	}

	tmpFile, err := os.CreateTemp("", "hotload-pgx-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(testDSN); err != nil {
		t.Fatalf("Failed to write DSN to file: %v", err)
	}
	tmpFile.Close()

	db, err := sql.Open("hotload", "fsnotify://pgx/"+tmpFile.Name())
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Begin transaction
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("Failed to begin transaction: %v", err)
	}

	// Execute query in transaction
	var result int
	err = tx.QueryRowContext(ctx, "SELECT 42").Scan(&result)
	if err != nil {
		tx.Rollback()
		t.Fatalf("Failed to execute query in transaction: %v", err)
	}

	if result != 42 {
		tx.Rollback()
		t.Errorf("Expected result 42, got %d", result)
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		t.Fatalf("Failed to commit transaction: %v", err)
	}

	t.Log("✓ Transactions working correctly with pgx")
}

// TestPGX_NativeTypes tests pgx-specific type support
func TestPGX_NativeTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available (no PG_DSN and container failed to start)")
	}

	tmpFile, err := os.CreateTemp("", "hotload-pgx-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(testDSN); err != nil {
		t.Fatalf("Failed to write DSN to file: %v", err)
	}
	tmpFile.Close()

	db, err := sql.Open("hotload", "fsnotify://pgx/"+tmpFile.Name())
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Test that pgx-specific features work through hotload
	// When using pgx through database/sql, arrays are returned as strings in PostgreSQL format
	// This is expected behavior for the database/sql interface
	var arrayStr string
	err = db.QueryRowContext(ctx, "SELECT ARRAY['foo', 'bar', 'baz']::text[]").Scan(&arrayStr)
	if err != nil {
		t.Fatalf("Failed to scan array: %v", err)
	}

	// Verify we got a PostgreSQL array string format
	if arrayStr != "{foo,bar,baz}" {
		t.Errorf("Expected array string '{foo,bar,baz}', got '%s'", arrayStr)
	}

	// Test JSON type support (another pgx strength)
	var jsonStr string
	err = db.QueryRowContext(ctx, "SELECT '{\"key\": \"value\"}'::jsonb").Scan(&jsonStr)
	if err != nil {
		t.Fatalf("Failed to scan JSON: %v", err)
	}

	if jsonStr != `{"key": "value"}` {
		t.Errorf("Expected JSON '%s', got '%s'", `{"key": "value"}`, jsonStr)
	}

	t.Log("✓ Native PostgreSQL types working with pgx through database/sql")
}
