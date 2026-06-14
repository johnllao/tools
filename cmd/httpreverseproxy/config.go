// Package main provides the httpreverseproxy binary — an HTTP reverse proxy that
// forwards incoming requests to upstream services based on routing rules defined
// in a JSON configuration file.
package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"time"
)

// Config is the top-level configuration structure loaded from the JSON file.
type Config struct {
	// Listen is the address (host:port) the proxy listens on, e.g. ":9090".
	Listen string `json:"listen"`

	// ReadTimeout is the maximum duration for reading the entire request,
	// including the body. Defaults to 30s.
	ReadTimeout time.Duration `json:"read_timeout"`

	// WriteTimeout is the maximum duration before timing out writes of the
	// response. Defaults to 30s.
	WriteTimeout time.Duration `json:"write_timeout"`

	// MaxIdleConns is the maximum number of idle (keep-alive) connections to
	// maintain per upstream host. Defaults to 100.
	MaxIdleConns int `json:"max_idle_conns"`

	// Routes is the ordered list of routing rules. The first matching rule
	// determines the upstream for a request.
	Routes []Route `json:"routes"`
}

// Route is a single routing rule that maps a (service, environment) tuple
// to an upstream server URL.
type Route struct {
	// Service is the exact value to match against the x-service-name header.
	// Use "*" to match any value (including a missing header).
	Service string `json:"service"`

	// Environment is the exact value to match against the x-environment header.
	// Use "*" to match any value (including a missing header).
	Environment string `json:"environment"`

	// Upstream is the upstream base URL to forward matching requests to.
	// Must include a scheme (http/https). The original request path is
	// appended as-is (e.g., a request to /users becomes <upstream>/users).
	Upstream string `json:"upstream"`
}

// rawConfig is an intermediate type used for JSON unmarshalling. Duration
// fields are parsed from strings like "30s" rather than JSON numbers.
type rawConfig struct {
	Listen       string  `json:"listen"`
	ReadTimeout  string  `json:"read_timeout"`
	WriteTimeout string  `json:"write_timeout"`
	MaxIdleConns int     `json:"max_idle_conns"`
	Routes       []Route `json:"routes"`
}

// LoadConfig reads, parses, and validates the JSON configuration file at the
// given path. It returns a fully initialized Config with defaults applied for
// any optional fields that were omitted.
//
// Validation is performed at startup (fail-fast) so that configuration errors
// are surfaced before the proxy starts accepting traffic.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: cannot read %s: %s", path, err)
	}

	var raw rawConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("config: invalid JSON in %s: %s", path, err)
	}

	cfg := &Config{
		Listen:       raw.Listen,
		MaxIdleConns: raw.MaxIdleConns,
		Routes:       raw.Routes,
	}

	// Parse the read_timeout string into a time.Duration. Default: 30s.
	if raw.ReadTimeout != "" {
		cfg.ReadTimeout, err = time.ParseDuration(raw.ReadTimeout)
		if err != nil {
			return nil, fmt.Errorf("config: invalid read_timeout %q: %s", raw.ReadTimeout, err)
		}
	} else {
		cfg.ReadTimeout = 30 * time.Second
	}

	// Parse the write_timeout string into a time.Duration. Default: 30s.
	if raw.WriteTimeout != "" {
		cfg.WriteTimeout, err = time.ParseDuration(raw.WriteTimeout)
		if err != nil {
			return nil, fmt.Errorf("config: invalid write_timeout %q: %s", raw.WriteTimeout, err)
		}
	} else {
		cfg.WriteTimeout = 30 * time.Second
	}

	// Apply the default for max_idle_conns when omitted or set to zero.
	if cfg.MaxIdleConns == 0 {
		cfg.MaxIdleConns = 100
	}

	// --- Validation -------------------------------------------------------------

	// "listen" is required — without it, the proxy has no port to bind to.
	if cfg.Listen == "" {
		return nil, fmt.Errorf("config: \"listen\" is required")
	}

	// Validate each route entry.
	for i, route := range cfg.Routes {
		// "upstream" is required.
		if route.Upstream == "" {
			return nil, fmt.Errorf("config: route[%d]: \"upstream\" is required", i)
		}

		// Upstream URL must have a scheme (http or https).
		u, parseErr := url.Parse(route.Upstream)
		if parseErr != nil || u.Scheme == "" {
			return nil, fmt.Errorf("config: route[%d]: \"upstream\" must have a scheme (http/https)", i)
		}

		// Upstream URL must have a host.
		if u.Host == "" {
			return nil, fmt.Errorf("config: route[%d]: \"upstream\" must have a host", i)
		}

		// At least one of "service" or "environment" must be non-empty.
		if route.Service == "" && route.Environment == "" {
			return nil, fmt.Errorf("config: route[%d]: at least one of \"service\" or \"environment\" must be set", i)
		}
	}

	return cfg, nil
}
