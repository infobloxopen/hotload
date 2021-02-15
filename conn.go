package hotload

import (
	"context"
	"database/sql/driver"
)

// managedConn wraps a sql/driver.Conn so that it can be closed by
// a supervising context.
type managedConn struct {
	ctx  context.Context
	conn driver.Conn
}

func newManagedConn(ctx context.Context, conn driver.Conn) driver.Conn {
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

func (c *managedConn) Close() error {
	return c.conn.Close()
}
