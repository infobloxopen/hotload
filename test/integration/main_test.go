package integration

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

var (
	testDSN           string
	postgresContainer *PostgresContainer
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	testDSN = os.Getenv("PG_DSN")
	if testDSN == "" {
		fmt.Println("PG_DSN not set, attempting to start PostgreSQL container...")

		var err error
		postgresContainer, err = StartPostgresContainer(ctx)
		if err != nil {
			fmt.Printf("Failed to start PostgreSQL container: %v\n", err)
			fmt.Println("Skipping integration tests (Docker not available)")
			os.Exit(0)
		}

		testDSN = postgresContainer.DSN
		fmt.Printf("PostgreSQL container started: %s\n", testDSN)
	} else {
		fmt.Printf("Using provided PostgreSQL DSN: %s\n", testDSN)
	}

	code := m.Run()

	if postgresContainer != nil {
		fmt.Println("Terminating PostgreSQL container...")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := postgresContainer.Terminate(ctx); err != nil {
			fmt.Printf("Failed to terminate container: %v\n", err)
		}
	}

	os.Exit(code)
}
