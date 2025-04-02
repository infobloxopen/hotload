package integrationtests

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("hotload integration tests - long running db transaction", Serial, func() {
	var (
		hltDb  *sql.DB
		hlt1Db *sql.DB
	)

	BeforeEach(func(ctx context.Context) {
		By("Resetting and truncating test db")
		setDSN(hotloadTestDsn, configPath)
		hltDb = openDbPostgres(hotloadTestDsn)
		hlt1Db = openDbPostgres(hotloadTest1Dsn)

		_, err := hltDb.Exec(truncSqlSetup)
		if err != nil {
			Fail(fmt.Sprintf("error truncating test table in hltDb: %v", err))
		}

		_, err = hlt1Db.Exec(truncSqlSetup)
		if err != nil {
			Fail(fmt.Sprintf("error truncating test table in hlt1Db: %v", err))
		}
	}, NodeTimeout(60*time.Second))

	AfterEach(func(ctx context.Context) {
		hltDb.Close()
		hlt1Db.Close()
	}, NodeTimeout(60*time.Second))

	dbExecTest := func(ctx context.Context, forceKill bool) {
		GinkgoHelper()

		db := openDbHotload(forceKill)
		defer db.Close()

		var prevModTime time.Time
		prevModTime = expectModTime(configPath, prevModTime)

		By("Spawn long-running INSERT background thread")
		errChan := make(chan error)
		go func(errChan chan error) {
			log.Printf("start INSERT PG_SLEEP")
			result, err := db.Exec(fmt.Sprintf("INSERT INTO test (c1, csleep) VALUES (%d, PG_SLEEP(3))", 314159))
			if err == nil {
				log.Printf("INSERT PG_SLEEP result=%+v", result)
			} else {
				log.Printf("INSERT PG_SLEEP error=%+v", err)
			}
			errChan <- err
		}(errChan)

		By("Momentarily sleep/yield to long-running INSERT background thread")
		time.Sleep(100 * time.Millisecond)

		By("Set new DSN, this sleeps/yields for 250 millisecs")
		setDSN(hotloadTest1Dsn, configPath)

		By("Verify configPath modtime was updated after configPath was updated with new DSN")
		prevModTime = expectModTime(configPath, prevModTime)

		By("Wait for return from long-running INSERT background thread")
		insertErr := <-errChan
		if insertErr != nil {
			Fail(fmt.Sprintf("error return from inserting row: %v", insertErr))
		}
		By("Got return from long-running INSERT background thread")
	}

	It("long-running db.Exec, forceKill=false", func(ctx context.Context) {
		dbExecTest(ctx, false)

		expectValueInDb(hltDb, 0)
		expectValueInDb(hlt1Db, 123)
	}, NodeTimeout(60*time.Second))

	It("long-running db.Exec, forceKill=true", func(ctx context.Context) {
		dbExecTest(ctx, true)

		expectValueInDb(hltDb, 123)
		expectValueInDb(hlt1Db, 0)
	}, NodeTimeout(60*time.Second))
})
