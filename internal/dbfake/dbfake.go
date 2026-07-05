// Package dbfake is an in-memory database/sql/driver implementation for
// hotload's unit tests. A fake driver is configured with a capability set
// (Caps); the conns and stmts it opens implement exactly the optional driver
// interfaces selected by those capabilities, which lets tests verify that
// hotload's wrappers mirror the underlying driver truthfully and that
// database/sql takes the intended fallback paths.
//
// Every operation is recorded in a CallLog for assertions. Behavior can be
// scripted per driver via ExecFn/QueryFn (for example to block until a
// context is canceled). The default Query result is a single row with a
// single column "dsn" holding the DSN the conn was opened with, so tests can
// observe which generation served a query.
//
// dbfake deliberately does not import the hotload package (hotload's
// internal tests import dbfake, so an import in the other direction would
// be a cycle). For the same reason it contains no fake hotload.Strategy:
// the Strategy interface names the hotload.Watchable type, so a fake
// satisfying it must import hotload — each test package defines its own.
package dbfake

import (
	"context"
	"database/sql/driver"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// Caps selects which optional driver interfaces fake conns and stmts
// present.
type Caps uint32

const (
	CapExecer Caps = 1 << iota // legacy driver.Execer
	CapExecerContext
	CapQueryer // legacy driver.Queryer
	CapQueryerContext
	CapPinger
	CapNamedValueChecker
	CapConnPrepareContext
	CapConnBeginTx
	CapSessionResetter
	CapValidator
	CapStmtExecContext
	CapStmtQueryContext
	CapColumnConverter
	CapStmtNamedValueChecker
)

// CapsModern matches a typical modern driver: every context interface, no
// legacy ones.
const CapsModern = CapExecerContext | CapQueryerContext | CapPinger |
	CapNamedValueChecker | CapConnPrepareContext | CapConnBeginTx |
	CapSessionResetter | CapValidator | CapStmtExecContext | CapStmtQueryContext

// CapsAll turns everything on, including the legacy interfaces.
const CapsAll = CapsModern | CapExecer | CapQueryer | CapColumnConverter | CapStmtNamedValueChecker

// Driver is a fake driver.Driver. Configure its fields before registering it
// with hotload; they must not be changed once conns are being opened.
type Driver struct {
	Caps Caps

	// OpenErr, if set, is consulted on every Open; a non-nil return fails
	// the open.
	OpenErr func(dsn string) error
	// OpenDelay delays every Open, to simulate slow dials.
	OpenDelay time.Duration
	// ExecFn, if set, replaces the default exec behavior. It receives the
	// effective context (context.Background() for legacy paths).
	ExecFn func(c *Conn, ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error)
	// QueryFn, if set, replaces the default query behavior.
	QueryFn func(c *Conn, ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error)

	Log CallLog

	mu     sync.Mutex
	conns  []*Conn
	nextID int
}

var _ driver.Driver = (*Driver)(nil)

func (d *Driver) Open(dsn string) (driver.Conn, error) {
	if d.OpenDelay > 0 {
		time.Sleep(d.OpenDelay)
	}
	if d.OpenErr != nil {
		if err := d.OpenErr(dsn); err != nil {
			return nil, err
		}
	}
	d.mu.Lock()
	d.nextID++
	c := &Conn{drv: d, ID: d.nextID, DSN: dsn, caps: d.Caps}
	d.conns = append(d.conns, c)
	d.mu.Unlock()
	d.Log.add(c.ID, "Open", dsn)
	return viewConn(c), nil
}

// Conns returns a snapshot of every conn the driver has opened, in order.
func (d *Driver) Conns() []*Conn {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]*Conn, len(d.conns))
	copy(out, d.conns)
	return out
}

// OpenConns returns the conns that have not been closed yet.
func (d *Driver) OpenConns() []*Conn {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []*Conn
	for _, c := range d.conns {
		if !c.Closed() {
			out = append(out, c)
		}
	}
	return out
}

