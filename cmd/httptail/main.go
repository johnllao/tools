// Command httptail is an HTTP service that tails a log file and streams new
// lines to connected browsers via Server-Sent Events (SSE).
//
// It watches the file specified by -input for new lines using a polling
// approach (200 ms interval), keeps a ring buffer of recent lines for new
// clients, and broadcasts each new line to all connected SSE clients. The
// React frontend is served from disk via the -frontend directory.
//
// Usage:
//
//	httptail -input /var/log/app.log [-port 8080] [-tail-lines 50] [-frontend <dir>]
//
// The server runs until it receives SIGINT (Ctrl+C) or SIGTERM, then
// gracefully shuts down.
//
// Examples:
//
//	httptail -input /var/log/app.log
//	httptail -input /var/log/app.log -port 9090 -tail-lines 100
//	httptail -input /tmp/test.log -frontend web/http-tail/dist
//	go run ./cmd/httptail -input /tmp/test.log
//
// Frontend development:
//
//	cd web/http-tail && npm run dev    # Vite dev server with HMR
//	cd web/http-tail && npm run build  # build for production
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// --- Configuration (set once from CLI flags, read-only after startup) ---------

var (
	// inputFile is the path to the log file being tailed.
	inputFile string
	// port is the HTTP listen port.
	port int
	// tailLines is the number of recent lines to send to each new SSE client.
	tailLines int
	// frontendDir is the path to the built React frontend directory.
	frontendDir string
)

// --- File watcher state (guarded by fileMu) -----------------------------------

var (
	// fileMu guards all file-state fields.
	fileMu sync.Mutex
	// currentFile is the open handle to the log file being polled.
	currentFile *os.File
	// currentSize tracks how many bytes have been read from the current file.
	currentSize int64
	// currentIno is the inode number of the current file, used to detect
	// rotation. It is zero on platforms that do not report inode numbers.
	currentIno uint64
)

// --- SSE client registry (guarded by clientsMu) -------------------------------

var (
	// clientsMu guards the clients map.
	clientsMu sync.RWMutex
	// clients is the set of active SSE client channels. Each channel is
	// buffered to avoid blocking the broadcaster on a single slow client.
	clients = make(map[chan string]struct{})
)

// --- History ring buffer (guarded by historyMu) -------------------------------

var (
	// historyMu guards the ring-buffer fields.
	historyMu sync.Mutex
	// historyRing is a fixed-size slice used as a ring buffer for recent lines.
	historyRing []string
	// historyPos is the next write position in the ring.
	historyPos int
	// historyFull is true once the ring has wrapped around at least once.
	historyFull bool
)

// --- Flag parsing and main ----------------------------------------------------

