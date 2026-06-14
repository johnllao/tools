# httpreverseproxy — Reference Documentation

> **Generated for AI/LLM consumption** — intended as a complete reference for
> enhancing or debugging the HTTP Reverse Proxy implementation.

## Overview

`httpreverseproxy` is a Go HTTP reverse proxy binary under `cmd/httpreverseproxy/`.
It routes incoming HTTP requests to upstream services based on routing rules
defined in a JSON configuration file. Matching is done via the
`x-service-name` and `x-environment` request headers against an ordered list of
rules — the first match wins. Wildcards (`"*"`) match any header value.

**Architecture**: Single `main` package split into 4 files (446 lines total).
Uses only the Go standard library — zero third-party dependencies.

---

## File: `config.go` — Configuration Types and Loading

### Types

#### `Config`

Top-level configuration structure loaded from the JSON file. Holds server
settings and routing rules.

| Field | Type | JSON Key | Default | Description |
|---|---|---|---|---|
| `Listen` | `string` | `"listen"` | *required* | Address (`host:port`) the proxy listens on, e.g. `":9090"`. |
| `ReadTimeout` | `time.Duration` | `"read_timeout"` | `30s` | Max duration for reading the entire request (including body). Parsed from a string like `"30s"`. |
| `WriteTimeout` | `time.Duration` | `"write_timeout"` | `30s` | Max duration before timing out writes of the response. Parsed from a string like `"30s"`. |
| `MaxIdleConns` | `int` | `"max_idle_conns"` | `100` | Max idle (keep-alive) connections per upstream host. |
| `Routes` | `[]Route` | `"routes"` | *optional* | Ordered list of routing rules. Empty/omitted → every request returns 502. |

**Code logic**: Created by `LoadConfig`, used by `main.go` to configure the
`http.Server`, by `router.go` to build the `Router`, and by `proxy.go` for
`MaxIdleConns` transport settings. Read-only after creation — never mutated.

---

#### `Route`

A single routing rule that maps a `(service, environment)` header tuple to an
upstream server URL.

| Field | Type | JSON Key | Description |
|---|---|---|---|
| `Service` | `string` | `"service"` | Exact match for `x-service-name` header. `"*"` matches any value (including missing header). |
| `Environment` | `string` | `"environment"` | Exact match for `x-environment` header. `"*"` matches any value (including missing header). |
| `Upstream` | `string` | `"upstream"` | Base URL of the upstream server. Must include scheme (`http`/`https`) and host. The request path is appended as-is. |

**Applicable constraints**:
- If both `Service` and `Environment` are empty string (`""`), config loading
  rejects the rule with `"at least one must be set"`.
- `Upstream` must be a valid URL with a scheme and host — validated at startup.
- `""` (empty string) is NOT a wildcard. Only `"*"` acts as a wildcard. An empty
  string matches only when the header is missing or empty.

---

#### `rawConfig` (unexported)

Intermediate struct for JSON unmarshalling. Exists because `time.Duration`
unmarshals as nanoseconds from JSON numbers, not human-readable strings. This
intermediate type reads duration fields as `string`, then `LoadConfig` parses
them via `time.ParseDuration`.

| Field | Type | JSON Key |
|---|---|---|
| `Listen` | `string` | `"listen"` |
| `ReadTimeout` | `string` | `"read_timeout"` |
| `WriteTimeout` | `string` | `"write_timeout"` |
| `MaxIdleConns` | `int` | `"max_idle_conns"` |
| `Routes` | `[]Route` | `"routes"` |

---

### Functions

#### `LoadConfig(path string) (*Config, error)`

Reads, parses, and validates the JSON configuration file. **Fail-fast** — all
errors are surfaced before the proxy starts accepting traffic.

**Parameters:**
- `path` — filesystem path to the JSON config file.

**Return value:** Fully initialized `*Config` with defaults applied, or `nil`
and an error.

**Validation checks (in order):**

| Condition | Error Message |
|---|---|
| File not found / unreadable | `config: cannot read <path>: <err>` |
| Invalid JSON syntax | `config: invalid JSON in <path>: <err>` |
| `"listen"` is empty | `config: "listen" is required` |
| `route[i]` upstream is empty | `config: route[<i>]: "upstream" is required` |
| `route[i]` upstream has no scheme | `config: route[<i>]: "upstream" must have a scheme (http/https)` |
| `route[i]` upstream has no host | `config: route[<i>]: "upstream" must have a host` |
| `route[i]` both fields empty | `config: route[<i>]: at least one of "service" or "environment" must be set` |

**Defaults applied:**

| Config Field | Default |
|---|---|
| `read_timeout` | `30s` |
| `write_timeout` | `30s` |
| `max_idle_conns` | `100` |

