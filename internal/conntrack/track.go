package conntrack

import (
	"context"
	"database/sql/driver"
	"sync"
	"time"

	"github.com/infobloxopen/hotload/internal/clock"
)

// Tracker represents a connection that can be tracked for draining.
type Tracker interface {
	// Epoch returns the epoch this connection was created in
	Epoch() uint64

	// InFlight returns the current number of in-flight operations
	InFlight() int32

	// Close closes the underlying connection
	Close() error
}

// Registry tracks all active connections by epoch.
type Registry struct {
	mu    sync.RWMutex
	conns map[uint64][]Tracker
	clock clock.Clock
}

// NewRegistry creates a new connection registry.
func NewRegistry(clk clock.Clock) *Registry {
	if clk == nil {
		clk = clock.Real{}
	}
	return &Registry{
		conns: make(map[uint64][]Tracker),
		clock: clk,
	}
}

// Register adds a connection to the registry.
func (r *Registry) Register(epoch uint64, conn Tracker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.conns[epoch] = append(r.conns[epoch], conn)
}

// Unregister removes a connection from the registry.
func (r *Registry) Unregister(epoch uint64, conn Tracker) {
	r.mu.Lock()
	defer r.mu.Unlock()

	conns := r.conns[epoch]
	for i, c := range conns {
		if c == conn {
			r.conns[epoch] = append(conns[:i], conns[i+1:]...)
			if len(r.conns[epoch]) == 0 {
				delete(r.conns, epoch)
			}
			return
		}
	}
}

// DrainEpoch gracefully drains connections from a specific epoch.
// It waits for in-flight operations to complete, up to the timeout.
// Returns (drained, killed) counts.
func (r *Registry) DrainEpoch(ctx context.Context, epoch uint64, timeout time.Duration, forceKill bool) (drained, killed int) {
	r.mu.Lock()
	conns := r.conns[epoch]
	// Make a copy to avoid holding lock during drain
	connsCopy := make([]Tracker, len(conns))
	copy(connsCopy, conns)
	r.mu.Unlock()

	if len(connsCopy) == 0 {
		return 0, 0
	}

	deadline := r.clock.Now().Add(timeout)

	for _, conn := range connsCopy {
		// Wait for in-flight operations to complete
		if r.waitForIdle(ctx, conn, deadline) {
			// Connection became idle, close gracefully
			if err := conn.Close(); err == nil {
				drained++
			} else {
				killed++
			}
		} else {
			// Timeout or context cancelled
			if forceKill {
				conn.Close()
				killed++
			}
		}

		// Unregister after closing
		r.Unregister(epoch, conn)
	}

	return drained, killed
}

// waitForIdle waits for a connection to have no in-flight operations.
func (r *Registry) waitForIdle(ctx context.Context, conn Tracker, deadline time.Time) bool {
	ticker := r.clock.NewTimer(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		if conn.InFlight() == 0 {
			return true
		}

		now := r.clock.Now()
		if now.After(deadline) {
			return false
		}

		select {
		case <-ctx.Done():
			return false
		case <-ticker.C():
			ticker.Reset(10 * time.Millisecond)
		}
	}
}

// Count returns the number of connections in a specific epoch.
func (r *Registry) Count(epoch uint64) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.conns[epoch])
}

// Total returns the total number of tracked connections across all epochs.
func (r *Registry) Total() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	total := 0
	for _, conns := range r.conns {
		total += len(conns)
	}
	return total
}

// ConnWrapper wraps a driver.Conn with epoch tracking and refcounting.
type ConnWrapper struct {
	conn     driver.Conn
	epoch    uint64
	inFlight int32
	mu       sync.Mutex
}

// NewConnWrapper creates a new connection wrapper.
func NewConnWrapper(conn driver.Conn, epoch uint64) *ConnWrapper {
	return &ConnWrapper{
		conn:  conn,
		epoch: epoch,
	}
}

// Epoch implements Tracker.
func (c *ConnWrapper) Epoch() uint64 {
	return c.epoch
}

// InFlight implements Tracker.
func (c *ConnWrapper) InFlight() int32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inFlight
}

// IncInFlight increments the in-flight counter.
func (c *ConnWrapper) IncInFlight() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inFlight++
}

// DecInFlight decrements the in-flight counter.
func (c *ConnWrapper) DecInFlight() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inFlight > 0 {
		c.inFlight--
	}
}

// Close implements Tracker.
func (c *ConnWrapper) Close() error {
	return c.conn.Close()
}

// Unwrap returns the underlying driver.Conn.
func (c *ConnWrapper) Unwrap() driver.Conn {
	return c.conn
}
