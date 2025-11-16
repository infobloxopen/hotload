package internal

import (
	"fmt"
	"net/url"
	"testing"
)

func TestRedactUrl(t *testing.T) {
	testcases := []struct {
		inputDsn      string
		expectUser    string
		expectPassLen int
		expectHost    string
		expectPath    string
	}{
		{
			inputDsn:      "qwerty",
			expectUser:    "u---r",
			expectPassLen: 8,
			expectHost:    "qwerty",
		},
		{
			inputDsn:      "mysql://u:p@amazon.rds.com:5432/contacts",
			expectUser:    "u---u",
			expectPassLen: 8,
			expectHost:    "amazon.rds.com:5432",
			expectPath:    "/contacts",
		},
		{
			inputDsn:      "postgresql://admin:test@localhost:5432/hotload_test?sslmode=disable",
			expectUser:    "a---n",
			expectPassLen: 8,
			expectHost:    "localhost:5432",
			expectPath:    "/hotload_test",
		},
	}

	for _, tt := range testcases {
		t.Run(tt.inputDsn, func(t *testing.T) {
			gotDsn := RedactUrl(tt.inputDsn)

			// Parse the redacted URL
			uri, err := url.Parse(gotDsn)
			if err != nil {
				t.Fatalf("Failed to parse redacted URL: %v", err)
			}

			// Check username is redacted correctly
			gotUser := uri.User.Username()
			if gotUser != tt.expectUser {
				t.Errorf("expected user='%s' got='%s'", tt.expectUser, gotUser)
			}

			// Check password is redacted and has correct length (8 hex chars)
			gotPass, hasPass := uri.User.Password()
			if !hasPass {
				t.Error("expected password to be present")
			}
			if len(gotPass) != tt.expectPassLen {
				t.Errorf("expected password length=%d got=%d", tt.expectPassLen, len(gotPass))
			}
			// Verify it's a hex string
			for _, c := range gotPass {
				if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
					t.Errorf("password contains non-hex character: %c", c)
				}
			}

			// Check host if specified
			if tt.expectHost != "" && uri.Host != tt.expectHost {
				t.Errorf("expected host='%s' got='%s'", tt.expectHost, uri.Host)
			}

			// Check path if specified
			if tt.expectPath != "" && uri.Path != tt.expectPath {
				t.Errorf("expected path='%s' got='%s'", tt.expectPath, uri.Path)
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
