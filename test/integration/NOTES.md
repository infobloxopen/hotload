# Integration Test Development Notes

## Completed

✅ Created separate Go module at `test/integration/`
✅ Set up dependencies: lib/pq v1.10.9, pgx/v5 v5.5.0, testcontainers-go v0.40.0
✅ **Automatic PostgreSQL container management with testcontainers-go**
✅ Created lib/pq integration tests (3 tests)
✅ Created pgx integration tests (4 tests)
✅ Fixed driver registration to use direct driver instances
✅ All code compiles successfully
✅ Created comprehensive README with setup instructions
✅ Container lifecycle managed in TestMain

## Container Support

### Key Features

- **Zero Configuration**: Tests start PostgreSQL automatically if Docker is available
- **Uses Official Image**: postgres:16-alpine (lightweight, fast pulls)
- **Random Port Mapping**: Avoids conflicts with existing databases
- **Automatic Cleanup**: Container removed after tests complete
- **Graceful Degradation**: Tests skip if Docker unavailable
- **Override Support**: Set PG_DSN to use your own database

### Implementation

**docker.go**
- `StartPostgresContainer()` - Creates and starts postgres container
- `WaitForPostgres()` - Polls until database accepts connections
- `PostgresContainer.Terminate()` - Cleanup

**libpq_test.go (TestMain)**
1. Check PG_DSN environment variable
2. If not set, start postgres:16-alpine container
3. Run all tests
4. Terminate container (if started)

### Container Configuration

```go
Image:        "postgres:16-alpine"
ExposedPorts: []string{"5432/tcp"}
Env:
  POSTGRES_USER:     "testuser"
  POSTGRES_PASSWORD: "testpass"
  POSTGRES_DB:       "testdb"
WaitingFor:
  - Log: "database system is ready to accept connections" (occurrence: 2)
  - ListeningPort: "5432/tcp"
  - Timeout: 60 seconds
```

## Test Structure

### libpq_test.go
- `TestMain()` - Container lifecycle management + shared testDSN variable
- `TestLibPQ_BasicConnection` - Basic connectivity test
- `TestLibPQ_DSNReload` - Tests dynamic DSN reloading
- `TestLibPQ_ConnectionPooling` - Tests connection pooling behavior

### pgx_test.go
- Uses same testDSN variable from TestMain
- `TestPGX_BasicConnection` - Basic connectivity test
- `TestPGX_PreparedStatements` - Tests prepared statements with pgx
- `TestPGX_Transactions` - Tests transaction handling
- `TestPGX_NativeTypes` - Tests PostgreSQL native types (arrays, etc.)

### docker.go
- `PostgresContainer` struct - Wraps testcontainers.Container
- `StartPostgresContainer()` - Full container lifecycle
- `WaitForPostgres()` - Connection readiness polling

## Running Tests

### Local Development (Automatic)
```bash
cd test/integration
go test -v ./...
# Container starts automatically if Docker available
```

### Local Development (Manual DB)
```bash
export PG_DSN="host=localhost port=5432 user=postgres password=postgres dbname=testdb sslmode=disable"
cd test/integration
go test -v ./...
```

### CI/CD (Automatic)
```yaml
- name: Run Integration Tests
  working-directory: test/integration
  run: go test -v -timeout 5m ./...
```

**No services block needed** - testcontainers handles everything!

### With Coverage
```bash
cd test/integration
go test -v -cover -coverpkg=github.com/infobloxopen/hotload ./...
```

## Known Behaviors

### Container Startup Time
- First run: ~10-30 seconds (image pull + startup)
- Subsequent runs: ~3-5 seconds (cached image)

### Port Allocation
- testcontainers uses random available ports
- Prevents conflicts with existing PostgreSQL instances
- DSN contains actual mapped port (e.g., `localhost:54321`)

### Cleanup
- Container automatically removed after tests
- Even on test failures or panics
- Manual cleanup: `docker rm -f $(docker ps -aq --filter ancestor=postgres:16-alpine)`

### Docker Requirements
- Docker daemon must be running
- User must have Docker permissions
- Rootless Docker supported
- If unavailable: tests skip with informative message

## Known Linter Warnings

The integration tests have several linter warnings that are acceptable for test code:

1. **Function naming** (TestLibPQ_*, TestPGX_*): Using underscores for clarity
2. **Duplicate string literals**: Acceptable in test code for readability
3. **Unused imports**: All cleaned up

These warnings don't affect functionality and maintain test readability.

## Future Improvements

### Additional Tests
- [ ] Test connection failure scenarios
- [ ] Test concurrent DSN updates
- [ ] Test malformed DSN handling
- [ ] Test connection recovery after database restart
- [ ] Test with different PostgreSQL versions

### Additional Drivers
- [ ] MySQL driver (github.com/go-sql-driver/mysql)
- [ ] SQLite driver (github.com/mattn/go-sqlite3)
- [ ] SQL Server driver (github.com/denisenkom/go-mssqldb)

### Test Utilities
- [ ] Helper function to create temporary config files
- [ ] Helper function to register drivers (reduce duplication)
- [ ] Shared test fixtures for common scenarios
- [ ] Docker Compose file for local testing

### CI/CD
- [ ] GitHub Actions workflow for integration tests
- [ ] Docker-based PostgreSQL service
- [ ] Coverage reporting to Codecov
- [ ] Badge in main README

## Module Structure

```
test/integration/
├── .gitignore          # Ignores test binaries and temp files
├── README.md           # User-facing documentation
├── NOTES.md            # This file - development notes
├── go.mod              # Separate module with replace directive
├── go.sum              # Locked dependencies
├── libpq_test.go       # lib/pq driver tests
└── pgx_test.go         # pgx driver tests
```

## Design Decisions

### Separate Module
- Keeps main module dependency tree clean
- Allows testing with different driver versions
- Optional - users can run unit tests without database drivers

### Driver Registration Pattern
- lib/pq: `&pq.Driver{}` - Direct instantiation
- pgx: `stdlib.GetDefaultDriver()` - Uses pgx's recommended approach
- Both work with hotload's registration mechanism

### Test Isolation
- Each test creates its own temp config file
- Tests can run in parallel (with -parallel flag)
- Uses testing.Short() to allow skipping expensive tests
- Checks PG_DSN environment variable before running

### Coverage
- Use `-coverpkg=github.com/infobloxopen/hotload` to measure parent module
- Integration tests exercise real-world scenarios
- Can identify gaps that unit tests miss
