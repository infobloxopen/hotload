package hotload

import (
	"context"
	"database/sql/driver"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/infobloxopen/hotload/internal"
	"github.com/infobloxopen/hotload/metrics"
)

type testConn struct {
	closed bool
}

func (tc *testConn) Open(name string) (driver.Conn, error) {
	return tc, nil
}

func (tc *testConn) Prepare(query string) (driver.Stmt, error) {
	return nil, nil
}
func (tc *testConn) Begin() (driver.Tx, error) {
	return nil, nil
}

func (tc *testConn) Close() error {
	tc.closed = true
	return nil
}

type mockWatcher struct {
	values chan string
}

func newMockWatcher() *mockWatcher {
	return &mockWatcher{
		values: make(chan string),
	}
}

func (mw mockWatcher) getReceiveChan() <-chan string {
	return mw.values
}

func (mw mockWatcher) sendValue(value string) {
	log.Printf("mockWatcher: sending value '%s'...", value)
	mw.values <- value
	log.Printf("mockWatcher: sent value '%s'", value)
}

var _ = DescribeTableSubtree("Driver", Serial, func(forceKill bool) {
	var pctx context.Context
	var ctx context.Context
	var cancel context.CancelFunc
	var cg *chanGroup
	var mgdConns []*managedConn
	var mockw *mockWatcher
	Context("chanGroup", func() {
		BeforeEach(func(ginkgoCtx context.Context) {
			// Do NOT use ginkgoCtx as it will be canceled when BeforeEach finishes
			pctx = context.Background()
			ctx, cancel = context.WithCancel(pctx)
			mockw = newMockWatcher()
			cg = &chanGroup{
				name:       "fsnotify://postgres/tmp/mydsn.txt",
				value:      "1st-dsn",
				newValChan: mockw.getReceiveChan(),
				parentCtx:  pctx,
				ctx:        ctx,
				cancel:     cancel,
				sqlDriver:  nil,
				mu:         sync.RWMutex{},
				forceKill:  forceKill,
			}
			cg.conns = []*managedConn{
				newManagedConn(ctx, cg.value, cg.value, &testConn{}, cg.removeMgdConn),
				newManagedConn(ctx, cg.value, cg.value, &testConn{}, cg.removeMgdConn),
				newManagedConn(ctx, cg.value, cg.value, &testConn{}, cg.removeMgdConn),
			}
			mgdConns = cg.conns

			metrics.ResetCollectors()
		}, NodeTimeout(5*time.Second))

		It("Should change value when a value is pushed to the values channel", func(ginkgoCtx context.Context) {
			newVal := "2nd-dsn"
			go cg.runLoop()
			mockw.sendValue(newVal)

			// Yield to cg.runLoop() background thread
			time.Sleep(200 * time.Millisecond)

			cg.mu.RLock()
			defer cg.mu.RUnlock()
			Expect(cg.value).To(Equal(newVal))

			Expect(len(cg.conns)).To(Equal(0), "number of managed conns should be reset to zero")

			for _, mc := range mgdConns {
				Expect(mc.GetReset()).To(BeTrue(), "managed connection should be marked reset")
				if cg.forceKill {
					Expect(mc.GetKill()).To(BeTrue(), "managed connection should be marked killed")
					Expect(mc.conn.(*testConn).closed).To(BeTrue(), "Closed() should have been called on the underlying connection")
				}
			}

			// HotloadChangeTotal metric should be incremented
			err := testutil.CollectAndCompare(metrics.HotloadChangeTotal,
				strings.NewReader(expectHotloadChangeTotalHelp+
					fmt.Sprintf(expectHotloadChangeTotalMetric, cg.name, 1)))
			Expect(err).ShouldNot(HaveOccurred())
		}, NodeTimeout(5*time.Second))

		It("Should not reset conns when the same value is pushed to the values channel", func(ginkgoCtx context.Context) {
			sameVal := "1st-dsn"
			go cg.runLoop()
			mockw.sendValue(sameVal)

			// Yield to cg.runLoop() background thread
			time.Sleep(200 * time.Millisecond)

			Expect(cg.value).To(Equal(sameVal))

			Expect(len(cg.conns)).To(Equal(3), "number of managed conns should not be reset to zero")

			for _, c := range cg.conns {
				Expect(c.GetReset()).To(BeFalse())
				Expect(c.GetKill()).To(BeFalse())
				Expect(c.conn.(*testConn).closed).To(BeFalse())
			}

			for _, mc := range mgdConns {
				Expect(mc.GetReset()).To(BeFalse())
				Expect(mc.GetKill()).To(BeFalse())
				Expect(mc.conn.(*testConn).closed).To(BeFalse())
			}

			// HotloadChangeTotal metric should NOT be incremented
			err := testutil.CollectAndCompare(metrics.HotloadChangeTotal,
				strings.NewReader(""))
			Expect(err).ShouldNot(HaveOccurred())
		}, NodeTimeout(5*time.Second))

		It("Should change value and reset connections", func(ginkgoCtx context.Context) {
			newVal := "2nd-dsn"
			cg.processNewValue(newVal)
			Expect(cg.value).To(Equal(newVal))

			Expect(len(cg.conns)).To(Equal(0), "number of managed conns should be reset to zero")

			for _, mc := range mgdConns {
				Expect(mc.GetReset()).To(BeTrue(), "managed connection should be marked reset")
				if cg.forceKill {
					Expect(mc.GetKill()).To(BeTrue(), "managed connection should be marked killed")
					Expect(mc.conn.(*testConn).closed).To(BeTrue(), "Closed() should have been called on the underlying connection")
				}
			}

			// HotloadChangeTotal metric should be incremented
			err := testutil.CollectAndCompare(metrics.HotloadChangeTotal,
				strings.NewReader(expectHotloadChangeTotalHelp+
					fmt.Sprintf(expectHotloadChangeTotalMetric, cg.name, 1)))
			Expect(err).ShouldNot(HaveOccurred())

			err = internal.CollectAndRegexpCompare(metrics.HotloadLastChangedTimestampSeconds,
				strings.NewReader(expectHotloadLastChangedTimestampSecondsMetricRegexp),
				metrics.HotloadLastChangedTimestampSecondsName)
			Expect(err).ShouldNot(HaveOccurred())
		}, NodeTimeout(5*time.Second))
	})
},
	Entry("forceKill=false", false),
	Entry("forceKill=true", true),
)

var expectHotloadChangeTotalHelp = `
# HELP hotload_change_total Hotload change total by url
# TYPE hotload_change_total counter
`

var expectHotloadChangeTotalMetric = `
hotload_change_total{url="%s"} %d
`

var expectHotloadLastChangedTimestampSecondsMetricRegexp = `
# HELP hotload_last_changed_timestamp_seconds Hotload last changed \(unix timestamp\), by url
# TYPE hotload_last_changed_timestamp_seconds gauge
hotload_last_changed_timestamp_seconds{url="fsnotify://postgres/tmp/mydsn.txt"} \d\.\d+e\+\d+
`
