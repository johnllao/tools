package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/redis/go-redis/v9"
)

// ParseFields splits a line into fields, respecting double-quoted strings.
// "SET name \"John Doe\"" → ["SET", "name", "John Doe"]
func ParseFields(line string) []string {
	var args []string
	var buf strings.Builder
	inQuote := false

	for _, r := range line {
		// Toggle quote mode on double-quote, split on unquoted spaces,
		// and accumulate all other characters into the current token.
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == ' ' && !inQuote:
			if buf.Len() > 0 {
				args = append(args, buf.String())
				buf.Reset()
			}
		default:
			buf.WriteRune(r)
		}
	}
	if buf.Len() > 0 {
		args = append(args, buf.String())
	}
	return args
}

// ProcessCommand accepts any Redis command as a string and executes it
// via the universal Do() interface.
func ProcessCommand(ctx context.Context, rdb *redis.Client, cmd string) (interface{}, error) {
	// Parse the command string into fields and reject empty input.
	parts := ParseFields(cmd)
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	// Convert string fields to interface{} slices for Do() variadic args.
	args := make([]interface{}, len(parts))
	for i, p := range parts {
		args[i] = p
	}
	return rdb.Do(ctx, args...).Result()
}

func main() {
	// Command-line flags for Redis connection configuration.
	// host and port default to localhost:6379 when not provided.
	host := flag.String("host", "localhost", "Redis server host")
	port := flag.Int("port", 6379, "Redis server port")
	user := flag.String("user", "", "Redis username (for ACL-based auth)")
	password := flag.String("password", "", "Redis password")
	flag.Parse()

	// Create a Redis client using the parsed flags and set up the
	// background context used for all subsequent command calls.
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", *host, *port),
		Username: *user,
		Password: *password,
		DB:       0,
	})
	ctx := context.Background()
	defer rdb.Close()

	// Read Redis commands line by line from stdin. Blank lines and lines
	// starting with # or // are skipped. Each non-empty command is parsed,
	// executed against the Redis server, and the result is logged.
	scanner := bufio.NewScanner(os.Stdin)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())

		// Skip blank lines and comment lines (# and // prefixes).
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}

		// Execute the command and log the result (or error).
		result, err := ProcessCommand(ctx, rdb, line)
		if err != nil {
			log.Printf("Line %d: FAIL  %s → %v", lineNo, line, err)
		} else {
			log.Printf("Line %d: OK    %s → %v", lineNo, line, result)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatalf("error reading stdin: %v", err)
	}
}
