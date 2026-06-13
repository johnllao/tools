// Command httptrace implements a standalone HTTP/HTTPS MITM proxy for traffic
// inspection and tracing, similar to Fiddler.
//
// It captures request and response details (method, URL, status, headers,
// body, timing) for every HTTP exchange that passes through the proxy, and
// writes each completed session as a JSON line to a log file or stdout.
//
// Usage:
//
//	httptrace [-port <proxy-port>] [-log-file <path>] [-upstream-proxy <url>]
//
// The proxy starts immediately on launch and runs until the process receives
// SIGINT (Ctrl+C) or SIGTERM, at which point it gracefully shuts down, flushes
// the log, and exits.
//
// Captured sessions are written as JSON Lines (one JSON object per line).
// By default output goes to stdout; use -log-file to write to a file.
//
// In corporate environments that require an upstream proxy, use -upstream-proxy
// to route all outbound traffic through it. When not set, goproxy reads the
// HTTP_PROXY and HTTPS_PROXY environment variables.
//
// Examples:
//
//	httptrace -port 8080 -log-file traces.jsonl
//	httptrace -port 8080 -upstream-proxy http://corp-proxy:3128
//	httptrace > traces.jsonl &
//	curl --proxy http://localhost:8080 http://example.com
//	kill <pid>             # graceful shutdown
//	tail -f traces.jsonl | jq '.request.url'
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/elazarl/goproxy"
)

// --- Package-level state -----------------------------------------------------

// maxBodySize is the maximum number of body bytes to capture per request or
// response. Larger bodies are truncated in the stored session. Set from the
// -max-body-size CLI flag.
var maxBodySize int

// maxSessions is the maximum number of sessions to retain in memory. When the
// limit is reached, the oldest session is evicted. Set from -max-sessions.
var maxSessions int

// customCA holds an optional user-provided CA certificate for HTTPS MITM.
// When nil, goproxy auto-generates a CA on first use.
var customCA *tls.Certificate

// --- Log file state ----------------------------------------------------------

var (
	// logWriter is the buffered writer for captured-session output.
	logWriter *bufio.Writer

	// logFileOut is non-nil only when writing to a file (not stdout). It is
	// used for Sync() calls so that tail -f sees new lines immediately.
	logFileOut *os.File

	// logMu guards writes to logWriter so that concurrent captureResponse
	// goroutines do not interleave their output.
	logMu sync.Mutex
)

// --- Session storage (guarded by sessionsMu) ---------------------------------

// sessionsMu guards the sessions slice.
var sessionsMu sync.Mutex

// sessionCounter provides unique, monotonically increasing session IDs.
var sessionCounter int64

// sessions stores all captured HTTP sessions in order of capture (oldest first).
var sessions []*Session

// ctxKey is the type used for request-context keys to avoid collisions.
type ctxKey string

// sessionIDKey is the context key that carries the session ID from OnRequest to
// OnResponse inside the goproxy handler chain.
const sessionIDKey ctxKey = "sessionID"

// --- Session data structures -------------------------------------------------

// Session holds a complete HTTP request-response exchange captured by the proxy.
type Session struct {
	// ID is the unique session identifier, assigned at capture time.
	ID int64 `json:"id"`
	// Timestamp is when the request was received by the proxy.
	Timestamp time.Time `json:"timestamp"`
	// Duration is the time elapsed between the request being received and the
	// response being fully captured. Zero if the response has not yet arrived.
	Duration jsonDuration `json:"duration"`
	// Request holds the captured HTTP request details.
	Request *RequestInfo `json:"request"`
	// Response holds the captured HTTP response details. Nil if the response
	// has not yet been captured.
	Response *ResponseInfo `json:"response,omitempty"`
}

// RequestInfo holds the captured HTTP request details.
type RequestInfo struct {
	// Method is the HTTP method (GET, POST, etc.).
	Method string `json:"method"`
	// URL is the full request URL including scheme, host, and path.
	URL string `json:"url"`
	// Proto is the protocol version string (e.g. "HTTP/1.1").
	Proto string `json:"proto"`
	// Headers maps header names to their joined values.
	Headers map[string]string `json:"headers"`
	// Body is the captured request body, truncated if it exceeds maxBodySize.
	// Empty for requests without a body (e.g. GET).
	Body string `json:"body,omitempty"`
	// Size is the original body size in bytes before truncation.
	Size int64 `json:"size"`
}

