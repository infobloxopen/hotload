package engine

import (
	"sync"
	"testing"
	"time"

	"github.com/infobloxopen/hotload/internal/testkit"
)

// TestEngine_InitialState verifies the engine starts with epoch 0 and empty DSN.
func TestEngine_InitialState(t *testing.T) {
	fakeClock := testkit.NewFakeClock(time.Now())
	eng := New(Policy{}, Hooks{}, fakeClock)

	current := eng.Current()
	if current.Epoch != 0 {
		t.Errorf("expected initial epoch 0, got %d", current.Epoch)
	}
	if current.DSN != "" {
		t.Errorf("expected empty initial DSN, got %q", current.DSN)
	}
}

// TestEngine_FirstUpdate verifies the first DSN update creates epoch 1.
func TestEngine_FirstUpdate(t *testing.T) {
	fakeClock := testkit.NewFakeClock(time.Now())
	eng := New(Policy{}, Hooks{}, fakeClock)

	promoted, changed := eng.Update("postgres://localhost/db")
	if !changed {
		t.Fatal("expected first update to be promoted")
	}
	if promoted == nil {
		t.Fatal("expected promoted event")
	}
	if promoted.Epoch != 1 {
		t.Errorf("expected epoch 1, got %d", promoted.Epoch)
	}
	if promoted.DSN != "postgres://localhost/db" {
		t.Errorf("expected DSN to match, got %q", promoted.DSN)
	}

	current := eng.Current()
	if current.Epoch != 1 {
		t.Errorf("expected current epoch 1, got %d", current.Epoch)
	}
}

// TestEngine_Deduplication verifies identical DSNs are not promoted.
func TestEngine_Deduplication(t *testing.T) {
	fakeClock := testkit.NewFakeClock(time.Now())
	eng := New(Policy{}, Hooks{}, fakeClock)

	// First update - should promote
	_, changed := eng.Update("postgres://localhost/db")
	if !changed {
		t.Fatal("expected first update to be promoted")
	}

	// Same DSN - should deduplicate
	promoted, changed := eng.Update("postgres://localhost/db")
	if changed {
		t.Error("expected duplicate DSN to not be promoted")
	}
	if promoted != nil {
		t.Error("expected nil event for duplicate")
	}

	current := eng.Current()
	if current.Epoch != 1 {
		t.Errorf("expected epoch to remain 1, got %d", current.Epoch)
	}
}

// TestEngine_MultipleUpdates verifies epoch increments correctly.
func TestEngine_MultipleUpdates(t *testing.T) {
	fakeClock := testkit.NewFakeClock(time.Now())
	eng := New(Policy{}, Hooks{}, fakeClock)

	dsns := []string{
		"postgres://localhost/db1",
		"postgres://localhost/db2",
		"postgres://localhost/db3",
	}

	for i, dsn := range dsns {
		promoted, changed := eng.Update(dsn)
		if !changed {
			t.Errorf("update %d: expected promotion", i)
			continue
		}
		expectedEpoch := uint64(i + 1)
		if promoted.Epoch != expectedEpoch {
			t.Errorf("update %d: expected epoch %d, got %d", i, expectedEpoch, promoted.Epoch)
		}
		if promoted.DSN != dsn {
			t.Errorf("update %d: expected DSN %q, got %q", i, dsn, promoted.DSN)
		}
	}

	current := eng.Current()
	if current.Epoch != 3 {
		t.Errorf("expected final epoch 3, got %d", current.Epoch)
	}
	if current.DSN != dsns[2] {
		t.Errorf("expected final DSN %q, got %q", dsns[2], current.DSN)
	}
}

