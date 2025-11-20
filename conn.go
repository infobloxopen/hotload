package hotload

import (
	"context"
	"database/sql/driver"
	"io"
	"sync/atomic"
)

type wrappedConn struct {
	conn    driver.Conn
	epoch   Epoch
	tracker *epochTracker
	closed  atomic.Bool
	inTx    atomic.Int32 // >0 if in transaction
}

func newWrappedConn(conn driver.Conn, epoch Epoch, tracker *epochTracker) *wrappedConn {
	wc := &wrappedConn{
		conn:    conn,
		epoch:   epoch,
		tracker: tracker,
	}
	tracker.registerConn(epoch, wc)
	return wc
}

// Prepare implements driver.Conn.
func (wc *wrappedConn) Prepare(query string) (driver.Stmt, error) {
	stmt, err := wc.conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &wrappedStmt{stmt: stmt, conn: wc}, nil
}

// Close implements driver.Conn.
func (wc *wrappedConn) Close() error {
	if wc.closed.Swap(true) {
		return nil // Already closed
	}

	wc.tracker.unregisterConn(wc.epoch, wc)
	return wc.conn.Close()
}

// Begin implements driver.Conn.
func (wc *wrappedConn) Begin() (driver.Tx, error) {
	tx, err := wc.conn.Begin()
	if err != nil {
		return nil, err
	}
	wc.inTx.Add(1)
	return &wrappedTx{tx: tx, conn: wc}, nil
}

func (wc *wrappedConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if connCtx, ok := wc.conn.(driver.ConnPrepareContext); ok {
		stmt, err := connCtx.PrepareContext(ctx, query)
		if err != nil {
			return nil, err
		}
		return &wrappedStmt{stmt: stmt, conn: wc}, nil
	}
	return wc.Prepare(query)
}

// BeginTx implements driver.ConnBeginTx.
func (wc *wrappedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if connTx, ok := wc.conn.(driver.ConnBeginTx); ok {
		tx, err := connTx.BeginTx(ctx, opts)
		if err != nil {
			return nil, err
		}
		wc.inTx.Add(1)
		return &wrappedTx{tx: tx, conn: wc}, nil
	}
	return wc.Begin()
}

