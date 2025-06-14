package modtime

import (
	"context"
	"fmt"
	"log"
	"math"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	internal "github.com/infobloxopen/hotload/internal"
	"github.com/infobloxopen/hotload/metrics"
)

var fsnotifyStrategy = "fsnotify"

// TestAgainstUnixFS verifies that the use of io/fs.FS in the implementation
// works against the real Unix FS
func TestAgainstUnixFS(t *testing.T) {
	var zeroTime time.Time
	ctx, cancelCtxFn := context.WithCancel(context.Background())
	defer cancelCtxFn()

	metrics.ResetCollectors()

	logfn := func(args ...any) {
		log.Println(args...)
	}

	// Create ModTimeMonitor that monitors the (default) real host Unix FS
	mtm := NewModTimeMonitor(ctx,
		WithCheckInterval(time.Millisecond*200),
		WithLogger(logfn),
		WithErrLogger(logfn),
	)

	// Add well-known Unix path to monitor the mod-time of
	pth := "/dev/null"
	mtm.AddMonitoredPath(fsnotifyStrategy, pth)

	// Give time for ModTimeMonitor background thread to check mod-times
	time.Sleep(time.Millisecond * 500)

	// Verify valid mod-time has been retrieved
	sts, err := mtm.GetPathStatus(fsnotifyStrategy, pth)
	if err != nil {
		t.Errorf("GetPathStatus(%s): unexpected err=%s", pth, err)
	} else {
		t.Logf("sts=%+v", sts)
		if zeroTime.After(sts.ModTime) {
			t.Errorf("GetPathStatus(%s): unexpected ModTime=%+v", pth, sts.ModTime)
		}
	}

	// Unfortunately for some reason, os.Chtimes() returns
	//   "chtimes /dev/null: operation not permitted"
	// so we can't update /dev/null modtime for additional testing

	// Cancel ctx and give time for background threads to terminate
	cancelCtxFn()
	time.Sleep(time.Millisecond * 200)
}

// TestAgainstMapFS verifies using MapFS mock FS
func TestAgainstMapFS(t *testing.T) {
	var zeroTime time.Time
	prevTime := zeroTime

	ctx, cancelCtxFn := context.WithCancel(context.Background())
	defer cancelCtxFn()

	metrics.ResetCollectors()

	// Create MapFS mock FS
	pth := "/foo/bar"
	mfs := internal.NewSafeMapFS()
	mfs.UpsertMapFile(pth, &fstest.MapFile{ModTime: MustParseRFC3339("0002-02-02T02:02:02Z")})

	checkIntv := time.Millisecond * 1000

	// Create ModTimeMonitor that monitors mock FS
	mtm := NewModTimeMonitor(ctx,
		WithStatFS(mfs),
		WithCheckInterval(checkIntv),
		WithLogger(func(args ...any) {
			log.Println(args...)
		}),
	)

	// Add mock path to monitor the mod-time of.
	mtm.AddMonitoredPath(fsnotifyStrategy, pth)

	// Give time for ModTimeMonitor background thread to check mod-times.
	// Wait 2 cycles of checks.
	time.Sleep(2*checkIntv + 10*time.Millisecond)

	// Verify valid mod-time has been retrieved
	sts, err := mtm.GetPathStatus(fsnotifyStrategy, pth)
	if err != nil {
		t.Errorf("GetPathStatus(%s): unexpected err=%s", pth, err)
	} else {
		t.Logf("sts=%+v", sts)
		if sts.ModTime.Before(prevTime) || sts.ModTime.Equal(prevTime) {
			t.Errorf("GetPathStatus(%s): unexpectedly not updated: ModTime=%+v", pth, sts.ModTime)
		}
		prevTime = sts.ModTime
	}
	err = internal.CollectAndRegexpCompare(metrics.HotloadModtimeLatencyHistogram,
		strings.NewReader(expectMetricsRegexpInitial),
		metrics.HotloadModtimeLatencyHistogramName)
	if err != nil {
		t.Errorf("CollectAndRegexpCompare(): unexpected err=\n%s", err)
	}

	// Update mock path mod-time
	mapf, err := mfs.GetMapFile(pth)
	if err != nil {
		t.Errorf("GetMapFile(%s): unexpected err=%s", pth, err)
	}
	mapf.ModTime = time.Now()
	mfs.UpsertMapFile(pth, mapf)

	// Give time for ModTimeMonitor background thread to check mod-times.
	// Wait 1 cycle of checks.
	time.Sleep(1*checkIntv + 10*time.Millisecond)

	// Verify modtime updated
	sts, err = mtm.GetPathStatus(fsnotifyStrategy, pth)
	if err != nil {
		t.Errorf("GetPathStatus(%s): unexpected err=%s", pth, err)
	} else {
		t.Logf("sts=%+v", sts)
		if sts.ModTime.Before(prevTime) || sts.ModTime.Equal(prevTime) {
			t.Errorf("GetPathStatus(%s): unexpectedly not updated: ModTime=%+v", pth, sts.ModTime)
		}
		prevTime = sts.ModTime
	}
	err = internal.CollectAndRegexpCompare(metrics.HotloadModtimeLatencyHistogram,
		strings.NewReader(expectMetricsRegexpAfterModtimeUpdated),
		metrics.HotloadModtimeLatencyHistogramName)
	if err != nil {
		t.Errorf("CollectAndRegexpCompare(): unexpected err=\n%s", err)
	}

	// Give time for ModTimeMonitor background thread to check mod-times.
	// Wait 1 cycle of checks.
	time.Sleep(1*checkIntv + 10*time.Millisecond)

	// Verify modtime NOT updated because mock path modtime was NOT updated
	sts, err = mtm.GetPathStatus(fsnotifyStrategy, pth)
	if err != nil {
		t.Errorf("GetPathStatus(%s): unexpected err=%s", pth, err)
	} else {
		t.Logf("sts=%+v", sts)
		if sts.ModTime.After(prevTime) {
			t.Errorf("GetPathStatus(%s): unexpectedly updated: ModTime=%+v", pth, sts.ModTime)
		}
		prevTime = sts.ModTime
	}
	err = internal.CollectAndRegexpCompare(metrics.HotloadModtimeLatencyHistogram,
		strings.NewReader(expectMetricsRegexpAfterModtimeNotUpdated),
		metrics.HotloadModtimeLatencyHistogramName)
	if err != nil {
		t.Errorf("CollectAndRegexpCompare(): unexpected err=\n%s", err)
	}

	// Cancel ctx and give time for background threads to terminate
	cancelCtxFn()
	time.Sleep(time.Millisecond * 200)
}

