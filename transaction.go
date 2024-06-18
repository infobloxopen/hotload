package hotload

import (
	"context"
	"database/sql/driver"
)

// managedTx wraps a sql/driver.Tx so that it can store the context of the
// transaction and clean up the execqueryCallsCounter on Commit or Rollback.
type managedTx struct {
	tx   driver.Tx
	conn *managedConn
	ctx  context.Context
}

func (t *managedTx) Commit() error {
	err := t.tx.Commit()
	t.cleanup()
	return err
}

func (t *managedTx) Rollback() error {
	err := t.tx.Rollback()
	t.cleanup()
	return err
}

func observeSQLStmtsSummary(ctx context.Context, execStmtsCounter, queryStmtsCounter int) {
	labels := GetExecLabelsFromContext(ctx)
	service := labels[GRPCServiceKey]
	method := labels[GRPCMethodKey]

	sqlStmtsSummary.WithLabelValues(service, method, ExecStatement).Observe(float64(execStmtsCounter))
	sqlStmtsSummary.WithLabelValues(service, method, QueryStatement).Observe(float64(queryStmtsCounter))
}

func (t *managedTx) cleanup() {
	observeSQLStmtsSummary(t.ctx, t.conn.execStmtsCounter, t.conn.queryStmtsCounter)
	t.conn.resetExecStmtsCounter()
	t.conn.resetQueryStmtsCounter()
}

var promLabelKey = struct{}{}

func ContextWithExecLabels(ctx context.Context, labels map[string]string) context.Context {
	return context.WithValue(ctx, promLabelKey, labels)
}

func GetExecLabelsFromContext(ctx context.Context) map[string]string {
	if ctx == nil {
		return nil
	}

	if ctx.Value(promLabelKey) == nil {
		return nil
	}

	return ctx.Value(promLabelKey).(map[string]string)
}
