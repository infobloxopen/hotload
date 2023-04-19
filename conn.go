package hotload

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
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

// BeginTx calls the underlying BeginTx method unless the supervising context
// is closed.
// Returns an error if the underlying driver doesn't implement
// driver.ConnBeginTx interface and TxOptions are non default. If TxOptions are
// of default values it will call the underlying Begin method as like sql
// package.
// If the context is canceled by the user this method will call Tx.Rollback.
func (c *managedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	select {
	case <-c.ctx.Done():
		c.conn.Close()
		return nil, driver.ErrBadConn
	default:
	}

	if conn, ok := c.conn.(driver.ConnBeginTx); ok {
		return conn.BeginTx(ctx, opts)
	}

	// same as is defined in go sql package to call Begin method if the TxOptions are default
	if sql.IsolationLevel(opts.Isolation) != sql.LevelDefault {
		return nil, errors.New("hotload: underlying driver does not support non-default isolation level")
	}

	if opts.ReadOnly {
		return nil, errors.New("hotload: underlying driver does not support read-only transactions")
	}

	tx, err := c.conn.Begin()
	if err == nil {
		select {
		default:
		case <-ctx.Done():
			tx.Rollback()
			return nil, ctx.Err()
		}
	}

	return tx, err
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
