// stdio-service is a minimal JSON-RPC 2.0 server that reads requests from
// stdin and writes responses to stdout. It uses only the Go standard library.
//
// Requests are read as a stream of JSON values, so a single value may span
// multiple lines and values may be separated by arbitrary whitespace.
// Example session:
//
//	$ echo '{"jsonrpc":"2.0","id":1,"method":"echo","params":"hi"}' | go run .
//	{"jsonrpc":"2.0","result":"hi","id":1}
//
//	$ echo '{"jsonrpc":"2.0","id":2,"method":"add","params":[3,4]}' | go run .
//	{"jsonrpc":"2.0","result":7,"id":2}
//
//	$ echo '{"jsonrpc":"2.0","id":3,"method":"hello","params":{"name":"ada"}}' | go run .
//	{"jsonrpc":"2.0","result":"hello, ada","id":3}
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Request is a JSON-RPC 2.0 request object.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      interface{}     `json:"id"`
}

// Response is a JSON-RPC 2.0 response object.
type Response struct {
	JSONRPC string    `json:"jsonrpc"`
	Result  any       `json:"result,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
	ID      any       `json:"id,omitempty"`
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Error satisfies the error interface so RPCError values can be returned from
// ordinary Go helper functions.
func (e *RPCError) Error() string {
	return e.Message
}

// JSON-RPC 2.0 error codes.
const (
	ParseError     = -32700
	InvalidRequest = -32600
	MethodNotFound = -32601
	InvalidParams  = -32602
	InternalError  = -32603
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "stdio service error: %v\n", err)
		os.Exit(1)
	}
}

// run reads JSON-RPC requests from stdin as a stream of JSON values and writes
// responses to stdout. It returns only when stdin is closed or an unrecoverable
// read/write error occurs.
func run(stdin io.Reader, stdout io.Writer) error {
	dec := json.NewDecoder(stdin)
	enc := json.NewEncoder(stdout)

	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("decode request: %w", err)
		}

		responses := handleMessage(raw)
		for _, resp := range responses {
			if err := enc.Encode(resp); err != nil {
				return fmt.Errorf("encode response: %w", err)
			}
		}
	}
}

// handleMessage parses one raw JSON value that may be either a single JSON-RPC
// request or a batch array of requests. It returns the list of responses that
// must be sent back to the client (notifications are omitted).
func handleMessage(raw []byte) []*Response {
	// Try to interpret the line as a batch array first.
	var batch []json.RawMessage
	if err := json.Unmarshal(raw, &batch); err == nil {
		if len(batch) == 0 {
			return []*Response{errorResponse(nil, InvalidRequest, "invalid empty batch request")}
		}

		responses := make([]*Response, 0, len(batch))
		for _, item := range batch {
			if resp := handleSingle(item); resp != nil {
				responses = append(responses, resp)
			}
		}
		return responses
	}

	// Not an array: treat it as a single request.
	if resp := handleSingle(raw); resp != nil {
		return []*Response{resp}
	}
	return nil
}

// handleSingle parses and executes one JSON-RPC request. It returns nil when
// the request is a notification (no "id" field), which means no response should
// be emitted.
func handleSingle(raw []byte) *Response {
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return errorResponse(nil, ParseError, "parse error")
	}

	if req.JSONRPC != "2.0" {
		return errorResponse(req.ID, InvalidRequest, "invalid jsonrpc version")
	}

	if req.Method == "" {
		return errorResponse(req.ID, InvalidRequest, "missing method")
	}

	result, err := dispatch(req.Method, req.Params)
	if err != nil {
		rpcErr, ok := err.(*RPCError)
		if !ok {
			rpcErr = &RPCError{Code: InternalError, Message: err.Error()}
		}
		return &Response{JSONRPC: "2.0", Error: rpcErr, ID: req.ID}
	}

	// Notifications (requests without an "id") do not produce a response.
	if req.ID == nil {
		return nil
	}

	return &Response{JSONRPC: "2.0", Result: result, ID: req.ID}
}

// dispatch routes the method name to its handler. It returns an *RPCError for
// expected JSON-RPC failures (unknown method, bad params) so callers can
// produce a well-formed error response.
func dispatch(method string, params json.RawMessage) (any, error) {
	switch method {
	case "echo":
		return echo(params)
	case "add":
		return add(params)
	case "hello":
		return hello(params)
	default:
		return nil, &RPCError{
			Code:    MethodNotFound,
			Message: fmt.Sprintf("method not found: %s", method),
		}
	}
}

// echo returns its string parameter unchanged.
func echo(params json.RawMessage) (any, error) {
	var s string
	if err := json.Unmarshal(params, &s); err != nil {
		return nil, &RPCError{Code: InvalidParams, Message: "params must be a JSON string"}
	}
	return s, nil
}

// add returns the sum of two numbers.
func add(params json.RawMessage) (any, error) {
	var nums []float64
	if err := json.Unmarshal(params, &nums); err != nil || len(nums) != 2 {
		return nil, &RPCError{Code: InvalidParams, Message: "params must be an array of two numbers"}
	}
	return nums[0] + nums[1], nil
}

// hello greets the caller by name.
func hello(params json.RawMessage) (any, error) {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: InvalidParams, Message: "params must be an object with a string 'name' field"}
	}
	if p.Name == "" {
		p.Name = "world"
	}
	return fmt.Sprintf("hello, %s", p.Name), nil
}

// errorResponse builds a JSON-RPC error response.
func errorResponse(id any, code int, message string) *Response {
	return &Response{
		JSONRPC: "2.0",
		Error:   &RPCError{Code: code, Message: message},
		ID:      id,
	}
}
