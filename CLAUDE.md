# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A Go module that builds MCP (Model Context Protocol) stdio servers. Each command in `cmd/` is a standalone MCP server — a long-running process that speaks the MCP stdio protocol to provide tools to an MCP client (such as Claude Code).

The project uses `github.com/mark3labs/mcp-go` v0.54.1 as the MCP framework.

## Commands

| Command | Directory | Description |
|---|---|---|
| `foldermcp` | `cmd/foldermcp/` | Provides a `read_file` tool to read files from a designated root folder (with path traversal protection) |
| `logsmcp` | `cmd/logsmcp/` | Provides `list_log_files` and `read_logs` tools to browse and filter JSON-structured log files by level, tail, search text, and limit |

### Build & Run

```sh
# Build all commands
go build ./cmd/...

# Run foldermcp
go run ./cmd/foldermcp -root /path/to/folder

# Run logsmcp
go run ./cmd/logsmcp -root /path/to/logs
```

## Code Architecture

```
cmd/              — Main packages; each subdirectory is one MCP server binary
  foldermcp/        File-reading MCP server
  logsmcp/          Log-browsing MCP server
pkg/              — Reusable library packages
  validator/        (scaffolding)
internal/         — Internal packages (not importable outside this module)
scripts/          — Helper scripts (Python scripts in scripts/py/)
test/             — Tests and test fixtures
  testdata/         Test data files
config/           — Configuration files (scaffolding)
deploy/           — Deployment config (scaffolding)
docs/             — Documentation (scaffolding)
examples/         — Usage examples (scaffolding)
web/              — Web UI (scaffolding)
```

**Patterns to follow when adding new commands:**

1. Create a new package under `cmd/` (e.g., `cmd/mycommand/`).
2. The main package creates an `mcp.Server` via `server.NewMCPServer(...)`, defines tools with `mcp.NewTool(...)`, registers handlers via `s.AddTool(...)`, and starts with `server.ServeStdio(s)`.
3. Tool handlers accept `(context.Context, mcp.CallToolRequest)` and return `(*mcp.CallToolResult, error)`.
4. The root folder path is validated once at startup from a `-root` flag and stored in a package-level variable.
5. Path traversal is prevented by checking `filepath.Clean` results and ensuring the resolved path stays within rootFolder via `strings.HasPrefix`.
6. Shared path-validation logic should be extracted to a library package (`pkg/` or `internal/`) when reused across commands.

## Dependencies

- Go 1.26.3
- `github.com/mark3labs/mcp-go` v0.54.1 — MCP Go framework (tool definitions, server, stdio transport)
- Standard library — flag, os, path/filepath, strings, encoding/json, bufio
