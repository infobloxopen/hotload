# Migrating from hotload v1 to v3

For most applications the upgrade is an import-path change. The driver name
(`"hotload"`), the DSN format (`strategy://driver/path?forceKill=...`), the
registration functions, the `Strategy` interface and the graceful/forceKill
semantics are unchanged.

## 1. Import path

```diff
-import "github.com/infobloxopen/hotload"
-import _ "github.com/infobloxopen/hotload/fsnotify"
+import "github.com/infobloxopen/hotload/v3"
+import _ "github.com/infobloxopen/hotload/v3/fsnotify"
```

```sh
go get github.com/infobloxopen/hotload/v3
```

## 2. Metrics are now opt-in (action required if you scrape hotload metrics)

**This is the most important behavioral change.** Hotload v1 registered its
prometheus metrics with the default registerer as a side effect of importing
the package. The v3 core has no prometheus dependency at all; if you do
nothing, you get no metrics.

To keep your dashboards working, add the observability module:

```sh
go get github.com/infobloxopen/hotload/observability
```

```go
import "github.com/infobloxopen/hotload/observability"

func main() {
    observability.MustEnablePrometheus(nil) // nil = prometheus.DefaultRegisterer
    ...
}
```

Metric names, labels, and the `HOTLOAD_PATH_CHKSUM_METRICS_ENABLE` gate are
identical to v1:

| v1 metric | v3 |
|---|---|
| `transaction_sql_stmts` | same name, via observability module |
| `hotload_change_total` | same name, via observability module |
| `hotload_last_changed_timestamp_seconds` | same name, via observability module |
| `hotload_modtime_latency_histogram` | same name, via observability module |
| `hotload_path_chksum_timestamp_seconds` | same name, via observability module |

One accuracy improvement: statements executed through prepared statements now
count toward `transaction_sql_stmts` (v1 did not wrap `driver.Stmt`, so
prepared-statement traffic was invisible).

The `hotload/metrics` package is gone. Its helper APIs map as follows:

| v1 | v3 |
|---|---|
| `metrics.GetCollectors()` | `observability.NewCollectors().All()` or the return of `EnablePrometheus` |
| `metrics.GRPCServiceKey` etc. | `observability.GRPCServiceKey` etc. |
| `internal.CollectAndRegexpCompare` (not exported in v1) | `observability/promtest.CollectAndRegexpCompare` |

`hotload.ContextWithExecLabels` / `hotload.GetExecLabelsFromContext` are
unchanged and remain in the core.

## 3. Truthful driver capabilities

v1 implemented a fixed set of optional `database/sql/driver` interfaces and
answered `driver.ErrSkip` for unsupported ones. v3 wraps connections and
statements so optional interfaces exist if and only if the underlying driver
supports them. For mainstream drivers (lib/pq, pgx stdlib, mysql) behavior is
unchanged; for minimal drivers, `database/sql` now takes its documented
fallback paths (e.g. prepared statements) instead of `ErrSkip` round-trips.

Code that type-asserts on the raw driver conn (e.g. inside `sql.Conn.Raw`)
sees the new truthful method set.

## 4. Lifecycle changes

- **Errors at `sql.Open`:** the strategy watch starts when `sql.Open` is
  called (hotload now implements `driver.DriverContext`), so a bad DSN,
  unknown strategy/driver, or unreadable config file fails fast at `sql.Open`
  instead of at first query.
- **Teardown:** closing the last `sql.DB` for a DSN now stops the strategy
  watch and the background goroutine; v1 leaked both.
- **forceKill:** in-flight operations canceled by a config change fail with
  an error matching `hotload.ErrHotSwap` (use `errors.Is`). The new
  `killWindow` DSN parameter (default `100ms`) bounds how long hotload waits
  for in-flight work to observe cancellation before force-closing
  connections.
- **modtime:** `modtime.NewModTimeMonitor` API is unchanged but reports
  latency through hotload hooks; enable the observability module to keep the
  histogram.

## 5. Removed/changed APIs

| v1 | v3 |
|---|---|
| `hotload.WithLogger` / `hotload.GetLogger` | still present (deprecated); prefer `logger.WithLogger` / `logger.GetLogger` |
| `hotload/metrics` package | removed; see section 2 |
| `hotload.Register` (alias mentioned in old README) | was already `RegisterSQLDriver`; unchanged |

## 6. Dependency diet

The v3 core module depends only on `github.com/fsnotify/fsnotify`. The
following are gone from your module graph unless you import the
observability module: prometheus (client_golang, common, client_model,
procfs), pkg/errors, google/uuid, teivah/onecontext, colega/gaugefuncvec,
ginkgo/gomega, go-sqlmock, lib/pq.
