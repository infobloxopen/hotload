package internal

import (
	"fmt"
	"net/url"
	"testing"
)

func TestRedactUrl(t *testing.T) {
	testcases := []struct {
		inputDsn  string
		expectDsn string
	}{
		{
			inputDsn:  "qwerty",
			expectDsn: "//u---r:p---d@qwerty",
		},
		{
			inputDsn:  "mysql://u:p@amazon.rds.com:5432/contacts",
			expectDsn: "mysql://u---u:p---p@amazon.rds.com:5432/contacts",
		},
		{
			inputDsn:  "postgresql://admin:test@localhost:5432/hotload_test?sslmode=disable",
			expectDsn: "postgresql://a---n:t---t@localhost:5432/hotload_test?sslmode=disable",
		},
	}

	for _, tt := range testcases {
		t.Run(tt.inputDsn, func(t *testing.T) {
			gotDsn := RedactUrl(tt.inputDsn)
			if gotDsn != tt.expectDsn {
				t.Errorf("expectDsn='%s' gotDsn='%s'", tt.expectDsn, gotDsn)
			}
		})
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
