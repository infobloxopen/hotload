package integrationtests

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/infobloxopen/hotload"
	_ "github.com/infobloxopen/hotload/fsnotify"
	"github.com/infobloxopen/hotload/modtime"
	"github.com/lib/pq"
	_ "github.com/lib/pq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	fsnotifyStrategy = "fsnotify"
	configPath       = "/tmp/hotload_integration_test_dsn_config.txt"
	testSqlSetup     = "CREATE TABLE test (cnum int, csource text, csleep text)"
	testSqlTeardown  = "DROP TABLE IF EXISTS test"
	truncSqlSetup    = "TRUNCATE TABLE test"
	initSqlSetup     = "INSERT INTO test (cnum, csource) VALUES (161803, 'initial')"
)

var (
	mtmCtx         context.Context
	mtmCancelCtxFn context.CancelFunc
	mtm            *modtime.ModTimeMonitor
)

func init() {
	// this function call registers the lib/pq postgres driver with hotload
	hotload.RegisterSQLDriver("postgres", &pq.Driver{})
}

func setDSN(dsn string, path string) {
	err := os.WriteFile(path, []byte(dsn), 0777)
	if err != nil {
		Fail(fmt.Sprintf("setDSN: error writing dsn file: %v", err))
	}
	log.Printf("setDSN: success writing '%s' to '%s'", dsn, path)

	// Yield thread to let switch over take place
	dur := 250 * time.Millisecond
	time.Sleep(dur)
	log.Printf("setDSN: slept/yield %s", dur)
}

// Open a db using postgres driver, or die
func openDbPostgres(dsn string) *sql.DB {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		Fail(fmt.Sprintf("openDbPostgres: error opening db dsn '%s': %v", dsn, err))
	}
	log.Printf("openDbPostgres: opened db dsn '%s'", dsn)

	return db
}

// Open a db using hotload driver, or die
func openDbHotload(forceKill bool) *sql.DB {
	dsnUrl := "fsnotify://postgres" + configPath
	if forceKill {
		dsnUrl = dsnUrl + "?forceKill=true"
	}

	db, err := sql.Open("hotload", dsnUrl)
	if err != nil {
		Fail(fmt.Sprintf("openDbHotload: err opening db dsn '%s': %v", dsnUrl, err))
	}
	log.Printf("openDbHotload: opened hotload dsn '%s'", dsnUrl)

	err = db.Ping()
	if err != nil {
		Fail(fmt.Sprintf("openDbHotload: err pinging db dsn '%s': %v", dsnUrl, err))
	}
	log.Printf("openDbHotload: pinged hotload dsn '%s'", dsnUrl)

	return db
}

func expectValueInDb(db *sql.DB, expected int64) {
	GinkgoHelper()
	log.Printf("expectValueInDb: Querying cnum value, expecting value to be: %d", expected)
	r, err := db.Query("SELECT cnum FROM test WHERE csource = 'runtime'")
	var cnum int64
	Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("expectValueInDb: error reading from table test: %v", err))
	if !r.Next() {
		Expect(r.Err()).ToNot(HaveOccurred(), fmt.Sprintf("expectValueInDb: cursor iteration err: %v", r.Err()))
		Fail("expectValueInDb: no rows found")
	}
	err = r.Scan(&cnum)
	Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("expectValueInDb: error calling r.Scan(): %v", err))
	Expect(cnum).To(Equal(expected))
}

func expectModTime(modPath string, prevModTime time.Time) time.Time {
	nextModTime := prevModTime

	// Get modPath modtime
	sts, err := mtm.GetPathStatus(fsnotifyStrategy, modPath)
	if err != nil {
		Fail(fmt.Sprintf("expectModTime: GetPathStatus(%s) err: %v", modPath, err))
	}
	log.Printf("expectModTime: GetPathStatus(%s): %v", modPath, sts)

	// Verify modPath modtime was updated after modPath was updated with new DSN
	nextModTime = sts.ModTime
	if !nextModTime.After(prevModTime) {
		Fail(fmt.Sprintf("expectModTime: %s: new sts.ModTime(%s) <= prevModTime(%s)", modPath, nextModTime, prevModTime))
	}

	return nextModTime
}

