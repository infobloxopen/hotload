package hotload_test

import (
	"database/sql"
	"database/sql/driver"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/infobloxopen/hotload"
	"github.com/infobloxopen/hotload/fsnotify"
	_ "github.com/infobloxopen/hotload/fsnotify"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func getDriverFromSqlMock () driver.Driver {
	littleBuddy, _, _ := sqlmock.NewWithDSN("user=pqgotest dbname=pqgotest sslmode=verify-full")
	return littleBuddy.Driver()
}

func getRandomDriver () driver.Driver {
	db, _, _ := sqlmock.New()
	return db.Driver()
}

var _ = Describe("Driver", func() {
	BeforeSuite(func() {
		driver := getDriverFromSqlMock()

		if driver == nil {
			Fail("driver is nil, boo!")
		}

		hotload.RegisterSQLDriver("sqlmock", driver)
		Expect(hotload.SQLDrivers()).To(ContainElement("sqlmock"))
	})

	Context("RegisterSQLDriver", func() {
		It("Should panic when registering the same driver twice", func() {
			driver := getRandomDriver()
			Expect(func () { hotload.RegisterSQLDriver("sqlmock",  driver) }).
				To(PanicWith(MatchRegexp("Register called twice for driver")))
		})

		It("Should panic on nil driver", func() {
			Expect(func () { hotload.RegisterSQLDriver("", nil) } ).
				To(PanicWith(MatchRegexp("Register driver is nil")))
		})
	})

	Context("RegisterStrategy", func() {
		It("Should panic when registering the same strategy twice", func() {
			strat := fsnotify.NewStrategy()
			Expect(func () { hotload.RegisterStrategy("fsnotify",  strat) }).
				To(PanicWith(MatchRegexp("RegisterStrategy called twice for strategy")))
		})

		It("Should panic on nil driver", func() {
			Expect(func () { hotload.RegisterStrategy("", nil) } ).
				To(PanicWith(MatchRegexp("strategy is nil")))
		})
	})

	Context("Open", func() {
		It("Should throw an error with unknown driver", func() {
			db, err := sql.Open("hotload", "fsnotify://sqlmaybe?/tmp/myconfig.txt")
			Expect(err).ToNot(HaveOccurred())
			err = db.Ping()
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError(hotload.ErrUnknownDriver))

		})

		It("Should not throw an error with a registered driver and strategy", func() {
			db, err := sql.Open("hotload", "fsnotify://sqlmock/tmp/myconfig.txt")
			Expect(err).ToNot(HaveOccurred())

			Expect(db.Ping()).ToNot(HaveOccurred())
		})

		It("Should throw an unsupported strategy error", func() {
			db, err := sql.Open("hotload", "fstransmogrify://sqlmock/tmp/myconfig.txt")
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
	})
})
