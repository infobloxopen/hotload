// Package promtest provides helpers for asserting prometheus metric output
// against regexp patterns. Hotload v1 shipped these in its internal
// package; they live here so the hotload core stays free of prometheus
// dependencies.
package promtest

import (
	"errors"
	"fmt"
	"io"
	"regexp"
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

// SimpleRegexpLineDiff performs a simple/dumb line-by-line diff
// between two arrays of lines. The expected array of lines are regexp
// patterns. Returns line(s) which diff. Empty string is returned if there
// are no diffs.
func SimpleRegexpLineDiff(regexpLines []string, gotLines []string) string {
	maxLen := max(len(regexpLines), len(gotLines))

	for len(regexpLines) < maxLen {
		regexpLines = append(regexpLines, "")
	}
	for len(gotLines) < maxLen {
		gotLines = append(gotLines, "")
	}

	var diffBuf strings.Builder
	for k := 0; k < maxLen; k++ {
		expStr := strings.TrimSpace(regexpLines[k])
		gotStr := strings.TrimSpace(gotLines[k])
		expPat := `^` + expStr + `$`

		matched, err := regexp.MatchString(expPat, gotStr)
		if err != nil {
			return err.Error()
		}

		if !matched {
			fmt.Fprintf(&diffBuf, "-%s\n", expStr)
			fmt.Fprintf(&diffBuf, "+%s\n", gotStr)
		}
	}

	return diffBuf.String()
}