// main is the entry point. It parses CLI flags, validates the input file,
// preloads the history buffer, starts the HTTP server with the frontend and
// the SSE endpoint, and blocks until SIGINT or SIGTERM, then shuts down
// gracefully.
func main() {
	flag.StringVar(&inputFile, "input", "", "Path to the log file to tail (required)")
	flag.IntVar(&port, "port", 8080, "HTTP listen port")
	flag.IntVar(&tailLines, "tail-lines", 50, "Number of recent lines to send on each new SSE connection")
	flag.StringVar(&frontendDir, "frontend", "web/http-tail/dist", "Path to the built React frontend directory")
	flag.Parse()

	// The -input flag is required.
	if inputFile == "" {
		fmt.Fprintf(os.Stderr, "httptail: missing required flag -input\n")
		fmt.Fprintf(os.Stderr, "Usage: httptail -input <log-file> [-port <port>] [-tail-lines <n>] [-frontend <dir>]\n")
		os.Exit(1)
	}

	// Verify the input file exists and is readable.
	info, err := os.Stat(inputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "httptail: cannot access input file %q: %s\n", inputFile, err)
		os.Exit(1)
	}
	if info.IsDir() {
		fmt.Fprintf(os.Stderr, "httptail: input %q is a directory, not a file\n", inputFile)
		os.Exit(1)
	}

	// Preload the history buffer with the last N lines of the file so new
	// SSE clients get immediate context on connect.
	initHistory(tailLines)
	n := readLastNLines(inputFile, tailLines)
	fmt.Fprintf(os.Stderr, "httptail: loaded %d recent lines from %s\n", n, inputFile)

	// Resolve the frontend directory to an absolute path and verify it
	// contains an index.html (i.e. looks like a built Vite project).
	absFrontend, err := filepath.Abs(frontendDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "httptail: cannot resolve frontend path %q: %s\n", frontendDir, err)
		os.Exit(1)
	}
	frontendDir = absFrontend
	if _, err := os.Stat(filepath.Join(frontendDir, "index.html")); err != nil {
		fmt.Fprintf(os.Stderr, "httptail: frontend not found at %s — run 'cd web/http-tail && npm run build' first\n", frontendDir)
		os.Exit(1)
	}

	// Set up HTTP routes:
	//   /         – React frontend (static files from disk)
	//   /events   – SSE endpoint for log streaming
	mux := http.NewServeMux()
	mux.HandleFunc("/events", sseHandler)
	mux.Handle("/", http.FileServer(http.Dir(frontendDir)))

	// Start the HTTP server in a background goroutine.
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
		// No ReadTimeout / WriteTimeout — SSE connections are long-lived.
	}
	go func() {
		fmt.Fprintf(os.Stderr, "httptail: listening on :%d, watching %s (PID %d)\n",
			port, inputFile, os.Getpid())
		fmt.Fprintf(os.Stderr, "  Frontend: %s\n", frontendDir)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	// Start the background file poller.
	ctx, cancel := context.WithCancel(context.Background())
	go pollFile(ctx)

	// Block until SIGINT (Ctrl+C) or SIGTERM, then gracefully shut down.
	quitCh := make(chan os.Signal, 1)
	signal.Notify(quitCh, syscall.SIGINT, syscall.SIGTERM)
	<-quitCh

	// Shut down gracefully: stop the poller, close all client channels,
	// and drain in-flight HTTP connections.
	fmt.Fprintf(os.Stderr, "\nhttptail: shutting down...\n")
	cancel()

	clientsMu.Lock()
	for ch := range clients {
		close(ch)
	}
	clients = make(map[chan string]struct{})
	clientsMu.Unlock()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("httptail: shutdown error: %s", err)
	}

	// Close the watched file handle if open.
	fileMu.Lock()
	if currentFile != nil {
		currentFile.Close()
		currentFile = nil
	}
	fileMu.Unlock()

	fmt.Fprintf(os.Stderr, "httptail: stopped\n")
}

// --- File polling -------------------------------------------------------------

// pollFile runs a polling loop (200 ms interval) that reads new bytes from
// inputFile, splits them into lines, and broadcasts each line to all connected
// SSE clients. It stops when ctx is cancelled.
//
// The poller is resilient to file rotation and deletion:
//   - If the file's inode changes, it closes the old handle and opens the new
//     one (logrotate create mode).
//   - If the file is truncated (size < currentSize), it resets the read
//     position to the beginning (logrotate copytruncate mode).
//   - If the file is temporarily missing, it logs the error and retries on
//     the next tick.
func pollFile(ctx context.Context) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkAndReadFile()
		}
	}
}

