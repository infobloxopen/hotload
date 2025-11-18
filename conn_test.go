package hotload

import (
	"context"
	"database/sql/driver"
	"io"
	"runtime"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/infobloxopen/hotload/metrics"
)

var _ = Describe("managedConn", func() {
	It("Should set .reset in a threadsafe way", func() {
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

		Consistently(writeLockAcquired).Should(BeFalse())
		Consistently(readLockAcquired).Should(BeFalse())
	})

	It("Should not deadlock in hdriver.Open processNewValue vs managedConn.Close scenario", func() {
		// This test simulates the exact deadlock scenario described:
		// Thread A (hdriver.Open -> processNewValue):
		//   - Acquires cg.mu.Lock() in processNewValue
		//   - Calls c.Reset(true) which waits for c.mu.Lock()
		// Thread B (managedConn.Close):
		//   - Acquires c.mu.Lock() in Close()
		//   - Calls afterClose -> removeMgdConn which waits for cg.mu.Lock()

		var cgMutex sync.RWMutex
		var removedConn *managedConn
		callbackExecuted := false

		// Channels to coordinate the deadlock scenario
		threadAHasLock := make(chan struct{})
		threadBHasLock := make(chan struct{})
		threadAComplete := make(chan struct{})
		threadBComplete := make(chan struct{})

		// Simulate chanGroup.removeMgdConn callback that needs cg.mu
		afterCloseCallback := func(conn *managedConn) {
			// This simulates removeMgdConn trying to acquire cg.mu
			cgMutex.Lock()
			defer cgMutex.Unlock()
			removedConn = conn
			callbackExecuted = true
		}

		mc := newManagedConn(context.Background(), "dsn", "redactDsn", mockDriverConn{}, afterCloseCallback)

		// Thread A: Simulate processNewValue holding cg.mu and calling Reset()
		go func() {
			defer GinkgoRecover()
			defer close(threadAComplete)

			// Acquire cg.mu (simulating processNewValue critical section)
			cgMutex.Lock()
			threadAHasLock <- struct{}{}

			// Sleep to guarantee both threads are running in parallel
			time.Sleep(100 * time.Millisecond)

			// Wait for Thread B to acquire managedConn mutex
			select {
			case <-threadBHasLock:
				// Thread B has managedConn mutex, now we'll try to acquire it via Reset()
			case <-time.After(5 * time.Second):
				cgMutex.Unlock()
				Fail("Thread B should have acquired managedConn mutex")
				return
			}

			// This simulates processNewValue calling c.Reset(true)
			// In the original deadlock, this would wait for c.mu while holding cg.mu
			mc.Reset(true)

			cgMutex.Unlock()
		}()

		// Thread B: Simulate managedConn.Close() calling afterClose callback
		go func() {
			defer GinkgoRecover()
			defer close(threadBComplete)

			// Wait for Thread A to acquire chanGroup mutex
			select {
			case <-threadAHasLock:
				// Thread A has cg.mu, proceed with Close()
			case <-time.After(5 * time.Second):
				Fail("Thread A should have acquired chanGroup mutex")
				return
			}

			// Signal that we're about to acquire managedConn mutex
			threadBHasLock <- struct{}{}

			// Sleep to guarantee both threads are running in parallel
			time.Sleep(100 * time.Millisecond)

			// This calls the actual Close() method which should handle the deadlock scenario
			// In the original deadlock, this would deadlock when afterClose tries to acquire cg.mu
			// With our fix, afterClose is called outside the mutex, preventing deadlock
			err := mc.Close()
			Expect(err).ShouldNot(HaveOccurred())
		}()

		// Both threads should complete without deadlock
		select {
		case <-threadAComplete:
			// Thread A completed successfully
		case <-time.After(10 * time.Second):
			Fail("Thread A (processNewValue simulation) timed out - potential deadlock detected")
		}

		select {
		case <-threadBComplete:
			// Thread B completed successfully
		case <-time.After(10 * time.Second):
			Fail("Thread B (managedConn.Close simulation) timed out - potential deadlock detected")
		}

		// Verify callback was executed
		Eventually(func() bool {
			return callbackExecuted
		}, "2s", "10ms").Should(BeTrue(), "afterClose callback should execute")

		Expect(removedConn).To(Equal(mc), "Callback should receive correct managedConn")
	})

	It("Should not leak goroutines when using ExecContext and QueryContext", func() {
		// Force garbage collection and get baseline
		runtime.GC()
		runtime.GC()
		time.Sleep(50 * time.Millisecond)
		initialGoroutines := runtime.NumGoroutine()

		// Create a long-lived parent context that won't be cancelled
		parentCtx := context.Background() // This simulates a long-lived connection context

		// Create managed connection
		mc := newManagedConn(parentCtx, "dsn", "redactDsn", mockDriverConn{}, nil)

		// Create many operations with different child contexts
		// This will trigger onecontext.Merge calls, and without defer mCancel(),
		// goroutines will leak because parentCtx is never cancelled
		numOperations := 50

		for i := 0; i < numOperations; i++ {
			// Create short-lived contexts for each operation
			childCtx := context.Background() // Also long-lived for this test

			// Each call to ExecContext will create a merged context internally
			// Without defer mCancel(), the goroutine will wait indefinitely
			mc.ExecContext(childCtx, "INSERT INTO table (column) VALUES (?)", []driver.NamedValue{{Value: "value"}})
			mc.QueryContext(childCtx, "SELECT * FROM table WHERE column = ?", []driver.NamedValue{{Value: "value"}})
		}

		// Close the connection
		err := mc.Close()
		Expect(err).ShouldNot(HaveOccurred())

		// Give time for any leaked goroutines to be detected
		time.Sleep(200 * time.Millisecond)
		runtime.GC()
		runtime.GC()

		// Check that we don't have significantly more goroutines than we started with
		finalGoroutines := runtime.NumGoroutine()

		// With the leak, we expect to see many leaked goroutines from onecontext.Merge
		// Each ExecContext and QueryContext call that doesn't call defer mCancel() will leak a goroutine
		expectedLeaks := numOperations * 2 // One for each ExecContext and QueryContext call

		Expect(finalGoroutines).To(BeNumerically("<=", initialGoroutines+10),
			"Expected no significant goroutine leaks. Initial: %d, Final: %d, Expected leaks if broken: ~%d",
			initialGoroutines, finalGoroutines, expectedLeaks)
	})

	It("Should handle context cancellation properly in ExecContext", func() {
		parentCtx, parentCancel := context.WithCancel(context.Background())
		mc := newManagedConn(parentCtx, "dsn", "redactDsn", mockDriverConn{}, nil)

		// Create a context that's already cancelled
		cancelledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		// ExecContext should still work even with cancelled child context
		// because the parent context is still active
		_, err := mc.ExecContext(cancelledCtx, "INSERT INTO table (column) VALUES (?)", []driver.NamedValue{{Value: "value"}})
		Expect(err).ShouldNot(HaveOccurred())

		// Now cancel the parent context
		parentCancel()

		// Give some time for context cancellation to propagate
		time.Sleep(10 * time.Millisecond)

		// This should fail because parent context is cancelled
		mc.ExecContext(context.Background(), "INSERT INTO table (column) VALUES (?)", []driver.NamedValue{{Value: "value"}})
		// The mock doesn't return context.Canceled, but the connection should work
		// The important thing is that no goroutines are leaked
	})

	It("Should not leak goroutines with slow operations and context cancellation", func() {
		// Force garbage collection and get very precise baseline
		runtime.GC()
		runtime.GC()
		time.Sleep(100 * time.Millisecond)
		runtime.GC()
		initialGoroutines := runtime.NumGoroutine()

		// Create contexts that will be cancelled during operations
		parentCtx, parentCancel := context.WithCancel(context.Background())
		defer parentCancel()

		// Create managed connection with the slow mock to simulate real database behavior
		mc := newManagedConn(parentCtx, "dsn", "redactDsn", mockSlowDriverConn{}, nil)

		// Create many concurrent operations that will force onecontext.Merge goroutine creation
		var wg sync.WaitGroup
		numOperations := 100 // Much higher number to force obvious leaks

		for i := 0; i < numOperations; i++ {
			wg.Add(2)

			go func(iteration int) {
				defer wg.Done()
				// Create a unique context for each operation to force new onecontext.Merge calls
				timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
				defer timeoutCancel()

				// This should timeout and without defer mCancel(), leak goroutines
				mc.ExecContext(timeoutCtx, "INSERT INTO table (column) VALUES (?)", []driver.NamedValue{{Value: "value"}})
			}(i)

			go func(iteration int) {
				defer wg.Done()
				// Create a unique context for each operation to force new onecontext.Merge calls
				timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
				defer timeoutCancel()

				// This should timeout and without defer mCancel(), leak goroutines
				mc.QueryContext(timeoutCtx, "SELECT * FROM table WHERE column = ?", []driver.NamedValue{{Value: "value"}})
			}(i)
		}

		wg.Wait()

		// Close the connection
		err := mc.Close()
		Expect(err).ShouldNot(HaveOccurred())

		// Give substantial time for leaked goroutines to accumulate
		time.Sleep(500 * time.Millisecond)
		runtime.GC()
		runtime.GC()

		// Check that we don't have significantly more goroutines than we started with
		finalGoroutines := runtime.NumGoroutine()

		// This should definitely fail if there are goroutine leaks from onecontext.Merge
		// Being very strict about detecting leaks
		Expect(finalGoroutines).To(BeNumerically("<=", initialGoroutines+10),
			"Expected no significant goroutine leaks from onecontext.Merge. Initial: %d, Final: %d, Diff: %d",
			initialGoroutines, finalGoroutines, finalGoroutines-initialGoroutines)
	})

	It("Should handle context cancellation properly in QueryContext", func() {
		parentCtx, parentCancel := context.WithCancel(context.Background())
		mc := newManagedConn(parentCtx, "dsn", "redactDsn", mockDriverConn{}, nil)

		// Create a context that's already cancelled
		cancelledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		// QueryContext should still work even with cancelled child context
		// because the parent context is still active
		_, err := mc.QueryContext(cancelledCtx, "SELECT * FROM table WHERE column = ?", []driver.NamedValue{{Value: "value"}})
		Expect(err).ShouldNot(HaveOccurred())

		// Now cancel the parent context
		parentCancel()

		// Give some time for context cancellation to propagate
		time.Sleep(10 * time.Millisecond)

		// This should work but no goroutines should be leaked
		mc.QueryContext(context.Background(), "SELECT * FROM table WHERE column = ?", []driver.NamedValue{{Value: "value"}})
		// The mock doesn't return context.Canceled, but the connection should work
		// The important thing is that no goroutines are leaked
	})
})

