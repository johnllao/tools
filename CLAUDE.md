# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A multi-language project that builds MCP (Model Context Protocol) stdio servers and standalone services. Each command in `cmd/` is a Go binary — MCP servers speak the MCP stdio protocol to provide tools to an MCP client (such as Claude Code), while standalone services (like `httptrace`) run independently with their own interface. Python equivalents live under `scripts/py/`, CLI utilities under `examples/`, and a web console UI under `web/console/`.

### Languages & frameworks

- **Go** — Primary MCP servers and CLI tools, using `github.com/mark3labs/mcp-go` v0.54.1
- **Python** — MCP servers using `mcp` (FastMCP) under `scripts/py/`
- **JavaScript** — Web console using React + Vite under `web/console/`

## Quick Reference

```sh
# Build all Go commands
go build ./cmd/...

# Build everything (including examples)
go build ./...

# Run all tests (none yet — add them under cmd/, examples/, or test/)
go test ./...

# Vet all Go code for suspicious constructs
go vet ./...

# Format all Go code
gofmt -w .

# Tidy module dependencies
go mod tidy

# Run Go foldermcp
go run ./cmd/foldermcp -root /path/to/folder

# Run Go logsmcp
go run ./cmd/logsmcp -root /path/to/logs

# Run Python foldermcp
python scripts/py/foldermcp.py --root /path/to/folder
# or via mcp CLI:
mcp run scripts/py/foldermcp.py --root /path/to/folder

# Run httptrace (standalone HTTP/HTTPS MITM proxy — signal-controlled)
go run ./cmd/httptrace -port 8080 &              # start, then:
kill -SIGUSR1 $PID                                # start capturing
kill -SIGUSR2 $PID                                # stop capturing
tail -f trace.jsonl | jq '.request.url'          # inspect traffic live

# Run httptail (log file tailing via HTTP + SSE, with React frontend)
# Build the frontend first:
cd web/http-tail && npm run build && cd ../..
go run ./cmd/httptail -input /var/log/app.log -port 8080

# Run the rediscmd CSV→Redis generator
go run ./examples/rediscmd -input data.csv -prefix "STATIC:CUSTOMER:"

# Run the redisutil stdin→Redis executor
echo "SET foo bar" | go run ./examples/redisutil -host localhost -port 6379

# Start web console dev server
cd web/console && npm run dev

# Build web console for production
cd web/console && npm run build

# Lint web console
cd web/console && npm run lint

# Start http-tail dev server
cd web/http-tail && npm run dev

# Build http-tail for production
cd web/http-tail && npm run build

# Lint http-tail
cd web/http-tail && npm run lint
```

## Commands

| Command / Script | Directory | Description |
|---|---|---|
| `foldermcp` (Go) | `cmd/foldermcp/` | Provides a `read_file` tool to read files from a designated root folder (with path traversal protection) |
| `logsmcp` | `cmd/logsmcp/` | Provides `list_log_files` and `read_logs` tools to browse and filter JSON-structured log files by level, tail, search text, and limit |
| `httptrace` | `cmd/httptrace/` | Fiddler-like HTTP/HTTPS MITM proxy — signal-controlled (SIGUSR1 start, SIGUSR2 stop), writes captured sessions as JSON Lines to stdout or `-log-file` |
| `httptail` | `cmd/httptail/` | HTTP log tailing service — polls a log file and streams new lines to browsers via SSE; serves a React frontend from disk |
| `foldermcp` (Python) | `scripts/py/foldermcp.py` | Python FastMCP equivalent — provides `list_files` and `read_file` tools, with path traversal protection, shell-style glob patterns, and human-readable file sizes |
| `rediscmd` | `examples/rediscmd/` | Reads a CSV file and prints Redis `HSET` + `SADD` commands for each row, with an index-set pattern |
| `redisutil` | `examples/redisutil/` | Reads Redis commands line-by-line from stdin and executes them against a Redis server via the universal `Do()` interface |
| `console` | `web/console/` | React + Vite web UI with dark theme, 2-column layout (sidebar navigator + content area), and OpenSans fonts |
| `http-tail` | `web/http-tail/` | React + Vite web UI for httptail — SSE connection to `/events`, auto-scrolling log display, pause/resume, and clear |

## Code Architecture

