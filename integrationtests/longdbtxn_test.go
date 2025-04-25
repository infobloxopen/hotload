package integrationtests

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/infobloxopen/hotload"
	_ "github.com/infobloxopen/hotload/fsnotify"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type longTestType int

const (
	LongDatabaseChange longTestType = iota
	LongPasswordChange
)

var longTestNames = map[longTestType]string{
	LongDatabaseChange: "LongDatabaseChange",
	LongPasswordChange: "LongPasswordChange",
}

func (l longTestType) String() string {
	return longTestNames[l]
}

type longDbMode int

const (
	LongExec longDbMode = iota
	LongExecContext
	LongQuery
	LongQueryContext
	LongBegin
	LongBeginTx
)

var longModeNames = map[longDbMode]string{
	LongExec:         "LongExec",
	LongExecContext:  "LongExecContext",
	LongQueryContext: "LongQueryContext",
	LongQuery:        "LongQuery",
	LongBegin:        "LongBegin",
	LongBeginTx:      "LongBeginTx",
}

func (l longDbMode) String() string {
	return longModeNames[l]
}

type longDbTestCase struct {
	forceKill   bool
	testType    longTestType
	testMode    longDbMode
	source      string
	expErr      bool
	expRowCount int
	expCnum     int64
	gotErr      error
	gotRowCount int
	longChan    chan longDbTestCase
	userDb      *sql.DB
	superDb     *sql.DB
}

// longDbExecFn DB.Exec's a long-running INSERT stmt
func longDbExecFn(tc longDbTestCase) {
	tc.gotRowCount = 0
	log.Printf("start db.Exec INSERT PG_SLEEP")
	insertStmt := fmt.Sprintf("INSERT INTO test (cnum, csource, csleep) VALUES (%d, '%s', PG_SLEEP(1.0))", tc.expCnum, tc.source)
	var result sql.Result
	var err error
	if tc.testMode == LongExec {
		result, err = tc.userDb.Exec(insertStmt)
	} else {
		result, err = tc.userDb.ExecContext(context.Background(), insertStmt)
	}
	if err == nil {
		log.Printf("db.Exec INSERT PG_SLEEP result=%+v", result)
	} else {
		log.Printf("db.Exec INSERT PG_SLEEP error=%+v", err)
	}
	tc.gotErr = err
	tc.longChan <- tc
}

// longDbQueryFn DB.Query's a long-running SELECT stmt
func longDbQueryFn(tc longDbTestCase) {
	tc.gotRowCount = 0
	log.Printf("start db.Query SELECT PG_SLEEP")
	rows, err := tc.userDb.Query(fmt.Sprintf("SELECT COUNT(*), PG_SLEEP(1.0) FROM test WHERE csource = '%s' AND cnum = %d", tc.source, tc.expCnum))
	if err != nil {
		log.Printf("db.Query SELECT PG_SLEEP error=%+v", err)
	} else {
		log.Printf("db.Query SELECT PG_SLEEP rows=%+v", rows)
		if !rows.Next() {
			err = rows.Err()
			if err != nil {
				log.Printf("db.Query SELECT PG_SLEEP cursor iteration error=%+v", err)
			}
			log.Printf("db.Query SELECT PG_SLEEP cursor returned no rows")
		} else {
			var csleep string
			err = rows.Scan(&tc.gotRowCount, &csleep)
			if err != nil {
				log.Printf("db.Query SELECT PG_SLEEP cursor scan error=%+v", err)
			} else {
				log.Printf("db.Query SELECT PG_SLEEP cursor scanned rowcount=%d", tc.gotRowCount)
			}
		}
	}
	tc.gotErr = err
	tc.longChan <- tc
}