// TestEngine_Debounce verifies rapid updates are suppressed during debounce window.
func TestEngine_Debounce(t *testing.T) {
	fakeClock := testkit.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	policy := Policy{
		Debounce: 5 * time.Second,
	}
	eng := New(policy, Hooks{}, fakeClock)

	// First update - should promote
	promoted, changed := eng.Update("dsn-v1")
	if !changed {
		t.Fatal("expected first update to promote")
	}
	if promoted.Epoch != 1 {
		t.Errorf("expected epoch 1, got %d", promoted.Epoch)
	}

	// Advance time by 2 seconds (within debounce window)
	fakeClock.Advance(2 * time.Second)

	// Second update (different DSN) - should be debounced
	promoted, changed = eng.Update("dsn-v2")
	if changed {
		t.Error("expected update to be debounced")
	}
	if promoted != nil {
		t.Error("expected nil event during debounce")
	}

	current := eng.Current()
	if current.Epoch != 1 {
		t.Errorf("expected epoch to remain 1, got %d", current.Epoch)
	}
	if current.DSN != "dsn-v1" {
		t.Errorf("expected DSN to remain 'dsn-v1', got %q", current.DSN)
	}

	// Advance time past original debounce window (but debounce was extended)
	fakeClock.Advance(4 * time.Second) // Total: 6 seconds from first update

	// The debounce window was extended when we tried dsn-v2, so still debounced
	promoted, changed = eng.Update("dsn-v3")
	if changed {
		t.Error("expected update to still be debounced (window was extended)")
	}

	// Advance past the extended debounce window
	fakeClock.Advance(5 * time.Second) // Now past extension

	// Now should promote
	promoted, changed = eng.Update("dsn-v2")
	if !changed {
		t.Fatal("expected update after debounce to promote")
	}
	if promoted.Epoch != 2 {
		t.Errorf("expected epoch 2, got %d", promoted.Epoch)
	}
	if promoted.DSN != "dsn-v2" {
		t.Errorf("expected DSN 'dsn-v2', got %q", promoted.DSN)
	}
}

// TestEngine_DebounceExtension verifies debounce window extends on rapid updates.
func TestEngine_DebounceExtension(t *testing.T) {
	fakeClock := testkit.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	policy := Policy{
		Debounce: 5 * time.Second,
	}
	eng := New(policy, Hooks{}, fakeClock)

	// First update
	eng.Update("dsn-v1")

	// Rapid updates every 2 seconds (should keep extending debounce)
	for i := 0; i < 3; i++ {
		fakeClock.Advance(2 * time.Second)
		_, changed := eng.Update("dsn-v2")
		if changed {
			t.Errorf("update %d: expected to be debounced", i)
		}
	}

	// Total time: 6 seconds, but debounce should have extended
	current := eng.Current()
	if current.Epoch != 1 {
		t.Errorf("expected epoch to remain 1 due to debounce extension, got %d", current.Epoch)
	}

	// Wait for full debounce period from last attempt
	fakeClock.Advance(5 * time.Second)

	// Now should promote
	promoted, changed := eng.Update("dsn-v2")
	if !changed {
		t.Fatal("expected update to promote after debounce")
	}
	if promoted.Epoch != 2 {
		t.Errorf("expected epoch 2, got %d", promoted.Epoch)
	}
}

// TestEngine_OnPromoteHook verifies OnPromote callback is invoked.
func TestEngine_OnPromoteHook(t *testing.T) {
	fakeClock := testkit.NewFakeClock(time.Now())

	var mu sync.Mutex
	var promotedEpochs []uint64
	hooks := Hooks{
		OnPromote: func(ev Event) {
			mu.Lock()
			promotedEpochs = append(promotedEpochs, ev.Epoch)
			mu.Unlock()
		},
	}

	eng := New(Policy{}, hooks, fakeClock)

	// Multiple updates
	dsns := []string{"dsn-v1", "dsn-v2", "dsn-v3"}
	for _, dsn := range dsns {
		eng.Update(dsn)
	}

	// Wait for async hooks to complete
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	count := len(promotedEpochs)
	mu.Unlock()

	if count != 3 {
		t.Errorf("expected 3 OnPromote calls, got %d", count)
	}

	// Verify epochs were called (order may vary due to async)
	mu.Lock()
	defer mu.Unlock()

	epochMap := make(map[uint64]bool)
	for _, epoch := range promotedEpochs {
		epochMap[epoch] = true
	}

	for i := uint64(1); i <= 3; i++ {
		if !epochMap[i] {
			t.Errorf("expected epoch %d to be promoted", i)
		}
	}
}

