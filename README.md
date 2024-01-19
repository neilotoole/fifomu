[![Go Reference](https://pkg.go.dev/badge/github.com/neilotoole/fifomu.svg)](https://pkg.go.dev/github.com/neilotoole/fifomu)
[![Go Report Card](https://goreportcard.com/badge/neilotoole/fifomu)](https://goreportcard.com/report/neilotoole/fifomu)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](https://github.com/neilotoole/fifomu/blob/master/LICENSE)
![Pipeline](https://github.com/neilotoole/fifomu/actions/workflows/go.yml/badge.svg)

# fifomu: mutex with FIFO lock acquisition

[`fifomu`](https://pkg.go.dev/github.com/neilotoole/fifomu) is a Go
package that provides a [`Mutex`](https://pkg.go.dev/github.com/neilotoole/fifomu#Mutex)
whose [`Lock`](https://pkg.go.dev/github.com/neilotoole/fifomu#Mutex.Lock) method returns
the lock to callers in FIFO call order. This is in contrast to [`sync.Mutex`](https://pkg.go.dev/sync#Mutex),
where a single goroutine can repeatedly lock and unlock and relock the mutex
without handing off to other lock waiter goroutines (that is, until after a 1ms
starvation threshold, at which point `sync.Mutex` enters a FIFO "starvation mode"
for those starved waiters, but that's too late for some use cases).

`fifomu.Mutex` implements the exported methods of `sync.Mutex` and thus is
a drop-in replacement (and by extension, also implements [`sync.Locker`](https://pkg.go.dev/sync#Locker)).
It also provides a bonus context-aware [`LockContext`](https://pkg.go.dev/github.com/neilotoole/fifomu#Mutex.LockContext)
method.

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
method that blocks until the mutex is available or ctx is done, returning the
value of [`context.Cause(ctx)`](https://pkg.go.dev/context#Cause).

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

## Benchmarks

The benchmark results below were obtained on a 2021 MacBook Pro (M1 Max)
using Go 1.21.6.

The benchmarks compare three mutex implementations:

- `stdlib` is `sync.Mutex`
- `fifomu` is `fifomu.Mutex` (this package)
- `semaphoreMu` is a trivial mutex implementation built on top of 
 [`semaphore.Weighted`](https://pkg.go.dev/golang.org/x/sync/semaphore); it
 exists only in the test code, as a comparison baseline.

What conclusions can be drawn from the results below? Well, `fifomu`
is always at least 1.5x slower than `sync.Mutex`, and often more. The
worst results are in `BenchmarkMutexSpin`, where `fifomu` is 10x slower
than `sync.Mutex`. On the plus side, `fifomu` is always faster than
the baseline `semaphoreMu` implementation, and unlike that baseline,
calls to `fifomu`'s `Mutex.Lock` method do not allocate.

Benchmark your own workload before committing to `fifomu.Mutex`. In many
cases you will be able to design around the need for FIFO lock acquisition.
It's a bit of a code smell to begin with, but, that said, sometimes it's the
most straightforward solution.

```
$ GOMAXPROCS=10 go test -bench .
goos: darwin
goarch: arm64
pkg: github.com/neilotoole/fifomu
BenchmarkMutexUncontended/stdlib-10             738603763              1.654 ns/op             0 B/op          0 allocs/op
BenchmarkMutexUncontended/fifomu-10             361784637              3.266 ns/op             0 B/op          0 allocs/op
BenchmarkMutexUncontended/semaphoreMu-10        344456692              3.298 ns/op             0 B/op          0 allocs/op
BenchmarkMutex/stdlib-10                        10600124               110.2 ns/op             0 B/op          0 allocs/op
BenchmarkMutex/fifomu-10                         7731985               153.9 ns/op             0 B/op          0 allocs/op
BenchmarkMutex/semaphoreMu-10                    5520886               217.4 ns/op           159 B/op          2 allocs/op
BenchmarkMutexSlack/stdlib-10                   11174293               109.4 ns/op             0 B/op          0 allocs/op
BenchmarkMutexSlack/fifomu-10                    6911655               164.0 ns/op             0 B/op          0 allocs/op
BenchmarkMutexSlack/semaphoreMu-10               5330490               227.0 ns/op           159 B/op          2 allocs/op
BenchmarkMutexWork/stdlib-10                    10140280               120.0 ns/op             0 B/op          0 allocs/op
BenchmarkMutexWork/fifomu-10                     6797134               176.5 ns/op             0 B/op          0 allocs/op
BenchmarkMutexWork/semaphoreMu-10                4979712               240.6 ns/op           159 B/op          2 allocs/op
BenchmarkMutexWorkSlack/stdlib-10               10004154               119.4 ns/op             0 B/op          0 allocs/op
BenchmarkMutexWorkSlack/fifomu-10                6362150               188.7 ns/op             0 B/op          0 allocs/op
BenchmarkMutexWorkSlack/semaphoreMu-10           4678826               256.8 ns/op           160 B/op          3 allocs/op
BenchmarkMutexNoSpin/stdlib-10                   9500152               131.2 ns/op            12 B/op          0 allocs/op
BenchmarkMutexNoSpin/fifomu-10                   3132691               381.3 ns/op            12 B/op          0 allocs/op
BenchmarkMutexNoSpin/semaphoreMu-10              2502936               461.0 ns/op            51 B/op          1 allocs/op
BenchmarkMutexSpin/stdlib-10                     5025486               233.7 ns/op             0 B/op          0 allocs/op
BenchmarkMutexSpin/fifomu-10                      514082               2362 ns/op              0 B/op          0 allocs/op
BenchmarkMutexSpin/semaphoreMu-10                 496808               2512 ns/op            159 B/op          2 allocs/op
```