// longDbBeginFn DB.Begin's (or DB.BeginTx's) and DB.Commit/DB.Rollback's
// around a long-running db txn that does a INSERT, sleep(1s), SELECT.
func longDbBeginFn(tc longDbTestCase) {
	tc.gotRowCount = 0
	txnFn := func() error {
		var txnDb *sql.Tx
		var err error
		if tc.testMode == LongBeginTx {
			log.Printf("start db.BeginTx")
			txnDb, err = tc.userDb.BeginTx(context.Background(), &sql.TxOptions{
				Isolation: sql.LevelReadCommitted,
			})
			if err == nil {
				log.Printf("db.BeginTx success")
			} else {
				log.Printf("db.BeginTx failure err=%+v", err)
				return err
			}
		} else {
			log.Printf("start db.Begin")
			txnDb, err = tc.userDb.Begin()
			if err == nil {
				log.Printf("db.Begin success")
			} else {
				log.Printf("db.Begin failure err=%+v", err)
				return err
			}
		}
		defer func() {
			if err == nil {
				commitErr := txnDb.Commit()
				log.Printf("txnDb.Commit err=%v", commitErr)
			} else {
				rollbackErr := txnDb.Rollback()
				log.Printf("txnDb.Rollback err=%v", rollbackErr)
			}
		}()

		log.Printf("start db.Exec INSERT PG_SLEEP")
		result, err := txnDb.Exec(fmt.Sprintf("INSERT INTO test (cnum, csource, csleep) VALUES (%d, '%s', PG_SLEEP(0.000001))", tc.expCnum, tc.source))
		if err == nil {
			log.Printf("db.Exec INSERT PG_SLEEP result=%+v", result)
		} else {
			log.Printf("db.Exec INSERT PG_SLEEP error=%+v", err)
		}

		time.Sleep(1 * time.Second)

		log.Printf("start db.Query SELECT PG_SLEEP")
		rows, err := txnDb.Query(fmt.Sprintf("SELECT COUNT(*), PG_SLEEP(0.000001) FROM test WHERE csource = '%s' AND cnum = %d", tc.source, tc.expCnum))
		if err != nil {
			log.Printf("db.Query SELECT PG_SLEEP error=%+v", err)
		} else {
			log.Printf("db.Query SELECT PG_SLEEP rows=%+v", rows)
			if !rows.Next() {
				err = rows.Err()
				if err != nil {
					log.Printf("db.Query SELECT PG_SLEEP cursor iteration error=%+v", err)
				}
				log.Printf("db.Query SELECT PG_SLEEP cursor returned no rows")
			} else {
				var csleep string
				err = rows.Scan(&tc.gotRowCount, &csleep)
				if err != nil {
					log.Printf("db.Query SELECT PG_SLEEP cursor scan error=%+v", err)
				} else {
					log.Printf("db.Query SELECT PG_SLEEP cursor scanned rowcount=%d", tc.gotRowCount)
				}
			}
		}
		return err
	}
	err := txnFn()
	tc.gotErr = err
	tc.longChan <- tc
}

func longDbTestFn(ginkgoCtx context.Context, tc longDbTestCase) {
	//GinkgoHelper()

	dsn1 := hotloadTestDsn
	dsn2 := hotloadTest1Dsn
	if tc.testType == LongPasswordChange {
		dsn1 = hldatabasePassDsn(1)
		dsn2 = hldatabasePassDsn(2)
	}

	db := openDbHotload(tc.forceKill)
	defer func() {
		log.Println("longDbTestFn: closing hotload db conn")
		db.Close()
	}()

	var prevModTime time.Time
	prevModTime = expectModTime(configPath, prevModTime)

	longChan := make(chan longDbTestCase)
	longDbFn := longDbExecFn
	switch tc.testMode {
	case LongExec, LongExecContext:
		longDbFn = longDbExecFn
	case LongQuery, LongQueryContext:
		longDbFn = longDbQueryFn
	case LongBegin, LongBeginTx:
		longDbFn = longDbBeginFn
	default:
		longDbFn = nil
		Fail(fmt.Sprintf("invalid testMode=%d", int(tc.testMode)))
	}
	tc.longChan = longChan
	tc.userDb = db

	// 1st background long-running db thread, set new DSN in middle of long-running db txn

	By("1st: Spawn long-running db background thread")
	go longDbFn(tc)

	By("1st: Momentarily sleep/yield to long-running db background thread")
	time.Sleep(10 * time.Millisecond)

	By("1st: Set new DSN, this sleeps/yields for 250 millisecs")
	if tc.testType == LongPasswordChange {
		dbExecAlterUserPass(tc.superDb, testDbUser, testDbPass(2))
	}
	setDSN(dsn2, configPath)

	By("1st: Verify configPath modtime was updated after configPath was updated with new DSN")
	prevModTime = expectModTime(configPath, prevModTime)

	By("1st: Wait for return from long-running db background thread")
	tc = <-longChan
	log.Printf("1st: tc returned from long-running db background thread = %#v", tc)
	By("1st: Got return from long-running db background thread")
	if tc.expErr {
		Expect(tc.gotErr).To(HaveOccurred(), fmt.Sprintf("expect error inserting into table test"))
	} else {
		Expect(tc.gotErr).ToNot(HaveOccurred(), fmt.Sprintf("error inserting into table test: %v", tc.gotErr))
	}

	Expect(tc.gotRowCount).To(Equal(tc.expRowCount))

	// 2nd background long-running db thread, without setting new DSN

	By("2nd: Spawn long-running db background thread")
	go longDbFn(tc)

	By("2nd: Wait for return from long-running db background thread")
	tc = <-longChan
	log.Printf("2nd: tc returned from long-running db background thread = %#v", tc)
	By("2nd: Got return from long-running db background thread")
	Expect(tc.gotErr).ToNot(HaveOccurred(), fmt.Sprintf("2nd: error executing long db test: %v", tc.gotErr))

	// 3rd background long-running db thread, after setting new DSN

	By("3rd: Revert to old DSN, this sleeps/yields for 250 millisecs")
	if tc.testType == LongPasswordChange {
		dbExecAlterUserPass(tc.superDb, testDbUser, testDbPass(1))
	}
	setDSN(dsn1, configPath)

	By("3rd: Verify configPath modtime was updated after configPath was updated with new DSN")
	prevModTime = expectModTime(configPath, prevModTime)

	By("3rd: Spawn long-running db background thread")
	go longDbFn(tc)

	By("3rd: Wait for return from long-running db background thread")
	tc = <-longChan
	log.Printf("3rd: tc returned from long-running db background thread = %#v", tc)
	By("3rd: Got return from long-running db background thread")
	Expect(tc.gotErr).ToNot(HaveOccurred(), fmt.Sprintf("3rd: error executing long db test: %v", tc.gotErr))
}

