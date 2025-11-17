package dsnutil

import (
	"net/url"
	"sort"
	"strings"
)

// DSNComponents represents the parsed components of a DSN.
type DSNComponents struct {
	// Structural components - changes require connection drain
	Scheme   string
	Host     string
	Port     string
	Database string
	Options  string

	// Credential components - changes can be handled without drain
	Username string
	Password string
}

// Parse parses a database DSN into its structural and credential components.
func Parse(dsn string) (*DSNComponents, error) {
	if strings.Contains(dsn, "://") {
		return parseURL(dsn)
	}
	return parseKeyValue(dsn)
}

// parseURL parses URL-style DSNs
func parseURL(dsn string) (*DSNComponents, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return &DSNComponents{}, nil
	}

	comp := &DSNComponents{
		Scheme:   u.Scheme,
		Host:     u.Hostname(),
		Port:     u.Port(),
		Database: strings.TrimPrefix(u.Path, "/"),
	}

	if u.User != nil {
		comp.Username = u.User.Username()
		if pass, ok := u.User.Password(); ok {
			comp.Password = pass
		}
	}

	if u.RawQuery != "" {
		comp.Options = normalizeOptions(u.Query())
	}

	return comp, nil
}

// parseKeyValue parses PostgreSQL key=value DSNs
func parseKeyValue(dsn string) (*DSNComponents, error) {
	comp := &DSNComponents{}

	pairs := strings.Fields(dsn)
	structuralParts := []string{}

	for _, pair := range pairs {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			continue
		}

		key := strings.TrimSpace(kv[0])
		value := strings.TrimSpace(kv[1])

		switch key {
		case "host", "hostname":
			comp.Host = value
		case "port":
			comp.Port = value
		case "dbname", "database":
			comp.Database = value
		case "user", "username":
			comp.Username = value
		case "password", "pass":
			comp.Password = value
		default:
			structuralParts = append(structuralParts, pair)
		}
	}

	sort.Strings(structuralParts)
	comp.Options = strings.Join(structuralParts, " ")

	return comp, nil
}

// normalizeOptions sorts query parameters
func normalizeOptions(query url.Values) string {
	filtered := url.Values{}
	for k, v := range query {
		lk := strings.ToLower(k)
		if lk != "user" && lk != "username" && lk != "password" && lk != "pass" {
			filtered[k] = v
		}
	}

	keys := make([]string, 0, len(filtered))
	for k := range filtered {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := []string{}
	for _, k := range keys {
		for _, v := range filtered[k] {
			parts = append(parts, k+"="+v)
		}
	}

	return strings.Join(parts, "&")
}

// IsCredentialOnlyChange returns true if only username/password changed
func IsCredentialOnlyChange(oldDSN, newDSN string) bool {
	old, err := Parse(oldDSN)
	if err != nil {
		return false
	}

	new, err := Parse(newDSN)
	if err != nil {
		return false
	}

	structuralMatch := old.Scheme == new.Scheme &&
		old.Host == new.Host &&
		old.Port == new.Port &&
		old.Database == new.Database &&
		old.Options == new.Options

	if !structuralMatch {
		return false
	}

	credentialsChanged := old.Username != new.Username || old.Password != new.Password

	return credentialsChanged
}

// IsStructuralChange returns true if scheme/host/port/database/options changed
func IsStructuralChange(oldDSN, newDSN string) bool {
	old, err := Parse(oldDSN)
	if err != nil {
		return true
	}

	new, err := Parse(newDSN)
	if err != nil {
		return true
	}

	return old.Scheme != new.Scheme ||
		old.Host != new.Host ||
		old.Port != new.Port ||
		old.Database != new.Database ||
		old.Options != new.Options
}