// Conn is the fake connection. It is always handed to hotload behind a
// capability view (see views_gen.go), never directly.
type Conn struct {
	drv  *Driver
	ID   int
	DSN  string
	caps Caps

	closed     atomic.Bool
	closeCount atomic.Int64
	valid      atomic.Bool // inverted: true means IsValid returns false
}

// Closed reports whether the conn was closed.
func (c *Conn) Closed() bool { return c.closed.Load() }

// CloseCount reports how many times Close was called on the conn; hotload
// must never close an underlying conn twice.
func (c *Conn) CloseCount() int64 { return c.closeCount.Load() }

// SetInvalid makes a CapValidator conn report IsValid() == false.
func (c *Conn) SetInvalid() { c.valid.Store(true) }

func (c *Conn) log(method, detail string) { c.drv.Log.add(c.ID, method, detail) }

func (c *Conn) doPrepare(query string) (driver.Stmt, error) {
	c.log("Prepare", query)
	return viewStmt(&Stmt{conn: c, query: query}), nil
}

func (c *Conn) doPrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	c.log("PrepareContext", query)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return viewStmt(&Stmt{conn: c, query: query}), nil
}

func (c *Conn) doClose() error {
	c.closeCount.Add(1)
	c.closed.Store(true)
	c.log("Close", "")
	return nil
}

func (c *Conn) doBegin() (driver.Tx, error) {
	c.log("Begin", "")
	return &Tx{conn: c}, nil
}

func (c *Conn) doBeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	c.log("BeginTx", "")
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &Tx{conn: c}, nil
}

func (c *Conn) doExec(ctx context.Context, method, query string, args []driver.NamedValue) (driver.Result, error) {
	c.log(method, query)
	if c.drv.ExecFn != nil {
		return c.drv.ExecFn(c, ctx, query, args)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return driver.RowsAffected(1), nil
}

func (c *Conn) doQuery(ctx context.Context, method, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.log(method, query)
	if c.drv.QueryFn != nil {
		return c.drv.QueryFn(c, ctx, query, args)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &Rows{cols: []string{"dsn"}, rows: [][]driver.Value{{c.DSN}}}, nil
}

func (c *Conn) doPing(ctx context.Context) error {
	c.log("Ping", "")
	return ctx.Err()
}

func (c *Conn) doCheckNamedValue(nv *driver.NamedValue) error {
	c.log("CheckNamedValue", fmt.Sprint(nv.Value))
	return nil
}

func (c *Conn) doResetSession(ctx context.Context) error {
	c.log("ResetSession", "")
	return nil
}

func (c *Conn) doIsValid() bool {
	c.log("IsValid", "")
	return !c.valid.Load()
}

func namedToValues(args []driver.Value) []driver.NamedValue {
	out := make([]driver.NamedValue, len(args))
	for i, v := range args {
		out[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	return out
}

// fvBase implements the mandatory driver.Conn methods for every view.
type fvBase struct{ c *Conn }

func (v fvBase) Prepare(query string) (driver.Stmt, error) { return v.c.doPrepare(query) }
func (v fvBase) Close() error                              { return v.c.doClose() }
func (v fvBase) Begin() (driver.Tx, error)                 { return v.c.doBegin() }

// Unwrap exposes the fake conn behind a view for test assertions.
func (v fvBase) Unwrap() *Conn { return v.c }

// Unwrapper is implemented by every conn view; tests use it to reach the
// underlying fake conn.
type Unwrapper interface{ Unwrap() *Conn }

type fvExecer struct{ c *Conn }

func (v fvExecer) Exec(query string, args []driver.Value) (driver.Result, error) {
	return v.c.doExec(context.Background(), "Exec", query, namedToValues(args))
}

type fvExecerCtx struct{ c *Conn }

func (v fvExecerCtx) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return v.c.doExec(ctx, "ExecContext", query, args)
}

type fvQueryer struct{ c *Conn }

func (v fvQueryer) Query(query string, args []driver.Value) (driver.Rows, error) {
	return v.c.doQuery(context.Background(), "Query", query, namedToValues(args))
}

type fvQueryerCtx struct{ c *Conn }

func (v fvQueryerCtx) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return v.c.doQuery(ctx, "QueryContext", query, args)
}

type fvPinger struct{ c *Conn }

func (v fvPinger) Ping(ctx context.Context) error { return v.c.doPing(ctx) }

type fvNVChecker struct{ c *Conn }

func (v fvNVChecker) CheckNamedValue(nv *driver.NamedValue) error { return v.c.doCheckNamedValue(nv) }

// fvClassA groups the optional interfaces hotload always synthesizes when
// the underlying conn lacks them: ConnPrepareContext, ConnBeginTx,
// SessionResetter and Validator.
type fvClassA struct{ c *Conn }

func (v fvClassA) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	return v.c.doPrepareContext(ctx, query)
}

func (v fvClassA) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return v.c.doBeginTx(ctx, opts)
}

