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

var _ = Describe("hotload context test", Serial, func() {
	var (
		superDb *sql.DB
		userDb  [10]*sql.DB
	)

	BeforeEach(func(ctx context.Context) {
		superDb = openDbPostgres(hldatabaseSuperDsn)
		dbExecSqlStmt(superDb, userSqlTeardown)
		dbExecSqlStmt(superDb, userSqlSetup)
		dbExecSqlStmt(superDb, testSqlSetup)
		dbExecSqlStmt(superDb, truncSqlSetup)
		dbExecSqlStmt(superDb, initSqlSetup)
		dbExecSqlStmt(superDb, grantSqlSetup)

		dbExecAlterUserPass(superDb, testDbUser, testDbPass(0))
		setDSN(hldatabasePassDsn(0), configPath)

		for i := 0; i < len(userDb); i++ {
			userDb[i] = openDbHotload(false)
		}
	})

	AfterEach(func(ctx context.Context) {
		dbExecSqlStmt(superDb, testSqlTeardown)
		dbExecSqlStmt(superDb, userSqlTeardown)
		superDb.Close()

		for i := 0; i < len(userDb); i++ {
			userDb[i].Close()
		}
	})

	It("forceKill=false multiple long db.Exec in succession", func(ctx context.Context) {
		multipleLongTestFn(LongExec, superDb, userDb)
	})
})

func multipleLongTestFn(lMode longDbMode, superDb *sql.DB, userDb [10]*sql.DB) {
	callerCtx := context.Background()
	csource := "multiple-long-in-succession"
	cnum := 141421
	sleepSecs := 2
	insertStmtSleep := fmt.Sprintf("INSERT INTO test (cnum, csource, csleep) VALUES (%d, '%s', PG_SLEEP(%d))", cnum, csource, sleepSecs)
	insertStmtNoSleep := fmt.Sprintf("INSERT INTO test (cnum, csource) VALUES (%d, '%s')", cnum, csource)

	// userDb[0] inserts 1st row
	_, err := userDb[0].ExecContext(callerCtx, insertStmtNoSleep)
	Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("0: error inserting into table test"))

	// userDb[1] inserts 2nd row
	_, err = userDb[1].ExecContext(callerCtx, insertStmtNoSleep)
	Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("1: error inserting into table test"))

	// Long-running fn to insert more rows
	errChan := make(chan error)
	longDbFn := func(which int, lMode longDbMode, userDb *sql.DB, errChan chan error) {
		log.Printf("%d: start db.ExecContext INSERT PG_SLEEP", which)
		result, err := userDb.ExecContext(callerCtx, insertStmtSleep)
		if err == nil {
			log.Printf("%d: db.ExecContext INSERT PG_SLEEP result=%+v", which, result)
		} else {
			log.Printf("%d: db.ExecContext INSERT PG_SLEEP error=%+v", which, err)
		}
		errChan <- err
	}

	go longDbFn(2, lMode, userDb[2], errChan)
	time.Sleep(10 * time.Millisecond)

	// This dsn-change will reset existing conn/txn (userDb[2]),
	// but since forceKill=false, will gracefully let userDb[2] txn continue.
	// Any previous conn/txn will be canceled/closed (but there
	// isn't any previous txn at this point in the test).
	dbExecAlterUserPass(superDb, testDbUser, testDbPass(1))
	setDSN(hldatabasePassDsn(1), configPath)

	go longDbFn(3, lMode, userDb[3], errChan)
	time.Sleep(10 * time.Millisecond)

	// This dsn-change will reset existing conn/txn (userDb[3]),
	// but since forceKill=false, will gracefully let userDb[3] txn continue.
	// Any previous conn/txn will be canceled/closed (userDb[2]).
	dbExecAlterUserPass(superDb, testDbUser, testDbPass(2))
	setDSN(hldatabasePassDsn(2), configPath)

	go longDbFn(4, lMode, userDb[4], errChan)
	time.Sleep(10 * time.Millisecond)

	// This dsn-change will reset existing conn/txn (userDb[4]),
	// but since forceKill=false, will gracefully let userDb[4] txn continue.
	// Any previous conn/txn will be canceled/closed (userDb[3]).
	dbExecAlterUserPass(superDb, testDbUser, testDbPass(3))
	setDSN(hldatabasePassDsn(3), configPath)

	go longDbFn(5, lMode, userDb[5], errChan)
	time.Sleep(10 * time.Millisecond)

	// This dsn-change will reset existing conn/txn (userDb[5]),
	// but since forceKill=false, will gracefully let userDb[5] txn continue.
	// Any previous conn/txn will be canceled/closed (userDb[4]).
	dbExecAlterUserPass(superDb, testDbUser, testDbPass(4))
	setDSN(hldatabasePassDsn(4), configPath)

	// At this point in the test, only userDb[5] txn is allowed to gracefully continue
	// and insert 3rd row; the other txn (userDb[2]/3/4 have been canceled).

	for i := 2; i <= 5; i++ {
		err = <-errChan
		log.Printf("%d: returned from long-running db background thread, err=%v", i, err)
		if i < 5 {
			Expect(err).To(HaveOccurred(), fmt.Sprintf("%d: expect error inserting into table test", i))
		} else {
			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("%d: unexpected error inserting into table test: %v", i, err))
		}
	}
	expectRowCountInDb(superDb, csource, false, 3, int64(cnum))

	dbExecSqlStmt(userDb[0], insertStmtNoSleep)
	dbExecSqlStmt(userDb[1], insertStmtNoSleep)
	dbExecSqlStmt(userDb[2], insertStmtNoSleep)

	dbExecAlterUserPass(superDb, testDbUser, testDbPass(5))
	setDSN(hldatabasePassDsn(5), configPath)

	dbExecSqlStmt(userDb[3], insertStmtNoSleep)
	dbExecSqlStmt(userDb[4], insertStmtNoSleep)
	dbExecSqlStmt(userDb[5], insertStmtNoSleep)

	dbExecAlterUserPass(superDb, testDbUser, testDbPass(6))
	setDSN(hldatabasePassDsn(6), configPath)

	dbExecSqlStmt(userDb[6], insertStmtNoSleep)
	dbExecSqlStmt(userDb[7], insertStmtNoSleep)
	dbExecSqlStmt(userDb[8], insertStmtNoSleep)

	dbExecAlterUserPass(superDb, testDbUser, testDbPass(7))
	setDSN(hldatabasePassDsn(7), configPath)

	dbExecSqlStmt(userDb[9], insertStmtNoSleep)
	expectRowCountInDb(superDb, csource, false, 13, int64(cnum))
}
