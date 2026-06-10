package hotload

import (
	"context"
	"database/sql/driver"
)

// The piece types below each carry exactly one optional driver.Stmt method,
// mirroring the conn pieces in conn_pieces.go. See that file for the design.

// sExecer carries ExecContext (driver.StmtExecContext).
type sExecer struct{ b *baseStmt }

func (p sExecer) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return p.b.execContext(ctx, args)
}

// sQueryer carries QueryContext (driver.StmtQueryContext).
type sQueryer struct{ b *baseStmt }

func (p sQueryer) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return p.b.queryContext(ctx, args)
}

// sColConv carries ColumnConverter (driver.ColumnConverter).
type sColConv struct{ b *baseStmt }

func (p sColConv) ColumnConverter(idx int) driver.ValueConverter {
	return p.b.columnConverter(idx)
}

// sNVChecker carries CheckNamedValue (driver.NamedValueChecker).
type sNVChecker struct{ b *baseStmt }

func (p sNVChecker) CheckNamedValue(nv *driver.NamedValue) error {
	return p.b.checkNamedValue(nv)
}