var _ = BeforeSuite(func(ctx context.Context) {
	// create tables and chairs
	hlt, err := sql.Open("postgres", hotloadTestDsn)
	hlt1, err := sql.Open("postgres", hotloadTest1Dsn)
	defer hlt.Close()
	defer hlt1.Close()

	for {
		time.Sleep(5 * time.Second)
		_, err = hlt.Exec(testSqlSetup)
		if err != nil {
			log.Printf("BeforeSuite: error creating test table in hlt: %v", err)
			continue
			//Fail(fmt.Sprintf())
		}

		_, err = hlt1.Exec(testSqlSetup)
		if err != nil {
			log.Printf("BeforeSuite: error creating test table in hlt1: %v", err)
			continue
		}

		break
	}

	// enable ModTimeMonitor to monitor configPath
	// (do NOT use the ginkgo supplied ctx parm,
	// as it will be canceled when BeforeSuite finishes)
	mtmCtx, mtmCancelCtxFn = context.WithCancel(context.Background())
	mtm = modtime.NewModTimeMonitor(mtmCtx,
		// note the check-interval must be shorter than the sleep interval in setDSN()
		modtime.WithCheckInterval(100*time.Millisecond),
		modtime.WithLogger(testLogger),
	)
	mtm.AddMonitoredPath(fsnotifyStrategy, configPath)
	time.Sleep(200 * time.Millisecond)
}, NodeTimeout(240*time.Second))

var _ = AfterSuite(func(ctx context.Context) {
	log.Printf("AfterSuite canceling ModTimeMonitor context")
	mtmCancelCtxFn()
	time.Sleep(200 * time.Millisecond)

	hlt, err := sql.Open("postgres", hotloadTestDsn)
	hlt1, err := sql.Open("postgres", hotloadTest1Dsn)
	defer hlt.Close()
	defer hlt1.Close()

	_, err = hlt.Exec(testSqlTeardown)
	if err != nil {
		log.Printf("AfterSuite: error dropping test table in hlt: %v", err)
	}

	_, err = hlt1.Exec(testSqlTeardown)
	if err != nil {
		log.Printf("AfterSuite: error dropping test table in hlt1: %v", err)
	}
}, NodeTimeout(240*time.Second))

var _ = Describe("hotload integration tests - sanity", Serial, func() {
	var (
		db     *sql.DB
		hltDb  *sql.DB
		hlt1Db *sql.DB
	)

	BeforeEach(func(ctx context.Context) {
		setDSN(hotloadTestDsn, configPath)
		hltDb = openDbPostgres(hotloadTestDsn)
		hlt1Db = openDbPostgres(hotloadTest1Dsn)

		_, err := hltDb.Exec(truncSqlSetup)
		if err != nil {
			Fail(fmt.Sprintf("BeforeEach: error truncating test table in hltDb: %v", err))
		}

		_, err = hlt1Db.Exec(truncSqlSetup)
		if err != nil {
			Fail(fmt.Sprintf("BeforeEach: error truncating test table in hlt1Db: %v", err))
		}

		db = openDbHotload(false)
	})

	It("should connect to new db when file changes", func(ctx context.Context) {
		var prevModTime time.Time
		for i := 0; i < 2; i++ {
			// Verify configPath modtime was updated after configPath was updated with new DSN
			prevModTime = expectModTime(configPath, prevModTime)

			r, err := db.Exec(fmt.Sprintf("INSERT INTO test (cnum, csource) VALUES (%d, 'runtime')", i))
			if err != nil {
				Fail(fmt.Sprintf("error inserting cnum=%d row: %v", i, err))
			} else {
				log.Printf("inserted cnum=%d row", i)
			}
			log.Print(r)

			// Set new DSN, note that this sleeps for 250 millisecs
			setDSN(hotloadTest1Dsn, configPath)
		}
		expectValueInDb(hltDb, 0)
		expectValueInDb(hlt1Db, 1)
	})
})
