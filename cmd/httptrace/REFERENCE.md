# httptrace — Reference Documentation

> **File**: `cmd/httptrace/main.go` (single-file package `main`)
>
> **Purpose**: Standalone HTTP/HTTPS MITM proxy that captures request/response
> exchanges and writes them as JSON Lines to stdout or a file. Controlled via
> Unix signals (SIGUSR1/SIGUSR2 for start/stop, SIGINT/SIGTERM for shutdown).

---

## Table of Contents

1. [Overview & Data Flow](#1-overview--data-flow)
2. [Imports / Dependencies](#2-imports--dependencies)
3. [Package-Level State](#3-package-level-state)
   - [Configuration (set once at startup)](#31-configuration-set-once-at-startup)
   - [Log File State](#32-log-file-state)
   - [Proxy Runtime State (guarded by `proxyMu`)](#33-proxy-runtime-state-guarded-by-proxymu)
   - [Session Storage (guarded by `sessionsMu`)](#34-session-storage-guarded-by-sessionsmu)
4. [Types](#4-types)
   - [`ctxKey`](#41-ctxkey)
   - [`Session`](#42-session)
   - [`RequestInfo`](#43-requestinfo)
   - [`ResponseInfo`](#44-responseinfo)
   - [`jsonDuration`](#45-jsonduration)
5. [Constants](#5-constants)
   - [`sessionIDKey`](#51-sessionidkey)
6. [Functions](#6-functions)
   - [Entry Point](#61-main)
   - [Proxy Lifecycle](#62-startproxy--stopproxy)
   - [Capture Setup](#63-newcaptureproxy)
   - [Capture Hooks](#64-capturerequest--captureresponse)
   - [Helpers](#65-captureheaders--capturebody)
   - [Session Store](#66-addsession--findsession--nextsessionid)
   - [Log Writer](#67-writesessiontolog)
7. [Thread Safety Model](#7-thread-safety-model)
8. [JSON Output Format](#8-json-output-format)
9. [Extension / Enhancement Points](#9-extension--enhancement-points)

---

## 1. Overview & Data Flow

```
┌──────────────┐     ┌──────────────────┐     ┌────────────────┐
│  HTTP Client │────▶│  httptrace proxy │────▶│  Upstream      │
│  (curl, etc) │     │  (goproxy MITM)  │     │  HTTP server   │
└──────────────┘     └──────────────────┘     └────────────────┘
                           │
                           ▼
                    ┌──────────────┐
                    │  Session     │
                    │  store (mem) │
                    └──────┬───────┘
                           │ on response
                           ▼
                    ┌──────────────┐
                    │  Log output  │  <── JSON Lines
                    │  (stdout or  │      (one Session
                    │   -log-file) │       per line)
                    └──────────────┘
```

### Lifecycle:

1. **Process starts** — parses flags, prints PID + instructions to stderr, blocks
2. **SIGUSR1** → `startProxy(port)` → TCP listener → goproxy handler → background goroutine
3. **HTTP request arrives at proxy** → `captureRequest` creates `Session`, stores in memory, forwards upstream
4. **Response arrives from upstream** → `captureResponse` fills in response data, writes complete `Session` as JSON line
5. **SIGUSR2** → `stopProxy()` → graceful shutdown (5s drain) → sessions stay in memory
6. **SIGINT/SIGTERM** → stop proxy, flush log, close file, exit

---

## 2. Imports / Dependencies

### Standard Library

| Import | Used By | Purpose |
|---|---|---|
| `bufio` | `main`, `writeSessionToLog` | Buffered I/O for log output (flush on each line for `tail -f` compatibility) |
| `bytes` | `captureBody` | `bytes.NewBuffer` / `bytes.NewReader` — wraps captured body bytes into a reusable reader |
| `context` | `stopProxy`, `captureRequest` | `context.WithTimeout` for graceful shutdown; `context.WithValue` for session-ID propagation |
| `crypto/tls` | `main` | `tls.LoadX509KeyPair` — loads custom CA certificate/key for HTTPS interception |
| `encoding/json` | `writeSessionToLog` | `json.Marshal` — serializes `Session` to JSON for log output |
| `flag` | `main` | CLI flag parsing (`-port`, `-log-file`, `-max-sessions`, `-max-body-size`, `-cert`, `-key`) |
| `fmt` | Throughout | Status messages to stderr, error formatting |
| `io` | `captureBody` | `io.ReadAll`, `io.NopCloser` — body reading and reader reconstruction |
| `log` | `startProxy` goroutine, `writeSessionToLog` | Go standard logger (writes to stderr by default) for internal errors |
| `net` | `startProxy` | `net.Listen("tcp", addr)` — creates the TCP listener for the proxy |
| `net/http` | Multiple | `*http.Request`, `*http.Response`, `http.Header`, `http.NoBody`, `http.Server` — core HTTP types |
| `os` | `main` | `os.Create`, `os.Stdout`, `os.Stderr`, `os.Exit`, `os.Getpid`, `os.Signal` |
| `os/signal` | `main` | `signal.Notify` — registers OS signal delivery to channels |
| `strings` | `captureHeaders` | `strings.Join` — joins multi-valued HTTP headers with `", "` |
| `sync` | Throughout | `sync.Mutex` — guards shared state (proxy runtime, session store, log writer) |
| `sync/atomic` | `nextSessionID` | `atomic.AddInt64` — lock-free monotonically-increasing session IDs |
| `syscall` | `main` | `syscall.SIGUSR1`, `syscall.SIGUSR2`, `syscall.SIGINT`, `syscall.SIGTERM` |
| `time` | Throughout | `time.Now()`, `time.Since()`, `time.Duration`, `time.Time` — timestamps and durations |

### Third-Party

| Import | Version | Used By | Purpose |
|---|---|---|---|
| `github.com/elazarl/goproxy` | v1.8.4 | `startProxy`, `newCaptureProxy` | HTTP/HTTPS MITM proxy library — handles CONNECT tunneling, TLS interception, request/response forwarding. Provides `ProxyHttpServer`, `ProxyCtx`, `OnRequest().DoFunc()`, `OnResponse().DoFunc()`, and the package-level `GoproxyCa` for custom CA injection. |

---

## 3. Package-Level State

State is organized into four logical groups, each with its own owning mutex where applicable.

### 3.1 Configuration (set once at startup)

All four variables are set in `main()` from CLI flags and never modified afterward.

| Variable | Type | Flag | Default | Description |
|---|---|---|---|---|
| `proxyPort` | `int` | `-port` | `8080` | TCP port the proxy binds to. Written once at startup; also overwritten inside `startProxy` with the port actually used (relevant if called with a non-default port). |
| `maxBodySize` | `int` | `-max-body-size` | `65536` | Maximum bytes of request/response body to retain in the stored session. Bodies exceeding this are truncated with a `[truncated — N bytes total]` note appended. |
| `maxSessions` | `int` | `-max-sessions` | `1000` | Maximum in-memory sessions before the oldest is evicted (FIFO). This limits memory growth of the correlation store. |
| `customCA` | `*tls.Certificate` | `-cert` + `-key` | `nil` | Optional user-supplied CA certificate for HTTPS MITM. When nil, goproxy auto-generates a CA on the first HTTPS connection. Both `-cert` and `-key` must be provided together. |

**Thread safety**: Read-only after startup, so no mutex needed. (Exception: `proxyPort` is also written inside `startProxy` under `proxyMu` — but since only one proxy can run at a time, this is serialized.)

### 3.2 Log File State

Guarded by `logMu sync.Mutex`.

| Variable | Type | Description |
|---|---|---|
| `logWriter` | `*bufio.Writer` | Buffered writer wrapping the log output destination (stdout or a `*os.File`). Flushed after each session write. |
| `logFileOut` | `*os.File` | Non-nil only when writing to a real file (not stdout). Used for `Sync()` calls after each flush so that `tail -f` sees lines immediately. |
| `logMu` | `sync.Mutex` | Guards writes to `logWriter`. Prevents interleaved JSON lines when multiple response goroutines complete concurrently. |

### 3.3 Proxy Runtime State (guarded by `proxyMu`)

All fields in this group are protected by `proxyMu sync.Mutex`.

| Variable | Type | Description |
|---|---|---|
| `proxyMu` | `sync.Mutex` | Guards all proxy runtime state below. |
| `proxyServer` | `*http.Server` | The running `http.Server` instance. Set by `startProxy`, cleared by `stopProxy`. Used to call `Shutdown()` for graceful teardown. |
| `proxyRunning` | `bool` | Whether the proxy is currently accepting connections. Checked in signal handlers before start/stop operations. |
| `proxyStartTime` | `time.Time` | Wall clock time when the proxy was most recently started. Currently stored but not exposed in any output (available for future status reporting). |

### 3.4 Session Storage (guarded by `sessionsMu`)

All fields in this group are protected by `sessionsMu sync.Mutex`.

| Variable | Type | Description |
|---|---|---|
| `sessionsMu` | `sync.Mutex` | Guards session store reads and writes. |
| `sessionCounter` | `int64` | Monotonically increasing counter for unique session IDs. Incremented atomically via `atomic.AddInt64` — does not require `sessionsMu` for writes. |
| `sessions` | `[]*Session` | Slice of captured sessions, ordered oldest first. New sessions are appended; when `maxSessions` is reached, the oldest entry (`sessions[0]`) is dropped. |

---

## 4. Types

### 4.1 `ctxKey`

```go
type ctxKey string
```

A named string type used as a context key to avoid collisions with other packages that might store values in `context.Context`. Using a named type (rather than a bare string) ensures type-safety: only code in this package can retrieve values stored under keys of this type.

### 4.2 `Session`

```go
type Session struct {
    ID        int64         `json:"id"`
    Timestamp time.Time     `json:"timestamp"`
    Duration  jsonDuration  `json:"duration"`
    Request   *RequestInfo  `json:"request"`
    Response  *ResponseInfo `json:"response,omitempty"`
}
```

The core data structure representing one complete HTTP exchange. Created in `captureRequest` (request-only metadata), finalized in `captureResponse` (response metadata, duration), then serialized to the log.

**Fields:**

| Field | Type | JSON | Set By | Description |
|---|---|---|---|---|
| `ID` | `int64` | `"id"` | `captureRequest` | Unique session identifier from `nextSessionID()`. |
| `Timestamp` | `time.Time` | `"timestamp"` (RFC3339) | `captureRequest` | When the request was received by the proxy. |
| `Duration` | `jsonDuration` | `"duration"` (e.g. `"1.2s"`) | `captureResponse` | Wall-clock time from request receipt to response capture. Zero until the response arrives. |
| `Request` | `*RequestInfo` | `"request"` | `captureRequest` | Request metadata (method, URL, headers, body). Never nil for a valid session. |
| `Response` | `*ResponseInfo` | `"response"`, omitted when nil | `captureResponse` | Response metadata (status, headers, body). Nil until the upstream response arrives. `omitempty` keeps the JSON clean for incomplete sessions. |

### 4.3 `RequestInfo`

```go
type RequestInfo struct {
    Method  string            `json:"method"`
    URL     string            `json:"url"`
    Proto   string            `json:"proto"`
    Headers map[string]string `json:"headers"`
    Body    string            `json:"body,omitempty"`
    Size    int64             `json:"size"`
}
```

Captured metadata for one HTTP request.

**Fields:**

| Field | Type | JSON | Set By | Description |
|---|---|---|---|---|
| `Method` | `string` | `"method"` | `r.Method` | HTTP method (GET, POST, PUT, DELETE, etc.). |
| `URL` | `string` | `"url"` | `r.URL.String()` | Full request URL including scheme and host, e.g. `http://example.com/path?q=1`. |
| `Proto` | `string` | `"proto"` | `r.Proto` | Protocol version, e.g. `"HTTP/1.1"`, `"HTTP/2"`. |
| `Headers` | `map[string]string` | `"headers"` | `captureHeaders(r.Header)` | HTTP headers as a flat map. Multi-valued headers are joined with `", "`. Header names are canonicalized by Go's `net/http`. |
| `Body` | `string` | `"body"`, omitted when empty | `captureBody(r.Body)` | Request body content. Empty for methods without a body (GET, HEAD, etc.). Truncated at `maxBodySize` with a truncation note. |
| `Size` | `int64` | `"size"` | `captureBody` | Original body size in bytes **before** truncation. Useful even when `Body` is empty (`Size = 0`). |

### 4.4 `ResponseInfo`

```go
type ResponseInfo struct {
    StatusCode int               `json:"status_code"`
    Proto      string            `json:"proto"`
    Headers    map[string]string `json:"headers"`
    Body       string            `json:"body,omitempty"`
    Size       int64             `json:"size"`
}
```

Captured metadata for one HTTP response. Mirror of `RequestInfo` with `StatusCode` instead of `Method`/`URL`.

**Fields:**

| Field | Type | JSON | Set By | Description |
|---|---|---|---|---|
| `StatusCode` | `int` | `"status_code"` | `resp.StatusCode` | HTTP status code (200, 404, 500, etc.). |
| `Proto` | `string` | `"proto"` | `resp.Proto` | Response protocol version. |
| `Headers` | `map[string]string` | `"headers"` | `captureHeaders(resp.Header)` | Response headers. Same join semantics as `RequestInfo.Headers`. |
| `Body` | `string` | `"body"`, omitted when empty | `captureBody(resp.Body)` | Response body content. Truncated at `maxBodySize`. |
| `Size` | `int64` | `"size"` | `captureBody` | Original body size in bytes before truncation. |

### 4.5 `jsonDuration`

```go
type jsonDuration time.Duration
```

A wrapper around `time.Duration` that serializes to/from human-readable strings instead of nanosecond integers.

**Methods:**

| Method | Signature | Description |
|---|---|---|
| `MarshalJSON` | `(d jsonDuration) ([]byte, error)` | Formats the duration via `time.Duration(d).String()` so JSON output is `"350ms"`, `"1.2s"`, `"5m30s"` instead of a large integer. |
| `String` | `(d jsonDuration) string` | Convenience — returns the same human-readable format without JSON marshaling. Delegates to `time.Duration(d).String()`. |

---

## 5. Constants

### 5.1 `sessionIDKey`

```go
const sessionIDKey ctxKey = "sessionID"
```

Type: `ctxKey` (which is `string`-based).

Used as the context key to pass a session ID from `captureRequest` to `captureResponse` through the `*http.Request`'s context. goproxy guarantees the same `*http.Request` pointer reaches the `OnResponse` handler, so the value placed in context by `captureRequest` is available when `captureResponse` runs.

Stored via `context.WithValue(r.Context(), sessionIDKey, sess.ID)` and retrieved via `r.Context().Value(sessionIDKey).(int64)`.

---

## 6. Functions

### 6.1 `main()`

```go
func main()
```

**Role**: Entry point. Parses CLI flags, loads optional CA cert, opens log output, prints status to stderr, then blocks on signals.

**Flow:**

1. **Parse flags** — defines and parses `-port`, `-log-file`, `-max-sessions`, `-max-body-size`, `-cert`, `-key`.
2. **Validate CA** — if either `-cert` or `-key` is given, both are required; load via `tls.LoadX509KeyPair`.
3. **Open log output** — if `-log-file` is set, create/truncate that file and wrap in `bufio.Writer`; otherwise wrap `os.Stdout`.
4. **Print PID + instructions** — written to stderr so the JSON log stream on stdout is not polluted.
5. **Signal setup (start/stop)** — creates `usrCh`, registers `SIGUSR1`/`SIGUSR2` via `signal.Notify`, launches a goroutine to handle them.
6. **Signal setup (quit)** — creates `quitCh`, registers `SIGINT`/`SIGTERM`, blocks on `<-quitCh`.
7. **Shutdown** — calls `stopProxy()`, flushes and closes the log writer/file.

**Important**: Status/progress messages go to stderr (`fmt.Fprintf(os.Stderr, ...)`) so they never interleave with the JSON Lines output on stdout.

### 6.2 `startProxy(port int) error`

```go
func startProxy(port int) error
```

**Role**: Creates and starts the goproxy MITM proxy on the given port.

**Must NOT be called while holding `proxyMu`** — it acquires the lock internally.

**Flow:**

1. Acquires `proxyMu`.
2. Calls `newCaptureProxy()` to create the goproxy instance with capture hooks.
3. If `customCA` is set, assigns it to `goproxy.GoproxyCa` (the package-level CA variable goproxy checks when generating certs for HTTPS interception).
4. Calls `net.Listen("tcp", addr)` to open the TCP listener.
5. Creates `*http.Server` with the proxy as the handler (no explicit timeouts — client and server control their own).
6. Stores the server, sets `proxyRunning = true`, updates `proxyPort` and `proxyStartTime`.
7. Launches `srv.Serve(ln)` in a **background goroutine**.
8. Returns nil on success, or an error if the TCP listener fails.

**Error handling**: If `net.Listen` fails (e.g. port already in use), the mutex is released (via `defer`) and an error is returned. The caller prints the error to stderr.

### 6.3 `stopProxy()`

```go
func stopProxy()
```

**Role**: Gracefully shuts down the running proxy. Safe to call when the proxy is not running (no-op).

**Flow:**

1. Acquires `proxyMu`.
2. If `proxyServer != nil`, calls `proxyServer.Shutdown(ctx)` with a 5-second timeout, then sets `proxyServer = nil`.
3. Sets `proxyRunning = false`.

**Thread safety**: Double-stop protection — if called when no proxy is running, `proxyServer` is nil and `Shutdown` is never called. The 5-second timeout prevents indefinite hangs on long-lived connections.

### 6.4 `newCaptureProxy()`

```go
func newCaptureProxy() *goproxy.ProxyHttpServer
```

**Role**: Factory function that creates a `goproxy.ProxyHttpServer` configured with request/response capture hooks.

**Configuration:**

- `proxy.Verbose = false` — silences goproxy's own debug logging (capture and reporting happen through the log file).

**Hooks registered:**

| Hook | Handler | Purpose |
|---|---|---|
| `OnRequest().DoFunc` | `captureRequest` | Fires for every HTTP request (including CONNECT tunnels). Captures metadata, stores session, returns `(r, nil)` to forward upstream. |
| `OnResponse().DoFunc` | `captureResponse` | Fires when the upstream response arrives. Captures response metadata, computes duration, writes session to log. Returns `resp` to forward downstream. |

**Note on CONNECT**: For HTTPS traffic, goproxy first receives a CONNECT request, establishes a tunnel, then forwards the encrypted traffic through the tunnel. The `OnRequest` handler captures the CONNECT request, and `OnResponse` fires when the tunnel setup completes. The actual HTTPS request/response inside the tunnel is intercepted by goproxy's TLS MITM layer, which generates a separate series of `OnRequest`/`OnResponse` calls.

### 6.5 `captureRequest(r *http.Request)`

```go
func captureRequest(r *http.Request)
```

**Role**: Creates a new `Session` from the incoming HTTP request, stores it in the in-memory session store, and stashes the session ID in the request context for later correlation.

**Flow:**

1. Calls `nextSessionID()` to get a unique ID.
2. Creates a `Session` with:
   - `Timestamp = time.Now()`
   - `Request` populated from `r.Method`, `r.URL.String()`, `r.Proto`, and `captureHeaders(r.Header)`.
3. If the request has a body (`r.Body != nil && r.Body != http.NoBody`), calls `captureBody(r.Body)` to read, truncate, and replace the body reader.
4. Stashes the session ID in the request context:
   ```go
   *r = *r.WithContext(context.WithValue(r.Context(), sessionIDKey, sess.ID))
   ```
   The pointer reassignment is required because `WithContext` returns a new `*http.Request`.
5. Calls `addSession(sess)` to store the session in memory (where `captureResponse` will find it).

**Key design**: The request body is fully buffered in memory (via `io.ReadAll` inside `captureBody`). The original body bytes are restored as a fresh `io.NopCloser` so goproxy can forward the untouched body upstream.

### 6.6 `captureResponse(r *http.Request, resp *http.Response)`

```go
func captureResponse(r *http.Request, resp *http.Response)
```

**Role**: Finds the session matching the request, captures response details, and writes the completed session to the log.

**Flow:**

1. Extracts session ID from `r.Context().Value(sessionIDKey).(int64)`.
2. If ID not found in context (should not happen in normal operation), returns silently.
3. Acquires `sessionsMu`, calls `findSession(id)`, releases `sessionsMu`.
4. If session not found (e.g., evicted from store due to `maxSessions`), returns silently.
5. Creates `ResponseInfo` from `resp.StatusCode`, `resp.Proto`, `captureHeaders(resp.Header)`.
6. If the response has a body, calls `captureBody(resp.Body)` to read, truncate, and restore.
7. Sets `sess.Response` and `sess.Duration`.
8. Calls `writeSessionToLog(sess)` to emit the complete session as a JSON line.

**Key design**: The `sessionsMu` lock is released before calling `writeSessionToLog` to avoid lock contention between the log mutex and the session store mutex.

### 6.7 `captureHeaders(h http.Header) map[string]string`

```go
func captureHeaders(h http.Header) map[string]string
```

**Role**: Converts Go's `http.Header` (`map[string][]string`) to a flat `map[string]string` by joining multi-valued header entries with `", "`.

**Example**: `"Accept": ["text/html", "application/json"]` → `"Accept": "text/html, application/json"`.

**Edge cases**: Works correctly with nil or empty headers — returns an empty map. The map is pre-allocated with `len(h)` capacity.

### 6.8 `captureBody(reader io.ReadCloser) (captured string, originalSize int64, restored io.ReadCloser)`

```go
func captureBody(reader io.ReadCloser) (captured string, originalSize int64, restored io.ReadCloser)
```

**Role**: Reads the full body content, returns a truncated capture string and a fresh reader for the proxy to forward.

**Three return values:**

| Return | Type | Description |
|---|---|---|
| `captured` | `string` | Body content, truncated at `maxBodySize` with a `[truncated — N bytes total, showing first M]` suffix appended. Empty string for nil readers. |
| `originalSize` | `int64` | Total body size in bytes before truncation. |
| `restored` | `io.ReadCloser` | A new `io.NopCloser(bytes.NewBuffer(bodyBytes))` wrapping the **full** original bytes. The proxy uses this to forward the complete body upstream/downstream. |

**Flow:**

1. If `reader` is nil, returns `("", 0, nil)`.
2. Calls `io.ReadAll(reader)` to buffer the entire body.
3. Closes the original reader.
4. On read error, returns `("", 0, io.NopCloser(bytes.NewReader(nil)))` — a safe empty reader so the proxy doesn't receive a nil body.
5. If body exceeds `maxBodySize`, truncates `captured` and appends the truncation note.
6. Returns a fresh reader over the full bytes (`io.NopCloser(bytes.NewBuffer(bodyBytes))`).

**Memory**: The full body lives in memory at least temporarily (the `bodyBytes` slice). For large uploads/downloads, this can be significant. The stored capture is bounded by `maxBodySize`.

### 6.9 `addSession(s *Session)`

```go
func addSession(s *Session)
```

**Role**: Appends a session to the in-memory store, evicting the oldest if at capacity.

**Eviction policy**: FIFO. When `len(sessions) >= maxSessions`, the oldest session (`sessions[0]`) is dropped by creating a new slice `sessions[1:]`. This is O(n) per eviction but at the default cap of 1000 it's negligible. The evicted session is garbage-collected.

**Thread safety**: Acquires `sessionsMu` exclusively.

### 6.10 `findSession(id int64) *Session`

```go
func findSession(id int64) *Session
```

**Role**: Looks up a session by ID. Returns nil if not found.

**Search strategy**: Linear scan from the **end** of the slice (most recent first). Since sessions are stored in ID order and recently captured traffic is most likely to be queried, scanning from the end provides fast average-case lookup.

**Thread safety**: Caller **must** hold `sessionsMu` (read lock). This is a design choice — the caller is responsible for locking, and `captureResponse` needs to release the lock before calling `writeSessionToLog` (to avoid lock ordering issues), so the lock is held around the `findSession` call and released after the nil check.

### 6.11 `nextSessionID() int64`

```go
func nextSessionID() int64
```

**Role**: Returns a unique, monotonically increasing session identifier.

**Implementation**: Uses `atomic.AddInt64(&sessionCounter, 1)`. This is lock-free and safe for concurrent calls from multiple goroutines.

**Range**: Starts at 1 (zero value of int64 + 1 on first call). Wraps around after 9.2 quintillion sessions — effectively never.

### 6.12 `writeSessionToLog(s *Session)`

```go
func writeSessionToLog(s *Session)
```

**Role**: Marshals a completed session as a single JSON line and writes it to the configured log output.

**Flow:**

1. Calls `json.Marshal(s)` to produce compact JSON (not indented — JSON Lines convention is one compact object per line).
2. On marshal error, logs to stderr via `log.Printf` and returns (session is lost).
3. Acquires `logMu`.
4. Writes the JSON string + newline via `fmt.Fprintln(logWriter, ...)`.
5. Flushes the buffered writer.
6. If writing to a file (`logFileOut != nil`), calls `logFileOut.Sync()` to ensure the data reaches disk immediately. This makes `tail -f` see new lines without delay.
7. Releases `logMu`.

**Thread safety**: The `logMu` mutex prevents interleaved writes when multiple responses complete concurrently. Without it, two goroutines calling `writeSessionToLog` simultaneously could produce interleaved characters on the same output line.

---

## 7. Thread Safety Model

The program has three concurrent contexts:

1. **Signal handler goroutine** — handles SIGUSR1/SIGUSR2 (single goroutine, one signal at a time)
2. **Proxy server goroutines** — one per incoming request/response pair (goproxy spawns goroutines for each connection)
3. **Main goroutine** — blocks on SIGINT/SIGTERM, then performs shutdown

| Data | Guard | Accessed By | Pattern |
|---|---|---|---|
| `proxyServer`, `proxyRunning`, `proxyStartTime` | `proxyMu` | Signal handler goroutine, main goroutine | Full lock/unlock in signal handler before calling `startProxy`/`stopProxy`; those functions acquire the lock internally. |
| `sessions`, `sessionCounter` | `sessionsMu` + atomic | Proxy goroutines | `sessionCounter` uses `atomic.AddInt64` (no mutex). `sessions` slice uses `sessionsMu` for both reads and writes. `captureResponse` releases the lock before calling `writeSessionToLog`. |
| `logWriter`, `logFileOut` | `logMu` | Proxy goroutines (via `captureResponse` → `writeSessionToLog`) | Full lock/unlock per write. |
| `customCA`, `proxyPort`, `maxBodySize`, `maxSessions` | None (read-mostly) | Everywhere | Written once in `main()`, then read-only. `proxyPort` is also written in `startProxy` under `proxyMu`, but reads are only from the signal handler which also holds (or serializes with) `proxyMu`. |

**Lock ordering**: No explicit ordering is enforced, but the design ensures `logMu` is never acquired while holding `sessionsMu` (`captureResponse` releases `sessionsMu` before calling `writeSessionToLog`).

---

## 8. JSON Output Format

Each line of output is a compact JSON object representing one completed `Session`:

```json
{"id":1,"timestamp":"2026-06-13T10:30:00.123456Z","duration":"450ms","request":{"method":"GET","url":"http://example.com/","proto":"HTTP/1.1","headers":{"Host":"example.com","User-Agent":"curl/8.0"},"size":0},"response":{"status_code":200,"proto":"HTTP/1.1","headers":{"Content-Type":"text/html","Content-Length":"1256"},"body":"<!doctype html>\n<html>...","size":1256}}
```

**Pretty-printed for readability:**

```json
{
  "id": 1,
  "timestamp": "2026-06-13T10:30:00.123456Z",
  "duration": "450ms",
  "request": {
    "method": "GET",
    "url": "http://example.com/",
    "proto": "HTTP/1.1",
    "headers": {
      "Host": "example.com",
      "User-Agent": "curl/8.0"
    },
    "body": "",
    "size": 0
  },
  "response": {
    "status_code": 200,
    "proto": "HTTP/1.1",
    "headers": {
      "Content-Type": "text/html",
      "Content-Length": "1256"
    },
    "body": "<!doctype html>\n<html>...",
    "size": 1256
  }
}
```

### Notes on the format:

- **JSON Lines (NDJSON)**: One JSON object per line, no trailing comma between objects. The file is not a JSON array — each line is a self-contained JSON object.
- **`duration`**: Always a string (e.g. `"450ms"`, `"2.3s"`, `"1m5s"`). Uses `time.Duration.String()` formatting.
- **`timestamp`**: RFC 3339 format with nanosecond precision (Go's default `time.Time` JSON encoding).
- **`body`**: Omitted from the JSON (`omitempty`) when empty/zero-length. This applies to GET requests (`size: 0`) and responses with no body (e.g., 204 No Content).
- **`response`**: Omitted from the JSON (`omitempty`) when the response has not yet been captured. In current code, `writeSessionToLog` is only called from `captureResponse`, so this should always be present in the log.

---

## 9. Extension / Enhancement Points

This section identifies the most natural places to add or modify behavior, ordered from easiest to most involved.

### 9.1 Additional metadata capture

- **Add timing breakdown**: `captureResponse` already has `time.Since(sess.Timestamp)` for total duration. To add DNS lookup time, TLS handshake time, or TTFB, you would need to start a timer in `captureRequest` and record intermediate timestamps.
- **Add request source info**: `captureRequest` receives `*http.Request` which has `r.RemoteAddr`. Add it to `RequestInfo`.
- **Add TLS info**: `r.TLS` on the request contains the TLS connection state (cipher suite, protocol version, cert info).

### 9.2 Filtering / selective capture

- **Skip certain URLs**: In `captureRequest`, check `r.URL.Host` or `r.URL.Path` against an allow/deny list and skip `addSession` (and skip stashing the ID in context) to avoid capturing noise.
- **Body capture on demand**: Add a flag to skip body capture entirely and only capture headers/metadata.

### 9.3 Output format alternatives

- **Structured logging to a different format**: Replace `json.Marshal` in `writeSessionToLog` with a different encoder (CSV, protobuf, custom fields).
- **Multiple output destinations**: Add a flag to output to both stdout and a file, or to write request-only and response-only summaries to separate streams.
- **Rotating log files**: Replace `os.Create` in `main()` with a rotating file writer (e.g., `lumberjack` or custom rotation).

### 9.4 Rate limiting / backpressure

- **Backpressure on body capture**: `captureBody` calls `io.ReadAll` which buffers the entire body in memory. For large payloads, add streaming with a bounded read (e.g., `io.LimitReader`), or a `maxBodySize` limit at the `io.ReadAll` level to avoid unbounded memory growth. Note: the code already truncates the **stored** body at `maxBodySize`, but the full body is still read into memory before truncation.

### 9.5 HTTPS MITM tuning

- **Custom CA with flags**: Already supported via `-cert` and `-key`. The CA is injected into `goproxy.GoproxyCa` before the proxy starts.
- **Skip specific hosts**: goproxy supports `proxy.OnRequest().HandleConnect()` to bypass MITM for specific hostnames (e.g., pinned certificates).

### 9.6 Signal handling

- **Add SIGHUP**: Could trigger log rotation, config reload, or status dump.
- **Add SIGINFO/SIGQUIT**: Could dump current session count or memory stats to stderr.
- **Windows support**: `syscall.SIGUSR1`/`SIGUSR2` are Unix-only. To support Windows, add named pipes or a simple HTTP admin endpoint for lifecycle control.

### 9.7 goproxy alternatives

The code uses `github.com/elazarl/goproxy` v1.8.4, which:
- Is no longer actively maintained (last release 2023)
- Has known limitations with HTTP/2 (proxied connections are downgraded to HTTP/1.1)
- Generates TLS certificates on-the-fly with configurable CA

Alternative proxy libraries to consider for enhancement:
- `github.com/mitmproxy/mitmproxy-go` — requires separate mitmproxy process
- Custom `http.Handler` with `net/http/httputil.ReverseProxy` — more control but requires implementing MITM manually

### 9.8 Plugin / callback architecture

The current architecture is tightly coupled (all code in one file, all capture logic inline in goproxy callbacks). To add a plugin system:

1. Extract `Session`, `RequestInfo`, `ResponseInfo` into a shared package (e.g., `pkg/httptrace/types.go`).
2. Define an interface for capture sinks (e.g., `SessionSink { Write(*Session) error }`).
3. Implement the JSON Lines writer and in-memory store as `SessionSink` implementations.
4. Add additional sinks (e.g., Elasticsearch, statsd, WebSocket push) by implementing the interface.

---

## Appendix: goproxy Integration Notes

### How goproxy intercepts HTTPS

1. Client sends `CONNECT example.com:443` to the proxy.
2. `OnRequest` fires with the CONNECT request — `captureRequest` stores it.
3. goproxy responds `200 Connection established` and starts a TCP tunnel.
4. If the destination is HTTPS (TLS), goproxy impersonates the server by:
   - Generating a certificate signed by `goproxy.GoproxyCa` (either auto-generated or custom).
   - Terminating the TLS connection from the client.
   - Opening a new TLS connection to the actual server.
5. The decrypted HTTP request from the client goes through `OnRequest` again.
6. The response from the server goes through `OnResponse`.
7. `captureResponse` fires for each intercepted HTTP exchange inside the tunnel.

### Package-level CA variable

`goproxy.GoproxyCa` is a package-level `tls.Certificate` in the goproxy package. It must be set **before** the first HTTPS connection is handled. Setting it after connections are already being served can lead to race conditions.

---

*Generated from `cmd/httptrace/main.go` — keep in sync with code changes.*
