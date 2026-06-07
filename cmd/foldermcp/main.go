// Command foldermcp implements an MCP stdio server that reads files from a
// designated root folder and its sub-directories.
//
// Usage:
//
//	foldermcp -root <folder-path>
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// rootFolder is the absolute path of the directory that read_file is allowed
// to serve. It is set once during startup from the -root flag.
var rootFolder string

// main is the entry point. It parses flags, validates the root folder, sets up
// the MCP server with a single "read_file" tool, and starts the stdio transport.
//
// Usage:
//
//	foldermcp -root <folder-path>
func main() {
	rootFlag := flag.String("root", "", "Root folder to serve files from (required)")
	flag.Parse()

	if *rootFlag == "" {
		fmt.Fprintf(os.Stderr, "Usage: foldermcp -root <folder-path>\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

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
		"foldermcp",
		"1.0.0",
	)

	// ---------------------------------------------------------------------------
	// Tool: read_file
	// ---------------------------------------------------------------------------

	readFileTool := mcp.NewTool("read_file",
		mcp.WithDescription("Read the contents of a file inside the allowed folder. Path is relative to the root folder and may include sub-directories."),
		mcp.WithString("path",
			mcp.Description("Relative path from the root folder (e.g. \"config.json\" or \"subdir/data.txt\")"),
			mcp.Required(),
		),
	)

	s.AddTool(readFileTool, handleReadFile)

	// ---------------------------------------------------------------------------
	// Start stdio transport
	// ---------------------------------------------------------------------------

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %s\n", err)
		os.Exit(1)
	}
}

// handleReadFile implements the "read_file" tool handler.
//
// It reads the file at the given relative path, resolved against rootFolder.
// Path traversal attempts are detected and rejected. Directories are also
// rejected.
func handleReadFile(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]interface{})
	if !ok {
		return mcp.NewToolResultError("Invalid arguments"), nil
	}
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return mcp.NewToolResultError("Missing or invalid 'path' argument"), nil
	}

	// Prevent directory traversal.
	cleanPath := filepath.Clean(path)
	if strings.HasPrefix(cleanPath, "..") || strings.HasPrefix(cleanPath, "/") {
		return mcp.NewToolResultError("Path must be relative and must not escape the root folder"), nil
	}

	fullPath := filepath.Join(rootFolder, cleanPath)

	// Ensure the resolved path is still inside rootFolder.
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

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error reading file: %s", err)), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}
