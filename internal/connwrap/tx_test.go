package connwrap

import (
	"testing"

	"github.com/infobloxopen/hotload/internal/testkit"
)

// TestTx_RefcountingOnCommit tests in-flight tracking during Commit.
func TestTx_RefcountingOnCommit(t *testing.T) {
	fakeDriver := testkit.NewFakeDriver()
	underlying, _ := fakeDriver.Open("test")
	conn := NewConn(underlying, 1)

	rawTx, _ := conn.Begin()
	tx := &Tx{tx: rawTx, conn: conn}

	if conn.InFlight() != 0 {
		t.Errorf("expected 0 in-flight initially, got %d", conn.InFlight())
	}

	err := tx.Commit()
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// After Commit returns, refcount should be back to 0
	if conn.InFlight() != 0 {
		t.Errorf("expected 0 in-flight after Commit, got %d", conn.InFlight())
	}
}

// TestTx_RefcountingOnRollback tests in-flight tracking during Rollback.
func TestTx_RefcountingOnRollback(t *testing.T) {
	fakeDriver := testkit.NewFakeDriver()
	underlying, _ := fakeDriver.Open("test")
	conn := NewConn(underlying, 1)

	rawTx, _ := conn.Begin()
	tx := &Tx{tx: rawTx, conn: conn}

	if conn.InFlight() != 0 {
		t.Errorf("expected 0 in-flight initially, got %d", conn.InFlight())
	}

	err := tx.Rollback()
	if err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}

	// After Rollback returns, refcount should be back to 0
	if conn.InFlight() != 0 {
		t.Errorf("expected 0 in-flight after Rollback, got %d", conn.InFlight())
	}
}