**Dependencies**: `os.ReadFile`, `json.Unmarshal`, `url.Parse`, `time.ParseDuration`.

---

## File: `router.go` — Header-Based Routing

### Types

#### `Router`

Compiled routing engine that matches incoming requests to upstream URLs via
header-based rules.

| Field | Type | Access | Description |
|---|---|---|---|
| `routes` | `[]Route` | private | The routing rules, stored in order. **Immutable** after creation. |

**Thread safety**: Immutable — the `routes` slice is never modified after
construction. Safe for concurrent reads from multiple request goroutines with
no synchronization needed.

---

### Functions

#### `NewRouter(routes []Route) *Router`

Constructor. Creates a `Router` from a slice of route rules.

**Parameters:**
- `routes` — the ordered list of routing rules (from `Config.Routes`).

**Edge cases:**
- `nil` or empty slice → `Resolve` always errors → every request gets 502.

---

#### `(rt *Router) Resolve(r *http.Request) (*url.URL, error)`

Matches the request against routing rules and returns the target upstream URL,
including the original request path and query string appended to the upstream
base.

**Algorithm:**
1. Extract `x-service-name` header → `service` (empty string if missing).
2. Extract `x-environment` header → `environment` (empty string if missing).
3. Walk `rt.routes` in order:
   - If `route.Service != "*"` AND `route.Service != service` → skip.
   - If `route.Environment != "*"` AND `route.Environment != environment` → skip.
   - Otherwise → **match found**.
4. On match: `url.Parse(route.Upstream)`, then set `upstream.Path = r.URL.Path`,
   `upstream.RawQuery = r.URL.RawQuery`, return the `*url.URL`.
5. No match: return error `"no route matched service=... environment=..."`.

**Wildcard semantics:**
- `"*"` matches any value (including missing/empty headers).
- Any non-`"*"` value must match the header exactly (case-sensitive).
- Empty string `""` matches only when the header is missing or empty.

**Safety note**: `url.Parse(route.Upstream)` should never fail because
`LoadConfig` validates every upstream URL at startup. If it does fail, it's a
programming error, not a runtime condition.

**Returned URL**: The returned `*url.URL` has the upstream scheme, host, the
request's path, and request's query string. Used by `proxy.go` to construct the
cache key (`scheme + "://" + host`) and supply the Director closure.

---

## File: `proxy.go` — Reverse Proxy Handler

### Types

#### `loggingResponseWriter` (unexported)

Wraps `http.ResponseWriter` to capture the HTTP status code written by the
underlying handler. Required because `httputil.ReverseProxy.ServeHTTP` calls
`WriteHeader` internally but does not expose the status code it wrote.

| Field | Type | Description |
|---|---|---|
| `ResponseWriter` | `http.ResponseWriter` | Embedded — all writer calls delegate here. |
| `statusCode` | `int` (unexported) | Captured status code. Initialized to `http.StatusOK` (200) as the default. |

**Method**: `WriteHeader(code int)` — stores `code` in `lrw.statusCode`, then
delegates to the embedded `ResponseWriter.WriteHeader(code)`.

**Usage**: Created per-request in `NewHandler`'s closure, passed to
`proxy.ServeHTTP(lrw, r)`, then read after the proxy completes to log the
response status.

---

### Functions

#### `NewHandler(rt *Router, maxIdleConns int) http.Handler`

Creates an `http.Handler` that reverse-proxies requests to upstream servers
based on the `Router`'s rules.

**Parameters:**
- `rt` — the compiled router.
- `maxIdleConns` — max idle keep-alive connections for the HTTP transport.

**Return value**: `http.Handler` (via `http.HandlerFunc`).

**Per-request lifecycle (inside the closure):**

```
1. Log: "received <method> <path> from <addr> — service=<svc> env=<env>"

2. Router.Resolve(r) → upstream URL
   On error → write 502 JSON body:
     {"error": "no route matched", "service": "...", "environment": "..."}
   Return.

3. Compute upstreamKey = upstream.Scheme + "://" + upstream.Host

4. getOrCreateProxy(&proxyCache, upstreamKey, upstream, maxIdleConns) → proxy

5. Wrap ResponseWriter in loggingResponseWriter

6. proxy.ServeHTTP(lrw, r)
   → httputil.ReverseProxy forwards the request and copies response back

7. Log: "completed <method> <path> → <key> — <status> (<duration>)"
```

**Thread safety**: Each request creates a new goroutine from `http.Server`.
The `proxyCache` (`sync.Map`) is safe for concurrent access across handlers.

---

#### `getOrCreateProxy(cache *sync.Map, key string, upstream *url.URL, maxIdleConns int) *httputil.ReverseProxy`

