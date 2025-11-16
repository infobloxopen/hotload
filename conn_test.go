package hotload

import (
	"context"
	"database/sql/driver"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestManagedConn_ThreadSafeReset(t *testing.T) {
	mc := managedConn{
		ctx:   nil,
		conn:  nil,
		reset: false,
		mu:    sync.RWMutex{},
	}

	// Lock the mutex
	mc.mu.Lock()
	writeLockAcquired := false
	readLockAcquired := false

	// Verify that neither Reset or GetReset can return while the managedConn's write lock is held
	go func() {
		mc.Reset(true)
		writeLockAcquired = true
	}()

	go func() {
		mc.GetReset()
		readLockAcquired = true
	}()

	// Check consistently for 100ms that locks are not acquired
	timeout := time.After(100 * time.Millisecond)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if writeLockAcquired {
				t.Fatal("Expected Reset() to block while write lock is held")
			}
			if readLockAcquired {
				t.Fatal("Expected GetReset() to block while write lock is held")
			}
		case <-timeout:
			mc.mu.Unlock()
			return
		}
	}
}

func TestManagedConn_NoGoroutineLeaks(t *testing.T) {
	// Force garbage collection and get baseline
	runtime.GC()
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	initialGoroutines := runtime.NumGoroutine()

	// Create a long-lived parent context that won't be cancelled
	parentCtx := context.Background()

	// Create managed connection
	mc := newManagedConn(parentCtx, "dsn", "redactDsn", mockDriverConn{}, nil)

	// Create many operations with different child contexts
	numOperations := 50

	for i := 0; i < numOperations; i++ {
		// Create short-lived contexts for each operation
		childCtx := context.Background()

		// Each call to ExecContext will create a merged context internally
		mc.ExecContext(childCtx, "INSERT INTO table (column) VALUES (?)", []driver.NamedValue{{Value: "value"}})
		mc.QueryContext(childCtx, "SELECT * FROM table WHERE column = ?", []driver.NamedValue{{Value: "value"}})
	}

	// Close the connection
	err := mc.Close()
	if err != nil {
		t.Fatalf("Expected no error closing connection but got: %v", err)
	}

	// Give time for any leaked goroutines to be detected
	time.Sleep(200 * time.Millisecond)
	runtime.GC()
	runtime.GC()

	// Check that we don't have significantly more goroutines than we started with
	finalGoroutines := runtime.NumGoroutine()

	if finalGoroutines > initialGoroutines+10 {
		t.Fatalf("Expected no significant goroutine leaks. Initial: %d, Final: %d, Diff: %d",
			initialGoroutines, finalGoroutines, finalGoroutines-initialGoroutines)
	}
}

func TestManagedConn_ContextCancellationExec(t *testing.T) {
	parentCtx, parentCancel := context.WithCancel(context.Background())
	mc := newManagedConn(parentCtx, "dsn", "redactDsn", mockDriverConn{}, nil)

	// Create a context that's already cancelled
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	// ExecContext should still work even with cancelled child context
	_, err := mc.ExecContext(cancelledCtx, "INSERT INTO table (column) VALUES (?)", []driver.NamedValue{{Value: "value"}})
	if err != nil {
		t.Fatalf("Expected no error but got: %v", err)
	}

	// Now cancel the parent context
	parentCancel()

	// Give some time for context cancellation to propagate
	time.Sleep(10 * time.Millisecond)

	// This should work but no goroutines should be leaked
	mc.ExecContext(context.Background(), "INSERT INTO table (column) VALUES (?)", []driver.NamedValue{{Value: "value"}})
}