// ResponseInfo holds the captured HTTP response details.
type ResponseInfo struct {
	// StatusCode is the HTTP status code (e.g. 200, 404).
	StatusCode int `json:"status_code"`
	// Proto is the protocol version string (e.g. "HTTP/1.1").
	Proto string `json:"proto"`
	// Headers maps header names to their joined values.
	Headers map[string]string `json:"headers"`
	// Body is the captured response body, truncated if it exceeds maxBodySize.
	Body string `json:"body,omitempty"`
	// Size is the original body size in bytes before truncation.
	Size int64 `json:"size"`
}

// jsonDuration wraps time.Duration to marshal as a human-readable string
// ("123ms", "4.5s") instead of an integer nanosecond count.
type jsonDuration time.Duration

// MarshalJSON implements json.Marshaler, formatting the duration as a
// human-readable string like "1.2s" or "350ms".
func (d jsonDuration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// String returns the duration as a human-readable string.
func (d jsonDuration) String() string {
	return time.Duration(d).String()
}

// --- Session store helpers ---------------------------------------------------

// addSession appends a session to the in-memory store, evicting the oldest
// entry if the store has reached maxSessions.
func addSession(s *Session) {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()

	if len(sessions) >= maxSessions {
		// Drop the oldest session to stay within the limit.
		sessions = sessions[1:]
	}
	sessions = append(sessions, s)
}

// findSession looks up a session by ID. Returns nil if not found.
// Caller must hold sessionsMu.
func findSession(id int64) *Session {
	// Sessions are stored in ID order, so a linear scan from the end is
	// fast for recently captured traffic.
	for i := len(sessions) - 1; i >= 0; i-- {
		if sessions[i].ID == id {
			return sessions[i]
		}
	}
	return nil
}

// nextSessionID returns the next unique session identifier.
func nextSessionID() int64 {
	return atomic.AddInt64(&sessionCounter, 1)
}

// --- Log writer --------------------------------------------------------------

// writeSessionToLog marshals a completed session as a JSON line and writes it
// to the configured log output (stdout or file). If writing to a file, it
// syncs the file to disk so that tail -f picks up the new line immediately.
func writeSessionToLog(s *Session) {
	b, err := json.Marshal(s)
	if err != nil {
		log.Printf("httptrace: failed to marshal session %d: %s", s.ID, err)
		return
	}

	logMu.Lock()
	defer logMu.Unlock()

	fmt.Fprintln(logWriter, string(b))
	logWriter.Flush()
	if logFileOut != nil {
		// Sync to disk so that tools like tail -f see the line right away.
		logFileOut.Sync()
	}
}

// --- main --------------------------------------------------------------------

// main is the entry point. It parses CLI flags, starts the proxy immediately,
// and blocks until SIGINT (Ctrl+C) or SIGTERM, then gracefully shuts down and
// writes captured sessions as JSON Lines to the configured output.
//
// Usage:
//
//	httptrace [-port <proxy-port>] [-log-file <path>] [-upstream-proxy <url>]
func main() {
	portFlag := flag.Int("port", 8080, "Proxy listen port")
	logFileFlag := flag.String("log-file", "", "Path to write captured sessions as JSON Lines (default: stdout)")
	maxSessFlag := flag.Int("max-sessions", 1000, "Maximum number of captured sessions to retain in memory")
	maxBodyFlag := flag.Int("max-body-size", 65536, "Maximum body bytes to capture per request/response (64 KB default)")
	certFlag := flag.String("cert", "", "Path to a custom CA certificate file (PEM) for HTTPS MITM")
	keyFlag := flag.String("key", "", "Path to the private key for the custom CA certificate")
	upstreamFlag := flag.String("upstream-proxy", "", "Upstream HTTP proxy URL for corporate environments (e.g., http://proxy.corp.com:8080)")
	flag.Parse()

	proxyPort := *portFlag
	maxSessions = *maxSessFlag
	maxBodySize = *maxBodyFlag

	// If a custom CA certificate and key pair is provided, load and set it.
	// goproxy uses the package-level GoproxyCa variable; when left at nil
	// (the default), goproxy auto-generates a CA on first use.
	if *certFlag != "" || *keyFlag != "" {
		if *certFlag == "" || *keyFlag == "" {
			fmt.Fprintf(os.Stderr, "Both -cert and -key are required when providing a custom CA\n")
			os.Exit(1)
		}
		cert, err := tls.LoadX509KeyPair(*certFlag, *keyFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load CA certificate: %s\n", err)
			os.Exit(1)
		}
		customCA = &cert
	}

	// Open the log output: a specific file, or stdout by default.
	if *logFileFlag != "" {
		f, err := os.Create(*logFileFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create log file: %s\n", err)
			os.Exit(1)
		}
		logFileOut = f
		logWriter = bufio.NewWriter(f)
	} else {
		// Default to stdout so users can pipe the JSON output to jq, redirect
		// to a file, or tail from another process.
		logWriter = bufio.NewWriter(os.Stdout)
	}

	// Parse and validate the upstream proxy URL, if provided.
	// When set, all outbound traffic is routed through this proxy.
	upstreamURL, err := parseUpstreamProxyURL(*upstreamFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "httptrace: %s\n", err)
		os.Exit(1)
	}

	// Start the proxy immediately.
	srv, err := startProxy(proxyPort, upstreamURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "httptrace: failed to start proxy: %s\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "httptrace: proxy started on port %d (PID %d)\n", proxyPort, os.Getpid())
	if upstreamURL != nil {
		fmt.Fprintf(os.Stderr, "  Upstream proxy: %s\n", upstreamURL.String())
	}
	fmt.Fprintf(os.Stderr, "  Press Ctrl+C to stop\n")

	// Block until SIGINT (Ctrl+C) or SIGTERM, then gracefully shut down.
	quitCh := make(chan os.Signal, 1)
	signal.Notify(quitCh, syscall.SIGINT, syscall.SIGTERM)
	<-quitCh

	fmt.Fprintf(os.Stderr, "\nhttptrace: shutting down...\n")
	stopProxy(srv)
	logWriter.Flush()
	if logFileOut != nil {
		logFileOut.Sync()
		logFileOut.Close()
	}
}

// --- Proxy lifecycle ---------------------------------------------------------

// startProxy creates and starts the goproxy MITM server on the given port.
// If upstreamURL is non-nil, all outbound traffic is routed through it.
// It returns the *http.Server so the caller can shut it down later.
func startProxy(port int, upstreamURL *url.URL) (*http.Server, error) {
	// Build the proxy handler with request/response capture hooks.
	proxy := newCaptureProxy(upstreamURL)

	// If a custom CA was provided at startup, install it before the first
	// HTTPS connection is handled.
	if customCA != nil {
		goproxy.GoproxyCa = *customCA
	}

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("cannot listen on port %d: %w", port, err)
	}

	srv := &http.Server{
		Handler: proxy,
		// No ReadTimeout / WriteTimeout — the proxy passes through to the
		// client and server, which control their own timeouts.
	}

	// Serve in a background goroutine. The proxy runs until stopProxy
	// calls Shutdown on the server.
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("httptrace: proxy server error: %s", err)
		}
	}()

	return srv, nil
}

