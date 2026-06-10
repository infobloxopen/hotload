package hotload_test

import (
	"database/sql/driver"
	"fmt"
	"testing"

	"github.com/infobloxopen/hotload/v3/internal/dbfake"
)

// TestConnInterfaceTruthfulness verifies the heart of the wrapper design:
// the conn hotload hands to database/sql implements an optional interface if
// and only if the underlying conn supports the capability. It walks every
// combination of the mirrored capabilities plus the legacy-interface
// variants and asserts presence and absence of each interface.
func TestConnInterfaceTruthfulness(t *testing.T) {
	// The four mirrored axes.
	axes := []dbfake.Caps{
		dbfake.CapExecerContext,
		dbfake.CapQueryerContext,
		dbfake.CapPinger,
		dbfake.CapNamedValueChecker,
	}

	var combos []dbfake.Caps
	for mask := 0; mask < 1<<len(axes); mask++ {
		var caps dbfake.Caps
		for i, axis := range axes {
			if mask&(1<<i) != 0 {
				caps |= axis
			}
		}
		combos = append(combos, caps)
	}
	// Legacy-interface variants: the wrapper must expose the context flavor
	// when the underlying conn only has the legacy one, and must never
	// expose the legacy interfaces themselves.
	combos = append(combos,
		dbfake.CapExecer,
		dbfake.CapQueryer,
		dbfake.CapExecer|dbfake.CapQueryer,
		dbfake.CapExecer|dbfake.CapExecerContext,
		dbfake.CapQueryer|dbfake.CapQueryerContext,
		dbfake.CapsModern,
		dbfake.CapsAll,
	)

	for _, caps := range combos {
		t.Run(fmt.Sprintf("caps=%b", caps), func(t *testing.T) {
			fx := newFixture(t, fxCfg{caps: caps, rawCaps: true})
			fx.rawConn(func(dc driver.Conn) {
				assertIface(t, "ExecerContext", isA[driver.ExecerContext](dc),
					caps&(dbfake.CapExecer|dbfake.CapExecerContext) != 0)
				assertIface(t, "QueryerContext", isA[driver.QueryerContext](dc),
					caps&(dbfake.CapQueryer|dbfake.CapQueryerContext) != 0)
				assertIface(t, "Pinger", isA[driver.Pinger](dc),
					caps&dbfake.CapPinger != 0)
				assertIface(t, "NamedValueChecker", isA[driver.NamedValueChecker](dc),
					caps&dbfake.CapNamedValueChecker != 0)

				// The legacy interfaces are never exposed; their context
				// flavors stand in for them.
				assertIface(t, "Execer (legacy)", isA[driver.Execer](dc), false)
				assertIface(t, "Queryer (legacy)", isA[driver.Queryer](dc), false)

				// The synthesized set is always present: hotload replicates
				// database/sql's fallback exactly when the underlying conn
				// lacks these, so presence is behaviorally indistinguishable.
				assertIface(t, "ConnPrepareContext", isA[driver.ConnPrepareContext](dc), true)
				assertIface(t, "ConnBeginTx", isA[driver.ConnBeginTx](dc), true)
				assertIface(t, "SessionResetter", isA[driver.SessionResetter](dc), true)
				assertIface(t, "Validator", isA[driver.Validator](dc), true)
			})
		})
	}
}

// TestStmtInterfaceTruthfulness mirrors the conn matrix for prepared
// statements: hotload wraps driver.Stmt (it did not in v1) and must mirror
// the four optional stmt interfaces truthfully.
func TestStmtInterfaceTruthfulness(t *testing.T) {
	axes := []dbfake.Caps{
		dbfake.CapStmtExecContext,
		dbfake.CapStmtQueryContext,
		dbfake.CapColumnConverter,
		dbfake.CapStmtNamedValueChecker,
	}

	for mask := 0; mask < 1<<len(axes); mask++ {
		var caps dbfake.Caps
		for i, axis := range axes {
			if mask&(1<<i) != 0 {
				caps |= axis
			}
		}
		t.Run(fmt.Sprintf("caps=%b", caps), func(t *testing.T) {
			fx := newFixture(t, fxCfg{caps: caps, rawCaps: true})
			fx.rawConn(func(dc driver.Conn) {
				stmt, err := dc.Prepare("SELECT dsn")
				if err != nil {
					t.Fatalf("Prepare: %v", err)
				}
				defer stmt.Close()

				assertIface(t, "StmtExecContext", isA[driver.StmtExecContext](stmt),
					caps&dbfake.CapStmtExecContext != 0)
				assertIface(t, "StmtQueryContext", isA[driver.StmtQueryContext](stmt),
					caps&dbfake.CapStmtQueryContext != 0)
				assertIface(t, "ColumnConverter", isA[driver.ColumnConverter](stmt),
					caps&dbfake.CapColumnConverter != 0)
				assertIface(t, "NamedValueChecker", isA[driver.NamedValueChecker](stmt),
					caps&dbfake.CapStmtNamedValueChecker != 0)
			})
		})
	}
}

func isA[T any](v any) bool {
	_, ok := v.(T)
	return ok
}

func assertIface(t *testing.T, name string, got, want bool) {
	t.Helper()
	if got != want {
		if want {
			t.Errorf("wrapper does not implement %s but the underlying conn supports it", name)
		} else {
			t.Errorf("wrapper implements %s but the underlying conn does not support it", name)
		}
	}
}