var _ = Describe("hotload integration tests - long running db transaction", Serial, func() {
	var (
		hltDb  *sql.DB
		hlt1Db *sql.DB
	)

	BeforeEach(func(ginkgoCtx context.Context) {
		//hotload.UnregisterStrategy("fsnotify")
		//hotload.RegisterStrategy("fsnotify", fsnotify.NewStrategy())
		//time.Sleep(100 * time.Millisecond) // Sleep/yield to fsnotify background threads

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

		// insert 1 initial row into hltDb
		_, err = hltDb.Exec(initSqlSetup)
		if err != nil {
			Fail(fmt.Sprintf("error inserting initial row 1 into test table in hltDb: %v", err))
		}

		// insert 2 initial rows into hlt1Db
		_, err = hlt1Db.Exec(initSqlSetup)
		if err != nil {
			Fail(fmt.Sprintf("error inserting initial row 1 into test table in hlt1Db: %v", err))
		}
		time.Sleep(100 * time.Millisecond)
		_, err = hlt1Db.Exec(initSqlSetup)
		if err != nil {
			Fail(fmt.Sprintf("error inserting initial row 2 into test table in hlt1Db: %v", err))
		}
	}, NodeTimeout(60*time.Second))

	AfterEach(func(ginkgoCtx context.Context) {
		//expectConnCountInDb(hltDb, 3)
		hltDb.Close()
		hlt1Db.Close()
	}, NodeTimeout(60*time.Second))

	It("long-running db.Exec, forceKill=false", func(ginkgoCtx context.Context) {
		tc := longDbTestCase{
			forceKill:   false,
			testMode:    LongExec,
			source:      LongExec.String(),
			expErr:      false,
			expRowCount: 0, // expRowCount=0 b/c longDbExecFn doesn't query SELECT
			expCnum:     314159,
		}
		longDbTestFn(ginkgoCtx, tc)

		expectRowCountInDb(hltDb, tc.source, false, 2, tc.expCnum)
		expectRowCountInDb(hlt1Db, tc.source, false, 1, tc.expCnum)
	}, NodeTimeout(60*time.Second))

	It("long-running db.Exec, forceKill=true", func(ginkgoCtx context.Context) {
		tc := longDbTestCase{
			forceKill:   true,
			testMode:    LongExec,
			source:      LongExec.String(),
			expErr:      true,
			expRowCount: 0, // expRowCount=0 b/c longDbExecFn doesn't query SELECT
			expCnum:     314159,
		}
		longDbTestFn(ginkgoCtx, tc)

		// TODO: tc.expErr=true, so why expRowCount=2? why not expRowCount=1?
		// INSERT fails with this (tc.expErr=true above):
		//   error=read tcp 127.0.0.1:58390->127.0.0.1:5432: use of closed network connection
		// yet the INSERT seems to've succeeded?!?
		expectRowCountInDb(hltDb, tc.source, false, 1, tc.expCnum)

		expectRowCountInDb(hlt1Db, tc.source, false, 1, tc.expCnum)
	}, NodeTimeout(60*time.Second))

	It("long-running db.ExecContext, forceKill=false", func(ginkgoCtx context.Context) {
		tc := longDbTestCase{
			forceKill:   false,
			testMode:    LongExecContext,
			source:      LongExecContext.String(),
			expErr:      false,
			expRowCount: 0, // expRowCount=0 b/c longDbExecContextFn doesn't query SELECT
			expCnum:     314159,
		}
		longDbTestFn(ginkgoCtx, tc)

		expectRowCountInDb(hltDb, tc.source, false, 2, tc.expCnum)
		expectRowCountInDb(hlt1Db, tc.source, false, 1, tc.expCnum)
	}, NodeTimeout(60*time.Second))

	It("long-running db.ExecContext, forceKill=true", func(ginkgoCtx context.Context) {
		tc := longDbTestCase{
			forceKill:   true,
			testMode:    LongExecContext,
			source:      LongExecContext.String(),
			expErr:      true,
			expRowCount: 0, // expRowCount=0 b/c longDbExecFn doesn't query SELECT
			expCnum:     314159,
		}
		longDbTestFn(ginkgoCtx, tc)

		// TODO: tc.expErr=true, so why expRowCount=2? why not expRowCount=1?
		// INSERT fails with this (tc.expErr=true above):
		//   error=read tcp 127.0.0.1:58390->127.0.0.1:5432: use of closed network connection
		// yet the INSERT seems to've succeeded?!?
		expectRowCountInDb(hltDb, tc.source, false, 1, tc.expCnum)

		expectRowCountInDb(hlt1Db, tc.source, false, 1, tc.expCnum)
	}, NodeTimeout(60*time.Second))

	It("long-running db.Query, forceKill=false", func(ginkgoCtx context.Context) {
		tc := longDbTestCase{
			forceKill:   false,
			testMode:    LongQuery,
			source:      "initial", // see initSqlSetup variable
			expErr:      false,
			expRowCount: 1,
			expCnum:     161803, // see initSqlSetup variable
		}
		longDbTestFn(ginkgoCtx, tc)

		expectRowCountInDb(hltDb, tc.source, false, 1, tc.expCnum)
		expectRowCountInDb(hlt1Db, tc.source, false, 2, tc.expCnum)
	}, NodeTimeout(60*time.Second))

	It("long-running db.Query, forceKill=true", func(ginkgoCtx context.Context) {
		tc := longDbTestCase{
			forceKill:   true,
			testMode:    LongQuery,
			source:      "initial", // see initSqlSetup variable
			expErr:      true,
			expRowCount: 0,
			expCnum:     161803, // see initSqlSetup variable
		}
		longDbTestFn(ginkgoCtx, tc)

		expectRowCountInDb(hltDb, tc.source, false, 1, tc.expCnum)
		expectRowCountInDb(hlt1Db, tc.source, false, 2, tc.expCnum)
	}, NodeTimeout(60*time.Second))

	It("long-running db.Begin, forceKill=false", func(ginkgoCtx context.Context) {
		tc := longDbTestCase{
			forceKill:   false,
			testMode:    LongBegin,
			source:      LongBegin.String(),
			expErr:      false,
			expRowCount: 1,
			expCnum:     271828,
		}
		longDbTestFn(ginkgoCtx, tc)

		expectRowCountInDb(hltDb, tc.source, false, 2, tc.expCnum)
		expectRowCountInDb(hlt1Db, tc.source, false, 1, tc.expCnum)
	}, NodeTimeout(60*time.Second))

	It("long-running db.Begin, forceKill=true", func(ginkgoCtx context.Context) {
		tc := longDbTestCase{
			forceKill:   true,
			testMode:    LongBegin,
			source:      LongBegin.String(),
			expErr:      true,
			expRowCount: 0,
			expCnum:     271828,
		}
		longDbTestFn(ginkgoCtx, tc)

		expectRowCountInDb(hltDb, tc.source, false, 1, tc.expCnum)
		expectRowCountInDb(hlt1Db, tc.source, false, 1, tc.expCnum)
	}, NodeTimeout(60*time.Second))

	It("long-running db.BeginTx, forceKill=false", func(ginkgoCtx context.Context) {
		tc := longDbTestCase{
			forceKill:   false,
			testMode:    LongBeginTx,
			source:      LongBeginTx.String(),
			expErr:      false,
			expRowCount: 1,
			expCnum:     271828,
		}
		longDbTestFn(ginkgoCtx, tc)

		expectRowCountInDb(hltDb, tc.source, false, 2, tc.expCnum)
		expectRowCountInDb(hlt1Db, tc.source, false, 1, tc.expCnum)
	}, NodeTimeout(60*time.Second))

	It("long-running db.BeginTx, forceKill=true", func(ginkgoCtx context.Context) {
		tc := longDbTestCase{
			forceKill:   true,
			testMode:    LongBeginTx,
			source:      LongBeginTx.String(),
			expErr:      true,
			expRowCount: 0,
			expCnum:     271828,
		}
		longDbTestFn(ginkgoCtx, tc)

		expectRowCountInDb(hltDb, tc.source, false, 1, tc.expCnum)
		expectRowCountInDb(hlt1Db, tc.source, false, 1, tc.expCnum)
	}, NodeTimeout(60*time.Second))
})
