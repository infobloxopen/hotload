package hotload

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/infobloxopen/hotload/logger"
	"github.com/teivah/onecontext"
)

// managedConn wraps a sql/driver.Conn so that it can be closed by
// a supervising context.
type managedConn struct {
	ctx       context.Context
	dsn       string
	redactDsn string
	conn      driver.Conn
	reset     bool
	killed    bool
	mu        sync.RWMutex

	// callback function to be called after the connection is closed
	afterClose func(*managedConn)

	execStmtsCounter  atomic.Int64 // count the number of exec calls in a transaction
	queryStmtsCounter atomic.Int64 // count the number of query calls in a transaction
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

func newManagedConn(ctx context.Context, dsn, redactDsn string, conn driver.Conn, afterClose func(*managedConn)) *managedConn {
	return &managedConn{
		ctx:        ctx,
		dsn:        dsn,
		redactDsn:  redactDsn,
		conn:       conn,
		afterClose: afterClose,
	}
}

func (c *managedConn) Exec(query string, args []driver.Value) (driver.Result, error) {
	c.logf("managedConn.Exec", "Exec")

	connCtx, ok := c.conn.(driver.ExecerContext)
	if ok {
		namedArgs := make([]driver.NamedValue, len(args), len(args))
		for i := 0; i < len(args); i++ {
			namedArgs[i].Name = ""
			namedArgs[i].Ordinal = i
			namedArgs[i].Value = args[i]
		}
		c.incExecStmtsCounter() //increment the exec counter to keep track of the number of exec calls
		c.logf("managedConn.Exec", "calling underlying conn.ExecContext()")
		return connCtx.ExecContext(c.ctx, query, namedArgs)
	}

	connExr, ok := c.conn.(driver.Execer)
	if ok {
		c.incExecStmtsCounter() //increment the exec counter to keep track of the number of exec calls
		c.logf("managedConn.Exec", "calling underlying conn.Exec()")
		return connExr.Exec(query, args)
	}

	return nil, driver.ErrSkip
}

func (c *managedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.logf("managedConn.ExecContext", "ExecContext")
	conn, ok := c.conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	c.incExecStmtsCounter() //increment the exec counter to keep track of the number of exec calls
	c.logf("managedConn.ExecContext", "calling underlying conn.ExecContext()")
	mergedCtx, _ := onecontext.Merge(c.ctx, ctx)
	return conn.ExecContext(mergedCtx, query, args)
}

func (c *managedConn) CheckNamedValue(namedValue *driver.NamedValue) error {
	conn, ok := c.conn.(driver.NamedValueChecker)
	if !ok {
		return driver.ErrSkip
	}
	return conn.CheckNamedValue(namedValue)
}

func (c *managedConn) Query(query string, args []driver.Value) (driver.Rows, error) {
	c.logf("managedConn.Query", "Query")

	connCtx, ok := c.conn.(driver.QueryerContext)
	if ok {
		namedArgs := make([]driver.NamedValue, len(args), len(args))
		for i := 0; i < len(args); i++ {
			namedArgs[i].Name = ""
			namedArgs[i].Ordinal = i
			namedArgs[i].Value = args[i]
		}
		c.incQueryStmtsCounter() //increment the query counter to keep track of the number of query calls
		c.logf("managedConn.Query", "calling underlying conn.QueryContext()")
		return connCtx.QueryContext(c.ctx, query, namedArgs)
	}

	connQyr, ok := c.conn.(driver.Queryer)
	if ok {
		namedArgs := make([]driver.NamedValue, len(args), len(args))
		for i := 0; i < len(args); i++ {
			namedArgs[i].Name = ""
			namedArgs[i].Ordinal = i
			namedArgs[i].Value = args[i]
		}
		c.incQueryStmtsCounter() //increment the query counter to keep track of the number of query calls
		c.logf("managedConn.Query", "calling underlying conn.Query()")
		return connQyr.Query(query, args)
	}

	return nil, driver.ErrSkip
}

func (c *managedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.logf("managedConn.QueryContext", "QueryContext")
	conn, ok := c.conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	c.incQueryStmtsCounter() //increment the query counter to keep track of the number of query calls
	c.logf("managedConn.QueryContext", "calling underlying conn.QueryContext()")
	mergedCtx, _ := onecontext.Merge(c.ctx, ctx)
	return conn.QueryContext(mergedCtx, query, args)
}

func (c *managedConn) Prepare(query string) (driver.Stmt, error) {
	select {
	case <-c.ctx.Done():
		c.logf("managedConn.Prepare", "ctx done, calling close()")
		c.close()
		return nil, driver.ErrBadConn
	default:
	}
	c.logf("managedConn.Prepare", "calling underlying Prepare()")
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
		c.logf("managedConn.IsValid", "ctx done, calling close()")
		c.close()
		return false
	default:
	}
	s, ok := c.conn.(driver.Validator)
	if !ok {
		return true
	}
	c.logf("managedConn.IsValid", "calling underlying IsValid()")
	return s.IsValid()
}

func (c *managedConn) ResetSession(ctx context.Context) error {
	if c.GetReset() {
		c.logf("managedConn.ResetSession", "already reset")
		return driver.ErrBadConn
	}

	s, ok := c.conn.(driver.SessionResetter)
	if !ok {
		return nil
	}

	c.logf("managedConn.ResetSession", "calling underlying ResetSession()")
	return s.ResetSession(ctx)
}

func (c *managedConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.close()

	if err == nil {
		c.killed = true
	}
	c.logf("managedConn.Close", "closed")

	return err
}

func (c *managedConn) close() error {
	if c.afterClose != nil {
		defer c.afterClose(c)
	}
	c.logf("managedConn.close", "calling underlying Close()")
	return c.conn.Close()
}

func (c *managedConn) GetReset() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	c.logf("managedConn.GetReset", "reset=%v", c.reset)
	return c.reset
}

func (c *managedConn) Reset(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reset = v
	c.logf("managedConn.Reset", "reset=%v", v)
}

func (c *managedConn) GetKill() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	c.logf("managedConn.GetKill", "killed=%v", c.killed)
	return c.killed
}

func (c *managedConn) incExecStmtsCounter() {
	c.execStmtsCounter.Add(1)
}

func (c *managedConn) resetExecStmtsCounter() {
	c.execStmtsCounter.Store(0)
}

func (c *managedConn) incQueryStmtsCounter() {
	c.queryStmtsCounter.Add(1)
}

func (c *managedConn) resetQueryStmtsCounter() {
	c.queryStmtsCounter.Store(0)
}

func (c *managedConn) logf(prefix, format string, args ...any) {
	logPrefix := fmt.Sprintf("%s[%s]:", prefix, c.redactDsn)
	logger.Logf(logPrefix, format, args...)
}
