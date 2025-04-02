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

type longDbMode int

const (
	LongExec longDbMode = iota
	LongQuery
	LongBegin
)

var longModeNames = map[longDbMode]string{
	LongExec:  "LongExec",
	LongQuery: "LongQuery",
	LongBegin: "LongBegin",
}

func (ss longDbMode) String() string {
	return longModeNames[ss]
}

type longDbTestCase struct {
	forceKill   bool
	testMode    longDbMode
	expErr      bool
	expRowCount int
	expCnum     int64
	gotErr      error
	gotRowCount int
	gotCnum     int64
	longChan    chan longDbTestCase
	db          *sql.DB
}

func longDbExecFn(tc longDbTestCase) {
	log.Printf("start db.Exec INSERT PG_SLEEP")
	result, err := tc.db.Exec(fmt.Sprintf("INSERT INTO test (cnum, csource, csleep) VALUES (%d, 'longDbExecFn', PG_SLEEP(1.0))", 314159))
	if err == nil {
		log.Printf("db.Exec INSERT PG_SLEEP result=%+v", result)
	} else {
		log.Printf("db.Exec INSERT PG_SLEEP error=%+v", err)
	}
	tc.gotErr = err
	tc.longChan <- tc
}

func longDbQueryFn(tc longDbTestCase) {
	log.Printf("start db.Query SELECT PG_SLEEP")
	rows, err := tc.db.Query("SELECT cnum, PG_SLEEP(1.0) FROM test WHERE csource = 'initial'")
	if err != nil {
		log.Printf("db.Query SELECT PG_SLEEP error=%+v", err)
	} else {
		log.Printf("db.Query SELECT PG_SLEEP rows=%+v", rows)
		var cnum int64
		var csleep string
		tc.gotRowCount = 0
		if !rows.Next() {
			err = rows.Err()
			if err != nil {
				log.Printf("db.Query SELECT PG_SLEEP cursor iteration error=%+v", err)
			}
			log.Printf("db.Query SELECT PG_SLEEP cursor returned no rows")
		} else {
			err = rows.Scan(&cnum, &csleep)
			if err != nil {
				log.Printf("db.Query SELECT PG_SLEEP cursor scan error=%+v", err)
			} else {
				tc.gotRowCount = 1
				tc.gotCnum = cnum
			}
		}
	}
	tc.gotErr = err
	tc.longChan <- tc
}

func longDbTestFn(ginkgoCtx context.Context, tc longDbTestCase) {
	//GinkgoHelper()

	db := openDbHotload(tc.forceKill)
	defer db.Close()

	var prevModTime time.Time
	prevModTime = expectModTime(configPath, prevModTime)

	By("Spawn long-running db background thread")
	longChan := make(chan longDbTestCase)
	longDbFn := longDbExecFn
	switch tc.testMode {
	case LongExec:
		longDbFn = longDbExecFn
	case LongQuery:
		longDbFn = longDbQueryFn
	default:
		longDbFn = nil
	}
	tc.longChan = longChan
	tc.db = db
	go longDbFn(tc)

	By("Momentarily sleep/yield to long-running db background thread")
	time.Sleep(10 * time.Millisecond)

	By("Set new DSN, this sleeps/yields for 250 millisecs")
	setDSN(hotloadTest1Dsn, configPath)

	By("Verify configPath modtime was updated after configPath was updated with new DSN")
	prevModTime = expectModTime(configPath, prevModTime)

	By("Wait for return from long-running db background thread")
	tc = <-longChan
	By("Got return from long-running db background thread")
	if tc.expErr {
		Expect(tc.gotErr).To(HaveOccurred(), fmt.Sprintf("expect error inserting into table test"))
	} else {
		Expect(tc.gotErr).ToNot(HaveOccurred(), fmt.Sprintf("error inserting into table test: %v", tc.gotErr))
	}

	Expect(tc.gotRowCount).To(Equal(tc.expRowCount))
	Expect(tc.gotCnum).To(Equal(tc.expCnum))
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

		_, err = hltDb.Exec(initSqlSetup)
		if err != nil {
			Fail(fmt.Sprintf("error initializing test table in hltDb: %v", err))
		}

		//_, err = hlt1Db.Exec(initSqlSetup)
		//if err != nil {
		//	Fail(fmt.Sprintf("error initializing test table in hlt1Db: %v", err))
		//}
	}, NodeTimeout(60*time.Second))

	AfterEach(func(ginkgoCtx context.Context) {
		hltDb.Close()
		hlt1Db.Close()
	}, NodeTimeout(60*time.Second))

	It("long-running db.Exec, forceKill=false", func(ginkgoCtx context.Context) {
		longDbTestFn(ginkgoCtx, longDbTestCase{
			forceKill: false,
			testMode:  LongExec,
			expErr:    false,
		})

		expectValueInDb(hltDb, "longDbExecFn", false, 1, 314159)
		expectValueInDb(hlt1Db, "longDbExecFn", false, 0, 0)
	}, NodeTimeout(60*time.Second))

	It("long-running db.Exec, forceKill=true", func(ginkgoCtx context.Context) {
		longDbTestFn(ginkgoCtx, longDbTestCase{
			forceKill: true,
			testMode:  LongExec,
			expErr:    true,
		})

		expectValueInDb(hltDb, "longDbExecFn", false, 0, 0)
		expectValueInDb(hlt1Db, "longDbExecFn", false, 0, 0)
	}, NodeTimeout(60*time.Second))

	It("long-running db.Query, forceKill=false", func(ginkgoCtx context.Context) {
		longDbTestFn(ginkgoCtx, longDbTestCase{
			forceKill:   false,
			testMode:    LongQuery,
			expErr:      false,
			expRowCount: 1,
			expCnum:     161803,
		})

		expectValueInDb(hltDb, "initial", false, 1, 161803)
		expectValueInDb(hlt1Db, "initial", false, 0, 0)
	}, NodeTimeout(60*time.Second))

	It("long-running db.Query, forceKill=true", func(ginkgoCtx context.Context) {
		longDbTestFn(ginkgoCtx, longDbTestCase{
			forceKill: true,
			testMode:  LongQuery,
			expErr:    true,
		})

		expectValueInDb(hltDb, "initial", false, 1, 161803)
		expectValueInDb(hlt1Db, "initial", false, 0, 0)
	}, NodeTimeout(60*time.Second))
})
