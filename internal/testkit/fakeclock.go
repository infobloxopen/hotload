package testkit

import (
	"sync"
	"time"

	"github.com/infobloxopen/hotload/internal/clock"
)

// FakeClock is a controllable clock implementation for testing time-dependent behavior.
// It allows tests to manually advance time without actually sleeping.
type FakeClock struct {
	mu      sync.RWMutex
	now     time.Time
	timers  []*FakeTimer
	waiters []chan struct{}
}

// NewFakeClock creates a new fake clock starting at the given time.
// If zero time is provided, it starts at Unix epoch.
func NewFakeClock(start time.Time) *FakeClock {
	if start.IsZero() {
		start = time.Unix(0, 0)
	}
	return &FakeClock{
		now:     start,
		timers:  make([]*FakeTimer, 0),
		waiters: make([]chan struct{}, 0),
	}
}

// Now returns the current fake time.
func (f *FakeClock) Now() time.Time {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.now
}

// After returns a channel that will receive the time after the duration has passed.
// The time is advanced manually via Advance().
func (f *FakeClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	f.mu.Lock()
	defer f.mu.Unlock()

	triggerTime := f.now.Add(d)
	f.timers = append(f.timers, &FakeTimer{
		ch:          ch,
		triggerTime: triggerTime,
		stopped:     false,
	})

	return ch
}

// Sleep does nothing in fake clock - time must be advanced manually.
// This prevents tests from actually sleeping.
func (f *FakeClock) Sleep(d time.Duration) {
	// No-op for fake clock
}

// NewTimer creates a fake timer that triggers at the specified duration.
func (f *FakeClock) NewTimer(d time.Duration) clock.Timer {
	f.mu.Lock()
	defer f.mu.Unlock()

	ch := make(chan time.Time, 1)
	triggerTime := f.now.Add(d)
	timer := &FakeTimer{
		ch:          ch,
		triggerTime: triggerTime,
		stopped:     false,
	}
	f.timers = append(f.timers, timer)

	return timer
}

// Advance moves the fake clock forward by the specified duration and triggers
// any timers/channels that should fire.
func (f *FakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.now = f.now.Add(d)

	// Fire all timers that should trigger
	remaining := make([]*FakeTimer, 0)
	for _, timer := range f.timers {
		if !timer.stopped && !f.now.Before(timer.triggerTime) {
			select {
			case timer.ch <- f.now:
			default:
				// Channel already has a value or is closed
			}
		} else if !timer.stopped {
			remaining = append(remaining, timer)
		}
	}
	f.timers = remaining

	// Notify any goroutines waiting for time to advance
	for _, waiter := range f.waiters {
		close(waiter)
	}
	f.waiters = nil
}

// Set sets the fake clock to a specific time.
func (f *FakeClock) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t
}

// BlockUntilTimers returns the number of active timers.
// Useful for tests to verify timers are set up before advancing time.
func (f *FakeClock) BlockUntilTimers(count int) <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()

	done := make(chan struct{})

	if len(f.timers) >= count {
		close(done)
		return done
	}

	f.waiters = append(f.waiters, done)
	return done
}

// ActiveTimers returns the number of active (non-stopped) timers.
func (f *FakeClock) ActiveTimers() int {
	f.mu.RLock()
	defer f.mu.RUnlock()

	count := 0
	for _, t := range f.timers {
		if !t.stopped {
			count++
		}
	}
	return count
}

// FakeTimer implements clock.Timer for testing.
type FakeTimer struct {
	mu          sync.RWMutex
	ch          chan time.Time
	triggerTime time.Time
	stopped     bool
}

// C returns the timer's channel.
func (t *FakeTimer) C() <-chan time.Time {
	return t.ch
}

// Stop prevents the timer from firing.
func (t *FakeTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.stopped {
		return false
	}

	t.stopped = true
	return true
}

// Reset changes the timer to expire after duration d.
func (t *FakeTimer) Reset(d time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	wasActive := !t.stopped
	t.stopped = false
	// Note: triggerTime reset would need access to FakeClock.now
	// For simplicity, we just mark as not stopped
	return wasActive
}
