# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A multi-language project that builds MCP (Model Context Protocol) stdio servers. Each command in `cmd/` is a standalone Go MCP server — a long-running process that speaks the MCP stdio protocol to provide tools to an MCP client (such as Claude Code). Python equivalents live under `scripts/py/`, and a web console UI is under `web/console/`.

### Languages & frameworks

- **Go** — Primary MCP servers, using `github.com/mark3labs/mcp-go` v0.54.1
- **Python** — MCP servers using `mcp` (FastMCP) under `scripts/py/`
- **JavaScript** — Web console using React + Vite under `web/console/`

## Commands

| Command / Script | Directory | Description |
|---|---|---|
| `foldermcp` (Go) | `cmd/foldermcp/` | Provides a `read_file` tool to read files from a designated root folder (with path traversal protection) |
| `foldermcp` (Python) | `scripts/py/foldermcp.py` | Python FastMCP equivalent — provides `list_files` and `read_file` tools, with path traversal protection, shell-style glob patterns, and human-readable file sizes |
| `logsmcp` | `cmd/logsmcp/` | Provides `list_log_files` and `read_logs` tools to browse and filter JSON-structured log files by level, tail, search text, and limit |
| `console` | `web/console/` | React + Vite web UI with dark theme, 2-column layout (sidebar navigator + content area), and OpenSans fonts |

### Build & Run

```sh
# Build all Go commands
go build ./cmd/...

# Run Go foldermcp
go run ./cmd/foldermcp -root /path/to/folder

# Run Python foldermcp (requires `uv` or `mcp` package)
python scripts/py/foldermcp.py --root /path/to/folder
mcp run scripts/py/foldermcp.py --root /path/to/folder   # via mcp CLI

# Run logsmcp
go run ./cmd/logsmcp -root /path/to/logs

# Start web console dev server
cd web/console && npm run dev
```

## Code Architecture

```
cmd/              — Main packages; each subdirectory is one MCP server binary
  foldermcp/        File-reading MCP server (Go)
  logsmcp/          Log-browsing MCP server (Go)
scripts/          — Helper scripts
  py/               Python scripts
    foldermcp.py      Python FastMCP equivalent of Go foldermcp
web/              — Front-end web applications
  console/          React + Vite web UI with dark theme and sidebar layout
    src/              React app source
      main.jsx         App entry point with 2-column layout (sidebar + content)
      styles.css       Dark theme styles with OpenSans font-face declarations
      fonts/           OpenSans font files (Regular, SemiBold, Bold)
pkg/              — Reusable library packages (scaffolding — empty)
internal/         — Internal packages, not importable outside this module (empty)
test/             — Tests and test fixtures
  testdata/         Test data files (empty)
config/           — Configuration files (empty)
deploy/           — Deployment config (empty)
docs/             — Documentation (empty)
examples/         — Usage examples (empty)
```

## Patterns

### Adding new Go MCP commands

1. Create a new package under `cmd/` (e.g., `cmd/mycommand/`).
2. The main package creates an `mcp.Server` via `server.NewMCPServer(...)`, defines tools with `mcp.NewTool(...)`, registers handlers via `s.AddTool(...)`, and starts with `server.ServeStdio(s)`.
3. Tool handlers accept `(context.Context, mcp.CallToolRequest)` and return `(*mcp.CallToolResult, error)`.
4. The root folder path is validated once at startup from a `-root` flag and stored in a package-level variable.
5. Path traversal is prevented by checking `filepath.Clean` results and ensuring the resolved path stays within rootFolder via `strings.HasPrefix`.
6. Shared path-validation logic should be extracted to a library package (`pkg/` or `internal/`) when reused across commands.

### Adding Python MCP scripts

Python MCP servers use the `mcp` package (FastMCP) and live under `scripts/py/`:
- Define tools with `@mcp.tool()` decorators instead of `mcp.NewTool(...)`.
- Path traversal protection uses `pathlib.Path.relative_to(...)` instead of `strings.HasPrefix`.
- Can be run directly or via the `mcp run` CLI.

## Dependencies

### Go
- Go 1.26.3
- `github.com/mark3labs/mcp-go` v0.54.1 — MCP Go framework (tool definitions, server, stdio transport)
- Standard library — flag, os, path/filepath, strings, encoding/json, bufio

### Python
- `mcp` (FastMCP) — MCP Python framework via `mcp[cli]`
- Standard library — pathlib, os, argparse, fnmatch

### Web console
- Node.js + npm
- React 19, react-dom 19
- Vite 8 (dev bundler)
- ESLint 10 (dev, linting)
- OpenSans fonts (Regular 400, SemiBold 600, Bold 700) — stored locally under `src/fonts/`
