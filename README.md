[![Go Reference](https://pkg.go.dev/badge/github.com/neilotoole/fifomu.svg)](https://pkg.go.dev/github.com/neilotoole/fifomu)
[![Go Report Card](https://goreportcard.com/badge/neilotoole/fifomu)](https://goreportcard.com/report/neilotoole/fifomu)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](https://github.com/neilotoole/fifomu/blob/master/LICENSE)
![Pipeline](https://github.com/neilotoole/fifomu/actions/workflows/go.yml/badge.svg)

# fifomu: mutex with FIFO lock acquisition

[`fifomu`](https://pkg.go.dev/github.com/neilotoole/fifomu) is a Go
package that provides a [`Mutex`](https://pkg.go.dev/github.com/neilotoole/fifomu#Mutex)
whose [`Lock`](https://pkg.go.dev/github.com/neilotoole/fifomu#Mutex.Lock) method returns
the lock to queued callers in FIFO call order. This is in contrast to
[`sync.Mutex`](https://pkg.go.dev/sync#Mutex), where a single goroutine can
repeatedly lock and unlock and relock the mutex without handing off to other
lock waiter goroutines (that is, until after a 1ms starvation threshold, at
which point `sync.Mutex` enters a FIFO "starvation mode" for those starved
waiters, but that's too late for some use cases).

`fifomu.Mutex` implements the exported methods of `sync.Mutex` and is
API-compatible with it (and by extension implements [`sync.Locker`](https://pkg.go.dev/sync#Locker)).
It also provides a bonus context-aware [`LockContext`](https://pkg.go.dev/github.com/neilotoole/fifomu#Mutex.LockContext)
method.

> **FIFO caveat.** FIFO ordering applies to goroutines that have been queued
> as waiters. Arrival order across goroutines simultaneously contending for
> the internal `sync.Mutex` that protects the waiter queue is not guaranteed:
> a goroutine that called `Lock` slightly later may enqueue slightly earlier.
> The reordering window is bounded by the duration of the critical section
> that adds a waiter to the queue. If you need strict arrival-order
> semantics, you will need a different primitive (e.g., a ticket lock
> keyed by an atomically-incremented arrival counter).

> **`TryLock` deviation from `sync.Mutex`.** `fifomu.Mutex.TryLock` reports
> failure whenever the waiter queue is non-empty, even if the mutex is
> momentarily unheld. This is deliberate: a successful `TryLock` that jumped
> ahead of queued waiters would violate FIFO ordering.

Note: unless you need the FIFO behavior, you should prefer `sync.Mutex`.
For typical workloads, its "greedy-relock" behavior requires less goroutine
switching and yields better performance. See the [benchmarks](#benchmarks)
section below.


## When to use

`fifomu.Mutex` is useful when arrival-order fairness among contending
goroutines is a correctness property, not just a performance one. Concrete
examples:

- **Streaming fan-out.** A single producer writes bytes to a shared buffer;
  many consumers read from it. Each consumer needs the next chunk in the
  order it arrived at the read point, not whichever one the scheduler
  happens to favor. [`streamcache`](https://github.com/neilotoole/streamcache)
  uses `fifomu` to serialize readers this way.
- **Rate-limited work queues where ordering matters.** If 20 goroutines
  block on the same lock and each performs a few milliseconds of work,
  `sync.Mutex` may hand the lock back to the same goroutine multiple times
  before some waiters ever run. `fifomu.Mutex` distributes more evenly.
- **Test/debug scaffolding** where deterministic acquire order simplifies
  reasoning about which goroutine sees what state.

If your code doesn't care who acquires the lock next — which is the common
case — use `sync.Mutex`. It's simpler, faster, and the starvation-mode
fallback at 1ms is usually good enough.


## Usage

Add the package to your `go.mod` via `go get`:

```shell
go get github.com/neilotoole/fifomu
```

Then import it and use it as you would `sync.Mutex`:

```go
package main

import "github.com/neilotoole/fifomu"

func main() {
  mu := &fifomu.Mutex{}
  mu.Lock()
  defer mu.Unlock()
  
  // ... Do something critical
}
```

Additionally, `fifomu` provides a context-aware
[`Mutex.LockContext`](https://pkg.go.dev/github.com/neilotoole/fifomu#Mutex.LockContext)
method that blocks until the mutex is available or `ctx` is done, returning
the value of [`context.Cause(ctx)`](https://pkg.go.dev/context#Cause).

```go
func foo(ctx context.Context) error {
  mu := &fifomu.Mutex{}
  if err := mu.LockContext(ctx); err != nil {
    // Oh dear, ctx was cancelled
    return err
  }
  defer mu.Unlock()
  
  // ... Do something critical
  
  return nil
}
```

Two caveats for `LockContext`:

- If `ctx` is already done when `LockContext` is called and the lock is
  available with no queued waiters, `LockContext` may still succeed without
  blocking.
- If the mutex becomes available concurrently with `ctx` cancellation,
  `LockContext` may acquire the mutex and return `nil` even though `ctx`
  is done.

In both cases, callers that require ctx-strict behavior should re-check
`ctx.Err()` after acquiring.


## How it works

Internally, `fifomu.Mutex` is a `sync.Mutex` plus a FIFO queue of waiters:

- **Uncontended acquires** take the inner mutex briefly, flip a `locked`
  bool, and return. This is ~3–5 ns/op and allocation-free.
- **When the mutex is held**, a caller appends itself to the waiter queue
  — a pooled doubly-linked list of buffered(1) channels — and blocks on
  its own channel.
- **Unlock** walks the queue head, flips `locked`, and delivers a
  non-blocking send on the chosen waiter's channel. No spinning.
- **`LockContext`** adds a `<-ctx.Done()` arm to the block. If `ctx`
  fires before the signal, a cancel handler re-acquires the inner mutex,
  dequeues the waiter, and returns `context.Cause(ctx)`.
- **Every happy path and every cancel path** returns the waiter channel
  to a `sync.Pool` with its buffer empty, so `Lock` and `LockContext` are
  allocation-free in steady state.

Buffered(1) channels (rather than unbuffered) are load-bearing: the
`Unlock` side sends non-blockingly while still holding the inner mutex,
so a racing cancellation on the `LockContext` side cannot strand the
sender. Violating that invariant (enqueuing a non-empty channel)
triggers a panic rather than silently reintroducing a deadlock; this is
enforced in `notifyWaiters` and tested by `TestNotifyWaiters_PanicsOnViolatedInvariant`.


## Testing and correctness

`fifomu` is a concurrency primitive, so every change is validated under
the race detector:

```shell
go test -race -count=3 ./...
```

The test binary exercises:

- An adapted port of Go's stdlib `sync/mutex_test.go` (`TestMutex`,
  `TestMutexFairness`, etc.).
- A regression test for the `LockContext`/`Unlock` cancellation deadlock
  that the initial implementation shipped with. Under the old unbuffered-
  channel design, the test fails within seconds.
- Invariant tests: queue ordering under mixed `Lock`/`LockContext`,
  middle-of-queue cancel, cross-goroutine `Unlock`, idempotent
  `list.remove`, and the exact panic messages matching `sync.Mutex`.
- A chaos test: 20 goroutines for 2 seconds, each picking a random
  operation, with an atomic counter asserting at most one holder at a
  time.
- [`go.uber.org/goleak`](https://pkg.go.dev/go.uber.org/goleak) in
  `TestMain`: any goroutine left running after the test binary exits
  fails the run.

No data races or goroutine leaks are tolerated.


## Benchmarks

The benchmark results below were obtained on a 2021 MacBook Pro (M1 Max)
using Go 1.26.

The benchmarks compare three mutex implementations:

- `stdlib` is `sync.Mutex`
- `fifomu` is `fifomu.Mutex` (this package)
- `semaphoreMu` is a trivial mutex implementation built on top of 
 [`semaphore.Weighted`](https://pkg.go.dev/golang.org/x/sync/semaphore); it
 exists only in the test code, as a comparison baseline.

Across contended benchmarks, `fifomu.Mutex.Lock` runs roughly 1.3×–3.5×
slower than `sync.Mutex` (`BenchmarkMutex` and `BenchmarkMutexSlack` near
the low end, `BenchmarkMutexNoSpin` at the high end). The outlier is
`BenchmarkMutexSpin`, where `fifomu` is ~6.5× slower. `fifomu` is always
faster than the baseline `semaphoreMu` implementation, and unlike that
baseline, calls to `fifomu`'s `Lock` and `LockContext` methods do not
allocate.

`LockContext` adds a ~3-10% per-call overhead over `Lock` on contended
paths (the extra `ctx.Done()` arm in the select). Its cancel handler is
~55% the cost of a contended `Lock` and is fully allocation-free —
pooled waiter channels are recycled so cancel cycles don't heap-allocate.

Benchmark your own workload before committing to `fifomu.Mutex`. In many
cases you can design around the need for FIFO lock acquisition — reaching
for it should prompt a pause to check whether a different coordination
primitive (a channel, a bounded work queue, `sync.Cond`) would be a better
fit. But when FIFO semantics are what you genuinely need, `fifomu` is the
most straightforward path.

Benchmark name shapes (inherited from the stdlib `sync/mutex_test.go`):

- `Uncontended` — parallel workers, each with its own mutex. Measures
  raw fast-path cost.
- `Mutex` — parallel workers contending on one mutex. The baseline
  contended workload.
- `Slack` — like `Mutex` but with 10× goroutines over GOMAXPROCS, which
  forces heavier queueing.
- `Work` — like `Mutex` but with a short busy-loop in each critical
  section.
- `WorkSlack` — combines both.
- `NoSpin` — simulates a workload where spinning in the mutex is
  unprofitable; `fifomu` doesn't spin, so the gap is smaller here.
- `Spin` — simulates a workload where spinning *would* be profitable;
  `fifomu` can't spin, which is the fundamental reason it loses worst
  on this one.
- `LockContext_*` — adapts the shapes to `LockContext`, plus a `Cancel`
  variant exercising the slow-path cancel handler.

```
$ GOMAXPROCS=10 go test -bench . -benchmem -run=^$
goos: darwin
goarch: arm64
pkg: github.com/neilotoole/fifomu
cpu: Apple M1 Max
BenchmarkLockContext_Uncontended-10        349898984          3.457 ns/op    0 B/op   0 allocs/op
BenchmarkLockContext_Contended-10            6611028        191.8   ns/op    0 B/op   0 allocs/op
BenchmarkLockContext_Cancel-10              13624764         88.34  ns/op    0 B/op   0 allocs/op
BenchmarkMutexUncontended/stdlib-10        696781160          1.793 ns/op    0 B/op   0 allocs/op
BenchmarkMutexUncontended/fifomu-10        335351145          3.603 ns/op    0 B/op   0 allocs/op
BenchmarkMutexUncontended/semaphoreMu-10   305443803          3.504 ns/op    0 B/op   0 allocs/op
BenchmarkMutex/stdlib-10                     9246698        123.9   ns/op    0 B/op   0 allocs/op
BenchmarkMutex/fifomu-10                     7253898        163.1   ns/op    0 B/op   0 allocs/op
BenchmarkMutex/semaphoreMu-10                5127471        230.3   ns/op  175 B/op   2 allocs/op
BenchmarkMutexSlack/stdlib-10               10402440        113.9   ns/op    0 B/op   0 allocs/op
BenchmarkMutexSlack/fifomu-10                7020883        175.0   ns/op    0 B/op   0 allocs/op
BenchmarkMutexSlack/semaphoreMu-10           4795321        263.8   ns/op  176 B/op   3 allocs/op
BenchmarkMutexWork/stdlib-10                 9208046        132.7   ns/op    0 B/op   0 allocs/op
BenchmarkMutexWork/fifomu-10                 5993347        200.8   ns/op    0 B/op   0 allocs/op
BenchmarkMutexWork/semaphoreMu-10            4378946        277.6   ns/op  175 B/op   2 allocs/op
BenchmarkMutexWorkSlack/stdlib-10           10696843        111.9   ns/op    0 B/op   0 allocs/op
BenchmarkMutexWorkSlack/fifomu-10            5888556        201.2   ns/op    0 B/op   0 allocs/op
BenchmarkMutexWorkSlack/semaphoreMu-10       4249790        298.9   ns/op  175 B/op   2 allocs/op
BenchmarkMutexNoSpin/stdlib-10               6842372        183.0   ns/op   12 B/op   0 allocs/op
BenchmarkMutexNoSpin/fifomu-10               2220228        647.6   ns/op   12 B/op   0 allocs/op
BenchmarkMutexNoSpin/semaphoreMu-10          1978136        570.3   ns/op   56 B/op   1 allocs/op
BenchmarkMutexSpin/stdlib-10                 3180140        369.1   ns/op    0 B/op   0 allocs/op
BenchmarkMutexSpin/fifomu-10                  493010       2404     ns/op    0 B/op   0 allocs/op
BenchmarkMutexSpin/semaphoreMu-10             478358       2511     ns/op  175 B/op   2 allocs/op
```

## Requirements

- **Go 1.26 or later.** The package is generic-free, but the tests and
  benchmarks use `for b.Loop()` (Go 1.24+) and `sync.WaitGroup.Go`
  (Go 1.25+).
- **Runtime dependencies:** [`golang.org/x/sync`](https://pkg.go.dev/golang.org/x/sync),
  and that is used only by the test baselines — `fifomu.Mutex` itself
  depends only on the standard library (`context` and `sync`).
- **Test-only dependency:** [`go.uber.org/goleak`](https://pkg.go.dev/go.uber.org/goleak).


## Related

- [`streamcache`](https://github.com/neilotoole/streamcache) uses `fifomu` to ensure fairness
  for concurrent readers of a stream.
- [`sq`](https://github.com/neilotoole/sq) uses `fifomu` indirectly via `streamcache`.
