package observability

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/infobloxopen/hotload/observability/promtest"
)

func TestPathChksumCollector(t *testing.T) {
	t.Setenv(PathChksumMetricsEnableEnvVar, "true")

	var hash atomic.Uint64
	hash.Store(1)
	c := NewPathChksumCollector(func(filePath string) (uint64, error) {
		return hash.Load(), nil
	})

	c.AddPath("/etc/dsn")
	c.AddPath("/etc/dsn") // duplicate adds are ignored

	expect := `
# HELP hotload_path_chksum_timestamp_seconds Hotload path checksum last changed \(unix timestamp\), by path
# TYPE hotload_path_chksum_timestamp_seconds gauge
hotload_path_chksum_timestamp_seconds\{path="/etc/dsn"\} 1\.\d+e\+09
`
	if err := promtest.CollectAndRegexpCompare(c, strings.NewReader(expect), HotloadPathChksumTimestampSecondsName); err != nil {
		t.Errorf("first scrape diff:\n%s", err)
	}

	// An unchanged checksum keeps the timestamp; a changed one bumps it.
	first := collectValue(t, c)
	if v := collectValue(t, c); v != first {
		t.Errorf("timestamp changed without content change: %v -> %v", first, v)
	}
	hash.Store(2)
	if v := collectValue(t, c); v < first {
		t.Errorf("timestamp went backwards after content change: %v -> %v", first, v)
	}
}

func TestPathChksumCollectorDisabled(t *testing.T) {
	t.Setenv(PathChksumMetricsEnableEnvVar, "")

	c := NewPathChksumCollector(func(filePath string) (uint64, error) { return 1, nil })
	c.AddPath("/etc/dsn")

	if err := promtest.CollectAndRegexpCompare(c, strings.NewReader(""), HotloadPathChksumTimestampSecondsName); err != nil {
		t.Errorf("disabled collector should produce no series, got diff:\n%s", err)
	}
}

func TestPathChksumCollectorHashError(t *testing.T) {
	t.Setenv(PathChksumMetricsEnableEnvVar, "yes")

	c := NewPathChksumCollector(func(filePath string) (uint64, error) {
		return 0, fmt.Errorf("boom")
	})
	c.AddPath("/etc/dsn")

	// Hash errors keep the last value (zero here) rather than dropping the
	// series or panicking.
	expect := `
# HELP hotload_path_chksum_timestamp_seconds Hotload path checksum last changed \(unix timestamp\), by path
# TYPE hotload_path_chksum_timestamp_seconds gauge
hotload_path_chksum_timestamp_seconds\{path="/etc/dsn"\} 0
`
	if err := promtest.CollectAndRegexpCompare(c, strings.NewReader(expect), HotloadPathChksumTimestampSecondsName); err != nil {
		t.Errorf("unexpected diff:\n%s", err)
	}
}

// collectValue scrapes the single-series collector and returns its value.
func collectValue(t *testing.T, c *PathChksumCollector) float64 {
	t.Helper()
	return testutil.ToFloat64(c)
}