// stopProxy gracefully shuts down the proxy server, giving in-flight requests
// up to 5 seconds to complete before forcibly closing connections.
func stopProxy(srv *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("httptrace: proxy shutdown error: %s", err)
	}
}

// parseUpstreamProxyURL parses and validates the -upstream-proxy flag value.
// Returns nil if the flag is empty (no upstream proxy configured).
// Returns an error if the URL is missing a scheme or host.
func parseUpstreamProxyURL(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream proxy URL %q: %w", raw, err)
	}

	if u.Scheme == "" {
		return nil, fmt.Errorf("upstream proxy URL %q must have a scheme (http or https)", raw)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("upstream proxy URL %q must have a host", raw)
	}

	return u, nil
}

// --- Proxy capture setup -----------------------------------------------------

// newCaptureProxy creates a goproxy.ProxyHttpServer configured to capture
// every HTTP request-response exchange into the in-memory session store and
// write completed sessions to the log output.
//
// If upstreamURL is non-nil, all outbound traffic is routed through the
// specified proxy. When nil, goproxy defaults apply (HTTP_PROXY/HTTPS_PROXY
// environment variables).
func newCaptureProxy(upstreamURL *url.URL) *goproxy.ProxyHttpServer {
	proxy := goproxy.NewProxyHttpServer()

	// Keep goproxy's own logging quiet — we handle capture and reporting
	// through the log file.
	proxy.Verbose = false

	// Configure upstream proxy routing for corporate environments.
	// This must be done before any requests are handled.
	if upstreamURL != nil {
		proxyURLStr := upstreamURL.String()

		// Route plain HTTP (and MITM-decrypted HTTPS) through the upstream proxy.
		proxy.Tr.Proxy = http.ProxyURL(upstreamURL)

		// Route CONNECT tunnels (raw HTTPS) through the upstream proxy.
		proxy.ConnectDial = proxy.NewConnectDialToProxy(proxyURLStr)
	}

	// OnRequest fires for every HTTP request that reaches the proxy,
	// including CONNECT tunnel requests. We capture the request metadata
	// and body, then let the proxy forward it upstream.
	proxy.OnRequest().DoFunc(func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		captureRequest(r)
		return r, nil
	})

	// OnResponse fires when the upstream response arrives. We capture the
	// response metadata and body, compute the round-trip duration, and
	// write the completed session to the log file.
	proxy.OnResponse().DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		captureResponse(ctx.Req, resp)
		return resp
	})

	return proxy
}

