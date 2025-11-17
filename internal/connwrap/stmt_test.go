package connwrap

import (
	"context"
	"testing"

	"github.com/infobloxopen/hotload/internal/testkit"
)

// TestStmt_RefcountingOnExec tests in-flight tracking during Exec.
func TestStmt_RefcountingOnExec(t *testing.T) {
	fakeDriver := testkit.NewFakeDriver()
	underlying, _ := fakeDriver.Open("test")
	conn := NewConn(underlying, 1)

	rawStmt, _ := underlying.Prepare("SELECT 1")
	stmt := &Stmt{stmt: rawStmt, conn: conn}

	if conn.InFlight() != 0 {
		t.Errorf("expected 0 in-flight initially, got %d", conn.InFlight())
	}

	_, err := stmt.Exec(nil)
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}

	// After Exec returns, refcount should be back to 0
	if conn.InFlight() != 0 {
		t.Errorf("expected 0 in-flight after Exec, got %d", conn.InFlight())
	}
}

// TestStmt_RefcountingOnQuery tests in-flight tracking during Query.
func TestStmt_RefcountingOnQuery(t *testing.T) {
	fakeDriver := testkit.NewFakeDriver()
	underlying, _ := fakeDriver.Open("test")
	conn := NewConn(underlying, 1)

	rawStmt, _ := underlying.Prepare("SELECT 1")
	stmt := &Stmt{stmt: rawStmt, conn: conn}

	if conn.InFlight() != 0 {
		t.Errorf("expected 0 in-flight initially, got %d", conn.InFlight())
	}

	rows, err := stmt.Query(nil)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	defer rows.Close()

	// After Query returns, refcount should be back to 0
	if conn.InFlight() != 0 {
		t.Errorf("expected 0 in-flight after Query, got %d", conn.InFlight())
	}
}

// TestStmt_ExecContext tests ExecContext with in-flight tracking.
func TestStmt_ExecContext(t *testing.T) {
	fakeDriver := testkit.NewFakeDriver()
	underlying, _ := fakeDriver.Open("test")
	conn := NewConn(underlying, 1)

	rawStmt, _ := underlying.Prepare("INSERT INTO test VALUES (?)")
	stmt := &Stmt{stmt: rawStmt, conn: conn}

	ctx := context.Background()
	result, err := stmt.ExecContext(ctx, nil)
	if err != nil {
		t.Fatalf("ExecContext failed: %v", err)
	}

	if result == nil {
		t.Error("expected result, got nil")
	}

	// Refcount should be 0 after return
	if conn.InFlight() != 0 {
		t.Errorf("expected 0 in-flight, got %d", conn.InFlight())
	}
}

// TestStmt_QueryContext tests QueryContext with in-flight tracking.
func TestStmt_QueryContext(t *testing.T) {
	fakeDriver := testkit.NewFakeDriver()
	underlying, _ := fakeDriver.Open("test")
	conn := NewConn(underlying, 1)

	rawStmt, _ := underlying.Prepare("SELECT * FROM test")
	stmt := &Stmt{stmt: rawStmt, conn: conn}

	ctx := context.Background()
	rows, err := stmt.QueryContext(ctx, nil)
	if err != nil {
		t.Fatalf("QueryContext failed: %v", err)
	}
	defer rows.Close()

	if rows == nil {
		t.Error("expected rows, got nil")
	}

	// Refcount should be 0 after return
	if conn.InFlight() != 0 {
		t.Errorf("expected 0 in-flight, got %d", conn.InFlight())
	}
}

// TestStmt_ColumnConverter tests ColumnConverter delegation.
func TestStmt_ColumnConverter(t *testing.T) {
	fakeDriver := testkit.NewFakeDriver()
	underlying, _ := fakeDriver.Open("test")
	conn := NewConn(underlying, 1)

	rawStmt, _ := underlying.Prepare("SELECT ?")
	stmt := &Stmt{stmt: rawStmt, conn: conn}

	converter := stmt.ColumnConverter(0)
	if converter == nil {
		t.Error("expected converter, got nil")
	}
}

// TestStmt_NumInput tests NumInput delegation.
func TestStmt_NumInput(t *testing.T) {
	fakeDriver := testkit.NewFakeDriver()
	underlying, _ := fakeDriver.Open("test")
	conn := NewConn(underlying, 1)

	rawStmt, _ := underlying.Prepare("SELECT ?, ?")
	stmt := &Stmt{stmt: rawStmt, conn: conn}

	// FakeStmt returns -1 for NumInput
	if stmt.NumInput() != -1 {
		t.Errorf("expected -1 for NumInput, got %d", stmt.NumInput())
	}
}

// TestStmt_Close tests Close delegation.
func TestStmt_Close(t *testing.T) {
	fakeDriver := testkit.NewFakeDriver()
	underlying, _ := fakeDriver.Open("test")
	conn := NewConn(underlying, 1)

	rawStmt, _ := underlying.Prepare("SELECT 1")
	stmt := &Stmt{stmt: rawStmt, conn: conn}

	err := stmt.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}
}
