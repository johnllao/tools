# stdio-service

A minimal JSON-RPC 2.0 server that communicates over standard input and standard output. It uses only the Go standard library.

## Overview

- Reads JSON-RPC 2.0 requests from `stdin` as a stream of JSON values.
- Writes JSON-RPC 2.0 responses to `stdout`.
- Supports single requests and batch arrays.
- Skips notifications (requests without an `id`), as required by the spec.

## Build

From this directory:

```sh
go build -o stdio-service .
```

Or run directly without building:

```sh
go run .
```

## Example Requests

Send requests one per line:

```sh
echo '{"jsonrpc":"2.0","id":1,"method":"echo","params":"hi"}' | ./stdio-service
```

Output:

```json
{"jsonrpc":"2.0","result":"hi","id":1}
```

Send multiple whitespace-separated requests:

```sh
printf '%s' '{"jsonrpc":"2.0","id":2,"method":"add","params":[3,4]} {"jsonrpc":"2.0","id":3,"method":"hello","params":{"name":"ada"}}' | ./stdio-service
```

Output:

```json
{"jsonrpc":"2.0","result":7,"id":2}
{"jsonrpc":"2.0","result":"hello, ada","id":3}
```

Send a batch array:

```sh
echo '[{"jsonrpc":"2.0","id":10,"method":"add","params":[1,2]},{"jsonrpc":"2.0","id":11,"method":"echo","params":"batch"}]' | ./stdio-service
```

Output:

```json
{"jsonrpc":"2.0","result":3,"id":10}
{"jsonrpc":"2.0","result":"batch","id":11}
```

Pretty-printed and multi-line JSON also works because the server uses `json.Decoder` to read a stream of values.

## Supported Methods

| Method   | Params                              | Result                        |
|----------|-------------------------------------|-------------------------------|
| `echo`   | JSON string                         | Same string                   |
| `add`    | Array of two numbers                | Sum of the two numbers        |
| `hello`  | Object with a `name` string field   | `"hello, <name>"`             |

## JSON-RPC Error Codes

The server returns standard JSON-RPC 2.0 error codes:

| Code   | Meaning             |
|--------|---------------------|
| -32700 | Parse error         |
| -32600 | Invalid request     |
| -32601 | Method not found    |
| -32602 | Invalid params      |
| -32603 | Internal error      |

## Calling from a Go Client

A client can run the server as a subprocess and talk to it through pipes. The key types of interaction are:

1. Start the process with `exec.Command`.
2. Obtain `StdinPipe` and `StdoutPipe`.
3. Use `json.Encoder` to write requests to stdin.
4. Use `json.Decoder` to read responses from stdout.
5. Run the reader in a goroutine so the sender is not blocked.

Example snippet:

```go
cmd := exec.Command("go", "run", "..")
stdin, _ := cmd.StdinPipe()
stdout, _ := cmd.StdoutPipe()
cmd.Start()

enc := json.NewEncoder(stdin)
dec := json.NewDecoder(stdout)

// Reader goroutine
go func() {
    for {
        var resp Response
        if err := dec.Decode(&resp); err != nil {
            return
        }
        fmt.Printf("[%d] %s\n", resp.ID, string(resp.Result))
    }
}()

// Send requests
enc.Encode(Request{JSONRPC: "2.0", ID: 1, Method: "echo", Params: "hi"})
```

Close `stdin` when finished to send EOF to the server and let it shut down cleanly:

```go
stdin.Close()
cmd.Wait()
```

If requests must be sent strictly in order and responses awaited one-by-one, keep the encoder and decoder in the same goroutine and call `Decode` immediately after each `Encode`.

## Architecture

- `main.go` defines `Request`, `Response`, and `RPCError` structs.
- `run` creates a `json.Decoder` over `stdin` and a `json.Encoder` over `stdout`.
- `handleMessage` decides whether a decoded value is a single request or a batch array.
- `handleSingle` validates the request, dispatches the method, and returns `nil` for notifications.
- `dispatch` routes methods to their handlers (`echo`, `add`, `hello`).

Only the Go standard library is used: `encoding/json`, `fmt`, `io`, `os`, `os/exec`, and `sync` for the client example.
