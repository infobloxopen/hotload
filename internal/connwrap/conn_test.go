package connwrap

import (
	"context"
	"database/sql/driver"
	"testing"

	"github.com/infobloxopen/hotload/internal/testkit"
)

// TestConn_RefcountingOnPrepare tests in-flight tracking during Prepare.
func TestConn_RefcountingOnPrepare(t *testing.T) {
	fakeDriver := testkit.NewFakeDriver()
	underlying, _ := fakeDriver.Open("test")

	conn := NewConn(underlying, 1)

	if conn.InFlight() != 0 {
		t.Errorf("expected 0 in-flight initially, got %d", conn.InFlight())
	}

	stmt, err := conn.Prepare("SELECT 1")
	if err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	defer stmt.Close()

	// After Prepare returns, refcount should be back to 0
	if conn.InFlight() != 0 {
		t.Errorf("expected 0 in-flight after Prepare, got %d", conn.InFlight())
	}
}

// TestConn_RefcountingOnBegin tests in-flight tracking during Begin.
func TestConn_RefcountingOnBegin(t *testing.T) {
	fakeDriver := testkit.NewFakeDriver()
	underlying, _ := fakeDriver.Open("test")

	conn := NewConn(underlying, 1)

	tx, err := conn.Begin()
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}
	defer tx.Rollback()

	// After Begin returns, refcount should be back to 0
	if conn.InFlight() != 0 {
		t.Errorf("expected 0 in-flight after Begin, got %d", conn.InFlight())
	}
}

// TestConn_PrepareContext_WithSupport tests PrepareContext when underlying supports it.
func TestConn_PrepareContext_WithSupport(t *testing.T) {
	fakeDriver := testkit.NewFakeDriverWithCapabilities(
		false, false, false,
		true, // PrepareContext
		false, false, false,
	)
	underlying, _ := fakeDriver.Open("test")

	conn := NewConn(underlying, 1)

	ctx := context.Background()
	stmt, err := conn.PrepareContext(ctx, "SELECT 1")
	if err != nil {
		t.Fatalf("PrepareContext failed: %v", err)
	}
	defer stmt.Close()

	// Refcount should be 0 after return
	if conn.InFlight() != 0 {
		t.Errorf("expected 0 in-flight, got %d", conn.InFlight())
	}
}

// TestConn_PrepareContext_WithoutSupport tests PrepareContext returns ErrSkip when not supported.
func TestConn_PrepareContext_WithoutSupport(t *testing.T) {
	fakeDriver := testkit.NewFakeDriverWithCapabilities(
		false, false, false,
		false, // No PrepareContext
		false, false, false,
	)
	underlying, _ := fakeDriver.Open("test")

	conn := NewConn(underlying, 1)

	ctx := context.Background()
	_, err := conn.PrepareContext(ctx, "SELECT 1")
	if err != driver.ErrSkip {
		t.Errorf("expected ErrSkip, got %v", err)
	}
}

