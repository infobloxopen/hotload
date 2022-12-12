package hotload

import (
	"context"
	"sync"
)

// mConn wraps a ManagableConn so that it can be closed by
// a supervising context.
type mConn[T any] struct {
	ctx    context.Context
	conn   ManagableConn[T]
	reset  bool
	killed bool
	mu     sync.RWMutex
}

func newMConn[T any](ctx context.Context, conn ManagableConn[T]) *mConn[T] {
	return &mConn[T]{
		ctx:  ctx,
		conn: conn,
	}
}

func (c *mConn[T]) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.conn.Close()

	if err == nil {
		c.killed = true
	}

	return err
}

func (c *mConn[T]) GetReset() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.reset
}

func (c *mConn[T]) Reset(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reset = v
}

func (c *mConn[T]) GetKill() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.killed
}
