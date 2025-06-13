package integrationtests

import (
	"fmt"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/infobloxopen/hotload"
	"github.com/infobloxopen/hotload/internal"
	"github.com/infobloxopen/hotload/logger"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	postgresHost = "localhost"
	postgresPort = "5432"

	hotloadTestDsn  string
	hotloadTest1Dsn string

	hldatabaseSuperDsn string
	hldatabaseAdminDsn string

	superUser  = "postgres"
	superPass  = "postgres"
	adminUser  = "admin"
	adminPass  = "test"
	testDbUser = "uuser"
)

func testDbPass(which int) string {
	return fmt.Sprintf("ppass%d", which)
}

func hldatabasePassDsn(which int) string {
	return fmt.Sprintf("postgresql://%s:%s@%s:%s/hldatabase?sslmode=disable",
		testDbUser, testDbPass(which), postgresHost, postgresPort)
}

func testLogger(args ...any) {
	log.Println(args...)
}

func TestIntegrationtests(t *testing.T) {
	//log.SetFlags(log.Flags() | log.Lmicroseconds)
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.SetOutput(GinkgoWriter)
	logger.WithLogger(testLogger)
	logger.WithErrLogger(testLogger)

	nrr := internal.NewNonRandomReader(1)
	uuid.SetRand(nrr)

	os.Setenv(hotload.CheckIntervalEnvVar, "1s")

	pgHost, ok := os.LookupEnv("HOTLOAD_INTEGRATION_TEST_POSTGRES_HOST")
	pgHost = strings.TrimSpace(pgHost)
	if ok && len(pgHost) > 0 {
		postgresHost = pgHost
	}

	pgPort, ok := os.LookupEnv("HOTLOAD_INTEGRATION_TEST_POSTGRES_PORT")
	pgPort = strings.TrimSpace(pgPort)
	if ok && len(pgPort) > 0 {
		postgresPort = pgPort
	}

	hotloadTestDsn = fmt.Sprintf("postgresql://%s:%s@%s:%s/hotload_test?sslmode=disable",
		adminUser, adminPass, postgresHost, postgresPort)
	hotloadTest1Dsn = fmt.Sprintf("postgresql://%s:%s@%s:%s/hotload_test1?sslmode=disable",
		adminUser, adminPass, postgresHost, postgresPort)

	hldatabaseSuperDsn = fmt.Sprintf("postgresql://%s:%s@%s:%s/hldatabase?sslmode=disable",
		superUser, superPass, postgresHost, postgresPort)
	hldatabaseAdminDsn = fmt.Sprintf("postgresql://%s:%s@%s:%s/hldatabase?sslmode=disable",
		adminUser, adminPass, postgresHost, postgresPort)

	RegisterFailHandler(Fail)
	RunSpecs(t, "Integration Tests")
}
