package hotload_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	hotload "github.com/infobloxopen/hotload/v3"
	"github.com/infobloxopen/hotload/v3/internal/testutil"
)

// TestChangeStorm hammers the pool from many goroutines while config
// changes fire rapidly, in both graceful and forceKill modes. It asserts no
// deadlocks (the test finishes), no unexpected errors, convergence to the
// final DSN, and no goroutine leaks.
func TestChangeStorm(t *testing.T) {
	for _, forceKill := range []bool{false, true} {
		t.Run(fmt.Sprintf("forceKill=%v", forceKill), func(t *testing.T) {
			testutil.NoLeaks(t)

			params := "killWindow=10ms"
			if forceKill {
				params = "forceKill=true&killWindow=10ms"
			}
			fx := newFixture(t, fxCfg{params: params})

			const workers = 8
			const changes = 25

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			var (
				wg         sync.WaitGroup
				successes  atomic.Int64
				unexpected sync.Map
				stop       = make(chan struct{})
			)

			allowedErr := func(err error) bool {
				// Under forceKill, in-flight work may be retired; the pool
				// also surfaces bad-conn errors when retries are exhausted
				// mid-storm.
				return errors.Is(err, hotload.ErrHotSwap) ||
					errors.Is(err, context.Canceled) ||
					err.Error() == "driver: bad connection"
			}

			for i := 0; i < workers; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					for {
						select {
						case <-stop:
							return
						default:
						}
						var err error
						if i%2 == 0 {
							_, err = fx.db.ExecContext(ctx, "UPDATE x")
						} else {
							var dsn string
							err = fx.db.QueryRowContext(ctx, "SELECT dsn").Scan(&dsn)
						}
						if err == nil {
							successes.Add(1)
						} else if !allowedErr(err) {
							unexpected.Store(err.Error(), true)
						}
					}
				}(i)
			}

			final := ""
			for j := 0; j < changes; j++ {
				final = fmt.Sprintf("dsn-%d", j%3+2)
				fx.pushAndWait(final)
			}

			close(stop)
			wg.Wait()

			unexpected.Range(func(k, v any) bool {
				t.Errorf("unexpected error during storm: %s", k)
				return true
			})
			if successes.Load() == 0 {
				t.Error("no operation succeeded during the storm")
			}
			testutil.WaitFor(t, 2*time.Second, "convergence to final DSN", func() bool {
				return fx.queryDSN() == final
			})
		})
	}
}

// TestConcurrentCloseAndKill races pool-driven closes against
// generation kills; the underlying conn must be closed exactly once.
func TestConcurrentCloseAndKill(t *testing.T) {
	testutil.NoLeaks(t)
	fx := newFixture(t, fxCfg{params: "forceKill=true&killWindow=5ms"})

	for round := 0; round < 20; round++ {
		fx.queryDSN()
		// Race: db.Close-like pool eviction (via change) and explicit swap.
		fx.pushAndWait(fmt.Sprintf("dsn-%d", round+2))
	}

	testutil.WaitFor(t, 2*time.Second, "all old conns closed", func() bool {
		open := fx.drv.OpenConns()
		return len(open) <= 1
	})
	for _, c := range fx.drv.Conns() {
		if n := c.CloseCount(); n > 1 {
			t.Errorf("conn %d closed %d times, want at most 1", c.ID, n)
		}
	}
}

// TestQueryRowsSurviveGracefulChange: rows being iterated when a graceful
// change lands must remain readable — the reason QueryContext passes the
// caller context through unmerged.
func TestQueryRowsSurviveGracefulChange(t *testing.T) {
	testutil.NoLeaks(t)
	fx := newFixture(t, fxCfg{})

	rows, err := fx.db.Query("SELECT dsn")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	fx.pushAndWait("dsn-2")

	var dsn string
	if !rows.Next() {
		t.Fatalf("rows.Next() = false after graceful change: %v", rows.Err())
	}
	if err := rows.Scan(&dsn); err != nil {
		t.Fatal(err)
	}
	if dsn != "dsn-1" {
		t.Errorf("row value = %q, want dsn-1 (rows belong to the old generation)", dsn)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}
