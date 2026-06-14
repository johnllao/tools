package main

import (
	"fmt"
	"net/http"
	"net/url"
)

// Router holds the compiled routing rules and provides a method to resolve
// an incoming request to an upstream URL by matching the x-service-name and
// x-environment headers against the rule set.
//
// Router is immutable after creation — the route slice is never modified. This
// makes it safe for concurrent reads from multiple request goroutines without
// any additional synchronization.
type Router struct {
	routes []Route
}

// NewRouter creates a Router from a slice of Route rules. The rules are
// evaluated in order: the first matching rule wins. A nil or empty slice
// means no route will ever match, and Resolve will always return an error
// (resulting in a 502 response for every request).
func NewRouter(routes []Route) *Router {
	return &Router{routes: routes}
}

// Resolve determines the upstream URL for the given request by matching the
// x-service-name and x-environment headers against the routing rules.
//
// Matching rules:
//   - "*" in a route field matches any value, including missing/empty headers.
//   - Any other value must match the header exactly (case-sensitive).
//   - Rules are evaluated in order; the first matching rule wins.
//
// The returned URL includes the original request path and query string
// appended to the upstream base URL. Returns an error if no rule matched.
func (rt *Router) Resolve(r *http.Request) (*url.URL, error) {
	service := r.Header.Get("x-service-name")
	environment := r.Header.Get("x-environment")

	for i := range rt.routes {
		route := &rt.routes[i]

		// Check service match: wildcard "*" matches anything; otherwise
		// the header value must match exactly.
		if route.Service != "*" && route.Service != service {
			continue
		}

		// Check environment match: wildcard "*" matches anything; otherwise
		// the header value must match exactly.
		if route.Environment != "*" && route.Environment != environment {
			continue
		}

		// All conditions met — we have a winner. Parse the upstream URL,
		// then attach the original request path and query.
		upstream, err := url.Parse(route.Upstream)
		if err != nil {
			// This should never happen because LoadConfig validates every
			// upstream URL at startup. If we reach this point, there is a
			// programming error.
			return nil, fmt.Errorf("invalid upstream URL %q: %w", route.Upstream, err)
		}

		upstream.Path = r.URL.Path
		upstream.RawQuery = r.URL.RawQuery
		return upstream, nil
	}

	// No rule matched any combination of the request headers.
	return nil, fmt.Errorf("no route matched service=%q environment=%q", service, environment)
}