func TestManagedConn_NoLeaksWithSlowOperations(t *testing.T) {
	// Force garbage collection and get baseline
	runtime.GC()
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	runtime.GC()
	initialGoroutines := runtime.NumGoroutine()

	// Create contexts that will be cancelled during operations
	parentCtx, parentCancel := context.WithCancel(context.Background())
	defer parentCancel()

	// Create managed connection with the slow mock
	mc := newManagedConn(parentCtx, "dsn", "redactDsn", mockSlowDriverConn{}, nil)

	// Create many concurrent operations
	var wg sync.WaitGroup
	numOperations := 100

	for i := 0; i < numOperations; i++ {
		wg.Add(2)

		go func() {
			defer wg.Done()
			timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer timeoutCancel()
			mc.ExecContext(timeoutCtx, "INSERT INTO table (column) VALUES (?)", []driver.NamedValue{{Value: "value"}})
		}()

		go func() {
			defer wg.Done()
			timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer timeoutCancel()
			mc.QueryContext(timeoutCtx, "SELECT * FROM table WHERE column = ?", []driver.NamedValue{{Value: "value"}})
		}()
	}

	wg.Wait()

	// Close the connection
	err := mc.Close()
	if err != nil {
		t.Fatalf("Expected no error but got: %v", err)
	}

	// Give time for cleanup
	time.Sleep(500 * time.Millisecond)
	runtime.GC()
	runtime.GC()

	finalGoroutines := runtime.NumGoroutine()

	if finalGoroutines > initialGoroutines+10 {
		t.Fatalf("Expected no significant goroutine leaks. Initial: %d, Final: %d, Diff: %d",
			initialGoroutines, finalGoroutines, finalGoroutines-initialGoroutines)
	}
}

func TestManagedConn_ContextCancellationQuery(t *testing.T) {
	parentCtx, parentCancel := context.WithCancel(context.Background())
	mc := newManagedConn(parentCtx, "dsn", "redactDsn", mockDriverConn{}, nil)

	// Create a context that's already cancelled
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	// QueryContext should still work
	_, err := mc.QueryContext(cancelledCtx, "SELECT * FROM table WHERE column = ?", []driver.NamedValue{{Value: "value"}})
	if err != nil {
		t.Fatalf("Expected no error but got: %v", err)
	}

	// Now cancel the parent context
	parentCancel()
	time.Sleep(10 * time.Millisecond)

	// This should work but no goroutines should be leaked
	mc.QueryContext(context.Background(), "SELECT * FROM table WHERE column = ?", []driver.NamedValue{{Value: "value"}})
}

/**** Mocks for driver ****/

type mockDriverConn struct{}
type mockTx struct{}
type mockSlowDriverConn struct{}

func (mockTx) Commit() error   { return nil }
func (mockTx) Rollback() error { return nil }

func (mockDriverConn) Prepare(query string) (driver.Stmt, error) { return nil, nil }
func (mockDriverConn) Begin() (driver.Tx, error)                 { return mockTx{}, nil }
func (mockDriverConn) Close() error                              { return nil }
func (mockDriverConn) IsValid() bool                             { return true }
func (mockDriverConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return mockTx{}, nil
}
func (mockDriverConn) Exec(query string, args []driver.Value) (driver.Result, error) { return nil, nil }
func (mockDriverConn) Query(query string, args []driver.Value) (driver.Rows, error)  { return nil, nil }
func (mockDriverConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return nil, nil
}
func (mockDriverConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return nil, nil
}

func (mockSlowDriverConn) Prepare(query string) (driver.Stmt, error) { return nil, nil }
func (mockSlowDriverConn) Begin() (driver.Tx, error)                 { return mockTx{}, nil }
func (mockSlowDriverConn) Close() error                              { return nil }
func (mockSlowDriverConn) IsValid() bool                             { return true }
func (mockSlowDriverConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return mockTx{}, nil
}
func (mockSlowDriverConn) Exec(query string, args []driver.Value) (driver.Result, error) {
	return nil, nil
}
func (mockSlowDriverConn) Query(query string, args []driver.Value) (driver.Rows, error) {
	return nil, nil
}

func (mockSlowDriverConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (mockSlowDriverConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
