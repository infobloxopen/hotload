package integrationtests

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/infobloxopen/hotload"
	_ "github.com/infobloxopen/hotload/fsnotify"
	"github.com/infobloxopen/hotload/internal"
	"github.com/infobloxopen/hotload/metrics"
	"github.com/infobloxopen/hotload/modtime"
	"github.com/lib/pq"
	_ "github.com/lib/pq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	fsnotifyStrategy = "fsnotify"
	configPath       = "/tmp/hotload_integration_test_dsn_config.txt"
	userSqlSetup     = "CREATE USER uuser WITH PASSWORD 'ppass1'"
	userSqlTeardown  = "DROP USER IF EXISTS uuser"
	testSqlSetup     = "CREATE TABLE IF NOT EXISTS test (cnum INT, csource TEXT, csleep TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP)"
	testSqlTeardown  = "DROP TABLE IF EXISTS test"
	truncSqlSetup    = "TRUNCATE TABLE test"
	initSqlSetup     = "INSERT INTO test (cnum, csource) VALUES (161803, 'initial')"
	grantSqlSetup    = "GRANT ALL ON test TO PUBLIC"

	setDSNSleepDur = 100 * time.Millisecond
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

func formHotloadDsn(forceKill bool) string {
	dsnUrl := "fsnotify://postgres" + configPath
	if forceKill {
		dsnUrl = dsnUrl + "?forceKill=true"
	}
	return dsnUrl
}

func setDSN(dsn string, path string) {
	err := os.WriteFile(path, []byte(dsn), 0777)
	if err != nil {
		Fail(fmt.Sprintf("setDSN: error writing dsn file: %v", err))
	}
	log.Printf("setDSN: success writing '%s' to '%s'", dsn, path)

	// Yield thread to let switch over take place
	time.Sleep(setDSNSleepDur)
	log.Printf("setDSN: slept/yielded %s", setDSNSleepDur)
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
	dsnUrl := formHotloadDsn(forceKill)

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

func expectValueInDb(db *sql.DB, source string, expErr bool, expRowCount int, expVal int64) {
	GinkgoHelper()
	log.Printf("expectValueInDb: expErr=%v, expRowCount=%d, expVal=%d", expErr, expRowCount, expVal)
	r, err := db.Query(fmt.Sprintf("SELECT cnum FROM test WHERE csource = '%s'", source))
	var cnum int64
	if expErr {
		Expect(err).To(HaveOccurred(), fmt.Sprintf("expectValueInDb: expect error reading from table test"))
	} else {
		Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("expectValueInDb: error reading from table test: %v", err))
	}
	gotRowCount := 0
	if !r.Next() {
		Expect(r.Err()).ToNot(HaveOccurred(), fmt.Sprintf("expectValueInDb: cursor iteration err: %v", r.Err()))
	} else {
		gotRowCount = 1
		err = r.Scan(&cnum)
		Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("expectValueInDb: error calling r.Scan(): %v", err))
		Expect(cnum).To(Equal(expVal))
	}
	Expect(gotRowCount).To(Equal(expRowCount))
}

func expectRowCountInDb(db *sql.DB, source string, expErr bool, expRowCount int, expVal int64) {
	GinkgoHelper()
	log.Printf("expectRowCountInDb: expErr=%v, expRowCount=%d, expVal=%d", expErr, expRowCount, expVal)
	r, err := db.Query(fmt.Sprintf("SELECT COUNT(*) FROM test WHERE csource = '%s' AND cnum = %d", source, expVal))
	if expErr {
		Expect(err).To(HaveOccurred(), fmt.Sprintf("expectRowCountInDb: expect error reading from table test"))
	} else {
		Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("expectRowCountInDb: error reading from table test: %v", err))
	}
	if !r.Next() {
		Expect(r.Err()).ToNot(HaveOccurred(), fmt.Sprintf("expectRowCountInDb: cursor iteration err: %v", r.Err()))
	} else {
		var gotRowCount int
		err = r.Scan(&gotRowCount)
		Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("expectRowCountInDb: error calling r.Scan(): %v", err))
		Expect(gotRowCount).To(Equal(expRowCount))
	}
}