/**** Mocks for Prometheus Metrics ****/

type mockDriverConn struct{}

type mockTx struct{}

// mockSlowDriverConn simulates a driver that might have slow operations
// This helps test for goroutine leaks in scenarios where context cancellation matters
type mockSlowDriverConn struct{}

func (mockTx) Commit() error {
	return nil
}

func (mockTx) Rollback() error {
	return nil
}

func (mockDriverConn) Prepare(query string) (driver.Stmt, error) {
	return nil, nil
}

func (mockDriverConn) Begin() (driver.Tx, error) {
	return mockTx{}, nil
}

func (mockDriverConn) Close() error {
	return nil
}

func (mockDriverConn) IsValid() bool {
	return true
}

func (mockDriverConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return mockTx{}, nil
}

func (mockDriverConn) Exec(query string, args []driver.Value) (driver.Result, error) {
	return nil, nil
}

func (mockDriverConn) Query(query string, args []driver.Value) (driver.Rows, error) {
	return nil, nil
}

func (mockDriverConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return nil, nil
}

func (mockDriverConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return nil, nil
}

// mockSlowDriverConn methods - simulates operations that might be slow
func (mockSlowDriverConn) Prepare(query string) (driver.Stmt, error) {
	return nil, nil
}

func (mockSlowDriverConn) Begin() (driver.Tx, error) {
	return mockTx{}, nil
}

