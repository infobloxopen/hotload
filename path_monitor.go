package hotload

import (
	"context"
	"errors"
	"fmt"
	"hash/crc64"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/infobloxopen/hotload/logger"
	"github.com/infobloxopen/hotload/metrics"
)

const (
	CheckIntervalEnvVar = "HOTLOAD_PATH_MONITOR_CHECK_INTERVAL_DURATION"
)

var (
	ErrDuplicatePath = errors.New("duplicate path")
	ErrPathNotFound  = errors.New("path not found")

	crc64Table = crc64.MakeTable(crc64.ECMA)
)

type PathMonitor struct {
	sync.RWMutex // used to synchronize changes to the set of paths being monitored
	ctx          context.Context
	checkIntv    time.Duration
	paths        map[string]*pathRecord
}

type pathRecord struct {
	path  string
	crc64 uint64
}

func NewPathMonitor(ctx context.Context) *PathMonitor {
	durStr := os.Getenv(CheckIntervalEnvVar)
	durStr = strings.TrimSpace(durStr)
	if len(durStr) <= 0 {
		durStr = "60s"
	}
	checkIntv, err := time.ParseDuration(durStr)
	if err != nil {
		panic(fmt.Sprintf("time.ParseDuration(%s) err=%v", durStr, err))
	}

	pthm := &PathMonitor{
		ctx:       ctx,
		checkIntv: checkIntv,
		paths:     make(map[string]*pathRecord),
	}

	go pthm.runLoop(pthm.ctx)

	return pthm
}

// AddMonitoredPath adds a path to be checked for change in contents
func (pthm *PathMonitor) AddMonitoredPath(pathStr string) error {
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

	return nil
}

// checkPathChksums checks for changes in its set of paths
func (pthm *PathMonitor) checkPathChksums(ctx context.Context, nowTime time.Time) {
	nowUnix := float64(nowTime.Unix())

	pthm.RLock()
	defer pthm.RUnlock()

	for pathStr, pathRec := range pthm.paths {
		pathBytes, err := os.ReadFile(pathStr)
		if err != nil {
			// log error, but continue
			logger.ErrLogf("PathMonitor.checkPathChksums", "ReadFile(%s) err=%s", pathStr, err)
			continue
		}

		newCrc64 := crc64.Checksum(pathBytes, crc64Table)
		if pathRec.crc64 == newCrc64 {
			continue
		}

		pathRec.crc64 = newCrc64

		// Update metrics
		metrics.IncHotloadPathChksumChangeTotal(pathStr)
		metrics.SetHotloadPathChksumTimestampSeconds(pathStr, nowUnix)
	}
}

// runLoop is the background PathMonitor thread that
// periodically checks for changes in paths' contents in a loop.
// Terminated when context is done (canceled).
func (pthm *PathMonitor) runLoop(ctx context.Context) {
	logger.Logf("PathMonitor.runLoop", "started")

	checkTicker := time.NewTicker(pthm.checkIntv)
	defer checkTicker.Stop()

loop:
	for {
		select {
		case <-ctx.Done():
			checkTicker.Stop()
			break loop

		case curTime := <-checkTicker.C:
			pthm.checkPathChksums(ctx, curTime)
		}
	}

	logger.Logf("PathMonitor.runLoop", "terminated")
}

// CleanPath cleans and trimspaces path strings
func CleanPath(pathStr string) string {
	return path.Clean(strings.TrimSpace(pathStr))
}