// captureRequest creates a new Session for the incoming request, captures the
// request line, headers, and body, and stores the session for later retrieval.
// The session ID is stashed in the request context so captureResponse can
// find and update the session when the response arrives.
func captureRequest(r *http.Request) {
	sess := &Session{
		ID:        nextSessionID(),
		Timestamp: time.Now(),
		Request: &RequestInfo{
			Method:  r.Method,
			URL:     r.URL.String(),
			Proto:   r.Proto,
			Headers: captureHeaders(r.Header),
		},
	}

	// Capture the request body, then restore it so the proxy can forward
	// the request upstream. We read the body once, store a truncated copy
	// in the session, and give the proxy a fresh reader over the full bytes.
	if r.Body != nil && r.Body != http.NoBody {
		bodyStr, bodySize, newBody := captureBody(r.Body)
		sess.Request.Body = bodyStr
		sess.Request.Size = bodySize
		r.Body = newBody
	}

	// Stash the session ID in the request context so that captureResponse
	// can locate this session. goproxy carries the same *http.Request
	// through to the OnResponse handler.
	*r = *r.WithContext(context.WithValue(r.Context(), sessionIDKey, sess.ID))

	addSession(sess)
}

// captureResponse finds the session that matches the request (by session ID
// in the request context), captures the response metadata and body, records
// the round-trip duration, and writes the completed session to the log file.
func captureResponse(r *http.Request, resp *http.Response) {
	id, ok := r.Context().Value(sessionIDKey).(int64)
	if !ok {
		return // session not found; should not happen in normal operation
	}

	sessionsMu.Lock()
	sess := findSession(id)
	if sess == nil {
		sessionsMu.Unlock()
		return
	}
	sessionsMu.Unlock()

	info := &ResponseInfo{
		StatusCode: resp.StatusCode,
		Proto:      resp.Proto,
		Headers:    captureHeaders(resp.Header),
	}

	// Capture the response body, then restore it so the proxy can forward
	// the response to the client.
	if resp.Body != nil && resp.Body != http.NoBody {
		bodyStr, bodySize, newBody := captureBody(resp.Body)
		info.Body = bodyStr
		info.Size = bodySize
		resp.Body = newBody
	}

	sess.Response = info
	sess.Duration = jsonDuration(time.Since(sess.Timestamp))

	// Write the completed session to the log output as a JSON line.
	writeSessionToLog(sess)
}

// --- Helper functions --------------------------------------------------------

// captureHeaders converts an http.Header to a map[string]string by joining
// multi-valued headers with a comma and space.
func captureHeaders(h http.Header) map[string]string {
	result := make(map[string]string, len(h))
	for k, v := range h {
		result[k] = strings.Join(v, ", ")
	}
	return result
}

// captureBody reads the full body from reader, returns a truncated copy for
// session storage and a fresh io.ReadCloser wrapping the original bytes so
// the proxy can forward the body upstream.
func captureBody(reader io.ReadCloser) (captured string, originalSize int64, restored io.ReadCloser) {
	if reader == nil {
		return "", 0, nil
	}

	bodyBytes, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		return "", 0, io.NopCloser(bytes.NewReader(nil))
	}

	originalSize = int64(len(bodyBytes))

	if len(bodyBytes) > maxBodySize {
		captured = string(bodyBytes[:maxBodySize]) + fmt.Sprintf(
			"\n\n... [truncated — %d bytes total, showing first %d]",
			originalSize, maxBodySize,
		)
	} else {
		captured = string(bodyBytes)
	}

	// Return a fresh reader over the full bytes so the proxy can forward
	// the complete body upstream / downstream.
	restored = io.NopCloser(bytes.NewBuffer(bodyBytes))
	return captured, originalSize, restored
}
