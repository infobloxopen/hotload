package testkit

import (
	"context"
	"database/sql/driver"
	"fmt"
	"io"
)

// FakeDriver is a test implementation of database/sql/driver.Driver that can be
// configured to support various optional interfaces for testing capability detection.
type FakeDriver struct {
	// Capabilities to enable
	SupportsExecContext       bool
	SupportsQueryContext      bool
	SupportsConnBeginTx       bool
	SupportsPrepareContext    bool
	SupportsPinger            bool
	SupportsSessionResetter   bool
	SupportsNamedValueChecker bool

	// Tracking
	OpenCalls          int
	OpenConnectorCalls int
}

// NewFakeDriver creates a new fake driver with all capabilities disabled by default.
func NewFakeDriver() *FakeDriver {
	return &FakeDriver{}
}

// NewFakeDriverWithCapabilities creates a fake driver with specified capabilities.
func NewFakeDriverWithCapabilities(execCtx, queryCtx, beginTx, prepareCtx, ping, reset, namedValue bool) *FakeDriver {
	return &FakeDriver{
		SupportsExecContext:       execCtx,
		SupportsQueryContext:      queryCtx,
		SupportsConnBeginTx:       beginTx,
		SupportsPrepareContext:    prepareCtx,
		SupportsPinger:            ping,
		SupportsSessionResetter:   reset,
		SupportsNamedValueChecker: namedValue,
	}
}

// Open implements driver.Driver.
func (d *FakeDriver) Open(name string) (driver.Conn, error) {
	d.OpenCalls++
	return d.newConn(), nil
}

// OpenConnector implements driver.DriverContext (Go 1.20+).
func (d *FakeDriver) OpenConnector(name string) (driver.Connector, error) {
	d.OpenConnectorCalls++
	return &fakeConnector{driver: d, dsn: name}, nil
}

func (d *FakeDriver) newConn() driver.Conn {
	return &fakeConn{driver: d}
}

// fakeConnector implements driver.Connector
type fakeConnector struct {
	driver *FakeDriver
	dsn    string
}

func (c *fakeConnector) Connect(ctx context.Context) (driver.Conn, error) {
	return c.driver.newConn(), nil
}

func (c *fakeConnector) Driver() driver.Driver {
	return c.driver
}

// fakeConn implements driver.Conn and optional interfaces based on FakeDriver configuration
type fakeConn struct {
	driver *FakeDriver
	closed bool
}

func (c *fakeConn) Prepare(query string) (driver.Stmt, error) {
	if c.closed {
		return nil, driver.ErrBadConn
	}
	return &fakeStmt{}, nil
}

func (c *fakeConn) Close() error {
	c.closed = true
	return nil
}

func (c *fakeConn) Begin() (driver.Tx, error) {
	if c.closed {
		return nil, driver.ErrBadConn
	}
	return &fakeTx{}, nil
}

// Optional interface: driver.Pinger
func (c *fakeConn) Ping(ctx context.Context) error {
	if !c.driver.SupportsPinger {
		return driver.ErrSkip
	}
	if c.closed {
		return driver.ErrBadConn
	}
	return nil
}

// Optional interface: driver.ExecerContext
func (c *fakeConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if !c.driver.SupportsExecContext {
		return nil, driver.ErrSkip
	}
	if c.closed {
		return nil, driver.ErrBadConn
	}
	return &fakeResult{}, nil
}

// Optional interface: driver.QueryerContext
func (c *fakeConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if !c.driver.SupportsQueryContext {
		return nil, driver.ErrSkip
	}
	if c.closed {
		return nil, driver.ErrBadConn
	}
	return &fakeRows{}, nil
}

// Optional interface: driver.ConnBeginTx
func (c *fakeConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if !c.driver.SupportsConnBeginTx {
		return nil, driver.ErrSkip
	}
	if c.closed {
		return nil, driver.ErrBadConn
	}
	return &fakeTx{}, nil
}

// Optional interface: driver.ConnPrepareContext
func (c *fakeConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if !c.driver.SupportsPrepareContext {
		return nil, driver.ErrSkip
	}
	if c.closed {
		return nil, driver.ErrBadConn
	}
	return &fakeStmt{}, nil
}

// Optional interface: driver.SessionResetter
func (c *fakeConn) ResetSession(ctx context.Context) error {
	if !c.driver.SupportsSessionResetter {
		return driver.ErrSkip
	}
	if c.closed {
		return driver.ErrBadConn
	}
	return nil
}

// Optional interface: driver.NamedValueChecker
func (c *fakeConn) CheckNamedValue(nv *driver.NamedValue) error {
	if !c.driver.SupportsNamedValueChecker {
		return driver.ErrSkip
	}
	return nil
}

// fakeStmt implements driver.Stmt and optional interfaces
type fakeStmt struct {
	closed bool
}

func (s *fakeStmt) Close() error {
	s.closed = true
	return nil
}

func (s *fakeStmt) NumInput() int {
	return -1 // variable number of inputs
}

func (s *fakeStmt) Exec(args []driver.Value) (driver.Result, error) {
	if s.closed {
		return nil, driver.ErrBadConn
	}
	return &fakeResult{}, nil
}

func (s *fakeStmt) Query(args []driver.Value) (driver.Rows, error) {
	if s.closed {
		return nil, driver.ErrBadConn
	}
	return &fakeRows{}, nil
}

// Optional interface: driver.StmtExecContext
func (s *fakeStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if s.closed {
		return nil, driver.ErrBadConn
	}
	return &fakeResult{}, nil
}

// Optional interface: driver.StmtQueryContext
func (s *fakeStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if s.closed {
		return nil, driver.ErrBadConn
	}
	return &fakeRows{}, nil
}

// fakeTx implements driver.Tx
type fakeTx struct {
	committed  bool
	rolledback bool
}

func (t *fakeTx) Commit() error {
	if t.committed || t.rolledback {
		return fmt.Errorf("transaction already finished")
	}
	t.committed = true
	return nil
}

func (t *fakeTx) Rollback() error {
	if t.committed || t.rolledback {
		return fmt.Errorf("transaction already finished")
	}
	t.rolledback = true
	return nil
}

// fakeResult implements driver.Result
type fakeResult struct{}

func (r *fakeResult) LastInsertId() (int64, error) {
	return 1, nil
}

func (r *fakeResult) RowsAffected() (int64, error) {
	return 1, nil
}

// fakeRows implements driver.Rows
type fakeRows struct {
	closed bool
	pos    int
}

func (r *fakeRows) Columns() []string {
	return []string{"col1", "col2"}
}

func (r *fakeRows) Close() error {
	r.closed = true
	return nil
}

func (r *fakeRows) Next(dest []driver.Value) error {
	if r.closed {
		return io.EOF
	}
	if r.pos >= 1 { // Return 1 row
		return io.EOF
	}
	r.pos++
	dest[0] = "value1"
	dest[1] = "value2"
	return nil
}
