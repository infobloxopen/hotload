package fsnotify

import (
	"log"
	"testing"

	hlogger "github.com/infobloxopen/hotload/logger"
	stdlog "github.com/infobloxopen/hotload/logger/standardlog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestFsnotify(t *testing.T) {
	//log.SetFlags(log.Flags() | log.Lmicroseconds)
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.SetOutput(GinkgoWriter)
	hlogger.SetDefaultLevelLogger(stdlog.NewStdLogLevelLogger(log.Default(), stdlog.LevelDebug))

	RegisterFailHandler(Fail)
	RunSpecs(t, "Fsnotify Suite")
}
