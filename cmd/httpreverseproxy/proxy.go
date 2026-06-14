package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"
)

// NewHandler creates an http.Handler that reverse-proxies incoming requests
// to upstream servers based on the Router's routing rules.
//
// It uses httputil.ReverseProxy for each unique upstream URL, cached in a
// sync.Map to avoid recreating the proxy for every request. The cache key is
// the upstream base URL (scheme + host), so all requests routed to the same
// upstream share a single ReverseProxy instance.
//
// Each ReverseProxy is configured with:
//   - A Director that sets the scheme, host, and Host header to the upstream
//     target, and strips the x-service-name and x-environment routing headers.
//   - A Transport with the configured MaxIdleConns.
//   - An ErrorHandler that returns a JSON error body when the upstream is
//     unreachable.
//
// Request lifecycle logging goes to stderr via log.Printf:
//
//	received GET /api/users from 127.0.0.1:54321 — service=api env=production
//	completed GET /api/users → http://api-prod:8080 — 200 (12ms)
func NewHandler(rt *Router, maxIdleConns int) http.Handler {
	var proxyCache sync.Map

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		service := r.Header.Get("x-service-name")
		env := r.Header.Get("x-environment")
		log.Printf("received %s %s from %s — service=%s env=%s",
			r.Method, r.URL.Path, r.RemoteAddr, service, env)

		start := time.Now()

		// Step 1: Resolve the upstream URL from the routing rules.
		upstream, err := rt.Resolve(r)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, map[string]string{
				"error":       "no route matched",
				"service":     service,
				"environment": env,
			})
			return
		}

		// Step 2: Get or create a ReverseProxy for this upstream base URL.
		upstreamKey := upstream.Scheme + "://" + upstream.Host
		proxy := getOrCreateProxy(&proxyCache, upstreamKey, upstream, maxIdleConns)

		// Step 3: Forward the request and capture the response status code.
		lrw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		proxy.ServeHTTP(lrw, r)

		// Step 4: Log the completed request.
		log.Printf("completed %s %s → %s — %d (%s)",
			r.Method, r.URL.Path, upstreamKey, lrw.statusCode,
			time.Since(start).Round(time.Millisecond))
	})
}

// getOrCreateProxy returns a cached httputil.ReverseProxy for the given
// upstream key, or creates, caches, and returns a new one. The cache uses
// sync.Map for concurrent-safe lazy initialization: the first caller to a
// given key constructs the proxy, and subsequent callers retrieve the cached
// instance.
//
// The Director closure captures only the upstream scheme and host — the
// request path and query come from the current request, so the proxy is safe
// to reuse across requests with different paths.
func getOrCreateProxy(cache *sync.Map, key string, upstream *url.URL, maxIdleConns int) *httputil.ReverseProxy {
	if p, ok := cache.Load(key); ok {
		return p.(*httputil.ReverseProxy)
	}

	// Capture the upstream scheme and host for the Director closure. These
	// are the same for every request to this upstream, so they're safe to
	// close over. The request path and query are read from the current
	// request inside the Director.
	scheme := upstream.Scheme
	host := upstream.Host

	rp := &httputil.ReverseProxy{
		// Director modifies the outgoing request before it is sent upstream.
		// We set the scheme and host to the upstream target, preserve the
		// original path and query, and strip internal routing headers.
		Director: func(req *http.Request) {
			req.URL.Scheme = scheme
			req.URL.Host = host
			req.Host = host

			// Strip routing headers — these are internal metadata used only
			// for routing decisions and must not reach the upstream service.
			req.Header.Del("x-service-name")
			req.Header.Del("x-environment")
		},

		// Transport with a configurable MaxIdleConns limit. All other
		// transport settings use http.DefaultTransport defaults.
		Transport: &http.Transport{
			MaxIdleConns: maxIdleConns,
		},

		// ErrorHandler returns a JSON error body when the upstream is
		// unreachable (connection refused, DNS failure, timeout, etc.).
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("upstream error for %s %s → %s: %s",
				r.Method, r.URL.Path, key, err)
			writeJSONError(w, http.StatusBadGateway, map[string]string{
				"error":    "upstream unreachable",
				"upstream": key,
				"detail":   err.Error(),
			})
		},
	}

	// Store the proxy in the cache. If another goroutine beat us to it,
	// discard ours and return the cached one.
	p, _ := cache.LoadOrStore(key, rp)
	return p.(*httputil.ReverseProxy)
}

// writeJSONError writes a JSON error response with the given HTTP status code
// and the Content-Type header set to "application/json".
func writeJSONError(w http.ResponseWriter, statusCode int, body map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	// Best-effort encoding — if marshalling fails, the response has already
	// been written with the correct status code and an empty body.
	json.NewEncoder(w).Encode(body)
}

// loggingResponseWriter wraps http.ResponseWriter to capture the HTTP status
// code that was written. This allows the caller to log the response status
// after the handler has completed.
type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader captures the status code before delegating to the wrapped
// ResponseWriter. This is called by httputil.ReverseProxy when the upstream
// response arrives.
func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}
