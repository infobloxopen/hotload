package integrationtests

import (
	"fmt"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/infobloxopen/hotload/logger"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	hotloadTestDsn  string
	hotloadTest1Dsn string
)

func testLogger(args ...any) {
	log.Println(args...)
}

func TestIntegrationtests(t *testing.T) {
	log.SetOutput(GinkgoWriter)
	logger.WithLogger(testLogger)

	pgUser := "admin"
	pgPass := "test"
	pgPort := "5432"

	pgHost, ok := os.LookupEnv("HOTLOAD_INTEGRATION_TEST_POSTGRES_HOST")
	pgHost = strings.TrimSpace(pgHost)
	if !ok || len(pgHost) <= 0 {
		pgHost = "localhost"
	}

	hotloadTestDsn = fmt.Sprintf("postgresql://%s:%s@%s:%s/hotload_test?sslmode=disable",
		pgUser, pgPass, pgHost, pgPort)
	hotloadTest1Dsn = fmt.Sprintf("postgresql://%s:%s@%s:%s/hotload_test1?sslmode=disable",
		pgUser, pgPass, pgHost, pgPort)

	RegisterFailHandler(Fail)
	RunSpecs(t, "Integration Tests")
}
