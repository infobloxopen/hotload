package hotload

import (
	"context"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"sync"
)

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
			cg.markForReset()

			for _, c := range conns {
				Expect(c.reset).To(BeTrue())
			}
		})
	})
})