// TestConn_BeginTx_WithSupport tests BeginTx when underlying supports it.
func TestConn_BeginTx_WithSupport(t *testing.T) {
	fakeDriver := testkit.NewFakeDriverWithCapabilities(
		false, false,
		true, // BeginTx
		false, false, false, false,
	)
	underlying, _ := fakeDriver.Open("test")

	conn := NewConn(underlying, 1)

	ctx := context.Background()
	tx, err := conn.BeginTx(ctx, driver.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx.Rollback()

	// Refcount should be 0 after return
	if conn.InFlight() != 0 {
		t.Errorf("expected 0 in-flight, got %d", conn.InFlight())
	}
}

// TestConn_BeginTx_WithoutSupport tests BeginTx returns ErrSkip when not supported.
func TestConn_BeginTx_WithoutSupport(t *testing.T) {
	fakeDriver := testkit.NewFakeDriverWithCapabilities(
		false, false,
		false, // No BeginTx
		false, false, false, false,
	)
	underlying, _ := fakeDriver.Open("test")

	conn := NewConn(underlying, 1)

	ctx := context.Background()
	_, err := conn.BeginTx(ctx, driver.TxOptions{})
	if err != driver.ErrSkip {
		t.Errorf("expected ErrSkip, got %v", err)
	}
}

// TestConn_ExecContext_WithSupport tests ExecContext when supported.
func TestConn_ExecContext_WithSupport(t *testing.T) {
	fakeDriver := testkit.NewFakeDriverWithCapabilities(
		true, // ExecContext
		false, false, false, false, false, false,
	)
	underlying, _ := fakeDriver.Open("test")

	conn := NewConn(underlying, 1)

	ctx := context.Background()
	result, err := conn.ExecContext(ctx, "INSERT INTO test VALUES (1)", nil)
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

// TestConn_ExecContext_WithoutSupport tests ExecContext returns ErrSkip.
func TestConn_ExecContext_WithoutSupport(t *testing.T) {
	fakeDriver := testkit.NewFakeDriverWithCapabilities(
		false, // No ExecContext
		false, false, false, false, false, false,
	)
	underlying, _ := fakeDriver.Open("test")

	conn := NewConn(underlying, 1)

	ctx := context.Background()
	_, err := conn.ExecContext(ctx, "INSERT INTO test VALUES (1)", nil)
	if err != driver.ErrSkip {
		t.Errorf("expected ErrSkip, got %v", err)
	}
}

// TestConn_QueryContext_WithSupport tests QueryContext when supported.
func TestConn_QueryContext_WithSupport(t *testing.T) {
	fakeDriver := testkit.NewFakeDriverWithCapabilities(
		false,
		true, // QueryContext
		false, false, false, false, false,
	)
	underlying, _ := fakeDriver.Open("test")

	conn := NewConn(underlying, 1)

	ctx := context.Background()
	rows, err := conn.QueryContext(ctx, "SELECT * FROM test", nil)
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

// TestConn_QueryContext_WithoutSupport tests QueryContext returns ErrSkip.
func TestConn_QueryContext_WithoutSupport(t *testing.T) {
	fakeDriver := testkit.NewFakeDriverWithCapabilities(
		false,
		false, // No QueryContext
		false, false, false, false, false,
	)
	underlying, _ := fakeDriver.Open("test")

	conn := NewConn(underlying, 1)

	ctx := context.Background()
	_, err := conn.QueryContext(ctx, "SELECT * FROM test", nil)
	if err != driver.ErrSkip {
		t.Errorf("expected ErrSkip, got %v", err)
	}
}

// TestConn_Ping_WithSupport tests Ping when supported.
func TestConn_Ping_WithSupport(t *testing.T) {
	fakeDriver := testkit.NewFakeDriverWithCapabilities(
		false, false, false, false,
		true, // Ping
		false, false,
	)
	underlying, _ := fakeDriver.Open("test")

	conn := NewConn(underlying, 1)

	ctx := context.Background()
	err := conn.Ping(ctx)
	if err != nil {
		t.Errorf("Ping failed: %v", err)
	}

	// Refcount should be 0 after return
	if conn.InFlight() != 0 {
		t.Errorf("expected 0 in-flight, got %d", conn.InFlight())
	}
}

// TestConn_Ping_WithoutSupport tests Ping returns ErrSkip.
func TestConn_Ping_WithoutSupport(t *testing.T) {
	fakeDriver := testkit.NewFakeDriverWithCapabilities(
		false, false, false, false,
		false, // No Ping
		false, false,
	)
	underlying, _ := fakeDriver.Open("test")

	conn := NewConn(underlying, 1)

	ctx := context.Background()
	err := conn.Ping(ctx)
	if err != driver.ErrSkip {
		t.Errorf("expected ErrSkip, got %v", err)
	}
}

// TestConn_ResetSession_WithSupport tests ResetSession when supported.
func TestConn_ResetSession_WithSupport(t *testing.T) {
	fakeDriver := testkit.NewFakeDriverWithCapabilities(
		false, false, false, false, false,
		true, // SessionResetter
		false,
	)
	underlying, _ := fakeDriver.Open("test")

	conn := NewConn(underlying, 1)

	ctx := context.Background()
	err := conn.ResetSession(ctx)
	if err != nil {
		t.Errorf("ResetSession failed: %v", err)
	}
}

// TestConn_ResetSession_WithoutSupport tests ResetSession returns ErrSkip.
func TestConn_ResetSession_WithoutSupport(t *testing.T) {
	fakeDriver := testkit.NewFakeDriverWithCapabilities(
		false, false, false, false, false,
		false, // No SessionResetter
		false,
	)
	underlying, _ := fakeDriver.Open("test")

	conn := NewConn(underlying, 1)

	ctx := context.Background()
	err := conn.ResetSession(ctx)
	if err != driver.ErrSkip {
		t.Errorf("expected ErrSkip, got %v", err)
	}
}

// TestConn_CheckNamedValue_WithSupport tests CheckNamedValue when supported.
func TestConn_CheckNamedValue_WithSupport(t *testing.T) {
	fakeDriver := testkit.NewFakeDriverWithCapabilities(
		false, false, false, false, false, false,
		true, // NamedValueChecker
	)
	underlying, _ := fakeDriver.Open("test")

	conn := NewConn(underlying, 1)

	nv := &driver.NamedValue{Name: "test", Value: 42}
	err := conn.CheckNamedValue(nv)
	if err != nil {
		t.Errorf("CheckNamedValue failed: %v", err)
	}
}

// TestConn_CheckNamedValue_WithoutSupport tests CheckNamedValue returns ErrSkip.
func TestConn_CheckNamedValue_WithoutSupport(t *testing.T) {
	fakeDriver := testkit.NewFakeDriverWithCapabilities(
		false, false, false, false, false, false,
		false, // No NamedValueChecker
	)
	underlying, _ := fakeDriver.Open("test")

	conn := NewConn(underlying, 1)

	nv := &driver.NamedValue{Name: "test", Value: 42}
	err := conn.CheckNamedValue(nv)
	if err != driver.ErrSkip {
		t.Errorf("expected ErrSkip, got %v", err)
	}
}

// TestConn_EpochTracking tests that epoch is preserved.
func TestConn_EpochTracking(t *testing.T) {
	fakeDriver := testkit.NewFakeDriver()
	underlying, _ := fakeDriver.Open("test")

	conn := NewConn(underlying, 42)

	if conn.Epoch() != 42 {
		t.Errorf("expected epoch 42, got %d", conn.Epoch())
	}
}
