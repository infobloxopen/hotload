package hotload

import (
	"context"
	"database/sql/driver"
	"fmt"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/infobloxopen/hotload/v3/internal/dbfake"
)

// testStrategy is a minimal in-package fake Strategy. The richer fake in
// the external test package cannot be used here (it imports hotload), and
// dbfake cannot host one (the Strategy interface names hotload.Watchable).
type testStrategy struct {
	mu      sync.Mutex
	initial map[string]string
	watches int
}

func newTestStrategy(initial map[string]string) *testStrategy {
	return &testStrategy{initial: initial}
}

func (s *testStrategy) Watch(ctx context.Context, pth string, pathQry string) (string, Watchable, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.initial[pth]
	if !ok {
		return "", nil, fmt.Errorf("testStrategy: no initial value for path %q", pth)
	}
	s.watches++
	return value, &testWatch{strat: s, ch: make(chan string)}, nil
}

func (s *testStrategy) Watches() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.watches
}

type testWatch struct {
	strat  *testStrategy
	ch     chan string
	closed bool // guarded by strat.mu
}

func (w *testWatch) Values() <-chan string { return w.ch }

func (w *testWatch) Close() error {
	w.strat.mu.Lock()
	defer w.strat.mu.Unlock()
	if !w.closed {
		w.closed = true
		close(w.ch)
		w.strat.watches--
	}
	return nil
}

func Test_mergeConnStringOptions(t *testing.T) {
	tests := []struct {
		name    string
		dsn     string
		options map[string]string
		want    string
		wantErr bool
	}{
		{
			name: "empty",
			want: "",
		},
		{
			name: "bad dsn with no options",
			dsn:  "bad dsn",
			want: "bad dsn",
		},
		{
			name:    "bad dsn with options",
			dsn:     "bad dsn",
			options: map[string]string{"a": "b"},
			wantErr: true,
		},
		{
			name: "good dsn with no options",
			dsn:  "postgres://localhost:5432/postgres?sslmode=disable",
			want: "postgres://localhost:5432/postgres?sslmode=disable",
		},
		{
			name:    "good dsn with options",
			dsn:     "postgres://localhost:5432/postgres?sslmode=disable",
			options: map[string]string{"disable_cache": "true"},
			want:    "postgres://localhost:5432/postgres?disable_cache=true&sslmode=disable",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mergeConnStringOptions(tt.dsn, tt.options)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func Test_parseGroupParams(t *testing.T) {
	tests := []struct {
		name           string
		query          string
		wantForceKill  bool
		wantKillWindow time.Duration
		wantErr        bool
	}{
		{name: "defaults", query: "", wantKillWindow: DefaultKillWindow},
		{name: "forceKill true", query: "forceKill=true", wantForceKill: true, wantKillWindow: DefaultKillWindow},
		{name: "forceKill false", query: "forceKill=false", wantKillWindow: DefaultKillWindow},
		{name: "forceKill garbage", query: "forceKill=yes", wantKillWindow: DefaultKillWindow},
		{name: "killWindow", query: "killWindow=250ms", wantKillWindow: 250 * time.Millisecond},
		{name: "killWindow invalid", query: "killWindow=bogus", wantErr: true},
		{name: "both", query: "forceKill=true&killWindow=1s", wantForceKill: true, wantKillWindow: time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs, err := url.ParseQuery(tt.query)
			if err != nil {
				t.Fatal(err)
			}
			forceKill, killWindow, err := parseGroupParams(vs)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if forceKill != tt.wantForceKill {
				t.Errorf("forceKill = %v, want %v", forceKill, tt.wantForceKill)
			}
			if killWindow != tt.wantKillWindow {
				t.Errorf("killWindow = %v, want %v", killWindow, tt.wantKillWindow)
			}
		})
	}
}

func TestRegisterSQLDriverWithOptions(t *testing.T) {
	name := "internal-test-driver-options"
	RegisterSQLDriver(name, &dbfake.Driver{}, WithDriverOptions(map[string]string{"a": "b"}))

	mu.RLock()
	defer mu.RUnlock()
	di, ok := sqlDrivers[name]
	if !ok {
		t.Fatal("RegisterSQLDriver did not register the driver")
	}
	if di.options["a"] != "b" {
		t.Errorf("options = %v, want a=b", di.options)
	}
}

func TestRegisterPanics(t *testing.T) {
	mustPanic := func(name string, fn func()) {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("expected panic")
				}
			}()
			fn()
		})
	}

	mustPanic("nil driver", func() { RegisterSQLDriver("internal-test-nil", nil) })
	mustPanic("dup driver", func() {
		RegisterSQLDriver("internal-test-dup", &dbfake.Driver{})
		RegisterSQLDriver("internal-test-dup", &dbfake.Driver{})
	})
	mustPanic("nil strategy", func() { RegisterStrategy("internal-test-nilstrat", nil) })
	mustPanic("dup strategy", func() {
		s := newTestStrategy(nil)
		RegisterStrategy("internal-test-dupstrat", s)
		defer UnregisterStrategy("internal-test-dupstrat")
		RegisterStrategy("internal-test-dupstrat", s)
	})
}

