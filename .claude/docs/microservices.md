# Go Microservice Best Practices & Project Layout

This document captures community-standard patterns for developing Go microservices. There is no single "official" Go microservice template — the Go community resists heavy code generation and over-structuring — but the patterns below are widely adopted and have aged well in production.

## Project Layout

### The Standard Go Layout (de facto)

```
myservice/
├── cmd/
│   └── myservice/          # One main per binary
│       └── main.go          #   - flag parsing, config loading, dependency wiring, Run()
├── internal/                # Private to this module (compiler-enforced)
│   ├── handler/             #   HTTP/gRPC handlers (transport layer)
│   ├── service/             #   Business logic (use-case layer)
│   ├── repository/          #   Data access (DB, external APIs)
│   ├── domain/              #   Core types, interfaces, errors
│   └── middleware/          #   HTTP middleware
├── pkg/                     # Shared libraries (public API — use carefully)
├── config/                  # Config files or config-loading logic
├── migrations/              # DB migrations (golang-migrate, atlas, etc.)
├── api/                     # Protobuf / OpenAPI specs
├── Dockerfile
├── docker-compose.yaml
├── Makefile                 # or Taskfile.yaml
├── go.mod
└── go.sum
```

**Key principle**: `internal/` is compiler-enforced privacy. Everything outside your module that imports you cannot see `internal/`. Use it aggressively for any code you do not want external consumers depending on.

### A Simpler Alternative — Flat with Purpose

For smaller services (under ~5k lines), many teams skip the nested hierarchy entirely. This is **totally fine** — do not over-structure early. Pull packages apart when there is real pain, not before.

```
myservice/
├── main.go                  # Everything in one package
├── handler.go
├── service.go
├── store.go
├── domain.go
├── server.go
└── server_test.go
```

## Hexagonal / Clean Architecture in Go

Every Go microservice that "aged well" follows these separation rules:

```
handler (transport)  →  service (business logic)  →  repository (data)
       ↑                        ↑                          ↑
   HTTP/gRPC           interfaces defined            implements
   serialization       in domain package             domain interfaces
```

1. **`domain/`** — Core types (`User`, `Order`) and **interfaces** (`UserStore`, `EventPublisher`). No external dependencies.
2. **`service/`** — Business logic. Depends only on `domain` interfaces — never on `sql.DB`, `*http.Client`, etc.
3. **`repository/`** — Implements domain interfaces using concrete tech (Postgres, Redis, S3).
4. **`handler/`** — Deserializes requests, calls services, serializes responses. The transport layer.

This makes testing trivial: services are tested with mock implementations of domain interfaces; repositories are tested with testcontainers against real infrastructure.

### Interface Rule

Define small interfaces at the **call site** — the `service` package defines `UserStore` with only the 1-3 methods it actually needs. Do not define interfaces where they are implemented. This avoids bloated interfaces and makes mocking straightforward.

## Dependency Wiring

The dominant Go pattern is **explicit constructor injection** — no DI frameworks:

```go
// main.go — wire by hand
func main() {
    cfg := config.Load()

    db, _ := sql.Open("postgres", cfg.DSN)
    defer db.Close()

    store  := repository.NewUserStore(db)
    svc    := service.NewUserService(store)
    handler := handler.NewUserHandler(svc)

    srv := server.New(cfg.Addr, handler)
    srv.ListenAndServe()
}
```

This is called **"wire by hand"** — it is readable, testable, and involves zero magic. Google's `wire` tool can code-generate this wiring when the dependency graph gets large, but start by hand.

## HTTP Server Pattern

### Explicit Timeouts (Never Use Defaults)

The idiomatic Go HTTP server uses explicit timeouts. The stdlib defaults are zero (infinite), which is a production risk:

```go
srv := &http.Server{
    Addr:         cfg.Addr,
    Handler:      mux,
    ReadTimeout:  5 * time.Second,
    WriteTimeout: 10 * time.Second,
    IdleTimeout:  120 * time.Second,
}
```

### Handler Signature — Return Errors

Well-structured handlers return errors rather than writing responses directly. The router or middleware serializes the error into the HTTP response:

```go
// handler signature — returns error, the router serializes it
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

func (h *UserHandler) GetUser(w http.ResponseWriter, r *http.Request) error {
    id := chi.URLParam(r, "id")
    user, err := h.svc.GetUser(r.Context(), id)
    if err != nil {
        return err
    }
    return json.NewEncoder(w).Encode(user)
}
```

## Graceful Shutdown

Standard pattern for every Go service — catch OS signals, drain in-flight requests, close resources:

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

go func() {
    if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
        log.Fatal(err)
    }
}()

<-ctx.Done()
log.Println("shutting down...")

shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
srv.Shutdown(shutdownCtx)
```

## Config / Observability

Most production Go services wire in the following:

| Concern | Common choice | Notes |
|---|---|---|
| **Config** | `os.Getenv` + env vars | 12-factor; also `caarlos0/env`, `knadh/koanf`, `spf13/viper` |
| **Logging** | `log/slog` (stdlib, Go 1.21+) | Structured, leveled, zero dependencies |
| **Metrics** | `prometheus/client_golang` | Standard Prometheus instrumented handlers |
| **Tracing** | `go.opentelemetry.io/otel` | Distributed tracing standard |
| **Health checks** | `/healthz` and `/readyz` endpoints | Liveness vs. readiness probes (Kubernetes convention) |
| **Routing** | `go-chi/chi` or stdlib `http.ServeMux` (Go 1.22+) | `chi` is idiomatic and stdlib-compatible; stdlib mux gained method routing in 1.22 |

## Common Libraries

| Purpose | Package | Notes |
|---|---|---|
| HTTP router | `go-chi/chi`, `gin`, or stdlib (1.22+) | `chi` is the most idiomatic |
| SQL | `database/sql` + `sqlx`, or `pgx` | `pgx` is preferred for Postgres-only services |
| Migrations | `golang-migrate/migrate` | File-based and embeddable |
| Validation | `go-playground/validator` | Struct tag-based |
| Serialization | `encoding/json` (stdlib) or `jsoniter` | `jsoniter` is a drop-in faster replacement |
| gRPC | `google.golang.org/grpc` + protobuf | Standard gRPC stack |
| Testing | `testcontainers-go`, `stretchr/testify` | `testify` for assertions; `testcontainers` for real-infra integration tests |
| Config | `caarlos0/env`, `knadh/koanf` | Env-var based, 12-factor |

## Testing

### Table-Driven Tests

Table-driven tests are the Go norm — a slice of test cases, each with inputs and expected outputs, iterated in a `t.Run` loop:

```go
func TestGetUser(t *testing.T) {
    tests := []struct {
        name    string
        id      string
        want    *User
        wantErr bool
    }{
        {"found", "1", &User{ID: "1"}, false},
        {"not found", "99", nil, true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // exercise code with tt inputs, compare against tt.want/tt.wantErr
        })
    }
}
```

### Integration Tests

For integration tests, `testcontainers-go` is the standard — spin up real Postgres/Redis/other infrastructure in Docker containers, test against real dependencies, then tear down. This catches schema mismatches, query bugs, and driver issues that mocks cannot.

## Scaffolding Tools

There is no single "official" scaffolding tool for Go microservices. The Go community prefers copying from a well-structured template over heavy code generation. Available options:

| Tool | Description |
|---|---|
| **`gonew`** (official, Go 1.23+) | Copies an existing Go project template from a remote module. Point it at any public repo — it clones the structure, renames the module path, and you start coding. The Go team's recommended approach. |
| **`encore.dev`** | Full platform — scaffolds services, handles infra (DBs, queues, cron), deploys. Define APIs as Go functions; it generates boilerplate. Very opinionated but batteries-included. |
| **`go-kit`** | Toolkit (not a generator). Provides packages for building microservices with consistent transports, endpoints, and middleware. Wire it yourself. |
| **`kratos`** | Full microservice framework with its own CLI generator (`kratos new`). Generates scaffold with handlers, config, middleware, etc. Popular in the Chinese Go community. |
| **`go-micro`** | Microservice framework with CLI (`micro new`). Opinionated plugin-based architecture with service discovery, message brokers, etc. |
| **`buf`** | Generates gRPC/Connect service stubs from protobuf. `buf generate` is the standard tool for protobuf-based services. |
| **`oapi-codegen`** | Generates HTTP server stubs + types from OpenAPI specs. Define your API contract in OpenAPI, generate the handler interfaces to implement. |

### The Most Common Approach

Most Go shops maintain their own template repository and use `gonew` to spin up new services:

```sh
gonew github.com/yourorg/service-template ./cmd/newservice
```

The template repo encodes the team's conventions — standard layout, `Makefile`, Dockerfile, `docker-compose`, CI config, health check handlers, config loading, graceful shutdown — everything pre-wired. This is the approach the Go team recommends.

## Key Takeaways

1. **Start flat** — One package until there is real pain. Premature `internal/service/repository` separation is the most common Go over-engineering mistake.
2. **Interfaces where you need them** — Define small interfaces at the call site (services define `UserStore` with the 1-3 methods they actually need), not where they are implemented.
3. **Explicit wiring** — No magic DI containers. Pass dependencies through constructors. The dependency graph lives in `main()`.
4. **Context everywhere** — `context.Context` is the first argument to every function that performs I/O.
5. **Errors are values** — Wrap with `fmt.Errorf("doing X: %w", err)`, handle at the edge. Never panic.
6. **No global state** — No `init()` for anything non-trivial, no package-level `*sql.DB` or `*http.Client`. Everything is constructed and passed explicitly.
7. **Explicit timeouts** — Never use the zero-value defaults on `http.Server`. Set `ReadTimeout`, `WriteTimeout`, and `IdleTimeout`.
8. **Graceful shutdown** — Catch SIGTERM/SIGINT, drain in-flight requests with a deadline, close resources.

## References

- Mat Ryer — "[How I Write HTTP Services After Eight Years](https://pace.dev/blog/2018/05/09/how-I-write-http-services-after-eight-years)" — The canonical blog post on idiomatic Go HTTP service structure.
- `gonew` — [pkg.go.dev/golang.org/x/tools/cmd/gonew](https://pkg.go.dev/golang.org/x/tools/cmd/gonew)
- `testcontainers-go` — [testcontainers.com](https://testcontainers.com)
- `buf` — [buf.build](https://buf.build)