// TestEngine_OnErrorHook verifies OnError is called when OnPromote panics.
func TestEngine_OnErrorHook(t *testing.T) {
	fakeClock := testkit.NewFakeClock(time.Now())

	var mu sync.Mutex
	var capturedError error
	hooks := Hooks{
		OnPromote: func(ev Event) {
			panic("intentional panic for testing")
		},
		OnError: func(err error) {
			mu.Lock()
			capturedError = err
			mu.Unlock()
		},
	}

	eng := New(Policy{}, hooks, fakeClock)
	eng.Update("dsn-v1")

	// Wait for async hook and error handler
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if capturedError == nil {
		t.Fatal("expected OnError to be called when OnPromote panics")
	}
}

// TestEngine_PolicyRetrieval verifies Policy() returns the configured policy.
func TestEngine_PolicyRetrieval(t *testing.T) {
	policy := Policy{
		DrainTimeout: 10 * time.Second,
		ForceKill:    true,
		Debounce:     2 * time.Second,
		Preconnect:   true,
	}

	eng := New(policy, Hooks{}, nil)
	retrieved := eng.Policy()

	if retrieved.DrainTimeout != policy.DrainTimeout {
		t.Errorf("expected DrainTimeout %v, got %v", policy.DrainTimeout, retrieved.DrainTimeout)
	}
	if retrieved.ForceKill != policy.ForceKill {
		t.Errorf("expected ForceKill %v, got %v", policy.ForceKill, retrieved.ForceKill)
	}
	if retrieved.Debounce != policy.Debounce {
		t.Errorf("expected Debounce %v, got %v", policy.Debounce, retrieved.Debounce)
	}
	if retrieved.Preconnect != policy.Preconnect {
		t.Errorf("expected Preconnect %v, got %v", policy.Preconnect, retrieved.Preconnect)
	}
}

// TestEngine_ConcurrentUpdates verifies thread-safety of concurrent updates.
func TestEngine_ConcurrentUpdates(t *testing.T) {
	fakeClock := testkit.NewFakeClock(time.Now())
	eng := New(Policy{}, Hooks{}, fakeClock)

	var wg sync.WaitGroup
	updateCount := 50

	// Launch concurrent updates
	for i := 0; i < updateCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			dsn := "dsn-v" + string(rune('A'+id%10))
			eng.Update(dsn)
		}(i)
	}

	wg.Wait()

	// Should have some epoch > 0 (exact number depends on deduplication)
	current := eng.Current()
	if current.Epoch == 0 {
		t.Error("expected epoch to have incremented after concurrent updates")
	}
}

// TestEngine_TimeProgression verifies timestamps advance with clock.
func TestEngine_TimeProgression(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	fakeClock := testkit.NewFakeClock(startTime)
	eng := New(Policy{}, Hooks{}, fakeClock)

	// First update
	promoted1, _ := eng.Update("dsn-v1")
	if !promoted1.At.Equal(startTime) {
		t.Errorf("expected first event at %v, got %v", startTime, promoted1.At)
	}

	// Advance clock
	fakeClock.Advance(10 * time.Second)
	expectedTime := startTime.Add(10 * time.Second)

	// Second update
	promoted2, _ := eng.Update("dsn-v2")
	if !promoted2.At.Equal(expectedTime) {
		t.Errorf("expected second event at %v, got %v", expectedTime, promoted2.At)
	}
}

// TestEngine_ZeroDebounce verifies no debouncing occurs when Debounce is 0.
func TestEngine_ZeroDebounce(t *testing.T) {
	fakeClock := testkit.NewFakeClock(time.Now())
	policy := Policy{
		Debounce: 0, // No debounce
	}
	eng := New(policy, Hooks{}, fakeClock)

	// Rapid sequential updates should all promote
	for i := 1; i <= 5; i++ {
		dsn := "dsn-v" + string(rune('0'+i))
		promoted, changed := eng.Update(dsn)
		if !changed {
			t.Errorf("update %d: expected promotion with zero debounce", i)
		}
		if promoted.Epoch != uint64(i) {
			t.Errorf("update %d: expected epoch %d, got %d", i, i, promoted.Epoch)
		}
	}
}
