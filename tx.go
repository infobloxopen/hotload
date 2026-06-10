package hotload

import (
	"context"
	"database/sql/driver"
)

// managedTx wraps a driver.Tx so the statement counters accumulated on the
// conn can be reported to hooks when the transaction completes.
type managedTx struct {
	tx   driver.Tx
	conn *baseConn
	ctx  context.Context
}

func (t *managedTx) Commit() error {
	err := t.tx.Commit()
	t.complete(err == nil)
	return err
}

func (t *managedTx) Rollback() error {
	err := t.tx.Rollback()
	t.complete(false)
	return err
}

func (t *managedTx) complete(committed bool) {
	emitTxComplete(TxEvent{
		Ctx:        t.ctx,
		ExecStmts:  t.conn.execStmts.Load(),
		QueryStmts: t.conn.queryStmts.Load(),
		Committed:  committed,
	})
	t.conn.execStmts.Store(0)
	t.conn.queryStmts.Store(0)
}
