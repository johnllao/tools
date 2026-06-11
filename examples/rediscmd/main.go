// Command rediscmd reads a CSV file and prints Redis HSET commands for each row,
// along with SADD commands to maintain an index set of all keys under the prefix.
//
// Usage:
//
//	rediscmd -input <csv-file> -prefix <redis-key-prefix>
//
// The CSV header row provides the hash field names. The first column serves as
// the ID and is appended to the prefix to form the Redis key. All keys are also
// added to a Redis Set (keyed by the prefix itself) via SADD, so the set serves
// as an index — e.g. SMEMBERS STATIC:CUSTOMER: returns every customer key.
//
// Example:
//
//	rediscmd -input customers.csv -prefix "STATIC:CUSTOMER:"
//
// Given a CSV file:
//
//	ID,Name,Email
//	1,John Doe,john@example.com
//	2,Jane Smith,jane@example.com
//
// Output:
//
//	HSET STATIC:CUSTOMER:1 Name "John Doe" Email "john@example.com"
//	SADD STATIC:CUSTOMER: STATIC:CUSTOMER:1
//	HSET STATIC:CUSTOMER:2 Name "Jane Smith" Email "jane@example.com"
//	SADD STATIC:CUSTOMER: STATIC:CUSTOMER:2
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// needsQuote reports whether a field value should be quoted in the Redis command
// output. Values containing spaces, quotes, or other special characters are
// wrapped in double quotes to ensure the command can be safely parsed.
func needsQuote(s string) bool {
	return strings.ContainsAny(s, " \"\t\n\r")
}

// quoteField wraps a field value in double quotes and escapes any embedded
// double quotes and backslashes, making it safe for Redis command output.
func quoteField(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// formatValue returns the field value formatted for a Redis command string.
// Values requiring quoting are quoted; simple alphanumeric values are not.
func formatValue(s string) string {
	if needsQuote(s) {
		return quoteField(s)
	}
	return s
}

func main() {
	// -input specifies the CSV file to read. The first row is the header (field
	// names), and the first column of each data row is the ID appended to -prefix.
	input := flag.String("input", "", "Path to the CSV input file (required)")
	// -prefix is prepended to the ID column to form the full Redis hash key. It
	// also serves as the index-set key for the SADD commands (e.g. "STATIC:CUSTOMER:").
	prefix := flag.String("prefix", "", "Redis key prefix (e.g. \"STATIC:CUSTOMER:\")")
	// Parse CLI flags so -input and -prefix are available before opening the file.
	flag.Parse()

	// -input is required; if omitted, print usage and exit with a non-zero
	// status so the caller knows the invocation was invalid.
	if *input == "" {
		fmt.Fprintln(os.Stderr, "Usage: rediscmd -input <csv-file> -prefix <redis-key-prefix>")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Open and read the CSV file.
	file, err := os.Open(*input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening file: %s\n", err)
		os.Exit(1)
	}
	defer file.Close()

	reader := csv.NewReader(file)

	// Read the header row — these become the hash field names.
	headers, err := reader.Read()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading CSV header: %s\n", err)
		os.Exit(1)
	}
	// A CSV file with a zero-length header row has no field names to map.
	// Abort rather than producing commands with no hash fields.
	if len(headers) == 0 {
		fmt.Fprintln(os.Stderr, "CSV file has no header row")
		os.Exit(1)
	}

	// Track whether we produced any output.
	emitted := false

	for lineNo := 2; ; lineNo++ {
		// Read the next CSV row. io.EOF signals end-of-file (exit the loop
		// normally); any other error is fatal and aborts the program.
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading CSV at line %d: %s\n", lineNo, err)
			os.Exit(1)
		}
		if len(record) == 0 {
			continue
		}

		// The first column is the ID — it forms the Redis key: {prefix}{ID}.
		id := record[0]
		key := *prefix + id

		// Build the HSET command: HSET key field value [field value ...].
		var parts []string
		parts = append(parts, "HSET", key)

		// Determine how many field:value pairs we can emit.
		// Use the shorter of headers and record to handle truncated rows.
		pairs := len(headers)
		if len(record) < pairs {
			pairs = len(record)
		}
		// Skip the ID column (header[0]/record[0]) — it's already in the key.
		for i := 1; i < pairs; i++ {
			parts = append(parts, headers[i], formatValue(record[i]))
		}

		fmt.Println(strings.Join(parts, " "))
		// Emit SADD to add this key to the index set (keyed by the prefix).
		fmt.Printf("SADD %s %s\n", *prefix, key)
		emitted = true
	}

	if !emitted {
		fmt.Fprintln(os.Stderr, "Warning: CSV file contains no data rows")
	}
}
