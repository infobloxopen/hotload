package hotload

import (
	"context"
	"database/sql/driver"
)

// managedTx wraps a sql/driver.Tx so that it can store the context of the
// transaction and clean up the execQueryCounter on Commit or Rollback.
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

func observeExecQuerySummary(ctx context.Context, counter int) {
	labels := GetExecLabelsFromContext(ctx)
	execQuerySummary.WithLabelValues(labels[GRPCServiceKey], labels[GRPCMethodKey]).Observe(float64(counter))
}

func (t *managedTx) cleanup() {
	observeExecQuerySummary(t.ctx, t.conn.execQueryCounter)
	t.conn.resetExecQueryCounter()
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