Returns a cached `httputil.ReverseProxy` for the given upstream key, or
creates, caches, and returns a new one.

**Parameters:**
- `cache` — pointer to the `sync.Map` shared across all requests.
- `key` — cache key: `"scheme://host"` (e.g., `"http://api-prod:8080"`).
- `upstream` — first request's resolved upstream URL (used to extract scheme/host for the Director).
- `maxIdleConns` — transport idle connection limit.

**Lazy initialization pattern:**
1. `cache.Load(key)` — fast path if proxy already exists.
2. If not found: construct the `httputil.ReverseProxy`, then
   `cache.LoadOrStore(key, rp)` — handles race where two goroutines both miss.
   The loser discards their constructed proxy and returns the winner's.

**Director closure captures:**
- `scheme` (string) — upstream scheme (`http`/`https`), from the first request.
- `host` (string) — upstream host:port, from the first request.

**Director logic (called per-request by ReverseProxy):**
1. `req.URL.Scheme = scheme` — route to upstream.
2. `req.URL.Host = host` — route to upstream.
3. `req.Host = host` — set `Host` header for upstream.
4. `req.Header.Del("x-service-name")` — strip routing header.
5. `req.Header.Del("x-environment")` — strip routing header.

The request's path and query string are NOT modified — they pass through from
the original client request automatically (ReverseProxy copies the outgoing
request URL from the incoming one before calling Director).

**Transport configuration:**
```go
&http.Transport{MaxIdleConns: maxIdleConns}
```
All other transport settings default to `http.DefaultTransport`. This is a
minimal transport — custom TLS config, timeouts, or proxy settings would need
to be added here.

**ErrorHandler logic (called per-request by ReverseProxy when upstream fails):**
1. Log: `"upstream error for <method> <path> → <key>: <err>"`.
2. Write 502 JSON body:
   ```json
   {"error": "upstream unreachable", "upstream": "<key>", "detail": "<err>"}
   ```

---

#### `writeJSONError(w http.ResponseWriter, statusCode int, body map[string]string)`

Writes a JSON error response with the given HTTP status code.

**Parameters:**
- `w` — response writer.
- `statusCode` — HTTP status (e.g., 502, 500).
- `body` — map of string keys to string values, serialized as JSON.

**Code logic:**
1. Set `Content-Type: application/json`.
2. Write the HTTP status code via `w.WriteHeader(statusCode)`.
3. Best-effort `json.NewEncoder(w).Encode(body)` — if marshalling fails, the
   response has already been written with the correct status code.

**Called from:**
- `NewHandler` closure — when no route matches (502).
- `getOrCreateProxy`'s `ErrorHandler` — when upstream is unreachable (502).

---

## File: `main.go` — Entry Point

### Functions

#### `main()`

Top-level entry point. Startup sequence:

1. Parse `-config` CLI flag (default: `"config.json"`).
2. `LoadConfig(*configPath)` → load and validate JSON config.
   - On error: `fmt.Fprintf(os.Stderr, "%s\n", err)` + `os.Exit(1)`.
3. `NewRouter(cfg.Routes)` → build routing engine.
4. `NewHandler(router, cfg.MaxIdleConns)` → build HTTP handler.
5. Create `http.Server` with `Addr`, `Handler`, `ReadTimeout`, `WriteTimeout`.
6. Goroutine: `srv.ListenAndServe()` — blocks on listen.
   - Log: `"httpreverseproxy: listening on <addr> (<n> routes)"`.
   - Non-`ErrServerClosed` errors logged to stderr.
7. Main goroutine: `signal.Notify(quitCh, SIGINT, SIGTERM)`, then blocks.
8. On signal: `log.Printf("httpreverseproxy: shutting down...")` + `shutdown(srv)`.

---

#### `shutdown(srv *http.Server)`

Gracefully shuts down the HTTP server with a 5-second timeout.

**Parameters:**
- `srv` — the running `*http.Server` to shut down.

**Code logic:**
1. Create context with 5-second deadline.
2. `srv.Shutdown(ctx)` — stops accepting new connections, waits for in-flight
   requests.
3. If `Shutdown` returns an error (timeout or other), log it:
   `"httpreverseproxy: shutdown error: %s"`.

**Pattern**: Mirrors `cmd/httptrace/main.go` lines 350–358.

---

### CLI Flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `-config` | `string` | `"config.json"` | Path to the JSON configuration file. |

---

## Error Response Formats

All error responses are JSON with `Content-Type: application/json`.

### 502 — No Route Matched

```json
{
  "error": "no route matched",
  "service": "unknown-svc",
  "environment": "staging"
}
```

