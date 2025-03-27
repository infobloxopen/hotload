package hotload

import (
	"context"
	"database/sql/driver"

	"github.com/infobloxopen/hotload/metrics"
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
	if labels == nil{
		// TODO: Add defaults and remove return
		return
	}
	service := labels[metrics.GRPCServiceKey]
	method := labels[metrics.GRPCMethodKey]

	metrics.SqlStmtsSummary.WithLabelValues(service, method, metrics.ExecStatement).Observe(float64(execStmtsCounter))
	metrics.SqlStmtsSummary.WithLabelValues(service, method, metrics.QueryStatement).Observe(float64(queryStmtsCounter))
}

func (t *managedTx) cleanup() {
	observeSQLStmtsSummary(t.ctx, t.conn.execStmtsCounter, t.conn.queryStmtsCounter)
	t.conn.resetExecStmtsCounter()
	t.conn.resetQueryStmtsCounter()
}

type promLabelKeyType struct{}

var promLabelKey = promLabelKeyType{}

func ContextWithExecLabels(ctx context.Context, labels map[string]string) context.Context {
	if labels == nil {
		// ContextWithExecLabels called with nil label set
		return ctx
	}
	return context.WithValue(ctx, promLabelKey, labels)
}

func GetExecLabelsFromContext(ctx context.Context) map[string]string {
	if ctx == nil {
		return nil
	}

	value := ctx.Value(promLabelKey)
	if value == nil {
		return nil
	}
	labelMap, ok := value.(map[string]string)
	if !ok {
		// Bad value type used for promLabelKey, conversion error
		return nil
	}

	return labelMap
}
