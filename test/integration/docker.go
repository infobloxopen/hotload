package integration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/lib/pq"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	postgresImage     = "postgres:16-alpine"
	postgresUser      = "testuser"
	postgresPassword  = "testpass"
	postgresDB        = "testdb"
	maxStartupRetries = 30
	startupRetryDelay = time.Second
)

// PostgresContainer wraps a testcontainers PostgreSQL instance
type PostgresContainer struct {
	container testcontainers.Container
	DSN       string
}

// StartPostgresContainer starts a PostgreSQL container for testing
func StartPostgresContainer(ctx context.Context) (*PostgresContainer, error) {
	// Help testcontainers find Docker on Rancher Desktop, Docker Desktop, etc.
	if os.Getenv("DOCKER_HOST") == "" {
		possibleSockets := []string{
			"unix:///var/run/docker.sock",
			"unix:///Users/" + os.Getenv("USER") + "/.docker/run/docker.sock",
			"unix:///Users/" + os.Getenv("USER") + "/.rd/docker.sock",
		}

		for _, socket := range possibleSockets {
			socketPath := socket[7:]
			if _, err := os.Stat(socketPath); err == nil {
				os.Setenv("DOCKER_HOST", socket)
				break
			}
		}
	}

	if os.Getenv("TESTCONTAINERS_RYUK_DISABLED") == "" {
		os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")
	}

	req := testcontainers.ContainerRequest{
		Image:        postgresImage,
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     postgresUser,
			"POSTGRES_PASSWORD": postgresPassword,
			"POSTGRES_DB":       postgresDB,
		},
		WaitingFor: wait.ForAll(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
			wait.ForListeningPort("5432/tcp"),
		),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	mappedPort, err := container.MappedPort(ctx, "5432")
	if err != nil {
		container.Terminate(ctx)
		return nil, fmt.Errorf("failed to get mapped port: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		container.Terminate(ctx)
		return nil, fmt.Errorf("failed to get container host: %w", err)
	}

	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, mappedPort.Port(), postgresUser, postgresPassword, postgresDB)

	if err := WaitForPostgres(dsn, 30*time.Second); err != nil {
		container.Terminate(ctx)
		return nil, fmt.Errorf("PostgreSQL did not become ready: %w", err)
	}

	return &PostgresContainer{
		container: container,
		DSN:       dsn,
	}, nil
}

// Terminate stops and removes the PostgreSQL container
func (pc *PostgresContainer) Terminate(ctx context.Context) error {
	if pc.container != nil {
		return pc.container.Terminate(ctx)
	}
	return nil
}

// WaitForPostgres waits for PostgreSQL to be ready to accept connections
func WaitForPostgres(dsn string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(startupRetryDelay)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for PostgreSQL to be ready")
		case <-ticker.C:
			db, err := sql.Open("postgres", dsn)
			if err != nil {
				continue
			}

			err = db.PingContext(ctx)
			db.Close()
			if err == nil {
				return nil
			}
		}
	}
}
