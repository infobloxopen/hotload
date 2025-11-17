package conntrack

import (
	"context"
	"database/sql/driver"
	"sync"
	"testing"
	"time"

	"github.com/infobloxopen/hotload/internal/testkit"
)

// mockConn implements driver.Conn for testing
type mockConn struct {
	closed bool
	mu     sync.Mutex
}

func (m *mockConn) Prepare(query string) (driver.Stmt, error) {
	return nil, driver.ErrSkip
}

func (m *mockConn) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockConn) Begin() (driver.Tx, error) {
	return nil, driver.ErrSkip
}

func (m *mockConn) IsClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

// TestRegistry_RegisterAndCount tests basic registration.
func TestRegistry_RegisterAndCount(t *testing.T) {
	registry := NewRegistry(nil)

	conn1 := NewConnWrapper(&mockConn{}, 1)
	conn2 := NewConnWrapper(&mockConn{}, 1)
	conn3 := NewConnWrapper(&mockConn{}, 2)

	registry.Register(1, conn1)
	registry.Register(1, conn2)
	registry.Register(2, conn3)

	if count := registry.Count(1); count != 2 {
		t.Errorf("expected 2 connections in epoch 1, got %d", count)
	}

	if count := registry.Count(2); count != 1 {
		t.Errorf("expected 1 connection in epoch 2, got %d", count)
	}

	if total := registry.Total(); total != 3 {
		t.Errorf("expected 3 total connections, got %d", total)
	}
}

// TestRegistry_Unregister tests connection unregistration.
func TestRegistry_Unregister(t *testing.T) {
	registry := NewRegistry(nil)

	conn1 := NewConnWrapper(&mockConn{}, 1)
	conn2 := NewConnWrapper(&mockConn{}, 1)

	registry.Register(1, conn1)
	registry.Register(1, conn2)

	if count := registry.Count(1); count != 2 {
		t.Errorf("expected 2 connections, got %d", count)
	}

	registry.Unregister(1, conn1)

	if count := registry.Count(1); count != 1 {
		t.Errorf("expected 1 connection after unregister, got %d", count)
	}

	registry.Unregister(1, conn2)

	if count := registry.Count(1); count != 0 {
		t.Errorf("expected 0 connections after unregister all, got %d", count)
	}
}

// TestRegistry_DrainEpoch_IdleConnections tests draining idle connections.
func TestRegistry_DrainEpoch_IdleConnections(t *testing.T) {
	fakeClock := testkit.NewFakeClock(time.Now())
	registry := NewRegistry(fakeClock)

	mock1 := &mockConn{}
	mock2 := &mockConn{}
	conn1 := NewConnWrapper(mock1, 1)
	conn2 := NewConnWrapper(mock2, 1)

	registry.Register(1, conn1)
	registry.Register(1, conn2)

	ctx := context.Background()
	drained, killed := registry.DrainEpoch(ctx, 1, 5*time.Second, true)

	if drained != 2 {
		t.Errorf("expected 2 drained connections, got %d", drained)
	}

	if killed != 0 {
		t.Errorf("expected 0 killed connections, got %d", killed)
	}

	if !mock1.IsClosed() || !mock2.IsClosed() {
		t.Error("expected all connections to be closed")
	}

	if count := registry.Count(1); count != 0 {
		t.Errorf("expected 0 connections after drain, got %d", count)
	}
}

// TestRegistry_DrainEpoch_WithInflight tests draining with in-flight operations.
func TestRegistry_DrainEpoch_WithInflight(t *testing.T) {
	fakeClock := testkit.NewFakeClock(time.Now())
	registry := NewRegistry(fakeClock)

	mock := &mockConn{}
	conn := NewConnWrapper(mock, 1)

	// Simulate in-flight operation
	conn.IncInFlight()

	registry.Register(1, conn)

	// Start drain in background
	done := make(chan struct{})
	var drained, killed int
	go func() {
		ctx := context.Background()
		drained, killed = registry.DrainEpoch(ctx, 1, 100*time.Millisecond, true)
		close(done)
	}()

	// Let drain start
	time.Sleep(20 * time.Millisecond)

	// Advance time to trigger timeout
	fakeClock.Advance(150 * time.Millisecond)

	// Wait for drain to complete
	select {
	case <-done:
		// Expected
	case <-time.After(1 * time.Second):
		t.Fatal("drain did not complete")
	}

	// Should have been killed due to timeout
	if killed != 1 {
		t.Errorf("expected 1 killed connection, got %d", killed)
	}

	if drained != 0 {
		t.Errorf("expected 0 drained connections, got %d", drained)
	}

	if !mock.IsClosed() {
		t.Error("expected connection to be closed (killed)")
	}
}

