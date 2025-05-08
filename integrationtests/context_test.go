package integrationtests

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("postgres-direct (non-hotload) context test (verify underlying postgres driver is correct)", Serial, func() {
	var (
		superDb *sql.DB
		userDb  *sql.DB
	)

	BeforeEach(func(ctx context.Context) {
		superDb = openDbPostgres(hldatabaseSuperDsn)
		dbExecSqlStmt(superDb, userSqlTeardown)
		dbExecSqlStmt(superDb, userSqlSetup)
		dbExecSqlStmt(superDb, testSqlSetup)
		dbExecSqlStmt(superDb, truncSqlSetup)
		userDb = openDbPostgres(hldatabaseSuperDsn)
	})

	AfterEach(func(ctx context.Context) {
		dbExecSqlStmt(superDb, testSqlTeardown)
		dbExecSqlStmt(superDb, userSqlTeardown)
		superDb.Close()
		userDb.Close()
	})

	It("Cancel ExecContext, rowcount and error return should be consistent (forceKill=false)", func(ctx context.Context) {
		callerCancelContextTestFn(false, superDb, userDb)
	})

	It("Cancel ExecContext, rowcount and error return should be consistent (forceKill=true)", func(ctx context.Context) {
		callerCancelContextTestFn(true, superDb, userDb)
	})
})

var _ = Describe("hotload context test", Serial, func() {
	var (
		superDb *sql.DB
		userDb  *sql.DB
	)

	BeforeEach(func(ctx context.Context) {
		superDb = openDbPostgres(hldatabaseSuperDsn)
		dbExecSqlStmt(superDb, userSqlTeardown)
		dbExecSqlStmt(superDb, userSqlSetup)
		dbExecSqlStmt(superDb, testSqlSetup)
		dbExecSqlStmt(superDb, truncSqlSetup)
		dbExecSqlStmt(superDb, initSqlSetup)
		dbExecSqlStmt(superDb, grantSqlSetup)

		dbExecAlterUserPass(superDb, testDbUser, testDbPass(1))
		setDSN(hldatabasePassDsn(1), configPath)
		userDb = openDbHotload(false)
	})

	AfterEach(func(ctx context.Context) {
		dbExecSqlStmt(superDb, testSqlTeardown)
		dbExecSqlStmt(superDb, userSqlTeardown)
		superDb.Close()
		userDb.Close()
	})

	It("Cancel ExecContext, rowcount and error return should be consistent (forceKill=false)", func(ctx context.Context) {
		callerCancelContextTestFn(false, superDb, userDb)
	})

	It("Cancel ExecContext, rowcount and error return should be consistent (forceKill=true)", func(ctx context.Context) {
		callerCancelContextTestFn(true, superDb, userDb)
	})
})

func callerCancelContextTestFn(forceKill bool, superDb, userDb *sql.DB) {
	callerCtx, cancelFn := context.WithCancel(context.Background())
	csource := "caller-cancel-exec-context"
	cnum := 141421
	insertStmt := fmt.Sprintf("INSERT INTO test (cnum, csource, csleep) VALUES (%d, '%s', PG_SLEEP(1.0))", cnum, csource)

	By("Insert 1st row")
	_, err := userDb.ExecContext(callerCtx, insertStmt)
	Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("error inserting into table test"))

	// Long-running fn to insert 2nd row
	errChan := make(chan error)
	longExecContextFn := func(errChan chan error) {
		log.Printf("start db.ExecContext INSERT PG_SLEEP")
		result, err := userDb.ExecContext(callerCtx, insertStmt)
		if err == nil {
			log.Printf("db.ExecContext INSERT PG_SLEEP result=%+v", result)
		} else {
			log.Printf("db.ExecContext INSERT PG_SLEEP error=%+v", err)
		}
		errChan <- err
	}

	By("Spawn long-running db background thread")
	go longExecContextFn(errChan)

	By("Momentarily sleep/yield to long-running db background thread")
	time.Sleep(100 * time.Millisecond)

	By("Cancel context of long-running db background thread")
	cancelFn()
	time.Sleep(1 * time.Millisecond)

	if forceKill {
		By("Close connection of long-running db background thread")
		userDb.Close()
	}

	By("Wait for return from long-running db background thread")
	err = <-errChan
	log.Printf("returned from long-running db background thread, err=%v", err)
	By("Got return from long-running db background thread")
	Expect(err).To(HaveOccurred(), fmt.Sprintf("expect error inserting into table test"))

	expectRowCountInDb(superDb, csource, false, 1, int64(cnum))
}
