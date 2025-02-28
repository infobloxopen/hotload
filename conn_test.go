package hotload

import (
	"context"
	"database/sql/driver"
	"io"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/infobloxopen/hotload/metrics"
)

var _ = Describe("managedConn", func() {
	It("Should set .reset in a threadsafe way", func() {
		mc := managedConn{
			ctx:   nil,
			conn:  nil,
			reset: false,
			mu:    sync.RWMutex{},
		}
		// Lock the mutex
		mc.mu.Lock()
		writeLockAcquired := false
		readLockAcquired := false

		// Verify that neither Reset or GetReset can return while the managedConn's write lock is held
		go func() {
			mc.Reset(true)
			writeLockAcquired = true
		}()

		go func() {
			mc.GetReset()
			readLockAcquired = true
		}()

		Consistently(writeLockAcquired).Should(BeFalse())
		Consistently(readLockAcquired).Should(BeFalse())
	})
})

/**** Mocks for Prometheus Metrics ****/

type mockDriverConn struct{}

type mockTx struct{}

func (mockTx) Commit() error {
	return nil
}

func (mockTx) Rollback() error {
	return nil
}

func (mockDriverConn) Prepare(query string) (driver.Stmt, error) {
	return nil, nil
}

func (mockDriverConn) Begin() (driver.Tx, error) {
	return mockTx{}, nil
}

func (mockDriverConn) Close() error {
	return nil
}

func (mockDriverConn) IsValid() bool {
	return true
}

func (mockDriverConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return mockTx{}, nil
}

func (mockDriverConn) Exec(query string, args []driver.Value) (driver.Result, error) {
	return nil, nil
}

func (mockDriverConn) Query(query string, args []driver.Value) (driver.Rows, error) {
	return nil, nil
}

func (mockDriverConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return nil, nil
}

func (mockDriverConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return nil, nil
}

/**** End Mocks for Prometheus Metrics ****/