// TestRegistry_DrainEpoch_BecomesIdle tests connection becoming idle during drain.
func TestRegistry_DrainEpoch_BecomesIdle(t *testing.T) {
	fakeClock := testkit.NewFakeClock(time.Now())
	registry := NewRegistry(fakeClock)

	mock := &mockConn{}
	conn := NewConnWrapper(mock, 1)

	// Simulate in-flight operation
	conn.IncInFlight()

	registry.Register(1, conn)

	// Start drain in background
	done := make(chan struct{})
	var drained, killed int
	go func() {
		ctx := context.Background()
		drained, killed = registry.DrainEpoch(ctx, 1, 1*time.Second, true)
		close(done)
	}()

	// Let drain start and check once
	time.Sleep(30 * time.Millisecond)

	// Complete the in-flight operation
	conn.DecInFlight()

	// Advance time slightly to trigger next check
	fakeClock.Advance(20 * time.Millisecond)

	// Wait for drain to complete
	select {
	case <-done:
		// Expected
	case <-time.After(2 * time.Second):
		t.Fatal("drain did not complete")
	}

	// Should have been drained gracefully
	if drained != 1 {
		t.Errorf("expected 1 drained connection, got %d", drained)
	}

	if killed != 0 {
		t.Errorf("expected 0 killed connections, got %d", killed)
	}

	if !mock.IsClosed() {
		t.Error("expected connection to be closed")
	}
}

// TestRegistry_DrainEpoch_NoForceKill tests drain without force kill.
func TestRegistry_DrainEpoch_NoForceKill(t *testing.T) {
	fakeClock := testkit.NewFakeClock(time.Now())
	registry := NewRegistry(fakeClock)

	mock := &mockConn{}
	conn := NewConnWrapper(mock, 1)

	// Simulate in-flight operation that never completes
	conn.IncInFlight()

	registry.Register(1, conn)

	// Start drain in background
	done := make(chan struct{})
	var drained, killed int
	go func() {
		ctx := context.Background()
		drained, killed = registry.DrainEpoch(ctx, 1, 50*time.Millisecond, false) // No force kill
		close(done)
	}()

	// Let drain start
	time.Sleep(20 * time.Millisecond)

	// Advance time to trigger timeout
	fakeClock.Advance(100 * time.Millisecond)

	// Wait for drain to complete
	select {
	case <-done:
		// Expected
	case <-time.After(1 * time.Second):
		t.Fatal("drain did not complete")
	}

	// Should have 0 drained and 0 killed (connection left hanging)
	if drained != 0 {
		t.Errorf("expected 0 drained connections, got %d", drained)
	}

	if killed != 0 {
		t.Errorf("expected 0 killed connections, got %d", killed)
	}

	// Connection should NOT be closed because forceKill=false
	if mock.IsClosed() {
		t.Error("expected connection to remain open when forceKill=false")
	}

	// But it should be unregistered
	if count := registry.Count(1); count != 0 {
		t.Errorf("expected connection to be unregistered, count=%d", count)
	}
}