func (v fvClassA) ResetSession(ctx context.Context) error { return v.c.doResetSession(ctx) }
func (v fvClassA) IsValid() bool                          { return v.c.doIsValid() }

// Stmt is the fake prepared statement, always handed out behind a
// capability view.
type Stmt struct {
	conn  *Conn
	query string
}

func (s *Stmt) doClose() error {
	s.conn.log("StmtClose", s.query)
	return nil
}

func (s *Stmt) doNumInput() int { return -1 }

func (s *Stmt) doExec(ctx context.Context, method string, args []driver.NamedValue) (driver.Result, error) {
	return s.conn.doExec(ctx, method, s.query, args)
}

func (s *Stmt) doQuery(ctx context.Context, method string, args []driver.NamedValue) (driver.Rows, error) {
	return s.conn.doQuery(ctx, method, s.query, args)
}

type fsBase struct{ s *Stmt }

func (v fsBase) Close() error  { return v.s.doClose() }
func (v fsBase) NumInput() int { return v.s.doNumInput() }
func (v fsBase) Exec(args []driver.Value) (driver.Result, error) {
	return v.s.doExec(context.Background(), "StmtExec", namedToValues(args))
}
func (v fsBase) Query(args []driver.Value) (driver.Rows, error) {
	return v.s.doQuery(context.Background(), "StmtQuery", namedToValues(args))
}

type fsExecerCtx struct{ s *Stmt }

func (v fsExecerCtx) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return v.s.doExec(ctx, "StmtExecContext", args)
}

type fsQueryerCtx struct{ s *Stmt }

func (v fsQueryerCtx) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return v.s.doQuery(ctx, "StmtQueryContext", args)
}

type fsColConv struct{ s *Stmt }

func (v fsColConv) ColumnConverter(idx int) driver.ValueConverter {
	return driver.DefaultParameterConverter
}

type fsNVChecker struct{ s *Stmt }

func (v fsNVChecker) CheckNamedValue(nv *driver.NamedValue) error {
	return v.s.conn.doCheckNamedValue(nv)
}

// Tx is the fake transaction.
type Tx struct{ conn *Conn }

func (t *Tx) Commit() error {
	t.conn.log("Commit", "")
	return nil
}

func (t *Tx) Rollback() error {
	t.conn.log("Rollback", "")
	return nil
}

// Rows is the fake result set.
type Rows struct {
	cols []string
	rows [][]driver.Value
	pos  int
}

func (r *Rows) Columns() []string { return r.cols }
func (r *Rows) Close() error      { return nil }

func (r *Rows) Next(dest []driver.Value) error {
	if r.pos >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.pos])
	r.pos++
	return nil
}

// Call is one recorded driver operation.
type Call struct {
	ConnID int
	Method string
	Detail string
}

// CallLog records driver operations across all conns of a Driver.
type CallLog struct {
	mu    sync.Mutex
	calls []Call
}

func (l *CallLog) add(connID int, method, detail string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, Call{ConnID: connID, Method: method, Detail: detail})
}

// Calls returns a snapshot of all recorded calls in order.
func (l *CallLog) Calls() []Call {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Call, len(l.calls))
	copy(out, l.calls)
	return out
}

// Count returns how many times the named method was called.
func (l *CallLog) Count(method string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, c := range l.calls {
		if c.Method == method {
			n++
		}
	}
	return n
}

// Reset clears the log.
func (l *CallLog) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = nil
}
