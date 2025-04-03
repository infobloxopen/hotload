package hotload

import (
	"context"
	"database/sql/driver"
	"sync"

	"github.com/infobloxopen/hotload/logger"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

var _ = Describe("Driver", func() {
	var pctx context.Context
	var ctx context.Context
	var cg *chanGroup
	var conns []*managedConn
	var values chan string
	Context("chanGroup", func() {
		BeforeEach(func() {
			var cancel context.CancelFunc
			values = make(chan string)
			pctx = context.Background()
			ctx, cancel = context.WithCancel(pctx)
			conns = []*managedConn{{ctx: ctx, conn: nil}, {ctx: ctx, conn: nil}, {ctx: ctx, conn: nil}}
			cg = &chanGroup{
				value:     "",
				values:    values,
				parentCtx: pctx,
				ctx:       ctx,
				cancel:    cancel,
				sqlDriver: nil,
				mu:        sync.RWMutex{},
				conns:     conns,
				log:       logger.DefaultLogger,
			}
		})

		It("Should change value when a value is pushed to the values channel", func() {
			newVal := "new DSN"
			go cg.run()
			values <- newVal
			cg.mu.RLock()
			defer cg.mu.RUnlock()
			Expect(cg.value).To(Equal(newVal))
		})

		It("Should not reset conns when the same value is pushed to the values channel", func() {
			sameVal := ""
			go cg.run()
			values <- sameVal
			Expect(cg.value).To(Equal(sameVal))
			for _, c := range cg.conns {
				Expect(c.reset).To(BeFalse())
			}
		})

		It("Should change value and reset connections", func() {
			newVal := "new DSN"
			cg.valueChanged(newVal)
			Expect(cg.value).To(Equal(newVal))
		})

		It("Should mark all connections for reset", func() {
			cg.resetConnections()

			for _, c := range conns {
				Expect(c.reset).To(BeTrue())
			}
		})

		It("Should kill all connections when specified", func() {
			cg.forceKill = true
			testConns := make([]*testConn, 0)
			for _, c := range cg.conns {
				tc := &testConn{}
				c.conn = tc
				testConns = append(testConns, tc)
			}
			cg.resetConnections()

			for _, c := range conns {
				Expect(c.killed).To(BeTrue(), "connection should be marked killed")
			}

			for _, tc := range testConns {
				Expect(tc.closed).To(BeTrue(), "Closed() should have been called on the underlying connection")
			}
		})
	})
})
