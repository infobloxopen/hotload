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
	paths        map[pathKey]*pathRecord
}

type pathKey struct {
	strategy string
	path     string
}

// String implements fmt.Stringer interface
func (pk pathKey) String() string {
	return fmt.Sprintf("%s:%s", pk.strategy, pk.path)
}

// pathRecord holds the internal monitored data for a path.
// Atomics are used to allow concurrent update/read-access.
type pathRecord struct {
	key     pathKey
	modTime atomic.Value
}

// PathStatus holds status of a monitored path, returned by GetPathStatus()
type PathStatus struct {
	StrategyStr string
	PathStr     string
	ModTime     time.Time
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
		paths:     make(map[pathKey]*pathRecord),
	}
	go mtm.runLoop(ctx)
	return mtm
}

// AddMonitoredPath adds a path to be monitored for modtime changes
func (mtm *ModTimeMonitor) AddMonitoredPath(strategyStr, pathStr string, opts ...Option) error {
	defOpts := newDefaultOptions()
	for _, opt := range opts {
		opt(defOpts)
	}

	pathStr = CleanPath(pathStr)

	mtm.Lock()
	defer mtm.Unlock()

	var zeroTime time.Time
	pkey := pathKey{
		strategy: strategyStr,
		path:     pathStr,
	}

	pathRec, found := mtm.paths[pkey]
	if found {
		return ErrDuplicatePath
	}

	pathRec = &pathRecord{
		key: pkey,
	}
	pathRec.modTime.Store(zeroTime)

	mtm.paths[pkey] = pathRec

	return nil
}

// GetPathStatus returns the current status of a monitored path
func (mtm *ModTimeMonitor) GetPathStatus(strategyStr, pathStr string) (*PathStatus, error) {
	pathStr = CleanPath(pathStr)

	mtm.RLock()
	defer mtm.RUnlock()

	pkey := pathKey{
		strategy: strategyStr,
		path:     pathStr,
	}

	pathRec, found := mtm.paths[pkey]
	if !found {
		return nil, ErrPathNotFound
	}

	// Make a copy of status info returned,
	// do NOT return the actual internal status info
	// for thread-safety
	pathSts := &PathStatus{
		PathStr:     pathRec.key.path,
		StrategyStr: pathRec.key.strategy,
		ModTime:     pathRec.modTime.Load().(time.Time),
	}

	return pathSts, nil
}

// checkPathModTimes checks the modtimes of its set of paths
func (mtm *ModTimeMonitor) checkPathModTimes(ctx context.Context, nowTime time.Time) {
	mtm.RLock()
	defer mtm.RUnlock()

	for pkey, pathRec := range mtm.paths {
		// When using fs.FS, paths must be unrooted
		// See https://github.com/golang/go/issues/44279#issuecomment-956251702
		// See https://pkg.go.dev/io/fs#ValidPath
		unrooted := strings.TrimLeft(pkey.path, "/")
		fInfo, err := mtm.statFS.Stat(unrooted)
		if err != nil {
			// log error, but continue
			mtm.log(fmt.Sprintf("checkPathModTimes: Stat(%s) err=%s", pkey, err))
		} else {
			newTime := fInfo.ModTime()
			mtm.log(fmt.Sprintf("checkPathModTimes: Stat(%s) ModTime=%s", pkey, newTime))
			pathRec.modTime.Store(newTime)
		}

		latencyNano := nowTime.Sub(pathRec.modTime.Load().(time.Time))
		latencySecs := latencyNano.Seconds()
		metrics.ObserveHotloadModtimeLatencyHistogram(pkey.strategy, pkey.path, latencySecs)
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
