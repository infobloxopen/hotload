package modtime

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/infobloxopen/hotload/logger"
	"github.com/infobloxopen/hotload/metrics"
)

var (
	ErrDuplicatePath = errors.New("duplicate path")
	ErrPathNotFound  = errors.New("path not found")
)

// ModTimeMonitor monitors the modtimes for a set of paths in a filesystem
type ModTimeMonitor struct {
	sync.RWMutex // used to synchronize changes to the set of paths being monitored
	log          logger.Logger
	statFS       fs.StatFS // use io/fs.FS so we can mock FileSystem for unit-tests
	checkIntv    time.Duration
	paths        map[string]*pathRecord
}

// pathRecord holds the internal monitored data for a path.
// Atomics are used to allow concurrent update/read-access.
type pathRecord struct {
	pathStr     string
	refreshIntv time.Duration
	modTime     atomic.Value
	failCount   atomic.Int32
}

// PathStatus holds status of a monitored path, returned by GetPathStatus()
type PathStatus struct {
	PathStr   string
	ModTime   time.Time
	FailCount int32
}

// NewModTimeMonitor returns a new ModTimeMonitor with a
// background monitoring thread already started
func NewModTimeMonitor(ctx context.Context, opts ...Option) *ModTimeMonitor {
	defOpts := newDefaultOptions()
	for _, opt := range opts {
		opt(defOpts)
	}

	mtm := &ModTimeMonitor{
		log:       defOpts.log,
		statFS:    defOpts.statFS,
		checkIntv: defOpts.checkIntv,
		paths:     make(map[string]*pathRecord),
	}
	go mtm.runLoop(ctx)
	return mtm
}

// AddMonitoredPath adds a path to be monitored for modtime changes
func (mtm *ModTimeMonitor) AddMonitoredPath(pathStr string, opts ...Option) error {
	defOpts := newDefaultOptions()
	for _, opt := range opts {
		opt(defOpts)
	}

	pathStr = CleanPath(pathStr)

	mtm.Lock()
	defer mtm.Unlock()

	pathRec, found := mtm.paths[pathStr]
	if found {
		return ErrDuplicatePath
	}

	var zeroTime time.Time
	pathRec = &pathRecord{
		pathStr:     pathStr,
		refreshIntv: defOpts.refreshIntv,
	}
	pathRec.modTime.Store(zeroTime)
	mtm.paths[pathStr] = pathRec

	return nil
}

// GetPathStatus returns the current status of a monitored path
func (mtm *ModTimeMonitor) GetPathStatus(pathStr string) (*PathStatus, error) {
	pathStr = CleanPath(pathStr)

	mtm.RLock()
	defer mtm.RUnlock()

	pathRec, found := mtm.paths[pathStr]
	if !found {
		return nil, ErrPathNotFound
	}

	// Make a copy of status info returned,
	// do NOT return the actual internal status info
	// for thread-safety
	pathSts := &PathStatus{
		PathStr:   pathRec.pathStr,
		ModTime:   pathRec.modTime.Load().(time.Time),
		FailCount: pathRec.failCount.Load(),
	}

	return pathSts, nil
}

// checkPathModTimes checks the modtimes of its set of paths
func (mtm *ModTimeMonitor) checkPathModTimes(ctx context.Context, nowTime time.Time) {
	mtm.RLock()
	defer mtm.RUnlock()

	for pathStr, pathRec := range mtm.paths {
		// When using fs.FS, paths must be unrooted
		// See https://github.com/golang/go/issues/44279#issuecomment-956251702
		// See https://pkg.go.dev/io/fs#ValidPath
		unrooted := strings.TrimLeft(pathStr, "/")
		fInfo, err := mtm.statFS.Stat(unrooted)
		if err != nil {
			// log error, but continue
			mtm.log(fmt.Sprintf("checkPathModTimes: Stat(%s) err=%s", pathStr, err))
		} else {
			newTime := fInfo.ModTime()
			mtm.log(fmt.Sprintf("checkPathModTimes: Stat(%s) ModTime=%s", pathStr, newTime))
			pathRec.modTime.Store(newTime)
		}

		if nowTime.Sub(pathRec.modTime.Load().(time.Time)) > pathRec.refreshIntv {
			if pathRec.failCount.Load() <= 0 {
				metrics.SetHotloadFsnotifyModtimeCheckGauge(pathStr, 1)
				metrics.IncHotloadFsnotifyModtimeFailureCounter(pathStr)
			}
			pathRec.failCount.Add(1)
		} else {
			if pathRec.failCount.Load() > 0 {
				metrics.SetHotloadFsnotifyModtimeCheckGauge(pathStr, 0)
			}
			pathRec.failCount.Store(0)
		}
	}
}

// runLoop is the background ModTimeMonitor thread that
// periodically checks its paths' modtimes in a loop.
// Terminated when context is done (canceled).
func (mtm *ModTimeMonitor) runLoop(ctx context.Context) {
	mtm.log("ModTimeMonitor.runLoop: started")

	checkTicker := time.NewTicker(mtm.checkIntv)
	defer checkTicker.Stop()

loop:
	for {
		select {
		case <-ctx.Done():
			checkTicker.Stop()
			break loop

		case curTime := <-checkTicker.C:
			mtm.checkPathModTimes(ctx, curTime)
		}
	}

	mtm.log("ModTimeMonitor.runLoop: terminated")
}

// CleanPath cleans and trimspaces path strings
func CleanPath(pathStr string) string {
	return path.Clean(strings.TrimSpace(pathStr))
}
