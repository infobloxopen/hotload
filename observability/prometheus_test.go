package observability

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/infobloxopen/hotload/observability/promtest"
	hotload "github.com/infobloxopen/hotload/v3"
)

func TestConfigChangeMetrics(t *testing.T) {
	c := NewCollectors()
	h := c.Hooks()

	at := time.Unix(1764000000, 0)
	ev := hotload.ConfigChangeEvent{GroupName: "fsnotify://postgres/etc/dsn", At: at}
	h.OnConfigChange(ev)
	h.OnConfigChange(hotload.ConfigChangeEvent{GroupName: "fsnotify://postgres/etc/dsn", At: at.Add(time.Minute)})

	if got := testutil.ToFloat64(c.HotloadChangeTotal.WithLabelValues(ev.GroupName)); got != 2 {
		t.Errorf("hotload_change_total = %v, want 2", got)
	}
	want := float64(at.Add(time.Minute).Unix())
	if got := testutil.ToFloat64(c.HotloadLastChangedTimestampSeconds.WithLabelValues(ev.GroupName)); got != want {
		t.Errorf("hotload_last_changed_timestamp_seconds = %v, want %v", got, want)
	}
}

func TestTxMetrics(t *testing.T) {
	c := NewCollectors()
	h := c.Hooks()

	ctx := hotload.ContextWithExecLabels(context.Background(), map[string]string{
		GRPCServiceKey: "svc",
		GRPCMethodKey:  "m",
	})
	h.OnTxComplete(hotload.TxEvent{Ctx: ctx, ExecStmts: 3, QueryStmts: 1, Committed: true})

	expect := `
# HELP transaction_sql_stmts The number of sql stmts called in a transaction by statement type per grpc service and method
# TYPE transaction_sql_stmts summary
transaction_sql_stmts_sum\{grpc_method="m",grpc_service="svc",stmt="exec"\} 3
transaction_sql_stmts_count\{grpc_method="m",grpc_service="svc",stmt="exec"\} 1
transaction_sql_stmts_sum\{grpc_method="m",grpc_service="svc",stmt="query"\} 1
transaction_sql_stmts_count\{grpc_method="m",grpc_service="svc",stmt="query"\} 1
`
	err := promtest.CollectAndRegexpCompare(c.SqlStmtsSummary, strings.NewReader(expect), SqlStmtsSummaryName)
	if err != nil {
		t.Errorf("unexpected metrics diff:\n%s", err)
	}
}

func TestModTimeMetrics(t *testing.T) {
	c := NewCollectors()
	h := c.Hooks()

	h.OnModTimeCheck(hotload.ModTimeEvent{Strategy: "fsnotify", Path: "/etc/dsn", Latency: 1000 * time.Second})

	if got := testutil.CollectAndCount(c.HotloadModtimeLatencyHistogram, HotloadModtimeLatencyHistogramName); got != 1 {
		t.Errorf("histogram series count = %d, want 1", got)
	}
}

func TestEnablePrometheus(t *testing.T) {
	reg := prometheus.NewRegistry()
	c, err := EnablePrometheus(reg)
	if err != nil {
		t.Fatal(err)
	}

	c.Hooks().OnConfigChange(hotload.ConfigChangeEvent{GroupName: "g", At: time.Now()})
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, mf := range families {
		if mf.GetName() == HotloadChangeTotalName {
			found = true
		}
	}
	if !found {
		t.Errorf("%s not gatherable from registry", HotloadChangeTotalName)
	}

	// Double registration must error, not panic.
	if _, err := EnablePrometheus(reg); err == nil {
		t.Error("second EnablePrometheus on same registry should error")
	}
}

// TestPathChksumOnlyFsnotify: watch events register chksum paths only for
// the fsnotify strategy — other strategies' paths (Secret names, etcd keys)
// are not local files and must not be hashed at scrape time.
func TestPathChksumOnlyFsnotify(t *testing.T) {
	t.Setenv(PathChksumMetricsEnableEnvVar, "true")
	c := NewCollectors()
	h := c.Hooks()

	h.OnWatch(hotload.WatchEvent{Strategy: "fsnotify", Path: "/etc/dsn"})
	h.OnWatch(hotload.WatchEvent{Strategy: "k8ssecret", Path: "/mydb"})
	h.OnWatch(hotload.WatchEvent{Strategy: "fsnotify", Path: "/etc/other", Closed: true})

	col := c.HotloadPathChksumTimestampSeconds
	col.mu.Lock()
	defer col.mu.Unlock()
	if _, ok := col.paths["/etc/dsn"]; !ok {
		t.Error("fsnotify watch path not registered for chksum")
	}
	if _, ok := col.paths["/mydb"]; ok {
		t.Error("k8ssecret path must not be registered for chksum")
	}
	if _, ok := col.paths["/etc/other"]; ok {
		t.Error("closed watch event must not register a path")
	}
}

// TestEnablePrometheusDefaultIdempotent: hotload v1 enabled metrics as an
// import side effect, so migrated code may enable defensively in more than
// one place; with the default registerer the second call must return the
// same collectors instead of a duplicate-registration panic.
func TestEnablePrometheusDefaultIdempotent(t *testing.T) {
	c1 := MustEnablePrometheus(nil)
	c2 := MustEnablePrometheus(nil)
	if c1 != c2 {
		t.Error("second MustEnablePrometheus(nil) returned different collectors")
	}
	if c3 := MustEnablePrometheus(prometheus.DefaultRegisterer); c3 != c1 {
		t.Error("MustEnablePrometheus(DefaultRegisterer) should take the idempotent default path")
	}
}
