package hotload

//go:generate go run ./internal/gen

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync/atomic"
)

// baseConn wraps an underlying driver.Conn so that it can be retired by the
// generation that owns it. baseConn implements driver.Conn plus the optional
// interfaces whose behavior hotload must control or whose stdlib fallback it
// can replicate exactly: ConnPrepareContext, ConnBeginTx, SessionResetter
// and Validator. The remaining optional interfaces (ExecerContext,
// QueryerContext, Pinger, NamedValueChecker) are exposed only when the
// underlying conn supports them, via the generated combination wrappers
// returned by wrapConn.
type baseConn struct {
	inner      driver.Conn
	gen        *generation
	group      *group
	dsn        string
	redactDsn  string
	closed     atomic.Bool
	killed     atomic.Bool
	execStmts  atomic.Int64 // exec statements since the last completed transaction
	queryStmts atomic.Int64 // query statements since the last completed transaction
}

// opCtx returns a context canceled when either the caller's ctx or the
// owning generation's ctx is canceled. The returned release function must be
// called (usually deferred) when the operation completes; it unregisters the
// cancellation relay so no resources outlive the call. opCtx returns a nil
// context if the generation is already retired; the caller should return
// driver.ErrBadConn so database/sql retries on a fresh connection.
func (c *baseConn) opCtx(ctx context.Context) (context.Context, func()) {
	gen := c.gen
	gen.ops.Add(1)
	if gen.ctx.Err() != nil {
		gen.ops.Add(-1)
		return nil, nil
	}
	mctx, cancel := context.WithCancelCause(ctx)
	stop := context.AfterFunc(gen.ctx, func() {
		cancel(context.Cause(gen.ctx))
	})
	release := func() {
		stop()
		cancel(nil)
		gen.ops.Add(-1)
	}
	return mctx, release
}

// retired reports whether the conn should no longer be used: its generation
// was drained or killed, or the conn itself was closed.
func (c *baseConn) retired() bool {
	return c.closed.Load() || c.gen.retired()
}

func (c *baseConn) Prepare(query string) (driver.Stmt, error) {
	if c.gen.ctx.Err() != nil {
		return nil, driver.ErrBadConn
	}
	stmt, err := c.inner.Prepare(query)
	if err != nil {
		return nil, err
	}
	return wrapStmt(&baseStmt{inner: stmt, conn: c}), nil
}

// PrepareContext prepares a statement, honoring both the caller's context
// and the generation's context. When the underlying conn does not implement
// driver.ConnPrepareContext this replicates database/sql's fallback exactly:
// prepare without a context, then if the context is done, close the
// statement and return the context's error.
func (c *baseConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	octx, release := c.opCtx(ctx)
	if octx == nil {
		return nil, driver.ErrBadConn
	}
	defer release()

	if cp, ok := c.inner.(driver.ConnPrepareContext); ok {
		stmt, err := cp.PrepareContext(octx, query)
		if err != nil {
			return nil, err
		}
		return wrapStmt(&baseStmt{inner: stmt, conn: c}), nil
	}

	stmt, err := c.inner.Prepare(query)
	if err == nil {
		select {
		case <-octx.Done():
			stmt.Close()
			return nil, octx.Err()
		default:
		}
	}
	if err != nil {
		return nil, err
	}
	return wrapStmt(&baseStmt{inner: stmt, conn: c}), nil
}

func (c *baseConn) Begin() (driver.Tx, error) {
	if c.gen.ctx.Err() != nil {
		return nil, driver.ErrBadConn
	}
	tx, err := c.inner.Begin()
	if err != nil {
		return nil, err
	}
	return &managedTx{tx: tx, conn: c, ctx: context.Background()}, nil
}

// BeginTx starts a transaction. The caller's context is deliberately passed
// through unmerged: some drivers bind the context to the transaction's
// lifetime, and canceling a merged context when this call returns would
// roll back live transactions. The generation context is checked at entry
// instead; forceKill reaches in-flight transactions by closing the conn.
// When the underlying conn does not implement driver.ConnBeginTx this
// replicates database/sql's fallback exactly.
func (c *baseConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if c.gen.ctx.Err() != nil {
		return nil, driver.ErrBadConn
	}

	if cb, ok := c.inner.(driver.ConnBeginTx); ok {
		tx, err := cb.BeginTx(ctx, opts)
		if err != nil {
			return nil, err
		}
		return &managedTx{tx: tx, conn: c, ctx: ctx}, nil
	}

	if sql.IsolationLevel(opts.Isolation) != sql.LevelDefault {
		return nil, errors.New("hotload: underlying driver does not support non-default isolation level")
	}
	if opts.ReadOnly {
		return nil, errors.New("hotload: underlying driver does not support read-only transactions")
	}

	tx, err := c.inner.Begin()
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		tx.Rollback()
		return nil, ctx.Err()
	default:
	}
	return &managedTx{tx: tx, conn: c, ctx: ctx}, nil
}

