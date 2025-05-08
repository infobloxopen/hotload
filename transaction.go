package hotload

import (
	"context"
	"database/sql/driver"

	"github.com/infobloxopen/hotload/logger"
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
	var log = logger.GetLogger()
	log("managedTx.Commit")
	err := t.tx.Commit()
	t.cleanup()
	return err
}

func (t *managedTx) Rollback() error {
	var log = logger.GetLogger()
	log("managedTx.Rollback")
	err := t.tx.Rollback()
	t.cleanup()
	return err
}

func observeSQLStmtsSummary(ctx context.Context, execStmtsCounter, queryStmtsCounter int) {
	labels := GetExecLabelsFromContext(ctx)
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
	var log = logger.GetLogger()
	if labels == nil {
		log("ContextWithExecLabels called with nil label set")
		return ctx
	}
	return context.WithValue(ctx, promLabelKey, labels)
}

func GetExecLabelsFromContext(ctx context.Context) map[string]string {
	var log = logger.GetLogger()
	if ctx == nil {
		log("No context provided, returning")
		return nil
	}

	value := ctx.Value(promLabelKey)
	if value == nil {
		log("No value for promLabelKey, returning")
		return nil
	}
	labelMap, ok := value.(map[string]string)
	if !ok {
		log("Bad value type used for promLabelKey, conversion error")
		return nil
	}

	return labelMap
}
