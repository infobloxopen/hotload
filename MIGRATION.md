# Migrating from hotload v1 to v3

For most applications the upgrade is an import-path change. The driver name
(`"hotload"`), the DSN format (`strategy://driver/path?forceKill=...`), the
registration functions and the graceful/forceKill semantics are unchanged.
Only authors of custom strategies are affected by the reworked `Strategy`
interface (section 5).

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

With a nil (default) registerer the call is idempotent: the first call wins
and later calls return the same collectors, so an application and a shared
library can both enable metrics defensively without a duplicate-registration
panic. Explicit registerers register fresh collectors on every call.

As a safety net, if the first watch starts with no hooks registered at all,
hotload logs a one-time notice through its error logger (visible by default)
pointing at this section — so forgetting the call above shows up in logs
instead of as silently empty dashboards. Registering any hooks, or replacing
the error logger via `logger.WithErrLogger`, silences it.

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
| `Strategy.CloseWatch` / `Strategy.Close` | removed; `Watch` returns a per-watch `Watchable` handle (see below) |

**Custom strategy authors:** `Strategy` is now a single-method interface.
`Watch` still receives `(ctx, pth, pathQry)` and still returns the current
value synchronously, but the update channel is wrapped in a per-watch
`Watchable` handle:

```go
type Strategy interface {
	Watch(ctx context.Context, pth string, pathQry string) (value string, watch Watchable, err error)
}

type Watchable interface {
	Values() <-chan string
	Close() error
}
```

Lifecycle rules:

- Each `Watch` call establishes an independent watch with its own channel,
  even for a path/query pair already being watched (share the underlying
  resource watch internally if you like).
- The watch ends when its `Watchable` is closed **or** the `Watch` context
  is canceled; either way the strategy releases the watch's resources and
  closes the `Values` channel. `Close` must be idempotent and must not call
  back into hotload.
- There is no strategy-wide `Close` anymore: a registered strategy lives for
  the process. Tests wanting isolation construct fresh strategy instances.

This removes the identity bookkeeping v1 forced on strategies: `CloseWatch`
had to re-parse `pth`/`pathQry` to find the watch to close; now the handle
*is* the watch.

## 6. Dependency diet

The v3 core module depends only on `github.com/fsnotify/fsnotify`. The
following are gone from your module graph unless you import the
observability module: prometheus (client_golang, common, client_model,
procfs), pkg/errors, google/uuid, teivah/onecontext, colega/gaugefuncvec,
ginkgo/gomega, go-sqlmock, lib/pq.
