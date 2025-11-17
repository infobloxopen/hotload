package integration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/infobloxopen/hotload"
	_ "github.com/infobloxopen/hotload/strategy/fsnotify"
	gormv1 "github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/postgres"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestRecord is the model for testing GORM operations
type TestRecord struct {
	ID        uint      `gorm:"primaryKey"`
	Data      string    `gorm:"type:varchar(255)"`
	CreatedBy string    `gorm:"type:varchar(100)"` // PostgreSQL user who created the record
	CreatedAt time.Time `gorm:"autoCreateTime"`
}

// TableName returns the table name for this model
func (TestRecord) TableName() string {
	return "test_records"
}

// TestGORM2_HotReloadDuringContinuousWrites tests GORM v2 behavior during DSN hot reload.
//
// Scenario:
//  1. AutoMigrate schema with GORM
//  2. Start multiple goroutines continuously writing records
//  3. While writes are in progress, change DSN from user1 to user2
//  4. Measure connection acquisition time after DSN change
//  5. Verify all goroutines eventually pick up new credentials
//  6. Verify transactions complete successfully during the transition
//
// This validates that GORM v2 works correctly with hotload's connection draining,
// and that writes continue without errors during credential rotation.
func TestGORM2_HotReloadDuringContinuousWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available")
	}

	drivers := GetPostgresDriverConfigs(t)
	for _, driverCfg := range drivers {
		driverCfg := driverCfg
		t.Run(driverCfg.Name, func(t *testing.T) {
			testGORM2HotReloadDuringContinuousWrites(t, driverCfg)
		})
	}
}

