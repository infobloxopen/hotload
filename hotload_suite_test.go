package hotload_test

import (
	"log"
	"testing"

	"github.com/infobloxopen/hotload/internal"
	"github.com/infobloxopen/hotload/logger"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func testLogger(args ...any) {
	log.Println(args...)
}

func TestHotload(t *testing.T) {
	log.SetOutput(GinkgoWriter)
	logger.WithLogger(testLogger)
	logger.WithErrLogger(testLogger)

	nrr := internal.NewNonRandomReader(1)
	uuid.SetRand(nrr)

	RegisterFailHandler(Fail)
	RunSpecs(t, "Hotload Suite")
}
