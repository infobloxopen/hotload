package internal

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestRedactUrl(t *testing.T) {
	testcases := []struct {
		inputDsn string
		// expectPattern matches the redacted DSN; the password is replaced
		// with a random token, asserted separately.
		expectPattern string
	}{
		{
			inputDsn:      "qwerty",
			expectPattern: `^//u---r:[0-9a-f]{8}@qwerty$`,
		},
		{
			inputDsn:      "mysql://u:p@amazon.rds.com:5432/contacts",
			expectPattern: `^mysql://u---u:[0-9a-f]{8}@amazon\.rds\.com:5432/contacts$`,
		},
		{
			inputDsn:      "postgresql://admin:test@localhost:5432/hotload_test?sslmode=disable",
			expectPattern: `^postgresql://a---n:[0-9a-f]{8}@localhost:5432/hotload_test\?sslmode=disable$`,
		},
	}

	for _, tt := range testcases {
		t.Run(tt.inputDsn, func(t *testing.T) {
			gotDsn := RedactUrl(tt.inputDsn)
			re := regexp.MustCompile(tt.expectPattern)
			if !re.MatchString(gotDsn) {
				t.Errorf("RedactUrl(%q) = %q, want match for %q", tt.inputDsn, gotDsn, tt.expectPattern)
			}
			if strings.Contains(gotDsn, ":test@") {
				t.Errorf("RedactUrl(%q) = %q leaked the password", tt.inputDsn, gotDsn)
			}
		})
	}
}

// TestRedactUrlStablePassword verifies that the same password maps to the
// same random token across calls, so log lines remain correlatable.
func TestRedactUrlStablePassword(t *testing.T) {
	first := RedactUrl("postgresql://admin:hunter2@localhost/db")
	second := RedactUrl("postgresql://admin:hunter2@localhost/db")
	if first != second {
		t.Errorf("redaction not stable for identical input: %q vs %q", first, second)
	}

	other := RedactUrl("postgresql://admin:different@localhost/db")
	if other == first {
		t.Errorf("different passwords redacted to the same DSN: %q", other)
	}
}

func TestUrlEncodedQueryParams(t *testing.T) {
	testcases := []struct {
		inputParams   url.Values
		expectEncoded string
	}{
		{
			inputParams:   nil,
			expectEncoded: ``,
		},
		{
			inputParams:   url.Values{},
			expectEncoded: ``,
		},
		{
			inputParams: url.Values{
				"forceKill": nil,
			},
			expectEncoded: ``,
		},
		{
			inputParams: url.Values{
				"forceKill": []string{},
			},
			expectEncoded: ``,
		},
		{
			inputParams: url.Values{
				"forceKill": []string{"true"},
			},
			expectEncoded: `forceKill=true`,
		},
		{
			inputParams: url.Values{
				"forceKill": []string{"true"},
				"id":        []string{"1", "2"},
			},
			expectEncoded: `forceKill=true&id=1&id=2`,
		},
	}

	for ti, tt := range testcases {
		t.Run(fmt.Sprintf("%d", ti), func(t *testing.T) {
			gotEncoded := tt.inputParams.Encode()
			if gotEncoded != tt.expectEncoded {
				t.Errorf("expectEncoded='%s' gotEncoded='%s'", tt.expectEncoded, gotEncoded)
			}
		})
	}
}