var _ = Describe("PrometheusMetrics", func() {
	const help = `
		# HELP transaction_sql_stmts The number of sql stmts called in a transaction by statement type per grpc service and method
		# TYPE transaction_sql_stmts summary
	`

	var service1Metrics = `
		transaction_sql_stmts_sum{grpc_method="method_1",grpc_service="service_1",stmt="exec"} 3
		transaction_sql_stmts_count{grpc_method="method_1",grpc_service="service_1",stmt="exec"} 1
		transaction_sql_stmts_sum{grpc_method="method_1",grpc_service="service_1",stmt="query"} 3
		transaction_sql_stmts_count{grpc_method="method_1",grpc_service="service_1",stmt="query"} 1
	`

	var service2Metrics = `
		transaction_sql_stmts_sum{grpc_method="method_2",grpc_service="service_2",stmt="exec"} 4
		transaction_sql_stmts_count{grpc_method="method_2",grpc_service="service_2",stmt="exec"} 1
		transaction_sql_stmts_sum{grpc_method="method_2",grpc_service="service_2",stmt="query"} 4
		transaction_sql_stmts_count{grpc_method="method_2",grpc_service="service_2",stmt="query"} 1
	`

	var service1RerunMetrics = `
		transaction_sql_stmts_sum{grpc_method="method_1",grpc_service="service_1",stmt="exec"} 4
		transaction_sql_stmts_count{grpc_method="method_1",grpc_service="service_1",stmt="exec"} 2
		transaction_sql_stmts_sum{grpc_method="method_1",grpc_service="service_1",stmt="query"} 4
		transaction_sql_stmts_count{grpc_method="method_1",grpc_service="service_1",stmt="query"} 2
	`

	var noMethodMetrics = `
		transaction_sql_stmts_sum{grpc_method="",grpc_service="",stmt="exec"} 1
		transaction_sql_stmts_count{grpc_method="",grpc_service="",stmt="exec"} 1
		transaction_sql_stmts_sum{grpc_method="",grpc_service="",stmt="query"} 1
		transaction_sql_stmts_count{grpc_method="",grpc_service="",stmt="query"} 1
	`

	It("Should emit the correct metrics", func() {
		mc := newManagedConn(context.Background(), mockDriverConn{}, nil)

		ctx := ContextWithExecLabels(context.Background(), map[string]string{"grpc_method": "method_1", "grpc_service": "service_1"})

		// begin a transaction
		tx, err := mc.BeginTx(ctx, driver.TxOptions{})
		Expect(err).ShouldNot(HaveOccurred())

		// exec a statement
		mc.Exec("INSERT INTO table (column) VALUES (?)", []driver.Value{"value"})

		// query a statement
		mc.Query("SELECT * FROM table WHERE column = ?", []driver.Value{"value"})
		mc.Query("SELECT * FROM table WHERE column = ?", []driver.Value{"value"})

		// exec a statement with context
		mc.ExecContext(ctx, "INSERT INTO table (column) VALUES (?)", []driver.NamedValue{{Value: "value"}})
		mc.ExecContext(ctx, "INSERT INTO table (column) VALUES (?)", []driver.NamedValue{{Value: "value"}})

		// query a statement with context
		mc.QueryContext(ctx, "SELECT * FROM table WHERE column = ?", []driver.NamedValue{{Value: "value"}})

		// commit the transaction
		err = tx.Commit()
		Expect(err).ShouldNot(HaveOccurred())

		// collect and compare metrics
		err = testutil.CollectAndCompare(metrics.SqlStmtsSummary, strings.NewReader(help+service1Metrics))
		Expect(err).ShouldNot(HaveOccurred())

		// reset the metrics
		// new context
		ctx = ContextWithExecLabels(context.Background(), map[string]string{"grpc_method": "method_2", "grpc_service": "service_2"})
		// begin a transaction
		tx, err = mc.BeginTx(ctx, driver.TxOptions{})
		Expect(err).ShouldNot(HaveOccurred())

		// exec a statement
		mc.Exec("INSERT INTO table (column) VALUES (?)", []driver.Value{"value"})
		mc.Exec("INSERT INTO table (column) VALUES (?)", []driver.Value{"value"})

		// query a statement
		mc.Query("SELECT * FROM table WHERE column = ?", []driver.Value{"value"})
		mc.Query("SELECT * FROM table WHERE column = ?", []driver.Value{"value"})

		// exec a statement with context
		mc.ExecContext(ctx, "INSERT INTO table (column) VALUES (?)", []driver.NamedValue{{Value: "value"}})
		mc.ExecContext(ctx, "INSERT INTO table (column) VALUES (?)", []driver.NamedValue{{Value: "value"}})

		// query a statement with context
		mc.QueryContext(ctx, "SELECT * FROM table WHERE column = ?", []driver.NamedValue{{Value: "value"}})
		mc.QueryContext(ctx, "SELECT * FROM table WHERE column = ?", []driver.NamedValue{{Value: "value"}})

		// commit the transaction
		err = tx.Commit()
		Expect(err).ShouldNot(HaveOccurred())

		// collect and compare metrics
		err = testutil.CollectAndCompare(metrics.SqlStmtsSummary, strings.NewReader(help+service1Metrics+service2Metrics))
		Expect(err).ShouldNot(HaveOccurred())

		// rerun with initial metrics
		ctx = ContextWithExecLabels(context.Background(), map[string]string{"grpc_method": "method_1", "grpc_service": "service_1"})
		// begin a transaction
		tx, err = mc.BeginTx(ctx, driver.TxOptions{})
		Expect(err).ShouldNot(HaveOccurred())

		// exec a statement with context
		mc.ExecContext(ctx, "INSERT INTO table (column) VALUES (?)", []driver.NamedValue{{Value: "value"}})

		// query a statement with context
		mc.QueryContext(ctx, "SELECT * FROM table WHERE column = ?", []driver.NamedValue{{Value: "value"}})

		// rollback the transaction
		err = tx.Rollback()
		Expect(err).ShouldNot(HaveOccurred())

		// collect and compare metrics
		err = testutil.CollectAndCompare(metrics.SqlStmtsSummary, strings.NewReader(help+service1RerunMetrics+service2Metrics))
		Expect(err).ShouldNot(HaveOccurred())

		// non labeled context
		ctx = context.Background()
		// begin a transaction
		tx, err = mc.BeginTx(ctx, driver.TxOptions{})
		Expect(err).ShouldNot(HaveOccurred())

		// exec query context
		mc.ExecContext(ctx, "INSERT INTO table (column) VALUES (?)", []driver.NamedValue{{Value: "value"}})

		// query a statement with context
		mc.QueryContext(ctx, "SELECT * FROM table WHERE column = ?", []driver.NamedValue{{Value: "value"}})

		// commit the transaction
		err = tx.Commit()
		Expect(err).ShouldNot(HaveOccurred())

		// collect and compare metrics
		err = testutil.CollectAndCompare(metrics.SqlStmtsSummary, strings.NewReader(help+noMethodMetrics+service1RerunMetrics+service2Metrics))
		Expect(err).ShouldNot(HaveOccurred())
	})
})

func CollectAndCompareMetrics(r io.Reader) error {
	return testutil.CollectAndCompare(metrics.SqlStmtsSummary, r)
}
