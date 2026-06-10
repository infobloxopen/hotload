package hotload

import "context"

type execLabelKeyType struct{}

var execLabelKey = execLabelKeyType{}

// ContextWithExecLabels returns a context carrying labels that describe the
// caller (for example a gRPC service and method). Observability adapters can
// retrieve them from TxEvent.Ctx with GetExecLabelsFromContext.
func ContextWithExecLabels(ctx context.Context, labels map[string]string) context.Context {
	if labels == nil {
		return ctx
	}
	return context.WithValue(ctx, execLabelKey, labels)
}

// GetExecLabelsFromContext returns the labels stored by
// ContextWithExecLabels, or nil if there are none.
func GetExecLabelsFromContext(ctx context.Context) map[string]string {
	if ctx == nil {
		return nil
	}
	labels, _ := ctx.Value(execLabelKey).(map[string]string)
	return labels
}
