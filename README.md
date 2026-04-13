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

`fifomu.Mutex` implements the exported methods of `sync.Mutex` and thus is
a drop-in replacement (and by extension, also implements [`sync.Locker`](https://pkg.go.dev/sync#Locker)).
It also provides a bonus context-aware [`LockContext`](https://pkg.go.dev/github.com/neilotoole/fifomu#Mutex.LockContext)
method.

> **FIFO caveat.** FIFO ordering applies to goroutines that have been queued
> as waiters. Arrival order across goroutines simultaneously contending for
> the internal state protecting the waiter queue is not guaranteed: a
> goroutine that called `Lock` slightly later may enqueue slightly earlier.
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
paths (the ctx channel added to the select). Its cancel handler is
~55% the cost of a contended `Lock` and is fully allocation-free —
pooled waiter channels are recycled so cancel cycles don't heap-allocate.

Benchmark your own workload before committing to `fifomu.Mutex`. In many
cases you will be able to design around the need for FIFO lock acquisition.
It's a bit of a code smell to begin with, but, that said, sometimes it's the
most straightforward solution.

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

## Related

- [`streamcache`](https://github.com/neilotoole/streamcache) uses `fifomu` to ensure fairness
  for concurrent readers of a stream.
- [`sq`](https://github.com/neilotoole/sq) uses `fifomu` indirectly via `streamcache`.
