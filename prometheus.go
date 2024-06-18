package hotload

import (
	"context"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
)

const (
	GRPCMethodKey  = "grpc_method"
	GRPCServiceKey = "grpc_service"
	StatementKey   = "stmt" // either exec or query
	ExecStatement  = "exec"
	QueryStatement = "query"
)

// sqlStmtsSummary is a prometheus metric to keep track of the number of times
// a sql statement is called in a transaction by statement type per grpc service
var sqlStmtsSummary = prometheus.NewSummaryVec(prometheus.SummaryOpts{
	Name: "transaction_sql_stmts",
	Help: "The number of sql stmts called in a transaction by statement type per grpc service and method",
}, []string{GRPCServiceKey, GRPCMethodKey, StatementKey})

func init() {
	prometheus.MustRegister(sqlStmtsSummary)
}

// PromUnaryServerInterceptor returns a unary server interceptor that sets the
// prometheus labels for the grpc service and method. This is useful for
// population the prometheus metrics.
func PromUnaryServerInterceptor() func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		service, method := splitMethod(info.FullMethod)
		labels := map[string]string{
			GRPCMethodKey:  method,
			GRPCServiceKey: service,
		}
		ctx = ContextWithExecLabels(ctx, labels)
		return handler(ctx, req)
	}
}

func splitMethod(fullMethod string) (service, method string) {
	// fullMethod is in the form "/service/method"
	// We need to split it into service and method
	split := strings.Split(fullMethod, "/")
	if len(split) != 3 {
		return "unknown", "unknown"
	}
	return split[1], split[2]
}