// TestConcurrency verifies thread-safety by spawning multiple
// threads all running with the same interval.
// Should be tested with go test -race flag.
func TestConcurrency(t *testing.T) {
	var zeroTime time.Time
	ctx, cancelCtxFn := context.WithCancel(context.Background())
	defer cancelCtxFn()

	metrics.ResetCollectors()

	commonIntv := time.Millisecond * 100

	// Create MapFS mock FS
	mfs := internal.NewSafeMapFS()

	modTimePaths := []struct {
		pathStr    string
		startMtime time.Time
	}{
		{
			pathStr:    "/concurrency/sub10",
			startMtime: time.Now(),
		},
		{
			pathStr:    "/concurrency/sub20",
			startMtime: zeroTime,
		},
		{
			pathStr:    "/concurrency/sub30",
			startMtime: MustParseRFC3339("2010-04-01T23:07:59Z"),
		},
	}

	// Add paths to mock FS
	for _, pathRec := range modTimePaths {
		mfs.UpsertMapFile(pathRec.pathStr, &fstest.MapFile{ModTime: pathRec.startMtime})
	}

	// Create ModTimeMonitor that monitors mock FS
	mtm := NewModTimeMonitor(context.Background(),
		WithStatFS(mfs),
		WithCheckInterval(commonIntv),
		WithLogger(func(args ...any) {
			log.Println(args...)
		}),
	)

	modTimeUpdaterLoop := func(ctx context.Context, t *testing.T, pathStr string, updateIntv time.Duration) {
		mtm.log(fmt.Sprintf("modTimeUpdaterLoop(%s) started", pathStr))
		updateTicker := time.NewTicker(updateIntv)
		defer updateTicker.Stop()
	loop:
		for {
			select {
			case <-ctx.Done():
				break loop
			case curTime := <-updateTicker.C:
				mapf, err := mfs.GetMapFile(pathStr)
				if err != nil {
					t.Errorf("modTimeUpdaterLoop(%s): GetMapFile() err=%s", pathStr, err)
				} else {
					mapf.ModTime = curTime
					mfs.UpsertMapFile(pathStr, mapf)
				}
			}
		}
		mtm.log(fmt.Sprintf("modTimeUpdaterLoop(%s) terminated", pathStr))
	}

	modTimeReaderLoop := func(ctx context.Context, t *testing.T, pathStr string, readIntv time.Duration) {
		mtm.log(fmt.Sprintf("modTimeReaderLoop(%s) started", pathStr))
		readTicker := time.NewTicker(readIntv)
		defer readTicker.Stop()
	loop:
		for {
			select {
			case <-ctx.Done():
				break loop
			case <-readTicker.C:
				sts, err := mtm.GetPathStatus(fsnotifyStrategy, pathStr)
				if err != nil {
					t.Errorf("modTimeReaderLoop(%s): GetPathStatus() err=%s", pathStr, err)
				} else {
					t.Logf("sts=%+v", sts)
				}
			}
		}
		mtm.log(fmt.Sprintf("modTimeReaderLoop(%s) terminated", pathStr))
	}

	// Add paths to monitor
	for _, pathRec := range modTimePaths {
		mtm.AddMonitoredPath(fsnotifyStrategy, pathRec.pathStr)
		go modTimeUpdaterLoop(ctx, t, pathRec.pathStr, commonIntv)
		go modTimeReaderLoop(ctx, t, pathRec.pathStr, commonIntv)
	}

	// Give time for background threads to do their thing
	time.Sleep(time.Millisecond * 3000)

	// Cancel ctx and give time for background threads to terminate
	cancelCtxFn()
	time.Sleep(time.Millisecond * 1000)
}

