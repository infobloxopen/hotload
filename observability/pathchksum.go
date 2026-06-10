package observability

import (
	"hash/crc64"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/infobloxopen/hotload/v3/logger"
)

// PathChksumMetricsEnableEnvVar gates checksum collection: hashing every
// watched file on each scrape is not free, so it is opt-in, exactly as in
// hotload v1.
const PathChksumMetricsEnableEnvVar = "HOTLOAD_PATH_CHKSUM_METRICS_ENABLE"

// HotloadPathChksumTimestampSecondsName is the metric name, identical to
// hotload v1.
const HotloadPathChksumTimestampSecondsName = "hotload_path_chksum_timestamp_seconds"

// FileHasher hashes a file's contents; replaceable for unit tests.
type FileHasher func(filePath string) (uint64, error)

var crc64Table = crc64.MakeTable(crc64.ECMA)

// DefaultFileHasher hashes file contents using CRC64.
func DefaultFileHasher(filePath string) (uint64, error) {
	pathBytes, err := os.ReadFile(filePath)
	if err != nil {
		return 0, err
	}
	return crc64.Checksum(pathBytes, crc64Table), nil
}

// PathChksumCollector is a prometheus.Collector reporting, per watched
// path, the unix timestamp at which the file's content checksum last
// changed. The checksum is computed at scrape time, so the metric stays
// accurate without a background poller. It replaces hotload v1's
// gaugefuncvec-based implementation.
type PathChksumCollector struct {
	desc    *prometheus.Desc
	hasher  FileHasher
	enabled bool

	mu    sync.Mutex
	paths map[string]*chksumRecord
}

type chksumRecord struct {
	crc64       uint64
	lastChanged int64
}

// NewPathChksumCollector creates a collector using hasher (DefaultFileHasher
// when nil). Collection is enabled by PathChksumMetricsEnableEnvVar.
func NewPathChksumCollector(hasher FileHasher) *PathChksumCollector {
	if hasher == nil {
		hasher = DefaultFileHasher
	}
	enabled := false
	switch strings.ToLower(strings.TrimSpace(os.Getenv(PathChksumMetricsEnableEnvVar))) {
	case "1", "true", "yes":
		enabled = true
	}
	return &PathChksumCollector{
		desc: prometheus.NewDesc(
			HotloadPathChksumTimestampSecondsName,
			"Hotload path checksum last changed (unix timestamp), by path",
			[]string{PathKey}, nil,
		),
		hasher:  hasher,
		enabled: enabled,
		paths:   make(map[string]*chksumRecord),
	}
}

// AddPath starts reporting the checksum timestamp of pathStr. Duplicate adds
// are ignored. Fed automatically from hotload's watch events when the
// collector is wired through Collectors.Hooks.
func (p *PathChksumCollector) AddPath(pathStr string) {
	if !p.enabled {
		return
	}
	pathStr = cleanPath(pathStr)
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, found := p.paths[pathStr]; !found {
		p.paths[pathStr] = &chksumRecord{}
	}
}

// Describe implements prometheus.Collector.
func (p *PathChksumCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- p.desc
}

// Collect implements prometheus.Collector. Each watched file is hashed; a
// changed checksum bumps the path's last-changed timestamp.
func (p *PathChksumCollector) Collect(ch chan<- prometheus.Metric) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for pathStr, rec := range p.paths {
		newCrc, err := p.hasher(pathStr)
		if err != nil {
			logger.ErrLogf("PathChksumCollector", "hashing %s failed: %v", pathStr, err)
		} else if rec.crc64 != newCrc {
			rec.crc64 = newCrc
			rec.lastChanged = time.Now().Unix()
		}
		ch <- prometheus.MustNewConstMetric(p.desc, prometheus.GaugeValue, float64(rec.lastChanged), pathStr)
	}
}

func cleanPath(pathStr string) string {
	return path.Clean(strings.TrimSpace(pathStr))
}
