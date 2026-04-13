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
> In practice this reordering window is limited to the brief critical section
> protecting the queue itself (microseconds under typical load). If you need
> strict arrival-order semantics, consider a ticket lock.

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

Across contended benchmarks, `fifomu` runs roughly 1.3×–1.8× slower than
`sync.Mutex`. The worst case is `BenchmarkMutexSpin`, where `fifomu` is
~6.5× slower. On the plus side, `fifomu` is always faster than the baseline
`semaphoreMu` implementation, and unlike that baseline, calls to `fifomu`'s
`Mutex.Lock` method do not allocate.

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
BenchmarkMutexUncontended/stdlib-10         652553979          1.902 ns/op    0 B/op   0 allocs/op
BenchmarkMutexUncontended/fifomu-10         318887648          4.731 ns/op    0 B/op   0 allocs/op
BenchmarkMutexUncontended/semaphoreMu-10    332533878          3.260 ns/op    0 B/op   0 allocs/op
BenchmarkMutex/stdlib-10                      9594367        127.1   ns/op    0 B/op   0 allocs/op
BenchmarkMutex/fifomu-10                      7475685        160.0   ns/op    0 B/op   0 allocs/op
BenchmarkMutex/semaphoreMu-10                 5432120        224.9   ns/op  175 B/op   2 allocs/op
BenchmarkMutexSlack/stdlib-10                10897606        101.5   ns/op    0 B/op   0 allocs/op
BenchmarkMutexSlack/fifomu-10                 6567987        167.1   ns/op    0 B/op   0 allocs/op
BenchmarkMutexSlack/semaphoreMu-10            4994701        262.4   ns/op  175 B/op   2 allocs/op
BenchmarkMutexWork/stdlib-10                  9939190        135.4   ns/op    0 B/op   0 allocs/op
BenchmarkMutexWork/fifomu-10                  5911956        202.4   ns/op    0 B/op   0 allocs/op
BenchmarkMutexWork/semaphoreMu-10             4623722        269.5   ns/op  175 B/op   2 allocs/op
BenchmarkMutexWorkSlack/stdlib-10            10335938        110.1   ns/op    0 B/op   0 allocs/op
BenchmarkMutexWorkSlack/fifomu-10             6095788        193.9   ns/op    0 B/op   0 allocs/op
BenchmarkMutexWorkSlack/semaphoreMu-10        4360573        277.5   ns/op  175 B/op   2 allocs/op
BenchmarkMutexNoSpin/stdlib-10                8617663        148.4   ns/op   12 B/op   0 allocs/op
BenchmarkMutexNoSpin/fifomu-10                2890854        415.4   ns/op   12 B/op   0 allocs/op
BenchmarkMutexNoSpin/semaphoreMu-10           2581249        465.0   ns/op   55 B/op   1 allocs/op
BenchmarkMutexSpin/stdlib-10                  5219571        360.0   ns/op    0 B/op   0 allocs/op
BenchmarkMutexSpin/fifomu-10                   514402       2339     ns/op    0 B/op   0 allocs/op
BenchmarkMutexSpin/semaphoreMu-10              483753       2472     ns/op  175 B/op   2 allocs/op
```

## Related

- [`streamcache`](https://github.com/neilotoole/streamcache) uses `fifomu` to ensure fairness
  for concurrent readers of a stream.
- [`sq`](https://github.com/neilotoole/sq) uses `fifomu` indirectly via `streamcache`.
