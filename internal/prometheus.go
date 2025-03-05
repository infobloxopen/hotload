package internal

import (
	"errors"
	"io"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/prometheus/common/expfmt"
)

// CollectAndRegexpCompare is similar to testutil.CollectAndCompare()
// but the expected lines are regexp patterns.
// Note that unlike testutil.CollectAndCompare(),
// the metricName MUST be specified to get any collected result.
func CollectAndRegexpCompare(colltor prometheus.Collector, expectRdr io.Reader, metricNames ...string) error {
	expectBytes, err := io.ReadAll(expectRdr)
	if err != nil {
		return err
	}

	collectBytes, err := testutil.CollectAndFormat(colltor, expfmt.TypeTextPlain, metricNames...)
	if err != nil {
		return err
	}

	expectStr := strings.TrimSpace(string(expectBytes))
	collectStr := strings.TrimSpace(string(collectBytes))

	expectSplit := strings.Split(expectStr, "\n")
	collectSplit := strings.Split(collectStr, "\n")

	diffStr := strings.TrimSpace(SimpleRegexpLineDiff(expectSplit, collectSplit))
	if len(diffStr) > 0 {
		return errors.New(diffStr)
	}
	return nil
}
