package hotload

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"sync"
)

var _ = Describe("managedConn", func() {
	It("Should set .reset in a threadsafe way", func() {
		mc := managedConn{
			ctx:   nil,
			conn:  nil,
			reset: false,
			mu:    sync.RWMutex{},
		}
		// Lock the mutex
		mc.mu.Lock()
		writeLockAcquired := false
		readLockAcquired := false

		// Verify that neither Reset or GetReset can return while the managedConn's write lock is held
		go func() {
			mc.Reset(true)
			writeLockAcquired = true
		}()

		go func() {
			mc.GetReset()
			readLockAcquired = true
		}()

		Consistently(writeLockAcquired).Should(BeFalse())
		Consistently(readLockAcquired).Should(BeFalse())
	})
})
