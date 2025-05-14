package hotload_test

import (
	"log"
	"testing"

	"github.com/infobloxopen/hotload/internal"
	hlogger "github.com/infobloxopen/hotload/logger"
	stdlog "github.com/infobloxopen/hotload/logger/standardlog"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHotload(t *testing.T) {
	//log.SetFlags(log.Flags() | log.Lmicroseconds)
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.SetOutput(GinkgoWriter)
	hlogger.SetDefaultLevelLogger(stdlog.NewStdLogLevelLogger(log.Default(), stdlog.LevelDebug))

	nrr := internal.NewNonRandomReader(1)
	uuid.SetRand(nrr)

	RegisterFailHandler(Fail)
	RunSpecs(t, "Hotload Suite")
}
