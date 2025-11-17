// Package static provides a no-dependency static DSN strategy for hotload.
// This strategy is useful for testing and examples where you want to manually
// control DSN updates without file system watches or external dependencies.
package static

import (
	"context"
	"net/url"
	"sync"
)

// Strategy is a static hotload strategy that allows manual DSN updates.
type Strategy struct {
	mu      sync.RWMutex
	watches map[string]*watch
}

type watch struct {
	value   string
	updates chan string
	cancel  context.CancelFunc
}

// New creates a new static strategy.
func New() *Strategy {
	return &Strategy{
		watches: make(map[string]*watch),
	}
}

// Watch implements hotload.Strategy.
func (s *Strategy) Watch(ctx context.Context, path string, q url.Values) (value string, updates <-chan string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if already watching this path
	if w, exists := s.watches[path]; exists {
		return w.value, w.updates, nil
	}

	// Create new watch
	updatesChan := make(chan string, 10)
	watchCtx, cancel := context.WithCancel(ctx)

	w := &watch{
		value:   "",
		updates: updatesChan,
		cancel:  cancel,
	}

	s.watches[path] = w

	// Close updates channel when context is cancelled
	go func() {
		<-watchCtx.Done()
		close(updatesChan)
	}()

	return w.value, updatesChan, nil
}

// Update manually updates the DSN value for a given path.
// This triggers an update event for any watchers.
func (s *Strategy) Update(path, dsn string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	w, exists := s.watches[path]
	if !exists {
		return
	}

	w.value = dsn

	// Send update (non-blocking)
	select {
	case w.updates <- dsn:
	default:
		// Channel full, skip update
	}
}

// Close implements hotload.Strategy.
func (s *Strategy) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, w := range s.watches {
		w.cancel()
	}

	s.watches = make(map[string]*watch)
}