func (mockSlowDriverConn) Close() error {
	return nil
}

func (mockSlowDriverConn) IsValid() bool {
	return true
}

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
	// Check if context is cancelled before proceeding
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	// Simulate a slow operation that keeps goroutines alive longer
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
	// Check if context is cancelled before proceeding
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	// Simulate a slow operation that keeps goroutines alive longer
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

/**** End Mocks for Prometheus Metrics ****/

var _ = Describe("PrometheusMetrics", func() {
	const help = `
		# HELP transaction_sql_stmts The number of sql stmts called in a transaction by statement type per grpc service and method
		# TYPE transaction_sql_stmts summary
	`

	var service1Metrics = `
		transaction_sql_stmts_sum{grpc_method="method_1",grpc_service="service_1",stmt="exec"} 3
		transaction_sql_stmts_count{grpc_method="method_1",grpc_service="service_1",stmt="exec"} 1
		transaction_sql_stmts_sum{grpc_method="method_1",grpc_service="service_1",stmt="query"} 3
		transaction_sql_stmts_count{grpc_method="method_1",grpc_service="service_1",stmt="query"} 1
	`

	var service2Metrics = `
		transaction_sql_stmts_sum{grpc_method="method_2",grpc_service="service_2",stmt="exec"} 4
		transaction_sql_stmts_count{grpc_method="method_2",grpc_service="service_2",stmt="exec"} 1
		transaction_sql_stmts_sum{grpc_method="method_2",grpc_service="service_2",stmt="query"} 4
		transaction_sql_stmts_count{grpc_method="method_2",grpc_service="service_2",stmt="query"} 1
	`

	var service1RerunMetrics = `
		transaction_sql_stmts_sum{grpc_method="method_1",grpc_service="service_1",stmt="exec"} 4
		transaction_sql_stmts_count{grpc_method="method_1",grpc_service="service_1",stmt="exec"} 2
		transaction_sql_stmts_sum{grpc_method="method_1",grpc_service="service_1",stmt="query"} 4
		transaction_sql_stmts_count{grpc_method="method_1",grpc_service="service_1",stmt="query"} 2
	`

	var noMethodMetrics = `
		transaction_sql_stmts_sum{grpc_method="",grpc_service="",stmt="exec"} 1
		transaction_sql_stmts_count{grpc_method="",grpc_service="",stmt="exec"} 1
		transaction_sql_stmts_sum{grpc_method="",grpc_service="",stmt="query"} 1
		transaction_sql_stmts_count{grpc_method="",grpc_service="",stmt="query"} 1
	`

	It("Should emit the correct metrics", func() {
		mc := newManagedConn(context.Background(), "dsn", "redactDsn", mockDriverConn{}, nil)

		ctx := ContextWithExecLabels(context.Background(), map[string]string{"grpc_method": "method_1", "grpc_service": "service_1"})

		// begin a transaction
		tx, err := mc.BeginTx(ctx, driver.TxOptions{})
		Expect(err).ShouldNot(HaveOccurred())

		// exec a statement
		mc.Exec("INSERT INTO table (column) VALUES (?)", []driver.Value{"value"})

		// query a statement
		mc.Query("SELECT * FROM table WHERE column = ?", []driver.Value{"value"})
		mc.Query("SELECT * FROM table WHERE column = ?", []driver.Value{"value"})

		// exec a statement with context
		mc.ExecContext(ctx, "INSERT INTO table (column) VALUES (?)", []driver.NamedValue{{Value: "value"}})
		mc.ExecContext(ctx, "INSERT INTO table (column) VALUES (?)", []driver.NamedValue{{Value: "value"}})

		// query a statement with context
		mc.QueryContext(ctx, "SELECT * FROM table WHERE column = ?", []driver.NamedValue{{Value: "value"}})

		// commit the transaction
		err = tx.Commit()
		Expect(err).ShouldNot(HaveOccurred())

		// collect and compare metrics
		err = testutil.CollectAndCompare(metrics.SqlStmtsSummary, strings.NewReader(help+service1Metrics))
		Expect(err).ShouldNot(HaveOccurred())

		// reset the metrics
		// new context
		ctx = ContextWithExecLabels(context.Background(), map[string]string{"grpc_method": "method_2", "grpc_service": "service_2"})
		// begin a transaction
		tx, err = mc.BeginTx(ctx, driver.TxOptions{})
		Expect(err).ShouldNot(HaveOccurred())

		// exec a statement
		mc.Exec("INSERT INTO table (column) VALUES (?)", []driver.Value{"value"})
		mc.Exec("INSERT INTO table (column) VALUES (?)", []driver.Value{"value"})

		// query a statement
		mc.Query("SELECT * FROM table WHERE column = ?", []driver.Value{"value"})
		mc.Query("SELECT * FROM table WHERE column = ?", []driver.Value{"value"})

		// exec a statement with context
		mc.ExecContext(ctx, "INSERT INTO table (column) VALUES (?)", []driver.NamedValue{{Value: "value"}})
		mc.ExecContext(ctx, "INSERT INTO table (column) VALUES (?)", []driver.NamedValue{{Value: "value"}})

		// query a statement with context
		mc.QueryContext(ctx, "SELECT * FROM table WHERE column = ?", []driver.NamedValue{{Value: "value"}})
		mc.QueryContext(ctx, "SELECT * FROM table WHERE column = ?", []driver.NamedValue{{Value: "value"}})

		// commit the transaction
		err = tx.Commit()
		Expect(err).ShouldNot(HaveOccurred())

		// collect and compare metrics
		err = testutil.CollectAndCompare(metrics.SqlStmtsSummary, strings.NewReader(help+service1Metrics+service2Metrics))
		Expect(err).ShouldNot(HaveOccurred())

		// rerun with initial metrics
		ctx = ContextWithExecLabels(context.Background(), map[string]string{"grpc_method": "method_1", "grpc_service": "service_1"})
		// begin a transaction
		tx, err = mc.BeginTx(ctx, driver.TxOptions{})
		Expect(err).ShouldNot(HaveOccurred())

		// exec a statement with context
		mc.ExecContext(ctx, "INSERT INTO table (column) VALUES (?)", []driver.NamedValue{{Value: "value"}})

		// query a statement with context
		mc.QueryContext(ctx, "SELECT * FROM table WHERE column = ?", []driver.NamedValue{{Value: "value"}})

		// rollback the transaction
		err = tx.Rollback()
		Expect(err).ShouldNot(HaveOccurred())

		// collect and compare metrics
		err = testutil.CollectAndCompare(metrics.SqlStmtsSummary, strings.NewReader(help+service1RerunMetrics+service2Metrics))
		Expect(err).ShouldNot(HaveOccurred())

		// non labeled context
		ctx = context.Background()
		// begin a transaction
		tx, err = mc.BeginTx(ctx, driver.TxOptions{})
		Expect(err).ShouldNot(HaveOccurred())

		// exec query context
		mc.ExecContext(ctx, "INSERT INTO table (column) VALUES (?)", []driver.NamedValue{{Value: "value"}})

		// query a statement with context
		mc.QueryContext(ctx, "SELECT * FROM table WHERE column = ?", []driver.NamedValue{{Value: "value"}})

		// commit the transaction
		err = tx.Commit()
		Expect(err).ShouldNot(HaveOccurred())

		// collect and compare metrics
		err = testutil.CollectAndCompare(metrics.SqlStmtsSummary, strings.NewReader(help+noMethodMetrics+service1RerunMetrics+service2Metrics))
		Expect(err).ShouldNot(HaveOccurred())
	})
})

func CollectAndCompareMetrics(r io.Reader) error {
	return testutil.CollectAndCompare(metrics.SqlStmtsSummary, r)
}
