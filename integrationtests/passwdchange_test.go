package integrationtests

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	. "github.com/onsi/ginkgo/v2"
)

func dbExecSqlStmt(db *sql.DB, sqlStmt string) {
	GinkgoHelper()
	_, err := db.Exec(sqlStmt)
	if err != nil {
		Fail(fmt.Sprintf("db.Exec(%s) error: %v", sqlStmt, err))
	}
	time.Sleep(1 * time.Millisecond)
}

func dbExecAlterUserPass(db *sql.DB, user, pass string) {
	GinkgoHelper()
	sqlStmt := fmt.Sprintf("ALTER USER %s WITH PASSWORD '%s'", user, pass)
	dbExecSqlStmt(db, sqlStmt)
}

var _ = Describe("hotload integration tests - db passwd change", Serial, func() {
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
	})

	AfterEach(func(ctx context.Context) {
		dbExecSqlStmt(superDb, testSqlTeardown)
		dbExecSqlStmt(superDb, userSqlTeardown)
		superDb.Close()
	})

	It("should reconnect to db when password changes", func(ctx context.Context) {
		userDb = openDbHotload(false)
		defer userDb.Close()

		csource := "db-passwd-change"
		cnum := 184775
		var prevModTime time.Time
		for i := 0; i < 2; i++ {
			// Verify configPath modtime was updated after configPath was updated with new DSN
			prevModTime = expectModTime(configPath, prevModTime)

			r, err := userDb.Exec(fmt.Sprintf("INSERT INTO test (cnum, csource) VALUES (%d, '%s')",
				cnum, csource))
			if err != nil {
				Fail(fmt.Sprintf("%d: error inserting cnum=%d row: %v", i, cnum, err))
			} else {
				log.Printf("%d: inserted cnum=%d row", i, cnum)
			}
			log.Print(r)

			dbExecAlterUserPass(superDb, testDbUser, testDbPass(2))

			// Set new DSN, note that this sleeps for 250 millisecs
			setDSN(hldatabasePassDsn(2), configPath)
		}
		expectRowCountInDb(superDb, csource, false, 2, int64(cnum))
	})

	It("long-running db.Exec, forceKill=false", func(ginkgoCtx context.Context) {
		tc := longDbTestCase{
			testType:    LongPasswordChange,
			forceKill:   false,
			testMode:    LongExec,
			source:      LongExec.String(),
			expErr:      false,
			expRowCount: 0, // expRowCount=0 b/c longDbExecFn doesn't query SELECT
			expCnum:     184775,
			superDb:     superDb,
		}
		longDbTestFn(ginkgoCtx, tc)

		expectRowCountInDb(superDb, tc.source, false, 3, tc.expCnum)
	}, NodeTimeout(60*time.Second))

	It("long-running db.Exec, forceKill=true", func(ginkgoCtx context.Context) {
		tc := longDbTestCase{
			testType:    LongPasswordChange,
			forceKill:   true,
			testMode:    LongExec,
			source:      LongExec.String(),
			expErr:      true,
			expRowCount: 0, // expRowCount=0 b/c longDbExecFn doesn't query SELECT
			expCnum:     184775,
			superDb:     superDb,
		}
		longDbTestFn(ginkgoCtx, tc)

		expectRowCountInDb(superDb, tc.source, false, 2, tc.expCnum)
	}, NodeTimeout(60*time.Second))

	It("long-running db.ExecContext, forceKill=false", func(ginkgoCtx context.Context) {
		tc := longDbTestCase{
			testType:    LongPasswordChange,
			forceKill:   false,
			testMode:    LongExecContext,
			source:      LongExecContext.String(),
			expErr:      false,
			expRowCount: 0, // expRowCount=0 b/c longDbExecContextFn doesn't query SELECT
			expCnum:     184775,
			superDb:     superDb,
		}
		longDbTestFn(ginkgoCtx, tc)

		expectRowCountInDb(superDb, tc.source, false, 3, tc.expCnum)
	}, NodeTimeout(60*time.Second))

	It("long-running db.ExecContext, forceKill=true", func(ginkgoCtx context.Context) {
		tc := longDbTestCase{
			testType:    LongPasswordChange,
			forceKill:   true,
			testMode:    LongExecContext,
			source:      LongExecContext.String(),
			expErr:      true,
			expRowCount: 0, // expRowCount=0 b/c longDbExecFn doesn't query SELECT
			expCnum:     184775,
			superDb:     superDb,
		}
		longDbTestFn(ginkgoCtx, tc)

		// TODO: tc.expErr=true, so why expRowCount=2? why not expRowCount=1?
		// INSERT fails with this (tc.expErr=true above):
		//   error=read tcp 127.0.0.1:58390->127.0.0.1:5432: use of closed network connection
		// yet the INSERT seems to've succeeded?!?
		//expectRowCountInDb(hltDb, tc.source, false, 1, tc.expCnum)

		expectRowCountInDb(superDb, tc.source, false, 2, tc.expCnum)
	}, NodeTimeout(60*time.Second))

	It("long-running db.Query, forceKill=false", func(ginkgoCtx context.Context) {
		tc := longDbTestCase{
			testType:    LongPasswordChange,
			forceKill:   false,
			testMode:    LongQuery,
			source:      "initial", // see initSqlSetup variable
			expErr:      false,
			expRowCount: 1,
			expCnum:     161803, // see initSqlSetup variable
			superDb:     superDb,
		}
		longDbTestFn(ginkgoCtx, tc)

		expectRowCountInDb(superDb, tc.source, false, 1, tc.expCnum)
	}, NodeTimeout(60*time.Second))

	It("long-running db.Query, forceKill=true", func(ginkgoCtx context.Context) {
		tc := longDbTestCase{
			testType:    LongPasswordChange,
			forceKill:   true,
			testMode:    LongQuery,
			source:      "initial", // see initSqlSetup variable
			expErr:      true,
			expRowCount: 0,
			expCnum:     161803, // see initSqlSetup variable
			superDb:     superDb,
		}
		longDbTestFn(ginkgoCtx, tc)

		expectRowCountInDb(superDb, tc.source, false, 1, tc.expCnum)
	}, NodeTimeout(60*time.Second))

	It("long-running db.Begin, forceKill=false", func(ginkgoCtx context.Context) {
		tc := longDbTestCase{
			testType:    LongPasswordChange,
			forceKill:   false,
			testMode:    LongBegin,
			source:      LongBegin.String(),
			expErr:      false,
			expRowCount: 1,
			expCnum:     271828,
			superDb:     superDb,
		}
		longDbTestFn(ginkgoCtx, tc)

		expectRowCountInDb(superDb, tc.source, false, 3, tc.expCnum)
	}, NodeTimeout(60*time.Second))

	It("long-running db.Begin, forceKill=true", func(ginkgoCtx context.Context) {
		tc := longDbTestCase{
			testType:    LongPasswordChange,
			forceKill:   true,
			testMode:    LongBegin,
			source:      LongBegin.String(),
			expErr:      true,
			expRowCount: 0,
			expCnum:     271828,
			superDb:     superDb,
		}
		longDbTestFn(ginkgoCtx, tc)

		expectRowCountInDb(superDb, tc.source, false, 2, tc.expCnum)
	}, NodeTimeout(60*time.Second))

	It("long-running db.BeginTx, forceKill=false", func(ginkgoCtx context.Context) {
		tc := longDbTestCase{
			testType:    LongPasswordChange,
			forceKill:   false,
			testMode:    LongBeginTx,
			source:      LongBeginTx.String(),
			expErr:      false,
			expRowCount: 1,
			expCnum:     271828,
			superDb:     superDb,
		}
		longDbTestFn(ginkgoCtx, tc)

		expectRowCountInDb(superDb, tc.source, false, 3, tc.expCnum)
	}, NodeTimeout(60*time.Second))

	It("long-running db.BeginTx, forceKill=true", func(ginkgoCtx context.Context) {
		tc := longDbTestCase{
			testType:    LongPasswordChange,
			forceKill:   true,
			testMode:    LongBeginTx,
			source:      LongBeginTx.String(),
			expErr:      true,
			expRowCount: 0,
			expCnum:     271828,
			superDb:     superDb,
		}
		longDbTestFn(ginkgoCtx, tc)

		expectRowCountInDb(superDb, tc.source, false, 2, tc.expCnum)
	}, NodeTimeout(60*time.Second))
})