// TestRegistry_DrainEpoch_ContextCancellation tests drain with context cancellation.
func TestRegistry_DrainEpoch_ContextCancellation(t *testing.T) {
	fakeClock := testkit.NewFakeClock(time.Now())
	registry := NewRegistry(fakeClock)

	mock := &mockConn{}
	conn := NewConnWrapper(mock, 1)

	conn.IncInFlight() // In-flight operation

	registry.Register(1, conn)

	ctx, cancel := context.WithCancel(context.Background())

	// Start drain in background
	done := make(chan struct{})
	var drained, killed int
	go func() {
		drained, killed = registry.DrainEpoch(ctx, 1, 5*time.Second, true)
		close(done)
	}()

	// Let drain start
	time.Sleep(20 * time.Millisecond)

	// Cancel context
	cancel()

	// Wait for drain to complete
	select {
	case <-done:
		// Expected
	case <-time.After(1 * time.Second):
		t.Fatal("drain did not complete after context cancellation")
	}

	// Should have been killed due to context cancellation
	if killed != 1 {
		t.Errorf("expected 1 killed connection, got %d", killed)
	}

	if drained != 0 {
		t.Errorf("expected 0 drained connections, got %d", drained)
	}
}

// TestRegistry_DrainEpoch_EmptyEpoch tests draining non-existent epoch.
func TestRegistry_DrainEpoch_EmptyEpoch(t *testing.T) {
	registry := NewRegistry(nil)

	ctx := context.Background()
	drained, killed := registry.DrainEpoch(ctx, 999, 1*time.Second, true)

	if drained != 0 {
		t.Errorf("expected 0 drained, got %d", drained)
	}

	if killed != 0 {
		t.Errorf("expected 0 killed, got %d", killed)
	}
}

// TestConnWrapper_InFlightCounter tests in-flight operation tracking.
func TestConnWrapper_InFlightCounter(t *testing.T) {
	conn := NewConnWrapper(&mockConn{}, 1)

	if conn.InFlight() != 0 {
		t.Errorf("expected 0 in-flight, got %d", conn.InFlight())
	}

	conn.IncInFlight()
	if conn.InFlight() != 1 {
		t.Errorf("expected 1 in-flight after inc, got %d", conn.InFlight())
	}

	conn.IncInFlight()
	if conn.InFlight() != 2 {
		t.Errorf("expected 2 in-flight after inc, got %d", conn.InFlight())
	}

	conn.DecInFlight()
	if conn.InFlight() != 1 {
		t.Errorf("expected 1 in-flight after dec, got %d", conn.InFlight())
	}

	conn.DecInFlight()
	if conn.InFlight() != 0 {
		t.Errorf("expected 0 in-flight after dec, got %d", conn.InFlight())
	}

	// Dec when already 0 should stay at 0
	conn.DecInFlight()
	if conn.InFlight() != 0 {
		t.Errorf("expected 0 in-flight after dec below 0, got %d", conn.InFlight())
	}
}

// TestConnWrapper_ConcurrentInflight tests concurrent in-flight operations.
func TestConnWrapper_ConcurrentInflight(t *testing.T) {
	conn := NewConnWrapper(&mockConn{}, 1)

	const goroutines = 100
	var wg sync.WaitGroup

	// Concurrent increments
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			conn.IncInFlight()
		}()
	}
	wg.Wait()

	// Verify increments worked
	if conn.InFlight() != goroutines {
		t.Fatalf("expected %d in-flight after increments, got %d", goroutines, conn.InFlight())
	}

	// Concurrent decrements
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			conn.DecInFlight()
		}()
	}
	wg.Wait()

	// Should end at 0
	if conn.InFlight() != 0 {
		t.Errorf("expected 0 in-flight after concurrent ops, got %d", conn.InFlight())
	}
}

// TestConnWrapper_Epoch tests epoch tracking.
func TestConnWrapper_Epoch(t *testing.T) {
	conn1 := NewConnWrapper(&mockConn{}, 42)
	conn2 := NewConnWrapper(&mockConn{}, 99)

	if conn1.Epoch() != 42 {
		t.Errorf("expected epoch 42, got %d", conn1.Epoch())
	}

	if conn2.Epoch() != 99 {
		t.Errorf("expected epoch 99, got %d", conn2.Epoch())
	}
}

// TestConnWrapper_Unwrap tests unwrapping underlying connection.
func TestConnWrapper_Unwrap(t *testing.T) {
	underlying := &mockConn{}
	wrapper := NewConnWrapper(underlying, 1)

	unwrapped := wrapper.Unwrap()
	if unwrapped != underlying {
		t.Error("expected unwrapped connection to match underlying")
	}
}
