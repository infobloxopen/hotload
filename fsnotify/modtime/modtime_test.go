package modtime

import (
	"context"
	"fmt"
	"log"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/infobloxopen/hotload/metrics"
	utilt "github.com/infobloxopen/hotload/util_test"
)

// TestAgainstUnixFS verifies that the use of io/fs.FS in the implementation
// works against the real Unix FS
func TestAgainstUnixFS(t *testing.T) {
	var zeroTime time.Time
	ctx, cancelCtxFn := context.WithCancel(context.Background())
	defer cancelCtxFn()

	metrics.ResetHotloadCollectors()

	// Create ModTimeMonitor that monitors the (default) real host Unix FS
	mtm := NewModTimeMonitor(ctx,
		WithCheckInterval(time.Millisecond*200),
		WithLogger(func(args ...any) {
			log.Println(args...)
		}),
	)

	// Add well-known Unix path to monitor the mod-time of
	pth := "/dev/null"
	mtm.AddMonitoredPath(pth,
		WithRefreshInterval(time.Millisecond*2000),
	)

	// Give time for ModTimeMonitor background thread to check mod-times
	time.Sleep(time.Millisecond * 500)

	// Verify valid mod-time has been retrieved
	sts, err := mtm.GetPathStatus(pth)
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
	ctx, cancelCtxFn := context.WithCancel(context.Background())
	defer cancelCtxFn()

	metrics.ResetHotloadCollectors()

	// Create MapFS mock FS
	pth := "/foo/bar"
	mfs := utilt.NewSafeMapFS()
	mfs.UpsertMapFile(pth, &fstest.MapFile{ModTime: MustParseRFC3339("2010-04-01T23:07:59Z")})

	checkIntv := time.Millisecond * 200

	// Create ModTimeMonitor that monitors mock FS
	mtm := NewModTimeMonitor(ctx,
		WithStatFS(mfs),
		WithCheckInterval(checkIntv),
		WithLogger(func(args ...any) {
			log.Println(args...)
		}),
	)

	// Add mock path to monitor the mod-time of.
	// Notice that the path refreshIntv is the same as checkIntv for this test.
	mtm.AddMonitoredPath(pth,
		WithRefreshInterval(checkIntv),
	)

	// Give time for ModTimeMonitor background thread to check mod-times.
	// Wait 2 cycles of checks.
	time.Sleep(2*checkIntv + 10*time.Millisecond)

	// Verify valid mod-time has been retrieved
	sts, err := mtm.GetPathStatus(pth)
	if err != nil {
		t.Errorf("GetPathStatus(%s): unexpected err=%s", pth, err)
	} else {
		t.Logf("sts=%+v", sts)
		if zeroTime.After(sts.ModTime) {
			t.Errorf("GetPathStatus(%s): unexpected ModTime=%+v", pth, sts.ModTime)
		}
		if sts.FailCount != 2 {
			t.Errorf("GetPathStatus(%s): unexpected FailCount=%+v, expected 2", pth, sts.FailCount)
		}
	}
	err = testutil.CollectAndCompare(metrics.HotloadFsnotifyModtimeCheckGauge, strings.NewReader(
		fmt.Sprintf(metrics.HotloadFsnotifyModtimeCheckGaugeScrapeFmt, pth, 1)))
	if err != nil {
		t.Errorf("CollectAndCompare(): unexpected err=%s", err)
	}
	err = testutil.CollectAndCompare(metrics.HotloadFsnotifyModtimeFailureCounter, strings.NewReader(
		fmt.Sprintf(metrics.HotloadFsnotifyModtimeFailureCounterScrapeFmt, pth, 1)))
	if err != nil {
		t.Errorf("CollectAndCompare(): unexpected err=%s", err)
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

	// Verify FailCount is zero because mod-time was updated
	sts, err = mtm.GetPathStatus(pth)
	if err != nil {
		t.Errorf("GetPathStatus(%s): unexpected err=%s", pth, err)
	} else {

		t.Logf("sts=%+v", sts)
		if sts.FailCount > 0 {
			t.Errorf("GetPathStatus(%s): unexpected FailCount=%+v, expected 0", pth, sts.FailCount)
		}
	}
	err = testutil.CollectAndCompare(metrics.HotloadFsnotifyModtimeCheckGauge, strings.NewReader(
		fmt.Sprintf(metrics.HotloadFsnotifyModtimeCheckGaugeScrapeFmt, pth, 0)))
	if err != nil {
		t.Errorf("CollectAndCompare(): unexpected err=%s", err)
	}
	err = testutil.CollectAndCompare(metrics.HotloadFsnotifyModtimeFailureCounter, strings.NewReader(
		fmt.Sprintf(metrics.HotloadFsnotifyModtimeFailureCounterScrapeFmt, pth, 1)))
	if err != nil {
		t.Errorf("CollectAndCompare(): unexpected err=%s", err)
	}

	// Give time for ModTimeMonitor background thread to check mod-times.
	// Wait 1 cycle of checks.
	time.Sleep(1*checkIntv + 10*time.Millisecond)

	// Verify FailCount is now one
	sts, err = mtm.GetPathStatus(pth)
	if err != nil {
		t.Errorf("GetPathStatus(%s): unexpected err=%s", pth, err)
	} else {

		t.Logf("sts=%+v", sts)
		if sts.FailCount != 1 {
			t.Errorf("GetPathStatus(%s): unexpected FailCount=%+v, expected 1", pth, sts.FailCount)
		}
	}
	err = testutil.CollectAndCompare(metrics.HotloadFsnotifyModtimeCheckGauge, strings.NewReader(
		fmt.Sprintf(metrics.HotloadFsnotifyModtimeCheckGaugeScrapeFmt, pth, 1)))
	if err != nil {
		t.Errorf("CollectAndCompare(): unexpected err=%s", err)
	}
	err = testutil.CollectAndCompare(metrics.HotloadFsnotifyModtimeFailureCounter, strings.NewReader(
		fmt.Sprintf(metrics.HotloadFsnotifyModtimeFailureCounterScrapeFmt, pth, 2)))
	if err != nil {
		t.Errorf("CollectAndCompare(): unexpected err=%s", err)
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

	metrics.ResetHotloadCollectors()

	commonIntv := time.Millisecond * 100

	// Create MapFS mock FS
	mfs := utilt.NewSafeMapFS()

	modTimePaths := []struct {
		pathStr     string
		startMtime  time.Time
		refreshIntv time.Duration
	}{
		{
			pathStr:     "/concurrency/sub10",
			startMtime:  time.Now(),
			refreshIntv: time.Millisecond * 800,
		},
		{
			pathStr:     "/concurrency/sub20",
			startMtime:  zeroTime,
			refreshIntv: time.Millisecond * 800,
		},
		{
			pathStr:     "/concurrency/sub30",
			startMtime:  MustParseRFC3339("2010-04-01T23:07:59Z"),
			refreshIntv: time.Millisecond * 800,
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
				sts, err := mtm.GetPathStatus(pathStr)
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
		mtm.AddMonitoredPath(pathRec.pathStr, WithRefreshInterval(pathRec.refreshIntv))
		go modTimeUpdaterLoop(ctx, t, pathRec.pathStr, commonIntv)
		go modTimeReaderLoop(ctx, t, pathRec.pathStr, commonIntv)
	}

	// Give time for background threads to do their thing
	time.Sleep(time.Millisecond * 3000)

	// Cancel ctx and give time for background threads to terminate
	cancelCtxFn()
	time.Sleep(time.Millisecond * 1000)
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
