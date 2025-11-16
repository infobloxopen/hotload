# Prometheus Metrics for Hotload

This is an optional package that provides Prometheus metrics for the hotload library.

## Usage

To enable Prometheus metrics, import this package and register the provider:

```go
import (
    "github.com/infobloxopen/hotload"
    "github.com/infobloxopen/hotload/prometheusmetrics"
)

func main() {
    // Create and register the Prometheus provider
    provider := prometheusmetrics.NewPrometheusProvider()
    if err := provider.Register(); err != nil {
        log.Fatal(err)
    }
    
    // Now hotload will emit Prometheus metrics
    // ... rest of your code
}
```

## Metrics Provided

- `transaction_sql_stmts` - Summary of SQL statements per transaction
- `hotload_modtime_latency_histogram` - Histogram of modification time latency
- `hotload_change_total` - Counter of detected changes
- `hotload_last_changed_timestamp_seconds` - Gauge of last change timestamp

## Why a Separate Module?

This package is in a separate Go module to keep the main `hotload` library dependency-free from Prometheus. You only need to import this package if you want Prometheus metrics support.