// checkAndReadFile opens the input file if not already open, detects rotation
// or truncation, reads any new bytes since the last read, splits them into
// lines, appends each line to the history ring buffer, and broadcasts them to
// SSE clients.
func checkAndReadFile() {
	fileMu.Lock()
	defer fileMu.Unlock()

	// Stat the file to detect rotation / truncation / disappearance.
	ino, size, err := statFile(inputFile)
	if err != nil {
		// File may be temporarily gone (log rotation). Close the old
		// handle and retry on the next tick.
		if currentFile != nil {
			currentFile.Close()
			currentFile = nil
		}
		return
	}

	// Open the file on first poll, or reopen if the inode changed (rotation)
	// or the file shrunk (copytruncate).
	if currentFile == nil {
		f, openErr := os.Open(inputFile)
		if openErr != nil {
			return
		}
		currentFile = f
		currentIno = ino
		currentSize = size // start at end — only new lines are streamed
		return
	}

	if ino != currentIno && currentIno != 0 {
		// Inode changed — the file was rotated (create mode). Close the
		// old handle and open the new file.
		currentFile.Close()
		f, openErr := os.Open(inputFile)
		if openErr != nil {
			currentFile = nil
			return
		}
		currentFile = f
		currentIno = ino
		currentSize = 0 // read from the beginning of the new file
		fmt.Fprintf(os.Stderr, "httptail: detected file rotation, reopened %s\n", inputFile)
	} else if size < currentSize {
		// File truncated (copytruncate rotation). Reset to the beginning.
		currentFile.Seek(0, io.SeekStart)
		currentSize = 0
		fmt.Fprintf(os.Stderr, "httptail: detected file truncation, resetting offset for %s\n", inputFile)
	}

	// If no new bytes, nothing to do.
	if size <= currentSize {
		return
	}

	// Read only the new bytes since the last poll.
	delta := size - currentSize
	buf := make([]byte, delta)
	n, readErr := currentFile.ReadAt(buf, currentSize)
	if readErr != nil && readErr != io.EOF {
		log.Printf("httptail: read error: %s", readErr)
		return
	}
	currentSize += int64(n)

	// Split the new chunk into lines. A partial final line (no trailing \n)
	// is kept and prepended to the next read.
	text := string(buf[:n])
	lines := strings.Split(text, "\n")

	// The last element may be a partial line if the file write was not
	// newline-terminated (e.g. an in-progress write). Defer it: rewind
	// currentSize so that next poll re-reads from the partial-line start.
	if len(lines) > 0 && !strings.HasSuffix(text, "\n") {
		partial := lines[len(lines)-1]
		lines = lines[:len(lines)-1]
		currentSize -= int64(len(partial))
	}

	for _, line := range lines {
		if line == "" && len(lines) > 1 {
			// Preserve blank lines between non-blank lines (keep
			// the empty string). Skip only the trailing empty
			// element from a final newline split.
			appendToHistory(line)
			broadcastLine(line)
		} else if line != "" || len(lines) == 1 {
			appendToHistory(line)
			broadcastLine(line)
		}
		// If line is "" and len(lines) > 1, this is the trailing
		// empty element after a final \n — skip it.
	}
}

// statFile returns the inode number (0 on unsupported platforms), file size,
// and any error from os.Stat.
func statFile(path string) (ino uint64, size int64, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0, err
	}
	return fileIno(info), info.Size(), nil
}

// --- History ring buffer ------------------------------------------------------

// initHistory allocates the ring buffer with the given capacity.
func initHistory(capacity int) {
	historyMu.Lock()
	defer historyMu.Unlock()
	historyRing = make([]string, capacity)
	historyPos = 0
	historyFull = false
}

// appendToHistory adds a line to the ring buffer, overwriting the oldest entry
// once the buffer is full.
func appendToHistory(line string) {
	historyMu.Lock()
	defer historyMu.Unlock()
	historyRing[historyPos] = line
	historyPos++
	if historyPos >= len(historyRing) {
		historyPos = 0
		historyFull = true
	}
}

// getHistory returns the current history lines in chronological order (oldest
// first).
func getHistory() []string {
	historyMu.Lock()
	defer historyMu.Unlock()

	if !historyFull {
		// Before the first wrap, only the first historyPos slots are
		// populated and they are already in chronological order.
		out := make([]string, historyPos)
		copy(out, historyRing[:historyPos])
		return out
	}

	// After wrapping, the ring has two segments: [pos .. end] (oldest) and
	// [0 .. pos-1] (newest). Stitch them together in chronological order.
	out := make([]string, 0, len(historyRing))
	out = append(out, historyRing[historyPos:]...)
	out = append(out, historyRing[:historyPos]...)
	return out
}

