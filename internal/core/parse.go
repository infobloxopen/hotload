package core

import (
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// ParsedDSN represents a parsed hotload DSN.
type ParsedDSN struct {
	Strategy     string
	TargetDriver string
	Path         string
	Query        url.Values

	// Parsed policy options
	DrainTimeout         time.Duration
	ForceKill            bool
	Debounce             time.Duration
	Preconnect           bool
	CredentialOnlyReload bool
}

// Parse parses a hotload DSN: <strategy>://<driver>/<path>?opts
func Parse(dsn string) (*ParsedDSN, error) {
	uri, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("invalid hotload DSN: %w", err)
	}

	if uri.Scheme == "" {
		return nil, fmt.Errorf("missing strategy scheme in DSN")
	}

	if uri.Host == "" {
		return nil, fmt.Errorf("missing target driver in DSN")
	}

	path := uri.Path
	if len(path) > 0 && path[0] == '/' {
		path = path[1:] // Strip leading slash
	}

	parsed := &ParsedDSN{
		Strategy:     uri.Scheme,
		TargetDriver: uri.Host,
		Path:         path,
		Query:        uri.Query(),
	}

	// Parse policy options from query
	if err := parsed.parsePolicy(); err != nil {
		return nil, err
	}

	return parsed, nil
}

func (p *ParsedDSN) parsePolicy() error {
	// Parse drainTimeout
	if s := p.Query.Get("drainTimeout"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("invalid drainTimeout: %w", err)
		}
		p.DrainTimeout = d
	} else {
		p.DrainTimeout = 30 * time.Second // default
	}

	// Parse forceKill
	if s := p.Query.Get("forceKill"); s != "" {
		b, err := strconv.ParseBool(s)
		if err != nil {
			return fmt.Errorf("invalid forceKill: %w", err)
		}
		p.ForceKill = b
	} else {
		p.ForceKill = true // default to true for safety
	}

	// Parse debounce
	if s := p.Query.Get("debounce"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("invalid debounce: %w", err)
		}
		p.Debounce = d
	} else {
		p.Debounce = 250 * time.Millisecond // default
	}

	// Parse preconnect
	if s := p.Query.Get("preconnect"); s != "" {
		b, err := strconv.ParseBool(s)
		if err != nil {
			return fmt.Errorf("invalid preconnect: %w", err)
		}
		p.Preconnect = b
	}

	// Parse credentialOnlyReload
	if s := p.Query.Get("credentialOnlyReload"); s != "" {
		b, err := strconv.ParseBool(s)
		if err != nil {
			return fmt.Errorf("invalid credentialOnlyReload: %w", err)
		}
		p.CredentialOnlyReload = b
	}

	return nil
}
