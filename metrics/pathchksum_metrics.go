package metrics

import (
	"errors"
	"hash/crc64"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/colega/gaugefuncvec"
	"github.com/infobloxopen/hotload/logger"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	PathChksumMetricsEnableEnvVar = "HOTLOAD_PATH_CHKSUM_METRICS_ENABLE"
)

var (
	ErrDuplicatePath = errors.New("duplicate path")
	ErrPathNotFound  = errors.New("path not found")

	HotloadPathChksumTimestampSecondsName         = "hotload_path_chksum_timestamp_seconds"
	HotloadPathChksumTimestampSecondsHelp         = "Hotload path checksum last changed (unix timestamp), by path"
	HotloadPathChksumTimestampSecondsGaugeFuncVec = gaugefuncvec.New(prometheus.GaugeOpts{
		Name: HotloadPathChksumTimestampSecondsName,
		Help: HotloadPathChksumTimestampSecondsHelp,
	}, []string{PathKey})

	crc64Table = crc64.MakeTable(crc64.ECMA)

	defaultPathChksum *pathChksum
)

func init() {
	defaultPathChksum = newPathChksum(DefaultFileHasher)
	prometheus.MustRegister(HotloadPathChksumTimestampSecondsGaugeFuncVec)
}

// AddToDefaultPathChksum adds a path to the global defaultPathChksum for checksum metrics
func AddToDefaultPathChksum(pathStr string) error {
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

	scraperFn := func() float64 {
		if !pthm.enabled {
			return float64(0)
		}

		newCrc64, err := pthm.fileHasher(pathRec.path)
		if err != nil {
			// log error, but continue
			logger.ErrLogf("PathChksum.scraper", "fileHasher(%s) err=%s", pathRec.path, err)
		} else if pathRec.crc64 != newCrc64 {
			pathRec.crc64 = newCrc64
			pathRec.lastChanged = time.Now().Unix()
		}

		return float64(pathRec.lastChanged)
	}

	HotloadPathChksumTimestampSecondsGaugeFuncVec.MustRegister(
		prometheus.Labels{PathKey: pathStr},
		scraperFn,
	)

	return nil
}

// CleanPath cleans and trimspaces path strings
func CleanPath(pathStr string) string {
	return path.Clean(strings.TrimSpace(pathStr))
}

var ExpectHotloadPathChksumTimestampSecondsPreamble = `                                                             
# HELP hotload_path_chksum_timestamp_seconds Hotload path checksum last changed \(unix timestamp\), by path             
# TYPE hotload_path_chksum_timestamp_seconds gauge`

var ExpectHotloadPathChksumTimestampSecondsRegexp = `                                                             
hotload_path_chksum_timestamp_seconds{path="%s"} \d\.\d+e\+\d+`