```
cmd/              — Main packages; each subdirectory is a Go binary
  foldermcp/        File-reading MCP server (Go)
  logsmcp/          Log-browsing MCP server (Go)
  httptrace/        HTTP/HTTPS MITM proxy for traffic inspection (Go, standalone)
  httptail/         HTTP log tailing service with SSE streaming (Go, standalone)
examples/         — Standalone CLI tools (not MCP servers)
  rediscmd/         CSV → Redis HSET/SADD command generator
  redisutil/        stdin → Redis command executor (universal Do() interface)
scripts/          — Helper scripts
  py/               Python scripts
    foldermcp.py      Python FastMCP equivalent of Go foldermcp
web/              — Front-end web applications
  console/          React + Vite web UI with dark theme and sidebar layout
    src/              React app source
      main.jsx         App entry point with 2-column layout (sidebar + content)
      styles.css       Dark theme styles with OpenSans font-face declarations
      fonts/           OpenSans font files (Regular, SemiBold, Bold)
  http-tail/        React + Vite web UI for httptail log streaming
    src/              React app source
      main.jsx         App entry point with SSE client, auto-scroll, pause/resume
      styles.css       Dark theme styles (same variables as console)
pkg/              — Reusable library packages (scaffolding — empty)
internal/         — Internal packages, not importable outside this module (empty)
test/             — Tests and test fixtures
  testdata/         Test data files (empty)
config/           — Configuration files (empty)
deploy/           — Deployment config (empty)
docs/             — Documentation (empty)
```

## Conventions

### Go (MCP servers)

- **Style**: `gofmt` default formatting (tabs, no trailing whitespace). No custom linter config.
- **Package-level state**: MCP servers store configuration (e.g., `rootFolder`) in package-level variables set once at startup from CLI flags.
- **Tool handlers**: Signature is `func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)`. Arguments are extracted from `req.Params.Arguments.(map[string]interface{})`. Numeric JSON values come through as `float64` — cast with `int(val)`.
- **Error handling**: Errors in MCP tool handlers are communicated via `mcp.NewToolResultError(msg)` — the second return value is always `nil` (the error is in the result text, not the Go error).
- **No panics**: MCP servers must never panic. All errors should be returned as `mcp.NewToolResultError(...)`.
- **Imports**: Grouped as standard library first, then third-party, separated by a blank line (enforced by `gofmt`).
- **Comments**: Verbose, explanatory comments above every function, block, and non-obvious line. Each comment is a complete sentence. Follow the style in existing code.

### Python

- **Style**: Standard Python conventions. Use `pathlib.Path` for file operations.
- **Tools**: Defined with `@mcp.tool()` decorator. Return strings (not objects) from tool functions.
- **Flag parsing**: Use `argparse` with `parse_known_args()` to leave FastMCP/transport flags intact for `mcp.run()`.

### JavaScript

- **Style**: ESLint 10 with `eslint-plugin-react-hooks` and `eslint-plugin-react-refresh`. Run `npm run lint` before committing.
- **Fonts**: OpenSans font files are stored locally under `src/fonts/` — do not replace with CDN links.

### Cross-language

- **Path traversal protection**: Go uses `filepath.Clean` + `strings.HasPrefix(fullPath, rootFolder)`. Python uses `os.path.normpath` + `Path.relative_to(root)`. Both reject absolute paths and `..` prefixes.
- **CLI flags for root directories**: Always `-root` in Go, `--root` in Python. Must be an absolute path — validated at startup.
- **No HTTP (MCP servers)**: MCP servers (`foldermcp`, `logsmcp`) use stdio transport exclusively. `httptrace` is an exception — it is a standalone HTTP proxy with no MCP interface.
- **Signal-controlled services**: `httptrace` uses SIGUSR1/SIGUSR2 for proxy lifecycle; status messages go to stderr, JSON Lines data goes to stdout/`-log-file`.

## Gotchas

### MCP tool error handling (Go)

`mcp.NewToolResultError(msg)` returns `(*mcp.CallToolResult, error)`. The Go `error` is always `nil` — the error message is embedded in the result text. Never return a non-nil Go error from a tool handler; the MCP client won't receive the message properly.

```go
// CORRECT: error in result, nil Go error
return mcp.NewToolResultError("something went wrong"), nil

// WRONG: non-nil Go error — the client gets a generic failure, not your message
return nil, fmt.Errorf("something went wrong")
```

### JSON number types (Go)

