package hotload_test

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

func TestHotload(t *testing.T) {
	log.SetOutput(GinkgoWriter)
	logger.WithLogger(testLogger)

	RegisterFailHandler(Fail)
	RunSpecs(t, "Hotload Suite")
}
