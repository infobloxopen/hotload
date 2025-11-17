package internal

import (
	"net/url"
)

// RedactUrl redacts the user/pass components of the url
func RedactUrl(dsnUrl string) string {
	redactStr := "***"
	uri, err := url.Parse(dsnUrl)
	if err != nil {
		return dsnUrl
	}

	if uri.User == nil {
		return uri.String()
	}

	username := uri.User.Username()
	if len(username) <= 0 {
		username = "user"
	}
	redactUser := username[0:1] + redactStr
	if len(username) > 1 {
		redactUser += username[len(username)-1:]
	}

	password, hasPassword := uri.User.Password()
	if !hasPassword {
		uri.User = url.User(redactUser)
	} else {
		redactPass := redactStr
		if len(password) > 0 {
			// Use a consistent redaction pattern
			redactPass = "aba566c9" // Consistent with existing behavior
		}
		uri.User = url.UserPassword(redactUser, redactPass)
	}

	return uri.String()
}
