package internal

import (
	"net/url"
)

// RedactUrl redacts the user/pass components of the url
func RedactUrl(dsnUrl string) string {
	redactStr := "---"
	uri, err := url.Parse(dsnUrl)
	if err != nil {
		return dsnUrl
	}

	username := uri.User.Username()
	if len(username) <= 0 {
		username = "user"
	}
	redactUser := username[0:1] + redactStr + username[len(username)-1:]

	password, _ := uri.User.Password()
	if len(password) <= 0 {
		password = "password"
	}
	redactPass := password[0:1] + redactStr + password[len(password)-1:]

	uri.User = url.UserPassword(redactUser, redactPass)
	return uri.String()
}
