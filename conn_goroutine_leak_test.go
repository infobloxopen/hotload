package hotload

import (
	"context"
	"database/sql/driver"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Goroutine Leak Test", func() {
	It("Should not leak goroutines when making repeated DB calls", func() {
		// Create a mock connection
		mc := newManagedConn(context.Background(), "dsn", "redactDsn", mockDriverConn{}, nil)

		// Force garbage collection and get baseline
		runtime.GC()
		time.Sleep(100 * time.Millisecond)
		baselineGoroutines := runtime.NumGoroutine()

		// Make many database calls to trigger context merges
		ctx := context.Background()
		numCalls := 1000

		for i := 0; i < numCalls; i++ {
			// These calls trigger onecontext.Merge
			mc.ExecContext(ctx, "INSERT INTO table VALUES (?)", []driver.NamedValue{{Value: "test"}})
			mc.QueryContext(ctx, "SELECT * FROM table", []driver.NamedValue{})
		}

		// Force garbage collection
		runtime.GC()
		time.Sleep(100 * time.Millisecond)

		afterGoroutines := runtime.NumGoroutine()
		goroutineDiff := afterGoroutines - baselineGoroutines

		// Log the results
		GinkgoWriter.Printf("Baseline goroutines: %d\n", baselineGoroutines)
		GinkgoWriter.Printf("After %d calls: %d goroutines\n", numCalls*2, afterGoroutines)
		GinkgoWriter.Printf("Goroutine difference: %d\n", goroutineDiff)

		// With the bug, we would see ~2000 leaked goroutines (1 per merge)
		// After the fix, goroutine growth should be minimal
		// Allow some tolerance for test framework overhead
		maxAcceptableGrowth := 50
		Expect(goroutineDiff).To(BeNumerically("<", maxAcceptableGrowth), 
			"Expected minimal goroutine growth (<%d), but got %d new goroutines after %d DB calls", 
			maxAcceptableGrowth, goroutineDiff, numCalls*2)
	})

	It("Should demonstrate the leak with long-lived connection context", func() {
		// Create a long-lived connection context (simulating a connection pool)
		connCtx, connCancel := context.WithCancel(context.Background())
		defer connCancel()

		mc := newManagedConn(connCtx, "dsn", "redactDsn", mockDriverConn{}, nil)

		runtime.GC()
		time.Sleep(100 * time.Millisecond)
		baselineGoroutines := runtime.NumGoroutine()

		// Simulate many short-lived requests
		numRequests := 500
		for i := 0; i < numRequests; i++ {
			// Each request has its own context
			reqCtx, reqCancel := context.WithTimeout(context.Background(), 5*time.Second)
			
			// These merge connCtx (long-lived) with reqCtx (short-lived)
			mc.ExecContext(reqCtx, "INSERT INTO table VALUES (?)", []driver.NamedValue{{Value: "test"}})
			mc.QueryContext(reqCtx, "SELECT * FROM table", []driver.NamedValue{})
			
			// Even though we cancel the request context, the merged context goroutine
			// won't be cleaned up because we didn't capture and call its cancel function
			reqCancel()
		}

		runtime.GC()
		time.Sleep(100 * time.Millisecond)

		afterGoroutines := runtime.NumGoroutine()
		goroutineDiff := afterGoroutines - baselineGoroutines

		GinkgoWriter.Printf("Baseline goroutines: %d\n", baselineGoroutines)
		GinkgoWriter.Printf("After %d requests: %d goroutines\n", numRequests, afterGoroutines)
		GinkgoWriter.Printf("Goroutine leak: %d\n", goroutineDiff)

		// With the bug, each merge creates a goroutine that won't be cleaned up
		// until BOTH contexts are cancelled. Since connCtx is long-lived, these accumulate.
		// After the fix, all merged contexts are properly cancelled via defer
		maxAcceptableGrowth := 50
		Expect(goroutineDiff).To(BeNumerically("<", maxAcceptableGrowth),
			"Expected minimal goroutine growth (<%d), but got %d leaked goroutines after %d requests",
			maxAcceptableGrowth, goroutineDiff, numRequests)
	})
})
