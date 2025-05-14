package hotload

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"

	hlogger "github.com/infobloxopen/hotload/logger"
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
	llog := hlogger.WithKV("method", "hotload.managedConn.Exec", "redactDsn", c.redactDsn)
	llog.DebugKV("Exec")

	connCtx, ok := c.conn.(driver.ExecerContext)
	if ok {
		namedArgs := make([]driver.NamedValue, len(args), len(args))
		for i := 0; i < len(args); i++ {
			namedArgs[i].Name = ""
			namedArgs[i].Ordinal = i
			namedArgs[i].Value = args[i]
		}
		c.incExecStmtsCounter() //increment the exec counter to keep track of the number of exec calls
		llog.DebugKV("calling underlying conn.ExecContext()")
		return connCtx.ExecContext(c.ctx, query, namedArgs)
	}

	connExr, ok := c.conn.(driver.Execer)
	if ok {
		c.incExecStmtsCounter() //increment the exec counter to keep track of the number of exec calls
		llog.DebugKV("calling underlying conn.Exec()")
		return connExr.Exec(query, args)
	}

	return nil, driver.ErrSkip
}

func (c *managedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	llog := hlogger.WithKV("method", "hotload.managedConn.ExecContext", "redactDsn", c.redactDsn)
	llog.DebugKV("ExecContext")
	conn, ok := c.conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	c.incExecStmtsCounter() //increment the exec counter to keep track of the number of exec calls
	llog.DebugKV("calling underlying conn.ExecContext()")
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
	llog := hlogger.WithKV("method", "hotload.managedConn.Query", "redactDsn", c.redactDsn)
	llog.DebugKV("Query")

	connCtx, ok := c.conn.(driver.QueryerContext)
	if ok {
		namedArgs := make([]driver.NamedValue, len(args), len(args))
		for i := 0; i < len(args); i++ {
			namedArgs[i].Name = ""
			namedArgs[i].Ordinal = i
			namedArgs[i].Value = args[i]
		}
		c.incQueryStmtsCounter() //increment the query counter to keep track of the number of query calls
		llog.DebugKV("calling underlying conn.QueryContext()")
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
		llog.DebugKV("calling underlying conn.Query()")
		return connQyr.Query(query, args)
	}

	return nil, driver.ErrSkip
}

func (c *managedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	llog := hlogger.WithKV("method", "hotload.managedConn.QueryContext", "redactDsn", c.redactDsn)
	llog.DebugKV("QueryContext")
	conn, ok := c.conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	c.incQueryStmtsCounter() //increment the query counter to keep track of the number of query calls
	llog.DebugKV("calling underlying conn.QueryContext()")
	mergedCtx, _ := onecontext.Merge(c.ctx, ctx)
	return conn.QueryContext(mergedCtx, query, args)
}

func (c *managedConn) Prepare(query string) (driver.Stmt, error) {
	llog := hlogger.WithKV("method", "hotload.managedConn.Prepare", "redactDsn", c.redactDsn)
	select {
	case <-c.ctx.Done():
		llog.DebugKV("ctx done, calling close()")
		c.close()
		return nil, driver.ErrBadConn
	default:
	}
	llog.DebugKV("calling underlying Prepare()")
	return c.conn.Prepare(query)
}

// Begin calls the underlying Begin method unless the supervising
// context is closed.
func (c *managedConn) Begin() (driver.Tx, error) {
	llog := hlogger.WithKV("method", "hotload.managedConn.Begin", "redactDsn", c.redactDsn)
	select {
	case <-c.ctx.Done():
		llog.DebugKV("ctx done, calling close()")
		c.close()
		return nil, driver.ErrBadConn
	default:
	}
	llog.DebugKV("calling underlying Begin()")
	return c.conn.Begin()
}

func (c *managedConn) IsValid() bool {
	llog := hlogger.WithKV("method", "hotload.managedConn.IsInvalid", "redactDsn", c.redactDsn)
	select {
	case <-c.ctx.Done():
		llog.DebugKV("ctx done, calling close()")
		c.close()
		return false
	default:
	}
	s, ok := c.conn.(driver.Validator)
	if !ok {
		return true
	}
	llog.DebugKV("calling underlying IsValid()")
	return s.IsValid()
}

func (c *managedConn) ResetSession(ctx context.Context) error {
	llog := hlogger.WithKV("method", "hotload.managedConn.ResetSession", "redactDsn", c.redactDsn)
	if c.GetReset() {
		llog.DebugKV("already reset")
		return driver.ErrBadConn
	}

	s, ok := c.conn.(driver.SessionResetter)
	if !ok {
		return nil
	}

	llog.DebugKV("calling underlying ResetSession()")
	return s.ResetSession(ctx)
}

func (c *managedConn) Close() error {
	llog := hlogger.WithKV("method", "hotload.managedConn.Close", "redactDsn", c.redactDsn)
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.close()

	if err == nil {
		c.killed = true
	}
	llog.DebugKV("closed")

	return err
}

func (c *managedConn) close() error {
	llog := hlogger.WithKV("method", "hotload.managedConn.close", "redactDsn", c.redactDsn)
	if c.afterClose != nil {
		defer c.afterClose(c)
	}
	llog.DebugKV("calling underlying Close()")
	return c.conn.Close()
}

func (c *managedConn) GetReset() bool {
	llog := hlogger.WithKV("method", "hotload.managedConn.GetReset", "redactDsn", c.redactDsn)
	c.mu.RLock()
	defer c.mu.RUnlock()
	llog.DebugKV("GetReset", "reset", c.reset)
	return c.reset
}

func (c *managedConn) Reset(v bool) {
	llog := hlogger.WithKV("method", "hotload.managedConn.Reset", "redactDsn", c.redactDsn)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reset = v
	llog.DebugKV("Reset", "reset", v)
}

func (c *managedConn) GetKill() bool {
	llog := hlogger.WithKV("method", "hotload.managedConn.GetKill", "redactDsn", c.redactDsn)
	c.mu.RLock()
	defer c.mu.RUnlock()
	llog.DebugKV("GetKill", "killed", c.killed)
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