func (c *baseConn) Close() error {
	return c.closeConn(false)
}

// closeConn closes the underlying conn exactly once, no matter how many
// paths race to close it (the pool, a generation kill, or both).
func (c *baseConn) closeConn(killed bool) error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	if killed {
		c.killed.Store(true)
	}
	err := c.inner.Close()
	c.gen.remove(c)
	emitConnClose(ConnEvent{GroupName: c.group.name, RedactedDSN: c.redactDsn, Killed: killed})
	return err
}

// ResetSession is called by database/sql before reusing a pooled conn. A
// drained or killed generation answers driver.ErrBadConn so the pool
// discards the conn and dials a fresh one on the current DSN.
func (c *baseConn) ResetSession(ctx context.Context) error {
	if c.retired() {
		return driver.ErrBadConn
	}
	if sr, ok := c.inner.(driver.SessionResetter); ok {
		return sr.ResetSession(ctx)
	}
	return nil
}

// IsValid is called by database/sql when returning a conn to the pool.
func (c *baseConn) IsValid() bool {
	if c.retired() {
		return false
	}
	if v, ok := c.inner.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

// execContext backs the generated ExecerContext wrappers. It is only
// reachable when the underlying conn implements ExecerContext or the legacy
// Execer; for the legacy case it replicates database/sql's fallback exactly
// (convert named args, poll the context, call Exec).
func (c *baseConn) execContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	octx, release := c.opCtx(ctx)
	if octx == nil {
		return nil, driver.ErrBadConn
	}
	defer release()
	c.execStmts.Add(1)

	if ec, ok := c.inner.(driver.ExecerContext); ok {
		return ec.ExecContext(octx, query, args)
	}

	dargs, err := namedValueToValue(args)
	if err != nil {
		return nil, err
	}
	select {
	case <-octx.Done():
		return nil, octx.Err()
	default:
	}
	return c.inner.(driver.Execer).Exec(query, dargs)
}

// queryContext backs the generated QueryerContext wrappers. The caller's
// context is deliberately passed through unmerged: the returned driver.Rows
// captures the context, and canceling a merged context when this call
// returns would make cursor iteration fail with a context-canceled error.
// The generation context is checked at entry instead; forceKill reaches
// in-flight queries by closing the conn.
func (c *baseConn) queryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if c.gen.ctx.Err() != nil {
		return nil, driver.ErrBadConn
	}
	c.queryStmts.Add(1)

	if qc, ok := c.inner.(driver.QueryerContext); ok {
		return qc.QueryContext(ctx, query, args)
	}

	dargs, err := namedValueToValue(args)
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return c.inner.(driver.Queryer).Query(query, dargs)
}

// ping backs the generated Pinger wrappers; only reachable when the
// underlying conn implements driver.Pinger.
func (c *baseConn) ping(ctx context.Context) error {
	octx, release := c.opCtx(ctx)
	if octx == nil {
		return driver.ErrBadConn
	}
	defer release()
	return c.inner.(driver.Pinger).Ping(octx)
}

// checkNamedValue backs the generated NamedValueChecker wrappers; only
// reachable when the underlying conn implements driver.NamedValueChecker.
func (c *baseConn) checkNamedValue(nv *driver.NamedValue) error {
	return c.inner.(driver.NamedValueChecker).CheckNamedValue(nv)
}

// namedValueToValue converts named args to positional args, mirroring the
// unexported helper of the same name in database/sql.
func namedValueToValue(named []driver.NamedValue) ([]driver.Value, error) {
	dargs := make([]driver.Value, len(named))
	for n, param := range named {
		if len(param.Name) > 0 {
			return nil, errors.New("sql: driver does not support the use of Named Parameters")
		}
		dargs[n] = param.Value
	}
	return dargs, nil
}