// readLastNLines reads the last ~1 MB of the file, splits it into lines, and
// pushes up to n lines into the history ring buffer. It returns the actual
// number of lines loaded.
//
// The 1 MB window is a heuristic that covers most log files; if a single log
// line is longer than 1 MB, only the tail portion will be captured.
func readLastNLines(path string, n int) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return 0
	}

	// Read at most the last 1 MB of the file.
	const maxTailBytes int64 = 1 << 20 // 1 MB
	fileSize := info.Size()
	readSize := maxTailBytes
	if fileSize < readSize {
		readSize = fileSize
	}

	offset := fileSize - readSize
	buf := make([]byte, readSize)
	_, err = f.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return 0
	}

	text := string(buf)
	// Drop a leading partial line (the first line may be truncated if we
	// started reading mid-line).
	if offset > 0 {
		if idx := strings.Index(text, "\n"); idx >= 0 {
			text = text[idx+1:]
		}
	}

	lines := strings.Split(text, "\n")
	// Drop trailing empty element from final newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}

	for _, line := range lines {
		appendToHistory(line)
	}
	return len(lines)
}

// --- SSE handler --------------------------------------------------------------

// sseHandler implements the GET /events endpoint. It sets SSE headers, creates
// a buffered channel for this client, sends the current history buffer as a
// batch of data: events, emits a named connected event with metadata, and then
// streams new log lines as they arrive. It cleans up when the client
// disconnects.
func sseHandler(w http.ResponseWriter, r *http.Request) {
	// Verify the response writer supports flushing — required for SSE.
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Create a buffered channel for this client. A capacity of 64 means
	// the client can fall up to 64 lines behind before lines are dropped.
	ch := make(chan string, 64)

	clientsMu.Lock()
	clients[ch] = struct{}{}
	clientCount := len(clients)
	clientsMu.Unlock()

	// Clean up when the client disconnects.
	defer func() {
		clientsMu.Lock()
		delete(clients, ch)
		clientsMu.Unlock()
		close(ch)
	}()

	// Send the history buffer first so the client has immediate context.
	for _, line := range getHistory() {
		fmt.Fprintf(w, "data: %s\n\n", escapeSSE(line))
	}
	flusher.Flush()

	// Send a named "connected" event with metadata so the frontend knows
	// the initial batch is complete.
	meta, _ := json.Marshal(connectedEvent{
		LogPath:     inputFile,
		ClientCount: clientCount,
	})
	fmt.Fprintf(w, "event: connected\ndata: %s\n\n", string(meta))
	flusher.Flush()

	// Stream new lines until the client disconnects or the server shuts down.
	for {
		select {
		case line, open := <-ch:
			if !open {
				// Channel was closed (server shutdown).
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", escapeSSE(line))
			flusher.Flush()
		case <-r.Context().Done():
			// Client disconnected.
			return
		}
	}
}

// connectedEvent is the JSON payload sent to new SSE clients after the history
// batch, providing metadata about the tail session.
type connectedEvent struct {
	LogPath     string `json:"logPath"`
	ClientCount int    `json:"clientCount"`
}

// escapeSSE ensures the line text is safe for the SSE data: field. It replaces
// embedded newlines and carriage returns with their escaped forms so that a
// single log line maps to a single SSE data line.
func escapeSSE(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	return s
}

// --- SSE broadcaster ----------------------------------------------------------

// broadcastLine sends a line to every connected SSE client. It uses non-blocking
// sends so that a single slow client cannot stall the entire broadcast. If a
// client's channel buffer is full, the line is silently dropped for that client
// — the client's EventSource will reconnect and receive the current history.
func broadcastLine(line string) {
	clientsMu.RLock()
	defer clientsMu.RUnlock()

	for ch := range clients {
		select {
		case ch <- line:
			// Line sent successfully.
		default:
			// Channel buffer is full — the client is too slow.
			// Drop the line; the client will catch up via history
			// when the EventSource auto-reconnects.
		}
	}
}

// --- Platform-specific helpers ------------------------------------------------

// fileIno extracts the inode number from a file's FileInfo. It is implemented
// in the platform-specific files (ino_unix.go, ino_other.go).
func fileIno(info os.FileInfo) uint64 {
	return fileInoImpl(info)
}
