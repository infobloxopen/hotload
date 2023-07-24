package hotload_test

import (
	"database/sql"
	"database/sql/driver"
	"os"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/infobloxopen/hotload"
	"github.com/infobloxopen/hotload/fsnotify"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func getDriverFromSqlMock() driver.Driver {
	littleBuddy, mock, _ := sqlmock.NewWithDSN("user=pqgotest dbname=pqgotest sslmode=verify-full")
	mockDriver = mock
	return littleBuddy.Driver()
}

func getRandomDriver() driver.Driver {
	db, _, _ := sqlmock.New()
	return db.Driver()
}

var mockDriver sqlmock.Sqlmock
var configFile string
var configFileDir string

var _ = Describe("Driver", func() {
	BeforeSuite(func() {
		driver := getDriverFromSqlMock()

		if driver == nil {
			Fail("driver is nil, boo!")
		}

		hotload.RegisterSQLDriver("sqlmock", driver)
		Expect(hotload.SQLDrivers()).To(ContainElement("sqlmock"))
		var err error
		configFile, err = os.Getwd()
		Expect(err).ToNot(HaveOccurred())
		configFileDir = configFile + "/testdata/"
		configFile += "/testdata/myconfig.txt"
	})

	Context("RegisterSQLDriver", func() {
		It("Should panic when registering the same driver twice", func() {
			driver := getRandomDriver()
			Expect(func() { hotload.RegisterSQLDriver("sqlmock", driver) }).
				To(Panic())
		})

		It("Should panic on nil driver", func() {
			Expect(func() { hotload.RegisterSQLDriver("", nil) }).
				To(Panic())
		})
	})

	Context("RegisterStrategy", func() {
		It("Should panic when registering the same strategy twice", func() {
			strat := fsnotify.NewStrategy()
			Expect(func() { hotload.RegisterStrategy("fsnotify", strat) }).
				To(Panic())
		})

		It("Should panic on nil driver", func() {
			Expect(func() { hotload.RegisterStrategy("", nil) }).
				To(Panic())
		})
	})

	Context("Open", func() {
		It("Should throw an error with unknown driver", func() {
			db, err := sql.Open("hotload", "fsnotify://sqlmaybe?"+configFile)
			Expect(err).ToNot(HaveOccurred())
			err = db.Ping()
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError(hotload.ErrUnknownDriver))

		})

		It("Should not throw an error with a registered driver and strategy", func() {
			db, err := sql.Open("hotload", "fsnotify://sqlmock"+configFile)
			Expect(err).ToNot(HaveOccurred())

			Expect(db.Ping()).ToNot(HaveOccurred())
		})

		It("Should throw an unsupported strategy error", func() {
			db, err := sql.Open("hotload", "fstransmogrify://sqlmock/"+configFile)
			err = db.Ping()
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError(hotload.ErrUnsupportedStrategy))
		})

		It("Should throw an error if it can't find the config file", func() {
			db, err := sql.Open("hotload", "fsnotify://sqlmock/temple/run/2021-edition")
			err = db.Ping()
			Expect(err).To(HaveOccurred())
		})

		It("Should throw an error the url is unparseable", func() {
			db, err := sql.Open("hotload", "://")
			err = db.Ping()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("missing protocol scheme"))
		})

		//It("Should close my connection when the connection information changes", func() {
		//	db, err := sql.Open("hotload", "fsnotify://sqlmock"+configFileDir+"urconfig.txt")
		//	Expect(err).ToNot(HaveOccurred())
		//
		//	Expect(db.Ping()).ToNot(HaveOccurred())
		//	// Open dat
		//	// Do a thing
		//	// change connection file
		//	mockDriver.ExpectBegin()
		//	mockDriver.ExpectExec("SELECT 1")
		//	mockDriver.ExpectCommit()
		//	tx, err := db.Begin()
		//	Expect(err).ToNot(HaveOccurred())
		//	tx.Exec("SELECT 1")
		//	err = ioutil.WriteFile(configFileDir+"urconfig.txt", []byte("user=pqgotest dbname=pqgotestorooni sslmode=verify-full"), 0644)
		//	Expect(err).ToNot(HaveOccurred())
		//	go func () {
		//		tx.Commit()
		//		Expect(mockDriver.ExpectationsWereMet()).ToNot(HaveOccurred())
		//	}()
		//})
	})
})