func testGORM2HotReloadDuringContinuousWrites(t *testing.T, driverCfg DriverConfig) {
	// Register driver with unique name
	driverName := fmt.Sprintf("%s-gorm2-%d", driverCfg.Name, time.Now().UnixNano())
	hotload.RegisterSQLDriver(driverName, driverCfg.Driver)

	// Create a second user for DSN switching
	testUser2 := fmt.Sprintf("gormuser2_%s_%d", driverCfg.Name, time.Now().UnixNano()%10000)
	testPass2 := "gormpass2"

	db, err := sql.Open("postgres", testDSN)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Create user2 with same privileges
	_, err = db.Exec(fmt.Sprintf("DROP USER IF EXISTS %s", testUser2))
	if err != nil {
		t.Logf("Warning: failed to drop user2: %v", err)
	}

	_, err = db.Exec(fmt.Sprintf("CREATE USER %s WITH PASSWORD '%s'", testUser2, testPass2))
	if err != nil {
		t.Fatalf("Failed to create user2: %v", err)
	}
	defer db.Exec(fmt.Sprintf("DROP USER IF EXISTS %s", testUser2))

	_, err = db.Exec(fmt.Sprintf("GRANT ALL PRIVILEGES ON DATABASE testdb TO %s", testUser2))
	if err != nil {
		t.Fatalf("Failed to grant database privileges to user2: %v", err)
	}

	// Grant schema privileges (GORM needs to create tables)
	_, err = db.Exec(fmt.Sprintf("GRANT ALL PRIVILEGES ON SCHEMA public TO %s", testUser2))
	if err != nil {
		t.Fatalf("Failed to grant schema privileges to user2: %v", err)
	}

	// Create temp file for DSN with user1
	tmpFile, err := os.CreateTemp("", "hotload-gorm2-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write initial DSN (user1)
	dsn1 := testDSN
	if _, err := tmpFile.WriteString(dsn1); err != nil {
		t.Fatalf("Failed to write DSN: %v", err)
	}
	tmpFile.Close()

	// Open hotload connection
	hotloadDSN := fmt.Sprintf("fsnotify://%s/%s?drainTimeout=5s&forceKill=false", driverName, tmpFile.Name())
	sqlDB, err := sql.Open("hotload", hotloadDSN)
	if err != nil {
		t.Fatalf("Failed to open hotload database: %v", err)
	}
	defer sqlDB.Close()

	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(5)

	// Clean up any existing test table from previous runs
	_, _ = db.Exec("DROP TABLE IF EXISTS test_records CASCADE")

	// Initialize GORM v2
	gormDB, err := gorm.Open(postgres.New(postgres.Config{
		Conn: sqlDB,
	}), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent), // Reduce noise in tests
	})
	if err != nil {
		t.Fatalf("Failed to initialize GORM: %v", err)
	}

	// AutoMigrate schema
	err = gormDB.AutoMigrate(&TestRecord{})
	if err != nil {
		t.Fatalf("Failed to auto-migrate: %v", err)
	}

	t.Log("✓ GORM schema auto-migrated")

	// Grant table privileges to user2 (for tables created by user1/testuser)
	_, err = db.Exec(fmt.Sprintf("GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO %s", testUser2))
	if err != nil {
		t.Fatalf("Failed to grant table privileges to user2: %v", err)
	}
	_, err = db.Exec(fmt.Sprintf("GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO %s", testUser2))
	if err != nil {
		t.Fatalf("Failed to grant sequence privileges to user2: %v", err)
	}

	// Test metrics
	var (
		totalInserts      atomic.Int64
		insertsBeforeSwap atomic.Int64
		insertsAfterSwap  atomic.Int64
		user1Inserts      atomic.Int64
		user2Inserts      atomic.Int64
		errorCount        atomic.Int64
		swapped           atomic.Bool
	)

	// Start continuous write goroutines
	numWorkers := 5
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	startBarrier := make(chan struct{})

	t.Logf("🚀 Starting %d worker goroutines for continuous writes...", numWorkers)

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		workerID := i
		go func() {
			defer wg.Done()

			<-startBarrier // Wait for all goroutines to be ready

			for {
				select {
				case <-ctx.Done():
					return
				default:
					// Start transaction
					tx := gormDB.Begin()
					if tx.Error != nil {
						errorCount.Add(1)
						t.Logf("Worker %d: failed to begin transaction: %v", workerID, tx.Error)
						time.Sleep(10 * time.Millisecond)
						continue
					}

					// Get current database user
					var currentUser string
					err := tx.Raw("SELECT current_user").Scan(&currentUser).Error
					if err != nil {
						tx.Rollback()
						errorCount.Add(1)
						t.Logf("Worker %d: failed to get current user: %v", workerID, err)
						time.Sleep(10 * time.Millisecond)
						continue
					}

					// Create and save record
					record := TestRecord{
						Data:      fmt.Sprintf("worker-%d-record-%d", workerID, totalInserts.Add(1)),
						CreatedBy: currentUser,
					}

					err = tx.Create(&record).Error
					if err != nil {
						tx.Rollback()
						errorCount.Add(1)
						t.Logf("Worker %d: failed to create record: %v", workerID, err)
						time.Sleep(10 * time.Millisecond)
						continue
					}

					// Commit transaction
					err = tx.Commit().Error
					if err != nil {
						errorCount.Add(1)
						t.Logf("Worker %d: failed to commit: %v", workerID, err)
						time.Sleep(10 * time.Millisecond)
						continue
					}

					// Track metrics
					if swapped.Load() {
						insertsAfterSwap.Add(1)
					} else {
						insertsBeforeSwap.Add(1)
					}

					if currentUser == "testuser" {
						user1Inserts.Add(1)
					} else if currentUser == testUser2 {
						user2Inserts.Add(1)
					}

					// Small delay to avoid overwhelming the database
					time.Sleep(5 * time.Millisecond)
				}
			}
		}()
	}

	// Start all workers simultaneously
	close(startBarrier)

	// Let workers run for a bit with user1
	time.Sleep(2 * time.Second)

	insertsBeforeChange := totalInserts.Load()
	t.Logf("📊 Before DSN change: %d inserts completed", insertsBeforeChange)

	// Change DSN to user2
	t.Log("🔄 Changing DSN from testuser to " + testUser2 + "...")

	port := getPortFromDSN(testDSN)
	dsn2 := fmt.Sprintf("host=localhost port=%s user=%s password=%s dbname=testdb sslmode=disable", port, testUser2, testPass2)
	err = os.WriteFile(tmpFile.Name(), []byte(dsn2), 0644)
	if err != nil {
		t.Fatalf("Failed to update DSN file: %v", err)
	}

	swapped.Store(true)
	swapTime := time.Now()

	t.Log("⏱️  Measuring time for workers to pick up new credentials...")

	// Wait for all workers to pick up the change (or timeout)
	var firstUser2Seen time.Time
	var allWorkersConverged bool

	for i := 0; i < 100; i++ {
		time.Sleep(100 * time.Millisecond)

		u2Count := user2Inserts.Load()
		if u2Count > 0 && firstUser2Seen.IsZero() {
			firstUser2Seen = time.Now()
			timeSinceSwap := firstUser2Seen.Sub(swapTime)
			t.Logf("✓ First insert with %s after %v", testUser2, timeSinceSwap)
		}

		// Check if we have enough user2 inserts (good indicator all workers switched)
		if u2Count >= int64(numWorkers*5) {
			allWorkersConverged = true
			t.Logf("✓ All workers converged to %s after ~%v", testUser2, time.Since(swapTime))
			break
		}
	}

	// Let it run a bit more after convergence
	time.Sleep(2 * time.Second)

	// Stop workers
	cancel()
	wg.Wait()

	// Final metrics
	totalCount := totalInserts.Load()
	user1Count := user1Inserts.Load()
	user2Count := user2Inserts.Load()
	errCount := errorCount.Load()
	beforeSwap := insertsBeforeSwap.Load()
	afterSwap := insertsAfterSwap.Load()

	t.Log(strings.Repeat("=", 70))
	t.Log("📊 GORM v2 Hot Reload Test Results:")
	t.Log(strings.Repeat("=", 70))
	t.Logf("Total inserts attempted:    %d", totalCount)
	t.Logf("Inserts before DSN change:  %d", beforeSwap)
	t.Logf("Inserts after DSN change:   %d", afterSwap)
	t.Logf("Inserts by testuser:        %d", user1Count)
	t.Logf("Inserts by %s:        %d", testUser2, user2Count)
	t.Logf("Errors encountered:         %d", errCount)
	t.Log(strings.Repeat("=", 70))

	// Assertions
	if totalCount < 100 {
		t.Errorf("Expected at least 100 total inserts, got %d", totalCount)
	}

	if user1Count == 0 {
		t.Error("Expected some inserts with testuser before DSN change")
	}

	if user2Count == 0 {
		t.Error("Expected some inserts with user2 after DSN change")
	}

	if !allWorkersConverged {
		t.Errorf("Not all workers converged to %s within timeout", testUser2)
	}

	// Allow some errors during transition, but not too many
	errorRate := float64(errCount) / float64(totalCount)
	if errorRate > 0.05 { // Allow up to 5% error rate
		t.Errorf("Error rate too high: %.2f%% (%d/%d)", errorRate*100, errCount, totalCount)
	}

	// Verify records in database using direct connection
	var recordCount int64
	err = gormDB.Model(&TestRecord{}).Count(&recordCount).Error
	if err != nil {
		t.Fatalf("Failed to count records: %v", err)
	}

	expectedRecords := totalCount - errCount
	if recordCount != expectedRecords {
		t.Errorf("Expected %d records in database, found %d", expectedRecords, recordCount)
	}

	// Verify distribution of users
	var user1Records int64
	err = gormDB.Model(&TestRecord{}).Where("created_by = ?", "testuser").Count(&user1Records).Error
	if err != nil {
		t.Fatalf("Failed to count user1 records: %v", err)
	}

	var user2Records int64
	err = gormDB.Model(&TestRecord{}).Where("created_by = ?", testUser2).Count(&user2Records).Error
	if err != nil {
		t.Fatalf("Failed to count user2 records: %v", err)
	}

	t.Logf("✓ Database verification: %d total records (%d by testuser, %d by %s)",
		recordCount, user1Records, user2Records, testUser2)

	// Cleanup using admin connection (testuser has ownership)
	_, err = db.Exec("DROP TABLE IF EXISTS test_records CASCADE")
	if err != nil {
		t.Logf("Warning: failed to drop table: %v", err)
	}

	t.Log("✅ GORM v2 hot reload test passed!")
}