When extracting arguments from `req.Params.Arguments.(map[string]interface{})`, JSON numbers unmarshal as `float64`, not `int`. Always cast:

```go
if raw, ok := args["limit"].(float64); ok {
    limit = int(raw)
}
```

### ServeStdio blocks forever

`server.ServeStdio(s)` is a blocking call — it takes over the process and never returns (except on fatal error). All setup (flag parsing, validation, tool registration) must happen before calling it.

### Path traversal: double-check required

After resolving a relative path against the root folder, always verify the result is still inside the root with `strings.HasPrefix(fullPath, rootFolder)` (Go) or `full.relative_to(root)` (Python). Symlinks can bypass `..` prefix checks, so the double-check is essential.

### scanner.Buffer for large lines (Go)

Default `bufio.Scanner` buffer is 64 KB. For log files or other line-based formats with potentially long lines, increase it:

```go
scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // 1 MB
```

### No existing tests

The project currently has **zero tests**. When adding tests:
- Go tests go alongside the package (e.g., `cmd/foldermcp/main_test.go`) or under `test/`
- Python tests can go under `test/` or alongside the script
- Use table-driven tests for Go (standard library pattern)
- Test data files go in `test/testdata/`

### Git: main is the default branch

The default branch is `main`. Always branch off `main` for new work.

## Patterns

### Adding new Go MCP commands

1. Create a new package under `cmd/` (e.g., `cmd/mycommand/`).
2. The main package creates an `mcp.Server` via `server.NewMCPServer(name, version)`, defines tools with `mcp.NewTool(name, mcp.WithDescription(...), mcp.WithString(...))`, registers handlers via `s.AddTool(tool, handler)`, and starts with `server.ServeStdio(s)`.
3. Tool handlers accept `(context.Context, mcp.CallToolRequest)` and return `(*mcp.CallToolResult, error)`.
4. The root folder path is validated once at startup from a `-root` flag and stored in a package-level variable.
5. Path traversal is prevented by checking `filepath.Clean` results and ensuring the resolved path stays within rootFolder via `strings.HasPrefix`.
6. Shared path-validation logic should be extracted to a library package (`pkg/` or `internal/`) when reused across commands.
7. Follow the comment style in `cmd/logsmcp/main.go` — section separators, tool-level descriptions, handler-level descriptions.

### Adding new Go CLI tools (non-MCP)

1. Create a new package under `examples/` (e.g., `examples/mytool/`).
2. Standard `func main()` with `flag` package for CLI arguments.
3. Use `os.Exit(1)` for fatal errors with a message to stderr.
4. These are standalone binaries, not MCP servers — they read input, produce output, and exit.

### Adding Python MCP scripts

Python MCP servers use the `mcp` package (FastMCP) and live under `scripts/py/`:
- Define tools with `@mcp.tool()` decorators instead of `mcp.NewTool(...)`.
- Path traversal protection uses `pathlib.Path.relative_to(...)` instead of `strings.HasPrefix`.
- Use `parse_known_args()` to leave FastMCP flags intact for `mcp.run()`.
- Can be run directly or via the `mcp run` CLI.

## Dependencies

### Go
- Go 1.26.3
- `github.com/mark3labs/mcp-go` v0.54.1 — MCP Go framework (tool definitions, server, stdio transport; used by `foldermcp`, `logsmcp`)
- `github.com/redis/go-redis/v9` v9.20.1 — Redis client (used by `examples/redisutil`)
- `github.com/elazarl/goproxy` v1.8.4 — HTTP/HTTPS MITM proxy library (used by `cmd/httptrace`)
- Standard library — flag, os, os/signal, syscall, path/filepath, strings, encoding/json, bufio, context, fmt, log, encoding/csv, io, net, net/http, sync, sync/atomic, time

### Python
- `mcp` (FastMCP) — MCP Python framework via `mcp[cli]`
- Standard library — pathlib, os, argparse, fnmatch, sys

### Web console
- Node.js + npm
- React 19, react-dom 19
- Vite 8 (dev bundler)
- ESLint 10 (dev, linting)
- OpenSans fonts (Regular 400, SemiBold 600, Bold 700) — stored locally under `src/fonts/`

### Web http-tail
- Node.js + npm
- React 19, react-dom 19
- Vite 8 (dev bundler)
- ESLint 10 (dev, linting)
- OpenSans fonts (Regular 400, SemiBold 600, Bold 700) — stored locally under `src/`
