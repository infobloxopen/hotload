package testkit

import (
	"context"
	"fmt"
	"sync"
)

// FakeStrategy is a test implementation of hotload.Strategy that provides
// deterministic behavior for unit tests. It allows tests to control when
// updates are delivered and what values are returned.
type FakeStrategy struct {
	mu sync.RWMutex

	// Initial values keyed by path
	initialValues map[string]string

	// Update channels keyed by path
	updateChans map[string][]chan string

	// Errors to return for specific paths
	watchErrors map[string]error

	// Track Watch calls for verification
	watchCalls []WatchCall

	// Control whether Close has been called
	closed bool
}

// WatchCall records parameters passed to Watch
type WatchCall struct {
	Path  string
	Query string
}

// NewFakeStrategy creates a new fake strategy for testing.
func NewFakeStrategy() *FakeStrategy {
	return &FakeStrategy{
		initialValues: make(map[string]string),
		updateChans:   make(map[string][]chan string),
		watchErrors:   make(map[string]error),
		watchCalls:    []WatchCall{},
	}
}

// SetInitialValue configures the initial value returned for a path.
func (f *FakeStrategy) SetInitialValue(path, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.initialValues[path] = value
}

// SetWatchError configures an error to be returned when watching a path.
func (f *FakeStrategy) SetWatchError(path string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.watchErrors[path] = err
}

// Watch implements hotload.Strategy.
func (f *FakeStrategy) Watch(ctx context.Context, path string, query map[string][]string) (string, <-chan string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return "", nil, fmt.Errorf("strategy is closed")
	}

	// Record the call
	f.watchCalls = append(f.watchCalls, WatchCall{
		Path:  path,
		Query: fmt.Sprintf("%v", query),
	})

	// Check for configured error
	if err, ok := f.watchErrors[path]; ok {
		return "", nil, err
	}

	// Get initial value - return error if not configured (mimics file not found)
	initialValue, ok := f.initialValues[path]
	if !ok {
		return "", nil, fmt.Errorf("no initial value configured for path %s", path)
	}

	// Create update channel
	updateChan := make(chan string, 10)
	f.updateChans[path] = append(f.updateChans[path], updateChan)

	// Monitor context cancellation
	go func() {
		<-ctx.Done()
		f.mu.Lock()
		close(updateChan)
		// Remove from list
		chans := f.updateChans[path]
		for i, ch := range chans {
			if ch == updateChan {
				f.updateChans[path] = append(chans[:i], chans[i+1:]...)
				break
			}
		}
		f.mu.Unlock()
	}()

	return initialValue, updateChan, nil
}

// SendUpdate sends an update to all watchers of a path.
// This is a test helper method not part of the Strategy interface.
func (f *FakeStrategy) SendUpdate(path, value string) {
	f.mu.RLock()
	chans := f.updateChans[path]
	f.mu.RUnlock()

	for _, ch := range chans {
		select {
		case ch <- value:
			// Sent successfully
		default:
			// Channel full or closed, skip
		}
	}
}

// Close implements hotload.Strategy.
func (f *FakeStrategy) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.closed = true

	// Close all update channels
	for _, chans := range f.updateChans {
		for _, ch := range chans {
			close(ch)
		}
	}
	f.updateChans = make(map[string][]chan string)

	return nil
}

// WatchCalls returns all recorded Watch calls for test verification.
func (f *FakeStrategy) WatchCalls() []WatchCall {
	f.mu.RLock()
	defer f.mu.RUnlock()

	calls := make([]WatchCall, len(f.watchCalls))
	copy(calls, f.watchCalls)
	return calls
}

// ActiveWatchers returns the number of active watch channels for a path.
func (f *FakeStrategy) ActiveWatchers(path string) int {
	f.mu.RLock()
	defer f.mu.RUnlock()

	return len(f.updateChans[path])
}

// IsClosed returns whether Close has been called.
func (f *FakeStrategy) IsClosed() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	return f.closed
}
