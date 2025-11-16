package contextutil

import (
	"context"
	"sync"
	"time"
)

// Merge combines two contexts into a single context that is cancelled
// when either of the parent contexts is cancelled or when the returned
// cancel function is called.
func Merge(ctx1, ctx2 context.Context) (context.Context, context.CancelFunc) {
	if ctx1 == nil {
		ctx1 = context.Background()
	}
	if ctx2 == nil {
		ctx2 = context.Background()
	}

	merged := &mergedContext{
		ctx1: ctx1,
		ctx2: ctx2,
		done: make(chan struct{}),
	}

	go merged.watch()

	return merged, merged.cancel
}

type mergedContext struct {
	ctx1 context.Context
	ctx2 context.Context
	done chan struct{}
	once sync.Once
	mu   sync.Mutex
	err  error
}

func (m *mergedContext) watch() {
	select {
	case <-m.ctx1.Done():
		m.cancel()
	case <-m.ctx2.Done():
		m.cancel()
	case <-m.done:
	}
}

func (m *mergedContext) cancel() {
	m.once.Do(func() {
		m.mu.Lock()
		if m.ctx1.Err() != nil {
			m.err = m.ctx1.Err()
		} else if m.ctx2.Err() != nil {
			m.err = m.ctx2.Err()
		} else {
			m.err = context.Canceled
		}
		m.mu.Unlock()
		close(m.done)
	})
}

func (m *mergedContext) Deadline() (deadline time.Time, ok bool) {
	d1, ok1 := m.ctx1.Deadline()
	d2, ok2 := m.ctx2.Deadline()

	switch {
	case !ok1 && !ok2:
		return time.Time{}, false
	case !ok1:
		return d2, true
	case !ok2:
		return d1, true
	case d1.Before(d2):
		return d1, true
	default:
		return d2, true
	}
}

func (m *mergedContext) Done() <-chan struct{} {
	return m.done
}

func (m *mergedContext) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.err
}

func (m *mergedContext) Value(key interface{}) interface{} {
	// Try ctx2 first, then ctx1
	if val := m.ctx2.Value(key); val != nil {
		return val
	}
	return m.ctx1.Value(key)
}
