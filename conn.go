package hotload

import (
	"context"
	"database/sql/driver"
	"sync"
)

// managedConn wraps a sql/driver.Conn so that it can be closed by
// a supervising context.
type managedConn struct {
	ctx    context.Context
	conn   driver.Conn
	reset  bool
	killed bool
	mu     sync.RWMutex
}

func newManagedConn(ctx context.Context, conn driver.Conn) *managedConn {
	return &managedConn{
		ctx:  ctx,
		conn: conn,
	}
}

func (c *managedConn) Exec(query string, args []driver.Value) (driver.Result, error) {
	conn, ok := c.conn.(driver.Execer)
	if !ok {
		return nil, driver.ErrSkip
	}
	return conn.Exec(query, args)
}

func (c *managedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	conn, ok := c.conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return conn.ExecContext(ctx, query, args)
}

func (c *managedConn) CheckNamedValue(namedValue *driver.NamedValue) error {
	conn, ok := c.conn.(driver.NamedValueChecker)
	if !ok {
		return driver.ErrSkip
	}
	return conn.CheckNamedValue(namedValue)
}

func (c *managedConn) Query(query string, args []driver.Value) (driver.Rows, error) {
	conn, ok := c.conn.(driver.Queryer)
	if !ok {
		return nil, driver.ErrSkip
	}
	return conn.Query(query, args)
}

func (c *managedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	conn, ok := c.conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return conn.QueryContext(ctx, query, args)
}

func (c *managedConn) Prepare(query string) (driver.Stmt, error) {
	select {
	case <-c.ctx.Done():
		c.conn.Close()
		return nil, driver.ErrBadConn
	default:
	}
	return c.conn.Prepare(query)
}

// Begin calls the underlying Begin method unless the supervising
// context is closed.
func (c *managedConn) Begin() (driver.Tx, error) {
	select {
	case <-c.ctx.Done():
		c.conn.Close()
		return nil, driver.ErrBadConn
	default:
	}
	return c.conn.Begin()
}

func (c *managedConn) IsValid() bool {
	select {
	case <-c.ctx.Done():
		c.conn.Close()
		return false
	default:
	}
	s, ok := c.conn.(driver.Validator)
	if !ok {
		return true
	}
	return s.IsValid()
}

func (c *managedConn) ResetSession(ctx context.Context) error {
	if c.GetReset() {
		return driver.ErrBadConn
	}

	s, ok := c.conn.(driver.SessionResetter)
	if !ok {
		return nil
	}

	return s.ResetSession(ctx)
}

func (c *managedConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.conn.Close()

	if err == nil {
		c.killed = true
	}

	return err
}

func (c *managedConn) GetReset() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.reset
}

func (c *managedConn) Reset(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reset = v
}

func (c *managedConn) GetKill() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.killed
}
