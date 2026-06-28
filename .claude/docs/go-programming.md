# Go Programming Notes

## Goroutines and CPU Utilization

### Do goroutines need to align with CPU cores?

**No.** Goroutines are not OS threads — that's the whole point. You can (and should) spawn thousands or even millions of goroutines without worrying about CPU core count. The Go runtime scheduler maps M goroutines onto N OS threads (which then map to CPU cores), handling multiplexing for you.

```go
// This is perfectly fine — 10,000 goroutines on a 4-core machine
for i := 0; i < 10_000; i++ {
    go doWork(i)
}
```

### The Go scheduler model (G-M-P)

```
Goroutines (G)  ──▶  Logical Processors (P)  ──▶  OS Threads (M)  ──▶  CPU Cores
   (thousands)         (GOMAXPROCS, default = cores)   (grows as needed)
```

- **GOMAXPROCS** (defaults to `runtime.NumCPU()`) controls how many OS threads can execute Go code *simultaneously*. This is the only thing you tune to CPU cores.
- Goroutines are multiplexed onto those P's — when one blocks (I/O, channel, syscall), another takes its place. No core sits idle.

### How to fully utilize all cores

#### 1. Don't touch GOMAXPROCS (usually)

```go
// The default already uses all cores. You rarely need to change this.
// GOMAXPROCS defaults to runtime.NumCPU()
```

Only adjust it in containerized environments where cgroup limits expose fewer cores than the host reports:

```go
import "go.uber.org/automaxprocs/maxprocs"

func main() {
    // Automatically sets GOMAXPROCS to the cgroup CPU limit
    // Critical for Kubernetes/Docker deployments
    _, _ = maxprocs.Set(maxprocs.Logger(nil))
}
```

#### 2. Avoid serial bottlenecks

All cores are utilized only if the work is truly parallel. Common killers:

```go
// BAD: Mutex contention serializes everything
var mu sync.Mutex
for i := 0; i < 1000; i++ {
    go func() {
        mu.Lock()
        result = expensiveOp(result) // Only one goroutine runs at a time
        mu.Unlock()
    }()
}

// GOOD: Do the expensive work outside the lock
for i := 0; i < 1000; i++ {
    go func() {
        partial := expensiveOp()     // Parallel — all cores busy
        mu.Lock()
        results = append(results, partial) // Quick critical section
        mu.Unlock()
    }()
}
```

#### 3. Worker pool pattern for bounded parallelism

When you have many items but want to control concurrency (rate limits, memory):

```go
func processAll(items []Item, concurrency int) []Result {
    sem := make(chan struct{}, concurrency) // Bounded parallelism
    var wg sync.WaitGroup
    results := make([]Result, len(items))

    for i, item := range items {
        wg.Add(1)
        go func(i int, item Item) {
            defer wg.Done()
            sem <- struct{}{}        // Acquire
            defer func() { <-sem }() // Release
            results[i] = process(item)
        }(i, item)
    }
    wg.Wait()
    return results
}
```

Set `concurrency` based on the bottleneck, not core count:
- **CPU-bound work**: `runtime.GOMAXPROCS(0)` (match cores)
- **I/O-bound work**: much higher — 50, 100, or more
- **External API with rate limits**: whatever the API allows

#### 4. errgroup for structured concurrency

```go
import "golang.org/x/sync/errgroup"

func fanOut(ctx context.Context, urls []string) error {
    g, ctx := errgroup.WithContext(ctx)
    g.SetLimit(10) // max 10 concurrent

    for _, url := range urls {
        url := url
        g.Go(func() error {
            return fetch(ctx, url)
        })
    }
    return g.Wait() // returns first error, cancels context
}
```

### Key principles

| Principle | Why |
|---|---|
| **Goroutines are cheap** (~2 KB stack, grows/shrinks) | Never hesitate to spawn more |
| **Don't tie goroutine count to cores** | Scheduler handles mapping; you just express concurrency |
| **GOMAXPROCS = cores is the right default** | Already the default; only tweak for cgroup-aware containers |
| **Bound by bottleneck, not cores** | I/O-bound → high concurrency; CPU-bound → ~GOMAXPROCS |
| **Avoid shared mutable state** | Use channels, or isolate state with `sync.Mutex` only where needed |
| **Let the runtime schedule** | Don't try to pin goroutines to cores unless you're writing a database |

### Checking if cores are utilized

```go
import "runtime"

fmt.Println("GOMAXPROCS:", runtime.GOMAXPROCS(0)) // logical processors
fmt.Println("NumCPU:", runtime.NumCPU())           // cores visible to OS
fmt.Println("NumGoroutine:", runtime.NumGoroutine()) // active goroutines
```

If `NumGoroutine` is consistently near 1 during work you expect to be parallel, you likely have a serial bottleneck (single channel consumer, mutex contention, sequential loop without `go`).

**Bottom line**: spawn goroutines freely for any independent work, use a bound only when the underlying resource demands it, and trust the scheduler — that's what it's for.