// TODO: expectConnCountInDb is not reliable, pg_stat_activity connections fluctuates
func expectConnCountInDb(db *sql.DB, expConnCount int) {
	GinkgoHelper()
	log.Printf("expectConnCountInDb: expConnCount=%d", expConnCount)
	r, err := db.Query(fmt.Sprintf("SELECT datname, usename, application_name, client_addr, state, backend_type, query FROM pg_stat_activity WHERE client_addr IS NOT NULL"))
	Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("expectConnCountInDb: error reading from table pg_stat_activity: %v", err))
	gotConnCount := 0
	for r.Next() {
		gotConnCount++
		var datname string
		var usename string
		var application_name string
		var client_addr string
		var state string
		var backend_type string
		var query string
		err = r.Scan(&datname, &usename, &application_name, &client_addr, &state, &backend_type, &query)
		Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("expectConnCountInDb: error calling r.Scan(): %v", err))
		log.Printf("pg_stat_activity: datname='%s', usename='%s', app_name='%s', client_addr='%s', state='%s', backend_type='%s', query='%s'",
			datname, usename, application_name, client_addr, state, backend_type, query)
	}
	Expect(r.Err()).ToNot(HaveOccurred(), fmt.Sprintf("expectConnCountInDb: cursor iteration err: %v", r.Err()))
	Expect(gotConnCount).To(Equal(expConnCount))
}

func expectModTime(modPath string, prevModTime time.Time) time.Time {
	GinkgoHelper()
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
		modtime.WithCheckInterval(setDSNSleepDur/3),
		modtime.WithLogger(testLogger),
		modtime.WithErrLogger(testLogger),
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

	//expectConnCountInDb(hlt, 3)

	// HotloadPathChksumChangeTotal metric should be incremented
	err = internal.CollectAndRegexpCompare(metrics.HotloadPathChksumChangeTotal,
		strings.NewReader(fmt.Sprintf(expectHotloadPathChksumChangeTotalMetricRegexp,
			"/tmp/hotload_integration_test_dsn_config.txt")),
		metrics.HotloadPathChksumChangeTotalName)
	Expect(err).ShouldNot(HaveOccurred())

	err = internal.CollectAndRegexpCompare(metrics.HotloadPathChksumTimestampSeconds,
		strings.NewReader(fmt.Sprintf(expectHotloadPathChksumTimestampSecondsMetricRegexp,
			"/tmp/hotload_integration_test_dsn_config.txt")),
		metrics.HotloadPathChksumTimestampSecondsName)
	Expect(err).ShouldNot(HaveOccurred())
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

		_, err = hltDb.Exec(initSqlSetup)
		if err != nil {
			Fail(fmt.Sprintf("BeforeEach: error initing test table in hltDb: %v", err))
		}

		_, err = hlt1Db.Exec(initSqlSetup)
		if err != nil {
			Fail(fmt.Sprintf("BeforeEach: error initing test table in hlt1Db: %v", err))
		}

		db = openDbHotload(false)
	})

	AfterEach(func(ctx context.Context) {
		hltDb.Close()
		hlt1Db.Close()
		db.Close()
	})

	It("should connect to new db when file changes", func(ctx context.Context) {
		var prevModTime time.Time
		for i := 0; i < 2; i++ {
			// Verify configPath modtime was updated after configPath was updated with new DSN
			prevModTime = expectModTime(configPath, prevModTime)

			r, err := db.Exec(fmt.Sprintf("INSERT INTO test (cnum, csource) VALUES (%d, 'sanity')", i))
			if err != nil {
				Fail(fmt.Sprintf("error inserting cnum=%d row: %v", i, err))
			} else {
				log.Printf("inserted cnum=%d row", i)
			}
			log.Print(r)

			// Set new DSN, note that this sleeps for 250 millisecs
			setDSN(hotloadTest1Dsn, configPath)
		}
		expectValueInDb(hltDb, "sanity", false, 1, 0)
		expectValueInDb(hlt1Db, "sanity", false, 1, 1)
	})
})

var expectHotloadPathChksumChangeTotalMetricRegexp = `
# HELP hotload_path_chksum_change_total Hotload path checksum change total by path
# TYPE hotload_path_chksum_change_total counter
hotload_path_chksum_change_total{path="%s"} 4\d
`

var expectHotloadPathChksumTimestampSecondsMetricRegexp = `
# HELP hotload_path_chksum_timestamp_seconds Hotload path checksum last changed \(unix timestamp\), by path
# TYPE hotload_path_chksum_timestamp_seconds gauge
hotload_path_chksum_timestamp_seconds{path="%s"} \d\.\d+e\+\d+
`