// ExecContext implements driver.ExecerContext.
func (wc *wrappedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if execer, ok := wc.conn.(driver.ExecerContext); ok {
		return execer.ExecContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

// QueryContext implements driver.QueryerContext.
func (wc *wrappedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if queryer, ok := wc.conn.(driver.QueryerContext); ok {
		rows, err := queryer.QueryContext(ctx, query, args)
		if err != nil {
			return nil, err
		}
		return &wrappedRows{rows: rows, conn: wc}, nil
	}
	return nil, driver.ErrSkip
}

func (wc *wrappedConn) Ping(ctx context.Context) error {
	if pinger, ok := wc.conn.(driver.Pinger); ok {
		return pinger.Ping(ctx)
	}
	return nil
}

// ResetSession implements driver.SessionResetter.
func (wc *wrappedConn) ResetSession(ctx context.Context) error {
	if wc.tracker.isOldEpoch(wc.epoch) {
		return driver.ErrBadConn
	}

	if resetter, ok := wc.conn.(driver.SessionResetter); ok {
		return resetter.ResetSession(ctx)
	}
	return nil
}

// CheckNamedValue implements driver.NamedValueChecker.
func (wc *wrappedConn) CheckNamedValue(nv *driver.NamedValue) error {
	if checker, ok := wc.conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

type wrappedStmt struct {
	stmt driver.Stmt
	conn *wrappedConn
}

func (ws *wrappedStmt) Close() error {
	return ws.stmt.Close()
}

func (ws *wrappedStmt) NumInput() int {
	return ws.stmt.NumInput()
}

func (ws *wrappedStmt) Exec(args []driver.Value) (driver.Result, error) {
	if ws.conn.inTx.Load() == 0 && ws.conn.tracker.isOldEpoch(ws.conn.epoch) {
		return nil, driver.ErrBadConn
	}
	return ws.stmt.Exec(args)
}

func (ws *wrappedStmt) Query(args []driver.Value) (driver.Rows, error) {
	if ws.conn.inTx.Load() == 0 && ws.conn.tracker.isOldEpoch(ws.conn.epoch) {
		return nil, driver.ErrBadConn
	}
	rows, err := ws.stmt.Query(args)
	if err != nil {
		return nil, err
	}
	return &wrappedRows{rows: rows, conn: ws.conn}, nil
}

func (ws *wrappedStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if ws.conn.inTx.Load() == 0 && ws.conn.tracker.isOldEpoch(ws.conn.epoch) {
		return nil, driver.ErrBadConn
	}
	if stmtCtx, ok := ws.stmt.(driver.StmtExecContext); ok {
		return stmtCtx.ExecContext(ctx, args)
	}
	dargs, err := namedValueToValue(args)
	if err != nil {
		return nil, err
	}
	return ws.Exec(dargs)
}

func (ws *wrappedStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if ws.conn.inTx.Load() == 0 && ws.conn.tracker.isOldEpoch(ws.conn.epoch) {
		return nil, driver.ErrBadConn
	}
	if stmtCtx, ok := ws.stmt.(driver.StmtQueryContext); ok {
		rows, err := stmtCtx.QueryContext(ctx, args)
		if err != nil {
			return nil, err
		}
		return &wrappedRows{rows: rows, conn: ws.conn}, nil
	}
	dargs, err := namedValueToValue(args)
	if err != nil {
		return nil, err
	}
	return ws.Query(dargs)
}

type wrappedTx struct {
	tx   driver.Tx
	conn *wrappedConn
}

func (wt *wrappedTx) Commit() error {
	err := wt.tx.Commit()
	wt.conn.inTx.Add(-1)
	return err
}

func (wt *wrappedTx) Rollback() error {
	err := wt.tx.Rollback()
	wt.conn.inTx.Add(-1)
	return err
}

type wrappedRows struct {
	rows driver.Rows
	conn *wrappedConn
}

func (wr *wrappedRows) Columns() []string {
	return wr.rows.Columns()
}

func (wr *wrappedRows) Close() error {
	return wr.rows.Close()
}

func (wr *wrappedRows) Next(dest []driver.Value) error {
	if wr.conn.inTx.Load() == 0 && wr.conn.tracker.isOldEpoch(wr.conn.epoch) {
		return io.EOF
	}
	return wr.rows.Next(dest)
}

func (wr *wrappedRows) ColumnTypeScanType(index int) interface{} {
	if rowsType, ok := wr.rows.(driver.RowsColumnTypeScanType); ok {
		return rowsType.ColumnTypeScanType(index)
	}
	return nil
}

func (wr *wrappedRows) ColumnTypeDatabaseTypeName(index int) string {
	if rowsType, ok := wr.rows.(driver.RowsColumnTypeDatabaseTypeName); ok {
		return rowsType.ColumnTypeDatabaseTypeName(index)
	}
	return ""
}

func (wr *wrappedRows) ColumnTypeLength(index int) (length int64, ok bool) {
	if rowsType, ok := wr.rows.(driver.RowsColumnTypeLength); ok {
		return rowsType.ColumnTypeLength(index)
	}
	return 0, false
}

func (wr *wrappedRows) ColumnTypeNullable(index int) (nullable, ok bool) {
	if rowsType, ok := wr.rows.(driver.RowsColumnTypeNullable); ok {
		return rowsType.ColumnTypeNullable(index)
	}
	return false, false
}

func (wr *wrappedRows) ColumnTypePrecisionScale(index int) (precision, scale int64, ok bool) {
	if rowsType, ok := wr.rows.(driver.RowsColumnTypePrecisionScale); ok {
		return rowsType.ColumnTypePrecisionScale(index)
	}
	return 0, 0, false
}

func namedValueToValue(named []driver.NamedValue) ([]driver.Value, error) {
	dargs := make([]driver.Value, len(named))
	for n, param := range named {
		if len(param.Name) > 0 {
			return nil, driver.ErrSkip
		}
		dargs[n] = param.Value
	}
	return dargs, nil
}
