package metrics

import (
	"errors"
	"hash/crc64"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/infobloxopen/hotload/logger"
)

const (
	PathChksumMetricsEnableEnvVar = "HOTLOAD_PATH_CHKSUM_METRICS_ENABLE"
)

var (
	ErrDuplicatePath = errors.New("duplicate path")
	ErrPathNotFound  = errors.New("path not found")

	crc64Table = crc64.MakeTable(crc64.ECMA)

	defaultPathChksum *pathChksum
)

// InitPathChksumMetrics initializes path checksum metrics.
// This must be called explicitly if you want path checksum metrics.
// It is not called automatically to avoid prometheus dependencies.
func InitPathChksumMetrics() {
	if defaultPathChksum == nil {
		defaultPathChksum = newPathChksum(DefaultFileHasher)
	}
}

// AddToDefaultPathChksum adds a path to the global defaultPathChksum for checksum metrics
func AddToDefaultPathChksum(pathStr string) error {
	if defaultPathChksum == nil {
		// Path checksum metrics not initialized, skip silently
		return nil
	}
	return defaultPathChksum.addPath(pathStr)
}

type pathChksum struct {
	sync.RWMutex // used to synchronize changes to the set of paths being monitored
	enabled      bool
	fileHasher   FileHasher
	paths        map[string]*pathRecord
}

type pathRecord struct {
	path        string
	crc64       uint64
	lastChanged int64
}

// Define FileHasher type so we can mock it for unit-testing
type FileHasher func(filePath string) (uint64, error)

// DefaultFileHasher hashes file contents using CRC64
func DefaultFileHasher(filePath string) (uint64, error) {
	pathBytes, err := os.ReadFile(filePath)
	if err != nil {
		logger.ErrLogf("DefaultFileHasher", "ReadFile(%s) err=%s", filePath, err)
		return 0, err
	}

	newCrc64 := crc64.Checksum(pathBytes, crc64Table)
	return newCrc64, nil
}

// newPathChksum returns a new PathChksum
func newPathChksum(fileHasher FileHasher) *pathChksum {
	if fileHasher == nil {
		panic("nil FileHasher")
	}

	enabledStr := strings.ToLower(strings.TrimSpace(os.Getenv(PathChksumMetricsEnableEnvVar)))
	enabledFlg := false
	switch enabledStr {
	case "1", "true", "yes":
		enabledFlg = true
	}

	pthm := &pathChksum{
		enabled:    enabledFlg,
		fileHasher: fileHasher,
		paths:      make(map[string]*pathRecord),
	}
	return pthm
}

// addPath adds a path to be checksum'd for change in contents,
// and registers path for metrics collection
func (pthm *pathChksum) addPath(pathStr string) error {
	if !pthm.enabled {
		return nil
	}

	pathStr = CleanPath(pathStr)

	pthm.Lock()
	defer pthm.Unlock()

	pathRec, found := pthm.paths[pathStr]
	if found {
		return ErrDuplicatePath
	}

	pathRec = &pathRecord{
		path: pathStr,
	}
	pthm.paths[pathStr] = pathRec

	// Note: Prometheus registration removed to eliminate prometheus dependencies.
	// If you need path checksum metrics, implement a custom solution using
	// the pathRecord data or integrate with your metrics provider.

	return nil
}

// CleanPath cleans and trimspaces path strings
func CleanPath(pathStr string) string {
	return path.Clean(strings.TrimSpace(pathStr))
}
