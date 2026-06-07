// Command logsmcp implements an MCP stdio server that reads JSON-structured log
// files from a designated root folder and its sub-directories.
//
// Usage:
//
//	logsmcp -root <log-folder>
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// rootFolder is the absolute path of the log directory that all tools are
// allowed to serve. It is set once during startup from the -root flag.
var rootFolder string

// logExtensions lists file extensions that are considered log files.
var logExtensions = []string{".json", ".jsonl", ".log"}

// main is the entry point. It parses flags, validates the log folder, sets up
// the MCP server with log-focused tools, and starts the stdio transport.
//
// Usage:
//
//	logsmcp -root <log-folder>
func main() {
	rootFlag := flag.String("root", "", "Root folder containing log files (required)")
	flag.Parse()

	if *rootFlag == "" {
		fmt.Fprintf(os.Stderr, "Usage: logsmcp -root <log-folder>\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Resolve the -root flag to an absolute path and validate that it
	// exists and is a directory. Invalid paths cause an immediate exit
	// with a descriptive error message.
	var err error
	rootFolder, err = filepath.Abs(*rootFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid folder path: %s\n", err)
		os.Exit(1)
	}
	info, err := os.Stat(rootFolder)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Not a valid directory: %s\n", rootFolder)
		os.Exit(1)
	}

	s := server.NewMCPServer(
		"logsmcp",
		"1.0.0",
	)

	// -----------------------------------------------------------------------
	// list_log_files tool — walks the root folder and returns the relative
	// paths of all files with known log extensions (.json, .jsonl, .log).
	// -----------------------------------------------------------------------

	listTool := mcp.NewTool("list_log_files",
		mcp.WithDescription("List all log files (JSON, JSONL, LOG) inside the root folder, recursively."),
	)

	s.AddTool(listTool, handleListLogFiles)

	// -----------------------------------------------------------------------
	// -----------------------------------------------------------------------
	// read_logs tool — reads a log file and filters entries by level,
	// text search, or tail count, with an optional limit on the number
	// of entries returned.
	// -----------------------------------------------------------------------

	readTool := mcp.NewTool("read_logs",
		mcp.WithDescription("Read and filter JSON-structured log entries from a specific log file."),
		mcp.WithString("path",
			mcp.Description("Relative path to the log file from the root folder"),
			mcp.Required(),
		),
		mcp.WithString("level",
			mcp.Description("Filter by log level (e.g. \"error\", \"warn\", \"info\", \"debug\")"),
			mcp.Enum("error", "warn", "info", "debug", "trace", "fatal"),
		),
		mcp.WithInteger("tail",
			mcp.Description("Only return the last N log entries (cannot be combined with search)"),
		),
		mcp.WithString("search",
			mcp.Description("Return only entries whose JSON text contains this substring (case-insensitive)"),
		),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of log entries to return (default 50)"),
		),
	)

	s.AddTool(readTool, handleReadLogs)

	// -----------------------------------------------------------------------
	// Start stdio transport
	// -----------------------------------------------------------------------

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %s\n", err)
		os.Exit(1)
	}
}

// handleListLogFiles implements the "list_log_files" tool handler.
//
// It walks the root folder recursively and returns paths to files with known
// log extensions (.json, .jsonl, .log).
func handleListLogFiles(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var files []string

	// Walk the root folder recursively. For each file whose extension
	// matches a known log extension (.json, .jsonl, .log), compute its
	// relative path and collect it. Directories and non-log files are
	// silently skipped.
	err := filepath.WalkDir(rootFolder, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		for _, allowed := range logExtensions {
			if ext == allowed {
				rel, _ := filepath.Rel(rootFolder, path)
				files = append(files, rel)
				break
			}
		}
		return nil
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error walking folder: %s", err)), nil
	}

	// No matching log files found — return a friendly message instead of
	// an empty response.
	if len(files) == 0 {
		return mcp.NewToolResultText("No log files found."), nil
	}

	return mcp.NewToolResultText(strings.Join(files, "\n")), nil
}

// handleReadLogs implements the "read_logs" tool handler.
//
// It reads the specified log file, optionally filters by level, tail count,
// or a text search, and returns up to limit entries as formatted JSON.
func handleReadLogs(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]interface{})
	if !ok {
		return mcp.NewToolResultError("Invalid arguments"), nil
	}

	path, _ := args["path"].(string)
	if path == "" {
		return mcp.NewToolResultError("Missing or invalid 'path' argument"), nil
	}

	// Resolve and validate path – prevent directory traversal.
	cleanPath := filepath.Clean(path)
	if strings.HasPrefix(cleanPath, "..") || strings.HasPrefix(cleanPath, "/") {
		return mcp.NewToolResultError("Path must be relative and must not escape the root folder"), nil
	}

	fullPath := filepath.Join(rootFolder, cleanPath)

	if !strings.HasPrefix(fullPath, rootFolder) {
		return mcp.NewToolResultError("Path escapes the allowed folder"), nil
	}

	fi, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return mcp.NewToolResultError(fmt.Sprintf("File not found: %s", path)), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("Cannot access file: %s", err)), nil
	}
	if fi.IsDir() {
		return mcp.NewToolResultError(fmt.Sprintf("Path is a directory, not a file: %s", path)), nil
	}

	// Parse optional filters.
	levelFilter, _ := args["level"].(string)
	searchText, _ := args["search"].(string)

	var tail int
	if tailRaw, ok := args["tail"].(float64); ok {
		tail = int(tailRaw)
	}

	limit := 50
	if limitRaw, ok := args["limit"].(float64); ok {
		limit = int(limitRaw)
	}

	// Read the file line by line.
	file, err := os.Open(fullPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error opening file: %s", err)), nil
	}
	defer file.Close()

	var allLines []string
	scanner := bufio.NewScanner(file)
	// Increase the scanner buffer for potentially long log lines (up to 1 MB).
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		allLines = append(allLines, line)
	}
	if err := scanner.Err(); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error reading file: %s", err)), nil
	}

	// Apply tail filter first.
	if tail > 0 && tail < len(allLines) {
		allLines = allLines[len(allLines)-tail:]
	}

	// Filter and collect results.
	var results []string
	for _, line := range allLines {
		if limit > 0 && len(results) >= limit {
			break
		}

		var entryMap map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entryMap); err != nil {
			// If a line isn't valid JSON, include it as raw text for
			// visibility (unless a search filter is active, in which
			// case the search still applies to the raw text).
			if levelFilter == "" && searchText == "" {
				results = append(results, line)
			}
			if levelFilter == "" && searchText != "" &&
				strings.Contains(strings.ToLower(line), strings.ToLower(searchText)) {
				results = append(results, line)
			}
			continue
		}

		// Filter by level.
		if levelFilter != "" {
			entryLevel, _ := entryMap["level"].(string)
			if !strings.EqualFold(entryLevel, levelFilter) {
				continue
			}
		}

		// Filter by text search – case-insensitive match on the raw JSON.
		if searchText != "" {
			if !strings.Contains(strings.ToLower(line), strings.ToLower(searchText)) {
				continue
			}
		}

		// Pretty-print the entry.
		pretty, err := json.MarshalIndent(entryMap, "", "  ")
		if err != nil {
			results = append(results, line)
		} else {
			results = append(results, string(pretty))
		}
	}

	if len(results) == 0 {
		return mcp.NewToolResultText("No matching log entries found."), nil
	}

	return mcp.NewToolResultText(strings.Join(results, "\n---\n")), nil
}
