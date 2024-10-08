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

	// callback function to be called after the connection is closed
	afterClose func(*managedConn)

	execStmtsCounter  int // count the number of exec calls in a transaction
	queryStmtsCounter int // count the number of query calls in a transaction
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
		c.close()
		return nil, driver.ErrBadConn
	default:
	}

	if conn, ok := c.conn.(driver.ConnBeginTx); ok {
		tx, err := conn.BeginTx(ctx, opts)
		if err != nil {
			return nil, err
		}

		return &managedTx{tx: tx, conn: c, ctx: ctx}, nil
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

func newManagedConn(ctx context.Context, conn driver.Conn, afterClose func(*managedConn)) *managedConn {
	return &managedConn{
		ctx:        ctx,
		conn:       conn,
		afterClose: afterClose,
	}
}

func (c *managedConn) Exec(query string, args []driver.Value) (driver.Result, error) {
	conn, ok := c.conn.(driver.Execer)
	if !ok {
		return nil, driver.ErrSkip
	}
	c.incExecStmtsCounter() //increment the exec counter to keep track of the number of exec calls
	return conn.Exec(query, args)
}

func (c *managedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	conn, ok := c.conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	c.incExecStmtsCounter() //increment the exec counter to keep track of the number of exec calls
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
	c.incQueryStmtsCounter() //increment the query counter to keep track of the number of query calls
	return conn.Query(query, args)
}

func (c *managedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	conn, ok := c.conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	c.incQueryStmtsCounter() //increment the query counter to keep track of the number of query calls
	return conn.QueryContext(ctx, query, args)
}

func (c *managedConn) Prepare(query string) (driver.Stmt, error) {
	select {
	case <-c.ctx.Done():
		c.close()
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
		c.close()
		return nil, driver.ErrBadConn
	default:
	}
	return c.conn.Begin()
}

func (c *managedConn) IsValid() bool {
	select {
	case <-c.ctx.Done():
		c.close()
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
	err := c.close()

	if err == nil {
		c.killed = true
	}

	return err
}

func (c *managedConn) close() error {
	if c.afterClose != nil {
		defer c.afterClose(c)
	}
	return c.conn.Close()
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

func (c *managedConn) incExecStmtsCounter() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.execStmtsCounter++
}

func (c *managedConn) resetExecStmtsCounter() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.execStmtsCounter = 0
}

func (c *managedConn) incQueryStmtsCounter() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queryStmtsCounter++
}

func (c *managedConn) resetQueryStmtsCounter() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queryStmtsCounter = 0
}
