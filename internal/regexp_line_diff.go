package internal

import (
	"fmt"
	"regexp"
	"strings"
)

// SimpleRegexpLineDiff performs a simple/dumb line-by-line diff
// between two arrays of lines.  The expected array of lines are regexp patterns.
// Returns line(s) which diff.  Empty string is returned if there are no diffs.
func SimpleRegexpLineDiff(regexpLines []string, gotLines []string) string {
	maxLen := len(regexpLines)
	if maxLen < len(gotLines) {
		maxLen = len(gotLines)
	}

	if len(regexpLines) < maxLen {
		for k := len(regexpLines); k < maxLen; k++ {
			regexpLines = append(regexpLines, "")
		}
	}

	if len(gotLines) < maxLen {
		for k := len(gotLines); k < maxLen; k++ {
			gotLines = append(gotLines, "")
		}
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