func TestRegistryLists(t *testing.T) {
	RegisterSQLDriver("internal-test-list-b", &dbfake.Driver{})
	RegisterSQLDriver("internal-test-list-a", &dbfake.Driver{})
	RegisterStrategy("internal-test-list-strat", newTestStrategy(nil))
	defer UnregisterStrategy("internal-test-list-strat")

	drivers := SQLDrivers()
	prev := ""
	seen := 0
	for _, d := range drivers {
		if d < prev {
			t.Errorf("SQLDrivers not sorted: %v", drivers)
		}
		prev = d
		if d == "internal-test-list-a" || d == "internal-test-list-b" {
			seen++
		}
	}
	if seen != 2 {
		t.Errorf("registered drivers missing from SQLDrivers(): %v", drivers)
	}

	found := false
	for _, s := range Strategies() {
		if s == "internal-test-list-strat" {
			found = true
		}
	}
	if !found {
		t.Errorf("registered strategy missing from Strategies(): %v", Strategies())
	}
}

// TestLegacyOpenPath drives the non-connector driver.Open path directly
// against a private hdriver instance (the path database/sql no longer uses,
// but third-party pools might). The group is pinned, so teardown happens via
// the parent context.
func TestLegacyOpenPath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newHdriver(ctx)
	drv := &dbfake.Driver{Caps: dbfake.CapsModern}
	strat := newTestStrategy(map[string]string{"/cfg": "dsn-1"})
	RegisterSQLDriver("internal-test-legacy-drv", drv)
	RegisterStrategy("internal-test-legacy-strat", strat)
	defer UnregisterStrategy("internal-test-legacy-strat")

	name := "internal-test-legacy-strat://internal-test-legacy-drv/cfg"
	conn, err := h.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, ok := conn.(driver.QueryerContext); !ok {
		t.Error("legacy-opened conn should expose QueryerContext for a modern underlying conn")
	}

	// A second open shares the group (one watch).
	conn2, err := h.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close()
	if n := strat.Watches(); n != 1 {
		t.Errorf("watches = %d, want 1", n)
	}
}

// TestEmitHelpersNilSafe ensures hooks with nil fields are skipped.
func TestEmitHelpersNilSafe(t *testing.T) {
	resetHooks()
	defer resetHooks()
	RegisterHooks(Hooks{}) // all nil

	emitConfigChange(ConfigChangeEvent{})
	emitConnOpen(ConnEvent{})
	emitConnClose(ConnEvent{})
	emitTxComplete(TxEvent{})
	EmitWatchEvent(WatchEvent{})
	EmitModTimeEvent(ModTimeEvent{})
}

func ExampleContextWithExecLabels() {
	ctx := ContextWithExecLabels(context.Background(), map[string]string{
		"grpc_service": "ContactsService",
		"grpc_method":  "ListContacts",
	})
	labels := GetExecLabelsFromContext(ctx)
	fmt.Println(labels["grpc_service"], labels["grpc_method"])
	// Output: ContactsService ListContacts
}
