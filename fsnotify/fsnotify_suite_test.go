package fsnotify

import (
	"log"
	"testing"

	"github.com/infobloxopen/hotload/logger"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func testLogger(args ...any) {
	log.Println(args...)
}

func TestFsnotify(t *testing.T) {
	//log.SetFlags(log.Flags() | log.Lmicroseconds)
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.SetOutput(GinkgoWriter)
	logger.WithLogger(testLogger)
	logger.WithErrLogger(testLogger)

	RegisterFailHandler(Fail)
	RunSpecs(t, "Fsnotify Suite")
}
