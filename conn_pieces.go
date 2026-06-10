package hotload

import (
	"context"
	"database/sql/driver"
)

// The piece types below each carry exactly one optional driver.Conn method.
// The generated combination wrappers in conn_combos_gen.go embed a subset of
// them, so a wrapped conn's method set — and therefore the type assertions
// database/sql performs — reflects exactly what the underlying conn
// supports.

// cExecer carries ExecContext (driver.ExecerContext).
type cExecer struct{ b *baseConn }

func (p cExecer) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return p.b.execContext(ctx, query, args)
}

// cQueryer carries QueryContext (driver.QueryerContext).
type cQueryer struct{ b *baseConn }

func (p cQueryer) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return p.b.queryContext(ctx, query, args)
}

// cPinger carries Ping (driver.Pinger).
type cPinger struct{ b *baseConn }

func (p cPinger) Ping(ctx context.Context) error {
	return p.b.ping(ctx)
}

// cNVChecker carries CheckNamedValue (driver.NamedValueChecker).
type cNVChecker struct{ b *baseConn }

func (p cNVChecker) CheckNamedValue(nv *driver.NamedValue) error {
	return p.b.checkNamedValue(nv)
}
