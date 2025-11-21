package observability_test

import (
"testing"
"time"

"github.com/infobloxopen/hotload"
_ "github.com/infobloxopen/hotload/observability"
)

func TestPrometheusRecorderRegistered(t *testing.T) {
	recorder := hotload.GetMetricsRecorder()
	if recorder == nil {
		t.Fatal("expected metrics recorder to be set when observability is imported")
	}

	recorder.RecordEpochTransition("test-conn", 1)
	recorder.RecordConnectionCount("test-conn", 1, 5)
	recorder.RecordOldEpochConnectionCount("test-conn", 1, 3)
	recorder.RecordEpochDrainTime("test-conn", 1, 2*time.Second)
	recorder.RecordPreparedStatementReprepare("test-conn", 2)
	recorder.RecordTransactionResult("test-conn", 1, true, false)
	recorder.RecordDSNUpdatePropagation("test-conn", time.Now(), time.Now().Add(100*time.Millisecond))
	recorder.RecordQueryResult("test-conn", true, "normal")
	recorder.RecordOldEpochDiscard("test-conn")

	t.Log("All metrics recorded successfully")
}
