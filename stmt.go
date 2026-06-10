package hotload

import (
	"context"
	"database/sql/driver"
)

// baseStmt wraps an underlying driver.Stmt prepared on a baseConn. It
// implements the mandatory driver.Stmt methods; the optional interfaces
// (StmtExecContext, StmtQueryContext, ColumnConverter, NamedValueChecker)
// are exposed only when the underlying stmt supports them, via the generated
// combination wrappers returned by wrapStmt.
type baseStmt struct {
	inner driver.Stmt
	conn  *baseConn
}

func (s *baseStmt) Close() error {
	return s.inner.Close()
}

func (s *baseStmt) NumInput() int {
	return s.inner.NumInput()
}

func (s *baseStmt) Exec(args []driver.Value) (driver.Result, error) {
	s.conn.execStmts.Add(1)
	return s.inner.Exec(args)
}

func (s *baseStmt) Query(args []driver.Value) (driver.Rows, error) {
	s.conn.queryStmts.Add(1)
	return s.inner.Query(args)
}

// execContext backs the generated StmtExecContext wrappers; only reachable
// when the underlying stmt implements driver.StmtExecContext.
func (s *baseStmt) execContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	octx, release := s.conn.opCtx(ctx)
	if octx == nil {
		return nil, driver.ErrBadConn
	}
	defer release()
	s.conn.execStmts.Add(1)
	return s.inner.(driver.StmtExecContext).ExecContext(octx, args)
}

// queryContext backs the generated StmtQueryContext wrappers. As with
// baseConn.queryContext, the caller's context passes through unmerged
// because the returned driver.Rows captures it.
func (s *baseStmt) queryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if s.conn.gen.ctx.Err() != nil {
		return nil, driver.ErrBadConn
	}
	s.conn.queryStmts.Add(1)
	return s.inner.(driver.StmtQueryContext).QueryContext(ctx, args)
}

// columnConverter backs the generated ColumnConverter wrappers; only
// reachable when the underlying stmt implements driver.ColumnConverter.
func (s *baseStmt) columnConverter(idx int) driver.ValueConverter {
	return s.inner.(driver.ColumnConverter).ColumnConverter(idx)
}

// checkNamedValue backs the generated NamedValueChecker wrappers; only
// reachable when the underlying stmt implements driver.NamedValueChecker.
func (s *baseStmt) checkNamedValue(nv *driver.NamedValue) error {
	return s.inner.(driver.NamedValueChecker).CheckNamedValue(nv)
}
