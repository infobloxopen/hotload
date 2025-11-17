package connwrap

import (
	"context"
	"database/sql/driver"
)

// Stmt wraps a driver.Stmt with refcounting through its parent connection.
type Stmt struct {
	stmt driver.Stmt
	conn *Conn
}

// Close implements driver.Stmt.
func (s *Stmt) Close() error {
	return s.stmt.Close()
}

// NumInput implements driver.Stmt.
func (s *Stmt) NumInput() int {
	return s.stmt.NumInput()
}

// Exec implements driver.Stmt.
func (s *Stmt) Exec(args []driver.Value) (driver.Result, error) {
	s.conn.IncInFlight()
	defer s.conn.DecInFlight()
	return s.stmt.Exec(args)
}

// Query implements driver.Stmt.
func (s *Stmt) Query(args []driver.Value) (driver.Rows, error) {
	s.conn.IncInFlight()
	defer s.conn.DecInFlight()
	return s.stmt.Query(args)
}

// ExecContext implements driver.StmtExecContext (optional interface).
func (s *Stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if execer, ok := s.stmt.(driver.StmtExecContext); ok {
		s.conn.IncInFlight()
		defer s.conn.DecInFlight()
		return execer.ExecContext(ctx, args)
	}
	return nil, driver.ErrSkip
}

// QueryContext implements driver.StmtQueryContext (optional interface).
func (s *Stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if queryer, ok := s.stmt.(driver.StmtQueryContext); ok {
		s.conn.IncInFlight()
		defer s.conn.DecInFlight()
		return queryer.QueryContext(ctx, args)
	}
	return nil, driver.ErrSkip
}

// CheckNamedValue implements driver.NamedValueChecker (optional interface).
func (s *Stmt) CheckNamedValue(nv *driver.NamedValue) error {
	if checker, ok := s.stmt.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

// ColumnConverter implements driver.ColumnConverter (optional interface).
func (s *Stmt) ColumnConverter(idx int) driver.ValueConverter {
	if converter, ok := s.stmt.(driver.ColumnConverter); ok {
		return converter.ColumnConverter(idx)
	}
	return driver.DefaultParameterConverter
}
