package connwrap

import (
	"context"
	"database/sql/driver"

	"github.com/infobloxopen/hotload/internal/conntrack"
)

// Conn wraps a driver.Conn with capability preservation and refcounting.
type Conn struct {
	*conntrack.ConnWrapper
	conn driver.Conn
}

// NewConn creates a new capability-preserving connection wrapper.
func NewConn(conn driver.Conn, epoch uint64) *Conn {
	return &Conn{
		ConnWrapper: conntrack.NewConnWrapper(conn, epoch),
		conn:        conn,
	}
}

// Prepare implements driver.Conn.
func (c *Conn) Prepare(query string) (driver.Stmt, error) {
	c.IncInFlight()
	defer c.DecInFlight()

	stmt, err := c.conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &Stmt{stmt: stmt, conn: c}, nil
}

// Close implements driver.Conn.
func (c *Conn) Close() error {
	return c.ConnWrapper.Close()
}

// Begin implements driver.Conn.
func (c *Conn) Begin() (driver.Tx, error) {
	c.IncInFlight()
	defer c.DecInFlight()

	tx, err := c.conn.Begin()
	if err != nil {
		return nil, err
	}
	return &Tx{tx: tx, conn: c}, nil
}

// Prepare context support (optional interface)
func (c *Conn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if connCtx, ok := c.conn.(driver.ConnPrepareContext); ok {
		c.IncInFlight()
		defer c.DecInFlight()

		stmt, err := connCtx.PrepareContext(ctx, query)
		if err != nil {
			return nil, err
		}
		return &Stmt{stmt: stmt, conn: c}, nil
	}
	return nil, driver.ErrSkip
}

// BeginTx implements driver.ConnBeginTx (optional interface).
func (c *Conn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if connTx, ok := c.conn.(driver.ConnBeginTx); ok {
		c.IncInFlight()
		defer c.DecInFlight()

		tx, err := connTx.BeginTx(ctx, opts)
		if err != nil {
			return nil, err
		}
		return &Tx{tx: tx, conn: c}, nil
	}
	return nil, driver.ErrSkip
}

// ExecContext implements driver.ExecerContext (optional interface).
func (c *Conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if execer, ok := c.conn.(driver.ExecerContext); ok {
		c.IncInFlight()
		defer c.DecInFlight()
		return execer.ExecContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

// QueryContext implements driver.QueryerContext (optional interface).
func (c *Conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if queryer, ok := c.conn.(driver.QueryerContext); ok {
		c.IncInFlight()
		defer c.DecInFlight()
		return queryer.QueryContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

// Ping implements driver.Pinger (optional interface).
func (c *Conn) Ping(ctx context.Context) error {
	if pinger, ok := c.conn.(driver.Pinger); ok {
		c.IncInFlight()
		defer c.DecInFlight()
		return pinger.Ping(ctx)
	}
	return driver.ErrSkip
}

// ResetSession implements driver.SessionResetter (optional interface).
func (c *Conn) ResetSession(ctx context.Context) error {
	if resetter, ok := c.conn.(driver.SessionResetter); ok {
		c.IncInFlight()
		defer c.DecInFlight()
		return resetter.ResetSession(ctx)
	}
	return driver.ErrSkip
}

// CheckNamedValue implements driver.NamedValueChecker (optional interface).
func (c *Conn) CheckNamedValue(nv *driver.NamedValue) error {
	if checker, ok := c.conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}