// TestMaxTimeSubtractionDuration verifies that time.subtraction
// difference that is too large to be represented by time.Duration
// results in the max int64 value (approx 290 years), and does not fail.
func TestMaxTimeSubtractionDuration(t *testing.T) {
	var zeroTime time.Time
	modTime := MustParseRFC3339("2025-04-01T23:07:59Z")
	hugeDuration := modTime.Sub(zeroTime)
	t.Logf("zeroTime=%s", zeroTime)
	t.Logf("modTime=%s", modTime)
	t.Logf("hugeDuration=modTime.Sub(zeroTime)=0x%x=%d=%s",
		int64(hugeDuration), hugeDuration, hugeDuration)
	if int64(hugeDuration) != math.MaxInt64 {
		t.Errorf("unexpected hugeDuration=%x, should be %x", int64(hugeDuration), math.MaxInt64)
	}
}

func TestTimeComparisons(t *testing.T) {
	modTime := MustParseRFC3339("2025-04-01T23:07:59Z")
	isEqual := modTime.Equal(modTime)
	isAfter := modTime.After(modTime)
	isBefore := modTime.Before(modTime)
	t.Logf("modTime=%s", modTime)
	t.Logf("modTime.Equal(modTime)=%v, modTime.After(modTime)=%v, modTime.Before(modTime)=%v",
		isEqual, isAfter, isBefore)
	if !isEqual {
		t.Errorf("modTime.Equal(modTime)=false, should be true")
	}
	if isAfter {
		t.Errorf("modTime.After(modTime)=true, should be false")
	}
	if isBefore {
		t.Errorf("modTime.Before(modTime)=true, should be false")
	}
}

// MustParseRFC3339 calls time.Parse(time.RFC3339,...),
// and panics on error, otherwise returns parsed time.Time result
func MustParseRFC3339(str string) time.Time {
	t, err := time.Parse(time.RFC3339, str)
	if err != nil {
		panic(fmt.Sprintf("time.Parse(%s) err=%s", str, err))
	}
	return t
}

var expectMetricsRegexpInitial = `
# HELP hotload_modtime_latency_histogram Hotload modtime latency histogram \(seconds\) by strategy and path
# TYPE hotload_modtime_latency_histogram histogram
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="900"} 0
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="1800"} 0
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="2700"} 0
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="3600"} 0
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="4500"} 0
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="5400"} 0
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="7200"} 0
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="10800"} 0
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="14400"} 0
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="28800"} 0
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="86400"} 0
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="\+Inf"} 2
hotload_modtime_latency_histogram_sum{path="/foo/bar",strategy="fsnotify"} 1.8446744\d*e\+10
hotload_modtime_latency_histogram_count{path="/foo/bar",strategy="fsnotify"} 2
`

var expectMetricsRegexpAfterModtimeUpdated = `
# HELP hotload_modtime_latency_histogram Hotload modtime latency histogram \(seconds\) by strategy and path
# TYPE hotload_modtime_latency_histogram histogram
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="900"} 1
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="1800"} 1
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="2700"} 1
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="3600"} 1
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="4500"} 1
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="5400"} 1
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="7200"} 1
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="10800"} 1
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="14400"} 1
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="28800"} 1
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="86400"} 1
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="\+Inf"} 3
hotload_modtime_latency_histogram_sum{path="/foo/bar",strategy="fsnotify"} 1.8446744\d*e\+10
hotload_modtime_latency_histogram_count{path="/foo/bar",strategy="fsnotify"} 3
`

var expectMetricsRegexpAfterModtimeNotUpdated = `
# HELP hotload_modtime_latency_histogram Hotload modtime latency histogram \(seconds\) by strategy and path
# TYPE hotload_modtime_latency_histogram histogram
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="900"} 2
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="1800"} 2
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="2700"} 2
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="3600"} 2
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="4500"} 2
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="5400"} 2
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="7200"} 2
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="10800"} 2
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="14400"} 2
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="28800"} 2
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="86400"} 2
hotload_modtime_latency_histogram_bucket{path="/foo/bar",strategy="fsnotify",le="\+Inf"} 4
hotload_modtime_latency_histogram_sum{path="/foo/bar",strategy="fsnotify"} 1.8446744\d*e\+10
hotload_modtime_latency_histogram_count{path="/foo/bar",strategy="fsnotify"} 4
`
