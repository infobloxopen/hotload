package testkit_test

import (
	"context"
	"testing"
	"time"

	"github.com/infobloxopen/hotload/internal/testkit"
)

// TestFakeStrategy demonstrates basic FakeStrategy usage
func TestFakeStrategy(t *testing.T) {
	strategy := testkit.NewFakeStrategy()
	strategy.SetInitialValue("/path/to/file", "initial-dsn")

	ctx := context.Background()
	value, updates, err := strategy.Watch(ctx, "/path/to/file", map[string][]string{})
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	if value != "initial-dsn" {
		t.Errorf("expected initial value 'initial-dsn', got %q", value)
	}

	// Send update
	go strategy.SendUpdate("/path/to/file", "updated-dsn")

	select {
	case updated := <-updates:
		if updated != "updated-dsn" {
			t.Errorf("expected update 'updated-dsn', got %q", updated)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for update")
	}

	// Verify call tracking
	calls := strategy.WatchCalls()
	if len(calls) != 1 {
		t.Errorf("expected 1 watch call, got %d", len(calls))
	}
	if calls[0].Path != "/path/to/file" {
		t.Errorf("expected path '/path/to/file', got %q", calls[0].Path)
	}
}

// TestFakeClock demonstrates FakeClock usage for testing time-dependent code
func TestFakeClock(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	fakeClock := testkit.NewFakeClock(startTime)

	// Verify initial time
	if !fakeClock.Now().Equal(startTime) {
		t.Errorf("expected start time %v, got %v", startTime, fakeClock.Now())
	}

	// Create a timer
	timerCh := fakeClock.After(5 * time.Second)

	// Wait for timer to be registered
	time.Sleep(10 * time.Millisecond)

	// Advance time but not enough
	fakeClock.Advance(3 * time.Second)

	// Should not have fired yet
	select {
	case <-timerCh:
		t.Error("timer should not have fired yet")
	case <-time.After(10 * time.Millisecond):
		// Expected - no fire yet
	}

	// Advance time past the trigger
	fakeClock.Advance(3 * time.Second)

	// Should fire now
	select {
	case <-timerCh:
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Error("timer should have fired")
	}

	expectedTime := startTime.Add(6 * time.Second)
	if !fakeClock.Now().Equal(expectedTime) {
		t.Errorf("expected time %v, got %v", expectedTime, fakeClock.Now())
	}
}

// TestFakeDriver demonstrates FakeDriver with different capability combinations
func TestFakeDriver(t *testing.T) {
	tests := []struct {
		name       string
		execCtx    bool
		queryCtx   bool
		expectSkip bool
	}{
		{"with context support", true, true, false},
		{"without context support", false, false, true},
		{"partial support", true, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			drv := testkit.NewFakeDriverWithCapabilities(
				tt.execCtx,  // ExecContext
				tt.queryCtx, // QueryContext
				false,       // BeginTx
				false,       // PrepareContext
				false,       // Ping
				false,       // SessionResetter
				false,       // NamedValueChecker
			)

			conn, err := drv.Open("test-dsn")
			if err != nil {
				t.Fatalf("Open failed: %v", err)
			}
			defer conn.Close()

			if drv.OpenCalls != 1 {
				t.Errorf("expected 1 open call, got %d", drv.OpenCalls)
			}
		})
	}
}
