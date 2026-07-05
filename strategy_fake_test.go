package hotload_test

import (
	"context"
	"fmt"
	"sync"

	hotload "github.com/infobloxopen/hotload/v3"
)

// fakeStrategy is a hotload.Strategy driven by tests. It lives in the test
// package rather than in internal/dbfake because the Strategy interface
// names the hotload.Watchable type, and dbfake must not import hotload (the
// internal tests import dbfake).
type fakeStrategy struct {
	mu      sync.Mutex
	initial map[string]string
	subs    map[string][]*fakeWatch
	watches int
}

// newFakeStrategy returns a fakeStrategy that answers Watch with the given
// initial values keyed by path.
func newFakeStrategy(initial map[string]string) *fakeStrategy {
	cp := make(map[string]string, len(initial))
	for k, v := range initial {
		cp[k] = v
	}
	return &fakeStrategy{initial: cp, subs: make(map[string][]*fakeWatch)}
}

// Watch implements hotload.Strategy; every call opens an independent watch.
func (s *fakeStrategy) Watch(ctx context.Context, pth string, pathQry string) (string, hotload.Watchable, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.initial[pth]
	if !ok {
		return "", nil, fmt.Errorf("fakeStrategy: no initial value for path %q", pth)
	}
	w := &fakeWatch{strat: s, path: pth, ch: make(chan string)}
	s.subs[pth] = append(s.subs[pth], w)
	s.watches++
	context.AfterFunc(ctx, func() { w.Close() })
	return value, w, nil
}

// Push delivers a new value to every watcher of path. It blocks until each
// hotload run loop receives it, which makes change injection deterministic.
func (s *fakeStrategy) Push(path, value string) {
	s.mu.Lock()
	watches := append([]*fakeWatch(nil), s.subs[path]...)
	s.mu.Unlock()
	if len(watches) == 0 {
		panic("fakeStrategy: Push on unwatched path " + path)
	}
	for _, w := range watches {
		w.ch <- value
	}
}

// CloseChan closes the update channels of every watch on path, simulating a
// strategy that stops watching on its own.
func (s *fakeStrategy) CloseChan(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.subs[path] {
		w.closed = true
		close(w.ch)
		s.watches--
	}
	delete(s.subs, path)
}

// Watches reports how many watches are currently open.
func (s *fakeStrategy) Watches() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.watches
}

// fakeWatch is the hotload.Watchable handed out by fakeStrategy.Watch.
type fakeWatch struct {
	strat  *fakeStrategy
	path   string
	ch     chan string
	closed bool // guarded by strat.mu
}

func (w *fakeWatch) Values() <-chan string { return w.ch }

func (w *fakeWatch) Close() error {
	s := w.strat
	s.mu.Lock()
	defer s.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	close(w.ch)
	s.watches--
	watches := s.subs[w.path]
	for i, other := range watches {
		if other == w {
			s.subs[w.path] = append(watches[:i], watches[i+1:]...)
			break
		}
	}
	return nil
}