// TestGORM1_HotReloadDuringContinuousWrites tests GORM v1 behavior during DSN hot reload.
//
// Scenario:
//  1. AutoMigrate schema with GORM v1
//  2. Start multiple goroutines continuously writing records
//  3. While writes are in progress, change DSN from user1 to user2
//  4. Measure connection acquisition time after DSN change
//  5. Verify all goroutines eventually pick up new credentials
//  6. Verify transactions complete successfully during the transition
//
// This validates that GORM v1 (jinzhu/gorm) works correctly with hotload's
// connection draining during credential rotation.
func TestGORM1_HotReloadDuringContinuousWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if testDSN == "" {
		t.Skip("No PostgreSQL instance available")
	}

	drivers := GetPostgresDriverConfigs(t)
	for _, driverCfg := range drivers {
		driverCfg := driverCfg
		t.Run(driverCfg.Name, func(t *testing.T) {
			testGORM1HotReloadDuringContinuousWrites(t, driverCfg)
		})
	}
}

func testGORM1HotReloadDuringContinuousWrites(t *testing.T, driverCfg DriverConfig) {
	// Register driver with unique name
	driverName := fmt.Sprintf("%s-gorm1-%d", driverCfg.Name, time.Now().UnixNano())
	hotload.RegisterSQLDriver(driverName, driverCfg.Driver)

	// Create a second user for DSN switching
	testUser2 := fmt.Sprintf("gormv1user2_%s_%d", driverCfg.Name, time.Now().UnixNano()%10000)
	testPass2 := "gormv1pass2"

	db, err := sql.Open("postgres", testDSN)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Create user2 with same privileges
	_, err = db.Exec(fmt.Sprintf("DROP USER IF EXISTS %s", testUser2))
	if err != nil {
		t.Logf("Warning: failed to drop user2: %v", err)
	}

	_, err = db.Exec(fmt.Sprintf("CREATE USER %s WITH PASSWORD '%s'", testUser2, testPass2))
	if err != nil {
		t.Fatalf("Failed to create user2: %v", err)
	}
	defer db.Exec(fmt.Sprintf("DROP USER IF EXISTS %s", testUser2))

	_, err = db.Exec(fmt.Sprintf("GRANT ALL PRIVILEGES ON DATABASE testdb TO %s", testUser2))
	if err != nil {
		t.Fatalf("Failed to grant database privileges to user2: %v", err)
	}

	// Grant schema privileges (GORM needs to create tables)
	_, err = db.Exec(fmt.Sprintf("GRANT ALL PRIVILEGES ON SCHEMA public TO %s", testUser2))
	if err != nil {
		t.Fatalf("Failed to grant schema privileges to user2: %v", err)
	}

	// Create temp file for DSN with user1
	tmpFile, err := os.CreateTemp("", "hotload-gorm1-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write initial DSN (user1)
	dsn1 := testDSN
	if _, err := tmpFile.WriteString(dsn1); err != nil {
		t.Fatalf("Failed to write DSN: %v", err)
	}
	tmpFile.Close()

	// Open hotload connection
	hotloadDSN := fmt.Sprintf("fsnotify://%s/%s?drainTimeout=5s&forceKill=false", driverName, tmpFile.Name())
	sqlDB, err := sql.Open("hotload", hotloadDSN)
	if err != nil {
		t.Fatalf("Failed to open hotload database: %v", err)
	}
	defer sqlDB.Close()

	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(5)

	// Clean up any existing test table from previous runs
	_, _ = db.Exec("DROP TABLE IF EXISTS test_records CASCADE")

	// Initialize GORM v1 with the hotload connection
	gormDB, err := gormv1.Open("postgres", sqlDB)
	if err != nil {
		t.Fatalf("Failed to initialize GORM v1: %v", err)
	}
	defer gormDB.Close()

	// Disable logging for cleaner test output
	gormDB.LogMode(false)

	// AutoMigrate schema
	err = gormDB.AutoMigrate(&TestRecord{}).Error
	if err != nil {
		t.Fatalf("Failed to auto-migrate: %v", err)
	}

	t.Log("✓ GORM v1 schema auto-migrated")

	// Grant table privileges to user2
	_, err = db.Exec(fmt.Sprintf("GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO %s", testUser2))
	if err != nil {
		t.Fatalf("Failed to grant table privileges to user2: %v", err)
	}
	_, err = db.Exec(fmt.Sprintf("GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO %s", testUser2))
	if err != nil {
		t.Fatalf("Failed to grant sequence privileges to user2: %v", err)
	}

	// Test metrics
	var (
		totalInserts      atomic.Int64
		insertsBeforeSwap atomic.Int64
		insertsAfterSwap  atomic.Int64
		user1Inserts      atomic.Int64
		user2Inserts      atomic.Int64
		errorCount        atomic.Int64
		swapped           atomic.Bool
	)

	// Start continuous write goroutines
	numWorkers := 5
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	startBarrier := make(chan struct{})

	t.Logf("🚀 Starting %d worker goroutines for continuous writes...", numWorkers)

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		workerID := i
		go func() {
			defer wg.Done()

			<-startBarrier // Wait for all goroutines to be ready

			for {
				select {
				case <-ctx.Done():
					return
				default:
					// Start transaction
					tx := gormDB.Begin()
					if tx.Error != nil {
						errorCount.Add(1)
						t.Logf("Worker %d: failed to begin transaction: %v", workerID, tx.Error)
						time.Sleep(10 * time.Millisecond)
						continue
					}

					// Get current database user
					var currentUser string
					err := tx.Raw("SELECT current_user").Row().Scan(&currentUser)
					if err != nil {
						tx.Rollback()
						errorCount.Add(1)
						t.Logf("Worker %d: failed to get current user: %v", workerID, err)
						time.Sleep(10 * time.Millisecond)
						continue
					}

					// Create and save record
					record := TestRecord{
						Data:      fmt.Sprintf("worker-%d-record-%d", workerID, totalInserts.Add(1)),
						CreatedBy: currentUser,
					}

					err = tx.Create(&record).Error
					if err != nil {
						tx.Rollback()
						errorCount.Add(1)
						t.Logf("Worker %d: failed to create record: %v", workerID, err)
						time.Sleep(10 * time.Millisecond)
						continue
					}

					// Commit transaction
					err = tx.Commit().Error
					if err != nil {
						errorCount.Add(1)
						t.Logf("Worker %d: failed to commit: %v", workerID, err)
						time.Sleep(10 * time.Millisecond)
						continue
					}

					// Track metrics
					if swapped.Load() {
						insertsAfterSwap.Add(1)
					} else {
						insertsBeforeSwap.Add(1)
					}

					if currentUser == "testuser" {
						user1Inserts.Add(1)
					} else if currentUser == testUser2 {
						user2Inserts.Add(1)
					}

					// Small delay to avoid overwhelming the database
					time.Sleep(5 * time.Millisecond)
				}
			}
		}()
	}

	// Start all workers simultaneously
	close(startBarrier)

	// Let workers run for a bit with user1
	time.Sleep(2 * time.Second)

	insertsBeforeChange := totalInserts.Load()
	t.Logf("📊 Before DSN change: %d inserts completed", insertsBeforeChange)

	// Change DSN to user2
	t.Log("🔄 Changing DSN from testuser to " + testUser2 + "...")

	port := getPortFromDSN(testDSN)
	dsn2 := fmt.Sprintf("host=localhost port=%s user=%s password=%s dbname=testdb sslmode=disable", port, testUser2, testPass2)
	err = os.WriteFile(tmpFile.Name(), []byte(dsn2), 0644)
	if err != nil {
		t.Fatalf("Failed to update DSN file: %v", err)
	}

	swapped.Store(true)
	swapTime := time.Now()

	t.Log("⏱️  Measuring time for workers to pick up new credentials...")

	// Wait for all workers to pick up the change (or timeout)
	var firstUser2Seen time.Time
	var allWorkersConverged bool

	for i := 0; i < 100; i++ {
		time.Sleep(100 * time.Millisecond)

		u2Count := user2Inserts.Load()
		if u2Count > 0 && firstUser2Seen.IsZero() {
			firstUser2Seen = time.Now()
			timeSinceSwap := firstUser2Seen.Sub(swapTime)
			t.Logf("✓ First insert with %s after %v", testUser2, timeSinceSwap)
		}

		// Check if we have enough user2 inserts (good indicator all workers switched)
		if u2Count >= int64(numWorkers*5) {
			allWorkersConverged = true
			t.Logf("✓ All workers converged to %s after ~%v", testUser2, time.Since(swapTime))
			break
		}
	}

	// Let it run a bit more after convergence
	time.Sleep(2 * time.Second)

	// Stop workers
	cancel()
	wg.Wait()

	// Final metrics
	totalCount := totalInserts.Load()
	user1Count := user1Inserts.Load()
	user2Count := user2Inserts.Load()
	errCount := errorCount.Load()
	beforeSwap := insertsBeforeSwap.Load()
	afterSwap := insertsAfterSwap.Load()

	t.Log(strings.Repeat("=", 70))
	t.Log("📊 GORM v1 Hot Reload Test Results:")
	t.Log(strings.Repeat("=", 70))
	t.Logf("Total inserts attempted:    %d", totalCount)
	t.Logf("Inserts before DSN change:  %d", beforeSwap)
	t.Logf("Inserts after DSN change:   %d", afterSwap)
	t.Logf("Inserts by testuser:        %d", user1Count)
	t.Logf("Inserts by %s:        %d", testUser2, user2Count)
	t.Logf("Errors encountered:         %d", errCount)
	t.Log(strings.Repeat("=", 70))

	// Assertions
	if totalCount < 100 {
		t.Errorf("Expected at least 100 total inserts, got %d", totalCount)
	}

	if user1Count == 0 {
		t.Error("Expected some inserts with testuser before DSN change")
	}

	if user2Count == 0 {
		t.Error("Expected some inserts with user2 after DSN change")
	}

	if !allWorkersConverged {
		t.Errorf("Not all workers converged to %s within timeout", testUser2)
	}

	// Allow some errors during transition, but not too many
	errorRate := float64(errCount) / float64(totalCount)
	if errorRate > 0.05 { // Allow up to 5% error rate
		t.Errorf("Error rate too high: %.2f%% (%d/%d)", errorRate*100, errCount, totalCount)
	}

	// Verify records in database using direct connection
	var recordCount int64
	err = gormDB.Model(&TestRecord{}).Count(&recordCount).Error
	if err != nil {
		t.Fatalf("Failed to count records: %v", err)
	}

	expectedRecords := totalCount - errCount
	if recordCount != expectedRecords {
		t.Errorf("Expected %d records in database, found %d", expectedRecords, recordCount)
	}

	// Verify distribution of users
	var user1Records int64
	err = gormDB.Model(&TestRecord{}).Where("created_by = ?", "testuser").Count(&user1Records).Error
	if err != nil {
		t.Fatalf("Failed to count user1 records: %v", err)
	}

	var user2Records int64
	err = gormDB.Model(&TestRecord{}).Where("created_by = ?", testUser2).Count(&user2Records).Error
	if err != nil {
		t.Fatalf("Failed to count user2 records: %v", err)
	}

	t.Logf("✓ Database verification: %d total records (%d by testuser, %d by %s)",
		recordCount, user1Records, user2Records, testUser2)

	// Cleanup using admin connection
	_, err = db.Exec("DROP TABLE IF EXISTS test_records CASCADE")
	if err != nil {
		t.Logf("Warning: failed to drop table: %v", err)
	}

	t.Log("✅ GORM v1 hot reload test passed!")
}
