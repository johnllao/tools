# httptail — Reference Documentation

> **Files**: `cmd/httptail/main.go`, `ino_unix.go`, `ino_other.go` (package `main`)
>
> **Purpose**: Standalone HTTP service that tails a log file and streams new
> lines to connected browsers via Server-Sent Events (SSE). Serves a React
> frontend from disk. Controlled via SIGINT/SIGTERM for graceful shutdown.

---

## Table of Contents

1. [Overview & Data Flow](#1-overview--data-flow)
2. [Imports / Dependencies](#2-imports--dependencies)
3. [Package-Level State](#3-package-level-state)
   - [Configuration](#31-configuration-set-once-at-startup)
   - [File Watcher State](#32-file-watcher-state-guarded-by-filemu)
   - [SSE Client Registry](#33-sse-client-registry-guarded-by-clientsmu)
   - [History Ring Buffer](#34-history-ring-buffer-guarded-by-historymu)
4. [Types](#4-types)
   - [`connectedEvent`](#41-connectedevent)
5. [Functions](#5-functions)
   - [Entry Point](#51-main)
   - [File Polling](#52-pollfile--checkandreadfile--statfile)
   - [History Ring Buffer](#53-inithistory--appendtohistory--gethistory--readlastnlines)
   - [SSE Handler](#54-ssehandler)
   - [SSE Broadcaster](#55-broadcastline)
   - [SSE Helpers](#56-escapersse)
   - [Platform-Specific Helpers](#57-fileino--fileinoimpl)
6. [Thread Safety Model](#6-thread-safety-model)
7. [SSE Protocol Format](#7-sse-protocol-format)
8. [File Rotation Handling](#8-file-rotation-handling)
9. [Extension / Enhancement Points](#9-extension--enhancement-points)

---

## 1. Overview & Data Flow

```
┌──────────────────┐     ┌──────────────────────┐     ┌─────────────────┐
│  External Writer │────▶│  Log File on Disk    │     │                 │
│  (app, syslog,   │     │  (/var/log/app.log   │     │                 │
│   etc.)          │     └──────────┬───────────┘     │                 │
└──────────────────┘                │ poll every      │                 │
                                    │ 200 ms          │                 │
                                    ▼                 │                 │
                          ┌──────────────────┐        │                 │
                          │  File Poller     │        │  Browser(s)     │
                          │  (checkAndRead-  │        │  (React SPA)    │
                          │   File)          │        │                 │
                          └────────┬─────────┘        │                 │
                                   │ line             │                 │
                                   ▼                  │                 │
                          ┌──────────────────┐        │                 │
                          │  Ring Buffer     │───────▶│  GET /events    │
                          │  (history)       │  SSE   │  (EventSource)  │
                          └────────┬─────────┘        │                 │
                                   │ line             │                 │
                                   ▼                  │                 │
                          ┌──────────────────┐        │                 │
                          │  SSE Broadcaster │───────▶│                 │
                          │  (fan-out to al  │        │                 │
                          │   client chans   │        │                 │
                          └──────────────────┘        │                 │
                                                      │                 │
                          ┌──────────────────┐        │                 │
                          │  HTTP Server     │        │                 │
                          │  /       → Reac  │───────▶│  GET /          │
                          │  /events → SSE   │        │  (static files) │
                          └──────────────────┘        └─────────────────┘
```

### Lifecycle:

1. **Process starts** — parses flags, validates `-input` file exists, validates `-frontend` directory contains `index.html`, preloads history buffer with last N lines of the log file
2. **HTTP server starts** — listens on `-port` (default 8080), serves React frontend from `-frontend` directory, provides `/events` SSE endpoint
3. **File poller starts** — background goroutine ticks every 200 ms, reads new bytes from the log file, splits into lines, appends to ring buffer, broadcasts to all SSE clients
4. **Browser connects** to `http://host:port/` → loads React SPA → opens `EventSource("/events")`
5. **SSE handshake**: server sends history lines as `data:` events, then a named `connected` event with metadata (`logPath`, `clientCount`)
6. **Streaming**: each new log line is broadcast as a `data:` SSE event to all connected clients
7. **SIGINT/SIGTERM** → cancel poller → close all client channels → `srv.Shutdown` with 5s timeout → close watched file → exit

---

## 2. Imports / Dependencies

### Standard Library (main.go)

| Import | Used By | Purpose |
|---|---|---|
| `context` | `main`, `pollFile`, `sseHandler` | `context.WithCancel` for poller lifecycle; `context.WithTimeout` for graceful shutdown; `r.Context().Done()` for client disconnect detection |
| `encoding/json` | `sseHandler` | `json.Marshal` — serializes `connectedEvent` for the SSE metadata payload |
| `flag` | `main` | CLI flag parsing (`-input`, `-port`, `-tail-lines`, `-frontend`) |
| `fmt` | Throughout | Status messages to stderr, SSE event formatting via `fmt.Fprintf` |
| `io` | `checkAndReadFile`, `readLastNLines` | `io.SeekStart`, `io.EOF` — file seeking and read-completion detection |
| `log` | `checkAndReadFile`, `main`, `sseHandler` | Go standard logger (writes to stderr) for internal errors during polling and shutdown |
| `net/http` | `main`, `sseHandler` | `http.Server`, `http.ServeMux`, `http.ResponseWriter`, `http.Request`, `http.Flusher`, `http.FileServer`, `http.Dir` — HTTP serving, routing, SSE streaming, static file serving |
| `os` | `main`, `checkAndReadFile`, `readLastNLines`, `statFile` | `os.Open`, `os.Stat`, `os.Stderr`, `os.Stdout`, `os.Exit`, `os.Getpid`, `os.File`, `os.FileInfo`, `os.Signal` |
| `os/signal` | `main` | `signal.Notify` — registers OS signal delivery to channel |
| `path/filepath` | `main` | `filepath.Abs`, `filepath.Join` — resolves frontend path to absolute, verifies `index.html` exists |
| `strings` | `checkAndReadFile`, `readLastNLines`, `escapeSSE` | `strings.Split` (line splitting), `strings.HasSuffix` (partial-line detection), `strings.Index` (newline search), `strings.ReplaceAll` (SSE escaping) |
| `sync` | Throughout | `sync.Mutex` (fileMu, historyMu), `sync.RWMutex` (clientsMu) — guards shared state |
| `syscall` | `main` | `syscall.SIGINT`, `syscall.SIGTERM` — Unix signal constants |
| `time` | `main`, `pollFile` | `time.NewTicker` (200ms polling interval), `time.Second` (graceful shutdown timeout) |

### Standard Library (ino_unix.go)

| Import | Used By | Purpose |
|---|---|---|
| `os` | `fileInoImpl` | `os.FileInfo` parameter type |
| `syscall` | `fileInoImpl` | `syscall.Stat_t` — type assertion on `info.Sys()` to extract `Ino` field |

### Standard Library (ino_other.go)

| Import | Used By | Purpose |
|---|---|---|
| `os` | `fileInoImpl` | `os.FileInfo` parameter type (Windows builds only) |

### Third-Party

**None.** The service uses only Go standard library packages — no external dependencies beyond the Go toolchain.

---

## 3. Package-Level State

State is organized into four logical groups, each with its own owning mutex where applicable. All variables are declared at package scope in `main.go`.

### 3.1 Configuration (set once at startup)

All four variables are set in `main()` from CLI flags and never modified afterward (except `frontendDir`, which is resolved to an absolute path once).

| Variable | Type | Flag | Default | Description |
|---|---|---|---|---|
| `inputFile` | `string` | `-input` | (required) | Absolute or relative path to the log file being tailed. Validated at startup: must exist and must not be a directory. |
| `port` | `int` | `-port` | `8080` | TCP port for the HTTP server to listen on. |
| `tailLines` | `int` | `-tail-lines` | `50` | Number of recent lines to keep in the history ring buffer. These are sent to each new SSE client on connect. Also determines the ring buffer capacity. |
| `frontendDir` | `string` | `-frontend` | `web/http-tail/dist` | Path to the built React frontend directory. Resolved to an absolute path at startup via `filepath.Abs`. Must contain an `index.html` file (validated). |

**Thread safety**: Read-only after startup (with the exception of `frontendDir` being resolved once before any goroutines start), so no mutex is needed.

### 3.2 File Watcher State (guarded by `fileMu`)

All fields in this group are protected by `fileMu sync.Mutex`.

| Variable | Type | Description |
|---|---|---|
| `fileMu` | `sync.Mutex` | Exclusive lock guarding all file-watcher fields. Held across the entire `checkAndReadFile` call to serialize poll ticks — only one poll runs at a time. |
| `currentFile` | `*os.File` | Open file handle to the log file being polled. Nil when the file has not been opened yet or when the file is temporarily missing (e.g. during rotation). Reopened on the next poll tick when the file reappears. |
| `currentSize` | `int64` | Number of bytes read from `currentFile` so far. Used as the start offset for the next `ReadAt` call — only new bytes beyond this point are read. Reset to 0 on rotation or truncation. |
| `currentIno` | `uint64` | Inode number of `currentFile` at the time it was opened. Compared to the live inode on each poll to detect file rotation (create mode). Zero on Windows and other platforms without inode support. |

### 3.3 SSE Client Registry (guarded by `clientsMu`)

All fields in this group are protected by `clientsMu sync.RWMutex`.

| Variable | Type | Description |
|---|---|---|
| `clientsMu` | `sync.RWMutex` | Read-write mutex guarding the clients map. `broadcastLine` acquires a **read** lock (many concurrent broadcasts). `sseHandler` and `main` acquire a **write** lock when adding or removing clients. |
| `clients` | `map[chan string]struct{}` | Set of active SSE client channels. Each channel is buffered (capacity 64) to absorb brief slowdowns without blocking the broadcaster. The value type `struct{}` uses zero memory — the map is used purely as a set. |

### 3.4 History Ring Buffer (guarded by `historyMu`)

All fields in this group are protected by `historyMu sync.Mutex`.

| Variable | Type | Description |
|---|---|---|
| `historyMu` | `sync.Mutex` | Exclusive lock guarding the ring-buffer fields. Writers (`appendToHistory`) and readers (`getHistory`) both acquire this lock. |
| `historyRing` | `[]string` | Fixed-size slice used as a ring buffer. Capacity is set to `tailLines` at startup by `initHistory`. Each slot holds one log line. |
| `historyPos` | `int` | Next write position in the ring (0 to `len(historyRing)-1`). Increments on each append; wraps to 0 when it reaches capacity. |
| `historyFull` | `bool` | False until `historyPos` wraps around at least once. Used by `getHistory` to determine whether the ring has one segment (not yet full) or two segments (full, needs stitching). |

---

## 4. Types

### 4.1 `connectedEvent`

```go
type connectedEvent struct {
    LogPath     string `json:"logPath"`
    ClientCount int    `json:"clientCount"`
}
```

A lightweight struct serialized to JSON and sent as a named SSE `connected` event after the initial history batch.

**Fields:**

| Field | Type | JSON | Set By | Description |
|---|---|---|---|---|
| `LogPath` | `string` | `logPath` | `main`-level `inputFile` variable | The absolute or relative path of the log file being tailed, as provided via `-input`. Displayed in the React frontend header. |
| `ClientCount` | `int` | `clientCount` | `len(clients)` after adding this client | Number of currently connected SSE clients (including this one). Displayed in the React frontend status indicator when > 1. |

**Usage**: Constructed and marshaled inline in `sseHandler`:
```go
meta, _ := json.Marshal(connectedEvent{
    LogPath:     inputFile,
    ClientCount: clientCount,
})
fmt.Fprintf(w, "event: connected\ndata: %s\n\n", string(meta))
```

The `json.Marshal` error is discarded (assigned to `_`) because marshaling this simple struct with only string and int fields cannot fail.

---

## 5. Functions

### 5.1 `main()`

```go
func main()
```

**Role**: Entry point. Parses CLI flags, validates inputs, initializes state, starts the HTTP server and file poller, blocks on signals, and orchestrates graceful shutdown.

**Flow:**

1. **Parse flags** — defines `-input`, `-port`, `-tail-lines`, `-frontend` via `flag.StringVar` / `flag.IntVar`; calls `flag.Parse()`.
2. **Validate `-input`** — if empty, prints usage to stderr and `os.Exit(1)`. Calls `os.Stat(inputFile)` to verify the file exists and is not a directory. On error, prints to stderr and `os.Exit(1)`.
3. **Preload history** — calls `initHistory(tailLines)` to allocate the ring buffer, then `readLastNLines(inputFile, tailLines)` to populate it with the most recent lines. Prints count to stderr.
4. **Validate `-frontend`** — resolves `frontendDir` to an absolute path via `filepath.Abs`. Checks that `filepath.Join(frontendDir, "index.html")` exists. On failure, prints instructions to build the frontend and `os.Exit(1)`.
5. **Set up routes** — creates `http.NewServeMux()`, registers `/events` → `sseHandler`, registers `/` → `http.FileServer(http.Dir(frontendDir))`.
6. **Start HTTP server** — creates `*http.Server` with `Addr: ":<port>"` and no timeouts (SSE connections are long-lived). Launches `srv.ListenAndServe()` in a background goroutine. Prints listening address, watched file, PID, and frontend path to stderr.
7. **Start file poller** — creates a `context.WithCancel`, launches `pollFile(ctx)` in a background goroutine.
8. **Block on signal** — creates `quitCh`, registers `SIGINT`/`SIGTERM` via `signal.Notify`, blocks on `<-quitCh`.
9. **Graceful shutdown:**
   - Prints "shutting down..." to stderr
   - Calls `cancel()` to stop the file poller goroutine
   - Acquires `clientsMu` write lock, closes all client channels, replaces `clients` with an empty map, releases lock. This unblocks any `sseHandler` goroutines blocked on channel reads.
   - Calls `srv.Shutdown(shutdownCtx)` with a 5-second timeout to drain in-flight HTTP connections
   - Acquires `fileMu`, closes `currentFile` if non-nil, releases lock
   - Prints "stopped" to stderr

**Important**: All human-readable status/progress messages go to stderr (`fmt.Fprintf(os.Stderr, ...)`) so they do not interleave with any piped output. Errors during validation cause `os.Exit(1)` after printing to stderr.

### 5.2 `pollFile(ctx)` / `checkAndReadFile()` / `statFile()`

#### `pollFile(ctx context.Context)`

```go
func pollFile(ctx context.Context)
```

**Role**: Runs the polling loop. Creates a `time.NewTicker(200ms)`, then loops calling `checkAndReadFile()` on each tick until `ctx.Done()` fires. The ticker is stopped via `defer ticker.Stop()` when the function returns.

**Goroutine context**: Runs in a background goroutine launched from `main()`. Stopped via `cancel()` during shutdown.

#### `checkAndReadFile()`

```go
func checkAndReadFile()
```

**Role**: The core poll logic. Opens/rotates/reopens the log file, reads new bytes since the last poll, splits them into lines, appends to history, and broadcasts to SSE clients. Called from `pollFile` on each tick.

**Flow:**

1. **Acquires `fileMu`** — holds the lock for the entire call (all file state access is serialized).
2. **Stats the file** via `statFile(inputFile)`. If `os.Stat` fails (file gone), closes `currentFile`, sets it to nil, and returns — retry on next tick.
3. **Opens or rotates the file:**
   - **First open**: If `currentFile == nil`, opens the file via `os.Open`. Sets `currentSize = size` — only new lines written after startup are streamed (existing file content is only served via `readLastNLines` preload).
   - **Inode change** (rotation create mode): If `ino != currentIno && currentIno != 0`, closes the old handle, opens the new file, resets `currentSize = 0` to read the entire new file. Prints rotation notice to stderr.
   - **Truncation** (rotation copytruncate mode): If `size < currentSize`, seeks to the beginning (`currentFile.Seek(0, io.SeekStart)`), resets `currentSize = 0`. Prints truncation notice to stderr.
4. **Reads new bytes**: Computes `delta = size - currentSize`. If `delta <= 0`, returns (no new data). Allocates a `[]byte` of size `delta`, calls `currentFile.ReadAt(buf, currentSize)`. On error (other than `io.EOF`), logs and returns.
5. **Splits into lines**: Converts the read bytes to a string, splits on `"\n"`.
6. **Handles partial final line**: If the text does not end with `"\n"`, the last element is a partial line (the writer is mid-line). Removes it from the line batch and rewinds `currentSize` by the partial line's length so it is re-read on the next tick.
7. **Dispatches each complete line**: For each line that is not the trailing empty element from a final `\n` split, calls `appendToHistory(line)` then `broadcastLine(line)`. Blank lines between non-blank lines are preserved.

**Edge cases handled:**
- **File missing on tick**: Closes old handle, returns silently, retries on next tick
- **File recreated after deletion**: Detected as "first open" on a subsequent tick when `currentFile == nil`
- **In-progress write (partial line)**: Partial final line is deferred — `currentSize` is rewound so it is re-read (with its completion) on the next tick
- **Empty file**: `size <= currentSize` check handles this — no read attempted
- **Large single write**: The buffer is allocated to exactly `delta` bytes. For a single very large write (e.g. 100 MB dump), this allocates 100 MB in one tick — acceptable for log tailing use cases

#### `statFile(path string) (ino uint64, size int64, err error)`

```go
func statFile(path string) (ino uint64, size int64, err error)
```

**Role**: Wraps `os.Stat` and extracts the inode number from the `FileInfo`. Separates the platform-specific inode extraction from the polling logic.

| Return | Type | Description |
|---|---|---|
| `ino` | `uint64` | Inode number from `fileIno(info)`. Zero on Windows and when extraction fails. |
| `size` | `int64` | File size in bytes from `info.Size()`. |
| `err` | `error` | Error from `os.Stat` (e.g. file not found). |

### 5.3 `initHistory()` / `appendToHistory()` / `getHistory()` / `readLastNLines()`

#### `initHistory(capacity int)`

```go
func initHistory(capacity int)
```

**Role**: Allocates the ring buffer and resets position/fullness state. Must be called once before any calls to `appendToHistory` or `getHistory`.

**Thread safety**: Acquires `historyMu` exclusively.

**Behavior**: Sets `historyRing = make([]string, capacity)`, `historyPos = 0`, `historyFull = false`.

#### `appendToHistory(line string)`

```go
func appendToHistory(line string)
```

**Role**: Adds a single line to the ring buffer, overwriting the oldest entry when the buffer is full.

**Thread safety**: Acquires `historyMu` exclusively.

**Algorithm**: Writes `line` at `historyRing[historyPos]`, increments `historyPos`. If `historyPos` reaches `len(historyRing)`, wraps to 0 and sets `historyFull = true`. O(1) time, O(1) space.

#### `getHistory() []string`

```go
func getHistory() []string
```

**Role**: Returns a copy of all history lines in chronological order (oldest first). The returned slice is a copy — mutations to the ring buffer after return do not affect it.

**Thread safety**: Acquires `historyMu` exclusively.

**Algorithm:**

- **Before first wrap** (`!historyFull`): Returns a copy of `historyRing[0:historyPos]` — these are already in chronological order.
- **After wrap** (`historyFull`): The ring has two chronological segments:
  1. `historyRing[historyPos:]` — the oldest entries (written before the wrap)
  2. `historyRing[:historyPos]` — the newest entries (written after the wrap)
  
  Allocates a new slice and appends segment 1 then segment 2 to produce a chronologically ordered result. O(n) time and space where n = `tailLines`.

#### `readLastNLines(path string, n int) int`

```go
func readLastNLines(path string, n int) int
```

**Role**: Reads the last ~1 MB of the file, extracts up to `n` lines from the end, and pushes them into the history ring buffer via `appendToHistory`. Returns the actual number of lines loaded.

**Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `path` | `string` | Path to the log file. Must exist and be readable. |
| `n` | `int` | Maximum number of lines to load into history. |

**Returns**: Number of lines actually loaded (may be less than `n` if the file is shorter).

**Algorithm:**

1. Opens the file, gets its size via `f.Stat()`.
2. Computes read window: last `maxTailBytes` (1 MB) of the file, or the whole file if it's smaller.
3. Seeks to `fileSize - readSize` and reads that chunk via `f.ReadAt`.
4. If the read started mid-line (`offset > 0`), drops the first partial line (everything before the first `\n`).
5. Splits on `"\n"`, drops the trailing empty element from a final newline.
6. Takes at most the last `n` lines.
7. Pushes each line into history via `appendToHistory`.

**Memory**: Allocates up to `readSize` bytes (max 1 MB) for the read buffer, plus the resulting `[]string` slice. The 1 MB window is a heuristic — it covers millions of typical log lines. If a single line exceeds 1 MB, only its tail portion will be captured.

**Error handling**: On any error (file open failure, stat failure, read error other than `io.EOF`), returns 0 silently. The poller will open the file on the next tick.

### 5.4 `sseHandler(w, r)`

```go
func sseHandler(w http.ResponseWriter, r *http.Request)
```

**Role**: Implements the `GET /events` SSE endpoint. Creates a buffered channel for the client, registers it in the clients map, sends the current history as a batch of `data:` events, sends a named `connected` event with metadata, then enters a streaming loop forwarding new log lines from the channel as SSE `data:` events. Cleans up on client disconnect or channel close.

**Flow:**

1. **Flusher check** — type-asserts `w.(http.Flusher)`. If the `ResponseWriter` does not support flushing, returns HTTP 500 (streaming unsupported). This should never fail with Go's standard `net/http` server.
2. **Set SSE headers**:
   - `Content-Type: text/event-stream` — required for EventSource API
   - `Cache-Control: no-cache` — prevents intermediary caching
   - `Connection: keep-alive` — enables persistent connection
   - `X-Accel-Buffering: no` — disables nginx proxy buffering
3. **Create client channel** — `make(chan string, 64)`. A buffer of 64 means the client can survive brief bursts of 64 lines without drops.
4. **Register client** — acquires `clientsMu` write lock, adds `ch` to `clients` map, records `clientCount`, releases lock.
5. **Defer cleanup** — when the function returns, acquires `clientsMu` write lock, deletes `ch` from `clients`, releases lock, and closes `ch`.
6. **Send history** — iterates `getHistory()`, sending each line as `data: <escaped-line>\n\n`. Flushes after the batch.
7. **Send connected event** — marshals `connectedEvent{LogPath: inputFile, ClientCount: clientCount}` to JSON, sends as a named event `event: connected\ndata: <json>\n\n`. This signals to the frontend that the initial batch is complete.
8. **Streaming loop** — `select` on two cases:
   - `line, open := <-ch`: If `open` is false, the channel was closed (server shutdown) — returns. Otherwise sends `data: <escaped-line>\n\n` and flushes.
   - `<-r.Context().Done()`: The HTTP request context was cancelled (client disconnected) — returns.

**Key design details:**
- Each SSE event is explicitly flushed via `flusher.Flush()` so the browser receives it immediately (no buffering).
- Named events use `event: <name>\ndata: <payload>\n\n` format.
- Log lines are escaped via `escapeSSE` before being written to the response.
- The `defer` cleanup pattern ensures clients are always removed from the map even if the function panics or returns early.
- The history is sent *before* the connected event so the frontend can distinguish "history batch complete" from "now receiving live data".

### 5.5 `broadcastLine(line string)`

```go
func broadcastLine(line string)
```

**Role**: Sends a line to every connected SSE client via non-blocking channel send.

**Thread safety**: Acquires `clientsMu` **read** lock (`RLock`). Multiple `broadcastLine` calls can run concurrently (though in practice, `checkAndReadFile` serializes them via `fileMu`). The read lock allows other broadcasters to run simultaneously while preventing writers (`sseHandler` registration/deregistration, `main` shutdown) from modifying the map.

**Non-blocking send semantics**: For each client channel, uses a `select` with a `default` case:
```go
select {
case ch <- line:
    // Line sent successfully.
default:
    // Channel buffer is full — client is too slow.
    // Drop the line silently.
}
```

If a client's channel buffer is full (64 pending lines), the line is silently dropped for that client. This prevents a single slow client from stalling the entire broadcast pipeline. The client's browser EventSource will auto-reconnect (due to connection timeout), at which point the server sends the current history to bring the client back up to date.

### 5.6 `escapeSSE(s string) string`

```go
func escapeSSE(s string) string
```

**Role**: Escapes a log line for safe inclusion in an SSE `data:` field. Ensures that a single log line maps to a single SSE data line regardless of the line's content.

**Transformations:**

| Input | Output | Reason |
|---|---|---|
| `\` | `\\` | Backslash — escape first so subsequent replacements don't re-escape it |
| `\n` | `\\n` | Newline — would otherwise break the SSE data line into multiple lines |
| `\r` | `\\r` | Carriage return — would otherwise break the SSE protocol |

**Usage**: Called for every line sent via SSE — both history lines during initial handshake and live lines in the streaming loop.

### 5.7 `fileIno(info)` / `fileInoImpl(info)`

#### `fileIno(info os.FileInfo) uint64` (main.go)

```go
func fileIno(info os.FileInfo) uint64
```

**Role**: Public wrapper that delegates to the platform-specific `fileInoImpl`. This is the function called by `statFile` — callers do not need build tags.

#### `fileInoImpl(info os.FileInfo) uint64` (ino_unix.go)

```go
//go:build !windows

func fileInoImpl(info os.FileInfo) uint64
```

**Role** (Unix/macOS/Linux): Extracts the inode number from `info.Sys()` via type assertion to `*syscall.Stat_t`. Returns `stat.Ino` on success, 0 on failure (e.g. if the underlying filesystem does not provide inode information).

**Build constraint**: `//go:build !windows` — compiled on all platforms except Windows.

#### `fileInoImpl(info os.FileInfo) uint64` (ino_other.go)

```go
//go:build windows

func fileInoImpl(info os.FileInfo) uint64
```

**Role** (Windows): Always returns 0. On Windows, inode-based rotation detection is not available — only file truncation (size < currentSize) is detected.

**Build constraint**: `//go:build windows` — compiled only on Windows.

---

## 6. Thread Safety Model

The program has four concurrent contexts:

1. **Main goroutine** — runs `main()`, blocks on signal, performs shutdown
2. **File poller goroutine** — runs `pollFile()`, calls `checkAndReadFile()` on each tick
3. **HTTP server goroutines** — Go's `net/http` spawns one goroutine per incoming HTTP connection. SSE connections are long-lived goroutines inside `sseHandler`.
4. **SSE handler goroutines** — each active `/events` connection runs its own `sseHandler` goroutine in its streaming loop

| Data | Guard | Accessed By | Pattern |
|---|---|---|---|
| `inputFile`, `port`, `tailLines`, `frontendDir` | None (read-only after startup) | All goroutines | Set once in `main()` before any goroutines start. `frontendDir` is resolved once before goroutine launch. |
| `currentFile`, `currentSize`, `currentIno` | `fileMu` (exclusive) | File poller goroutine, main goroutine (shutdown) | Full lock held for entire `checkAndReadFile` call. Main acquires lock only during shutdown to close the file. |
| `clients` map | `clientsMu` (RWMutex) | SSE handlers (write for add/remove), broadcaster (read for fan-out), main (write for shutdown) | `broadcastLine` uses `RLock` (many concurrent readers). Registration, deregistration, and shutdown use full `Lock`. |
| `historyRing`, `historyPos`, `historyFull` | `historyMu` (exclusive) | File poller (write via `appendToHistory`), SSE handlers (read via `getHistory`) | Short critical sections — lock is held only for the duration of slice read/write operations. |

**Lock ordering**: No explicit lock ordering is enforced, but the design naturally prevents deadlocks:
- `fileMu` is never held while acquiring `historyMu` or `clientsMu` — `checkAndReadFile` calls `appendToHistory` and `broadcastLine` only after releasing `fileMu`? **Correction**: `checkAndReadFile` holds `fileMu` for its entire duration, including calls to `appendToHistory` (acquires `historyMu`) and `broadcastLine` (acquires `clientsMu.RLock`). This means the lock chain is `fileMu` → `historyMu` and `fileMu` → `clientsMu`. But `getHistory` (called from `sseHandler`) only acquires `historyMu`, and `sseHandler` registration only acquires `clientsMu` — neither acquires `fileMu`, so no deadlock is possible.
- `clientsMu` is never held while acquiring `historyMu` or vice versa — these are independent subsystems.

---

## 7. SSE Protocol Format

### Event types

The server sends two kinds of SSE events:

#### 1. Unnamed `data:` events (log lines)

Sent for each line in the history buffer and for each new live line.

```
data: 2026-06-15T10:30:00.123Z INFO Server started on port 8080

```

Format: `data: <escaped-line>\n\n`

The double newline (`\n\n`) terminates the SSE event. Lines are escaped via `escapeSSE` so that embedded newlines, carriage returns, and backslashes within the log line content do not break the SSE framing.

#### 2. Named `connected` event (metadata)

Sent once per connection, after all history lines have been sent.

```
event: connected
data: {"logPath":"/var/log/app.log","clientCount":3}

```

Format:
```
event: connected
data: <json-payload>
<blank line>
```

**JSON payload schema** (`connectedEvent`):

| Field | Type | Description |
|---|---|---|
| `logPath` | `string` | Path to the log file being tailed, as provided via `-input` |
| `clientCount` | `number` | Number of currently connected SSE clients (including this new connection) |

### Headers

| Header | Value | Purpose |
|---|---|---|
| `Content-Type` | `text/event-stream` | Required by the EventSource API specification |
| `Cache-Control` | `no-cache` | Prevents intermediary proxies and browsers from caching the event stream |
| `Connection` | `keep-alive` | Keeps the TCP connection open for the long-lived stream |
| `X-Accel-Buffering` | `no` | Disables response buffering in nginx reverse proxies |

### Client reconnection

The browser's `EventSource` API automatically reconnects with exponential backoff when the connection drops. On each new connection, the server:
1. Sends the current history buffer (last `tailLines` lines)
2. Sends a `connected` event with current metadata
3. Begins streaming new lines

This means a reconnecting client never misses content — it gets the most recent `tailLines` lines on reconnect, covering any gap during the disconnection period.

---

## 8. File Rotation Handling

The poller detects two common log rotation strategies and handles each gracefully:

### Create mode (logrotate default)

`logrotate` renames the current file (e.g. `app.log` → `app.log.1`) and creates a new `app.log`. A new inode is assigned to the new file.

**Detection**: `ino != currentIno && currentIno != 0` on a poll tick.

**Response**: Close the old file handle (pointing to the renamed file's inode), open the new file, reset `currentSize = 0`, start reading the new file from the beginning. A message is printed to stderr.

**Caveat**: Between the rename and the creation of the new file, the path `inputFile` may briefly not exist. This is handled gracefully — `os.Stat` fails, `currentFile` is set to nil, and the poller retries on the next tick. When the new file appears, it is opened as a "first open."

### Copytruncate mode

`logrotate` copies the current file contents to a backup, then truncates the original file to zero length. The inode remains the same.

**Detection**: `size < currentSize` on a poll tick.

**Response**: Seek to the beginning of the file (`io.SeekStart`), reset `currentSize = 0`, start re-reading from byte 0. A message is printed to stderr.

### File deletion without recreation

If the file is deleted and never recreated, the poller simply logs nothing and retries on each tick indefinitely. The file handle is closed each time `os.Stat` fails.

---

## 9. Extension / Enhancement Points

This section identifies the most natural places to add or modify behavior, ordered from easiest to most involved.

### 9.1 Configuration and deployment

- **Add `-host` flag**: Currently the server binds to all interfaces (`:port`). Add a `-host` flag to bind to a specific address (e.g. `127.0.0.1`).
- **Embed the frontend**: Replace `http.Dir` serving with `//go:embed` for a single-binary deployment. Requires building the frontend into `cmd/httptail/dist/` (or using a different embed path strategy) before compilation.
- **Add `-no-frontend` flag**: Allow running without a frontend directory for headless/API-only deployments where only the `/events` endpoint is needed.
- **Add `-read-only` flag**: Open the log file in read-only mode explicitly, though `os.Open` already does this.

### 9.2 Polling behavior

- **Configurable poll interval**: Replace the hardcoded 200ms with a `-poll-interval` flag. Shorter intervals provide lower latency at the cost of higher CPU usage.
- **Use fsnotify instead of polling**: Replace the `time.Ticker` loop with `github.com/fsnotify/fsnotify` for event-driven file watching. This eliminates the 200ms latency and reduces CPU usage, at the cost of an external dependency. The `checkAndReadFile` logic can be reused — just call it from the fsnotify event handler instead of the ticker.
- **Multi-file watching**: Extend the `-input` flag to accept a glob pattern or multiple paths. Would require one `fileMu`-guarded state set per watched file, plus a multiplexing broadcaster that tags each line with its source file.

### 9.3 History buffer

- **Configurable history size per client**: Currently all clients receive the same `tailLines` count. Add a query parameter to `/events` (e.g. `?lines=200`) to let clients request more or fewer history lines.
- **Time-based history window**: Instead of (or in addition to) a line-count limit, add a time-based eviction: drop lines older than N minutes from the ring buffer.
- **Search/filter in history**: Add a `/events?filter=<regex>` query parameter that filters both history and live lines before sending to the client.

### 9.4 SSE features

- **Named event types**: Currently all log lines are sent as unnamed `data:` events. If the log format is structured (JSON Lines), parse each line and emit different named events per log level (`event: info`, `event: error`, etc.) for richer frontend display.
- **Heartbeat/keepalive**: Send a periodic SSE comment line (`: heartbeat\n\n`) to keep the connection alive through proxies and load balancers that may close idle connections. Add a `time.Ticker` in `sseHandler` that sends a comment every 15–30 seconds.
- **Last-Event-ID**: Support the `Last-Event-ID` HTTP header that browsers send on EventSource reconnect. Assign each line a monotonically increasing ID so reconnecting clients can request only lines they missed. This is more bandwidth-efficient than re-sending the entire history buffer.
- **Client count broadcast**: Broadcast the updated `clientCount` to all clients whenever a client connects or disconnects, so the frontend always shows an accurate count without polling.

### 9.5 Frontend enhancements

- **Colorized log levels**: Parse each line for log level keywords (ERROR, WARN, INFO, DEBUG) and apply CSS classes for color-coding. Requires the SSE data to include level metadata (see §9.4), or client-side regex matching.
- **Search/filter UI**: Add a search bar to the React frontend that filters displayed lines client-side. Add a "highlight" mode for matching terms.
- **Export**: Add a button to download the current log buffer as a text file.
- **Dark/light theme toggle**: Add a theme switcher that toggles the CSS custom properties between dark and light variants.

### 9.6 Security

- **Authentication**: Add an optional `-auth-token` flag. If set, require a matching `Authorization: Bearer <token>` header on the `/events` endpoint. Return HTTP 401 otherwise.
- **TLS**: Add `-cert` and `-key` flags to serve over HTTPS instead of plain HTTP. Use `http.ListenAndServeTLS` or `srv.ListenAndServeTLS` with the provided certificate.
- **CORS**: Add `Access-Control-Allow-Origin` headers if the frontend is served from a different origin than the SSE endpoint. Currently this is unnecessary because both are served from the same origin.
- **Path traversal protection**: The static file server (`http.FileServer`) includes built-in path traversal protection via `http.Dir`. The `-input` flag path is validated only at startup — consider adding symlink resolution to prevent tailing unintended files.

### 9.7 Observability

- **Metrics**: Expose a `/metrics` endpoint (or add periodic stderr output) with gauges: number of connected clients, poll latency, lines per second, bytes read, file open/rotation count.
- **Health check**: Add a `/health` endpoint that returns HTTP 200 if the poller is running and the file is accessible, 503 otherwise.
- **Structured logging**: Replace `fmt.Fprintf(os.Stderr, ...)` calls with a structured logging library or at minimum add timestamps to stderr messages for debugging production issues.

### 9.8 Architectural refactoring

- **Extract poller to a reusable package**: Move `pollFile`, `checkAndReadFile`, `statFile`, the ring buffer, and the broadcaster into `pkg/httptail/` or `internal/httptail/`. This would allow the logic to be reused by other binaries (e.g., a CLI `tail -f` replacement, or a WebSocket-based variant).
- **WebSocket support**: Add a `/ws` endpoint using `github.com/gorilla/websocket` (or the standard library's upcoming WebSocket support) as an alternative to SSE. WebSocket enables bidirectional communication (e.g., client-sent filter commands) and has broader browser support.
- **Plugin system for line processing**: Define a `LineProcessor` interface (`Process(line string) string`) and allow registering processors via CLI flags or a config file. Processors could add timestamps, mask sensitive data, parse JSON, or enrich lines with metadata before broadcasting.

---

## Appendix: Platform Compatibility Notes

### Inode detection

| Platform | Inode available | Rotation detection |
|---|---|---|
| Linux | Yes (`syscall.Stat_t.Ino`) | Full (inode change + size change) |
| macOS | Yes (`syscall.Stat_t.Ino`) | Full (inode change + size change) |
| Windows | No (returns 0) | Size change only (truncation) |

On Windows, the create-mode rotation pattern (rename old file, create new file) is not detected as a rotation — the poller continues reading from the old file handle until it reaches EOF, then the size comparison on subsequent ticks will not see growth (the new file has a different handle). However, if the old file is deleted (which triggers `os.Stat` failure), the poller will close the handle and reopen the path on the next tick, effectively detecting the rotation.

### File locking

The poller opens the log file with `os.Open` (read-only). This does not acquire any filesystem lock — the file can be written to by other processes concurrently. This is intentional for log tailing use cases. On Windows, file sharing modes may prevent opening a file that another process has open for writing; this is not currently handled and would require `CreateFile` with specific sharing flags via `syscall` on Windows.

---

*Generated from `cmd/httptail/main.go`, `ino_unix.go`, `ino_other.go` — keep in sync with code changes.*