Status code: `502 Bad Gateway`. Triggered when `Router.Resolve` returns an
error (no rule matched the request headers).

### 502 — Upstream Unreachable

```json
{
  "error": "upstream unreachable",
  "upstream": "http://api-prod.internal:8080",
  "detail": "dial tcp: connection refused"
}
```

Status code: `502 Bad Gateway`. Triggered by `httputil.ReverseProxy`'s
`ErrorHandler` when the upstream server cannot be reached (connection refused,
DNS failure, timeout, etc.).

---

## Logging Format

All operational logging goes to **stderr** via `log.Printf`. No output to
stdout (reserved for future access logs in JSON Lines format).

### Startup

```
httpreverseproxy: listening on :9090 (3 routes)
```

### Per-request received

```
received GET /api/users from 127.0.0.1:54321 — service=api env=production
```

### Per-request completed

```
completed GET /api/users → http://api-prod:8080 — 200 (12ms)
```

### Upstream error

```
upstream error for GET /api/users → http://api-prod:8080: dial tcp: connection refused
```

### Shutdown

```
httpreverseproxy: shutting down...
```

---

## Data Flow Diagram

```
┌──────────────┐     ┌──────────────────────────────┐     ┌────────────────────┐
│  HTTP Client  │────▶│  httpreverseproxy            │────▶│  Upstream Service  │
│  (curl, etc)  │     │  (net/http/httputil)         │     │  (target server)   │
└──────────────┘     └──────────────────────────────┘     └────────────────────┘
       │                        │                                  │
       │  x-service-name: api   │  1. Router.Resolve(r)             │
       │  x-environment: prod   │     → match rule → upstream URL  │
       │                        │  2. getOrCreateProxy()           │
       │                        │     → cached ReverseProxy        │
       │                        │  3. Director:                    │
       │                        │     - Set scheme/host to upstream│
       │                        │     - Strip routing headers       │
       │                        │  4. ReverseProxy.ServeHTTP()     │
       │                        │     → forward and copy response  │
```

---

## Thread Safety Model

| Component | Strategy |
|---|---|
| `Router` | Immutable after creation (`routes` never modified). Safe for concurrent reads. |
| `sync.Map` (proxy cache) | Concurrent-safe read-heavy cache. Proxies created once per upstream URL. |
| `Config` | Loaded once at startup, never mutated. |
| `http.Server` | Uses goroutine-per-connection model. Handler functions run concurrently. |
| `loggingResponseWriter` | Per-request allocation. No shared state across requests. |

---

## Dependencies (all standard library)

| Package | Used In | Purpose |
|---|---|---|
| `context` | `main.go` | Graceful shutdown with timeout. |
| `encoding/json` | `config.go`, `proxy.go` | Config parsing; JSON error response encoding. |
| `flag` | `main.go` | CLI flag parsing (`-config`). |
| `fmt` | `config.go`, `main.go`, `router.go` | Error formatting. |
| `log` | `proxy.go`, `main.go` | Structured logging to stderr. |
| `net/http` | `main.go`, `proxy.go`, `router.go` | HTTP server, request/response types. |
| `net/http/httputil` | `proxy.go` | `ReverseProxy` implementation. |
| `net/url` | `config.go`, `proxy.go`, `router.go` | URL parsing and manipulation. |
| `os` | `config.go`, `main.go` | File reading, exit codes. |
| `os/signal` | `main.go` | SIGINT/SIGTERM handling. |
| `sync` | `proxy.go` | `sync.Map` for concurrent proxy cache. |
| `syscall` | `main.go` | Signal constants (`SIGINT`, `SIGTERM`). |
| `time` | `config.go`, `proxy.go`, `main.go` | Duration parsing, timeouts, request timing. |

---

## Extension Points

Adding new features would typically involve modifying or adding code in:

1. **New routing strategies** (in `router.go`):
   - Add path-based routing (prefix/glob matching) alongside header matching.
   - Add weighted routing / canary deployments.
   - Add regex matching for header values.

2. **New config block types** (in `config.go`):
   - Add rate limiting, timeouts, retry policies.
   - Add TLS configuration for the listener and transport.

3. **New middleware** (in `proxy.go`):
   - Add request/response header manipulation (e.g., `X-Forwarded-For`).
   - Add request/response body inspection or modification.
   - Add metrics collection (request counts, latency histograms).
   - Add circuit breaker per upstream.

4. **New protocols** (in `proxy.go` or new file):
   - Add WebSocket support (requires hijacking the connection).
   - Add h2c (HTTP/2 cleartext) support.

5. **Access logging** (in `proxy.go`):
   - The plan reserves stdout for JSON Lines access logs. Add a JSON logger
     that writes structured request/response data in parallel to the stderr
     operational logs.
