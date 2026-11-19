[![Go Reference](https://pkg.go.dev/badge/github.com/neilotoole/fifomu.svg)](https://pkg.go.dev/github.com/neilotoole/fifomu)
[![Go Report Card](https://goreportcard.com/badge/neilotoole/fifomu)](https://goreportcard.com/report/neilotoole/fifomu)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](https://github.com/neilotoole/fifomu/blob/master/LICENSE)
![Pipeline](https://github.com/neilotoole/fifomu/actions/workflows/go.yml/badge.svg)
[![codecov](https://codecov.io/gh/neilotoole/fifomu/branch/master/graph/badge.svg)](https://codecov.io/gh/neilotoole/fifomu)
[![Benchmarks](https://img.shields.io/badge/benchmarks-trend-blue)](https://neilotoole.github.io/fifomu/dev/bench/)

# fifomu: mutex with FIFO lock acquisition

[`fifomu`](https://pkg.go.dev/github.com/neilotoole/fifomu) is a Go
package that provides a [`Mutex`](https://pkg.go.dev/github.com/neilotoole/fifomu#Mutex)
whose [`Lock`](https://pkg.go.dev/github.com/neilotoole/fifomu#Mutex.Lock) method grants
the lock to callers in the order in which they called it. This is in contrast to
[`sync.Mutex`](https://pkg.go.dev/sync#Mutex), where a single goroutine can
repeatedly lock and unlock and relock the mutex without handing off to other
lock waiter goroutines (that is, until after a 1ms starvation threshold, at
which point `sync.Mutex` enters a FIFO "starvation mode" for those starved
waiters, but that's too late for some use cases).

`fifomu.Mutex` implements the exported methods of `sync.Mutex` and is
API-compatible with it (and by extension implements [`sync.Locker`](https://pkg.go.dev/sync#Locker)).
It also provides a bonus context-aware [`LockContext`](https://pkg.go.dev/github.com/neilotoole/fifomu#Mutex.LockContext)
method.

> **FIFO guarantee.** `Lock` and `LockContext` begin by taking a ticket
> from an atomic counter, and the mutex is granted strictly in ticket order.
> A caller's place in line is fixed by that single atomic increment, which
> never blocks: once a goroutine holds a ticket, no goroutine that takes a
> later ticket acquires the mutex first, however the scheduler treats either
> of them afterwards. Two goroutines that call `Lock` at almost the same
> instant are ordered by which of them increments the counter first.

> **`TryLock` deviation from `sync.Mutex`.** `fifomu.Mutex.TryLock` succeeds
> only if the mutex is unlocked and no other caller is in the process of
> acquiring it. This is deliberate: a `TryLock` that took the lock ahead of
> a goroutine that had already called `Lock` would violate FIFO ordering.

> **`Unlock` deviation from `sync.Mutex`.** Unlocking an unlocked
> `fifomu.Mutex` is an ordinary panic, with the same message `sync.Mutex`
> reports. In `sync.Mutex` it is a fatal error that cannot be recovered.

Note: unless you need the FIFO behavior, you should prefer `sync.Mutex`.
For typical workloads, its "greedy-relock" behavior requires less goroutine
switching and yields better performance. See the [benchmarks](#benchmarks)
section below.


## When to use

`fifomu.Mutex` is useful when the order in which contending goroutines
acquire the lock matters, and `sync.Mutex`'s willingness to let a running
goroutine re-take the lock ahead of sleeping waiters is a problem. Concrete
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
- **Test/debug scaffolding** where acquiring in call order simplifies
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
  available with no callers waiting, `LockContext` may still succeed without
  blocking.
- If the mutex becomes available concurrently with `ctx` cancellation,
  `LockContext` may acquire the mutex and return `nil` even though `ctx`
  is done.

In both cases, callers that require ctx-strict behavior should re-check
`ctx.Err()` after acquiring.


## How it works

Internally, `fifomu.Mutex` is a [ticket lock](https://en.wikipedia.org/wiki/Ticket_lock):
two atomic counters, `next` (the next ticket to issue) and `serving` (the
ticket that owns the mutex), plus a `sync.Mutex` guarding a ticket-sorted
list of waiters.

- **`Lock`** takes a ticket by incrementing `next`. If that ticket is
  already being served, the caller owns the mutex: an uncontended acquire
  is two atomic operations, never touches the inner mutex, and does not
  allocate.
- **When the mutex is held**, the caller takes the inner mutex, queues a
  buffered(1) channel under its ticket, and blocks on that channel. The
  list is kept sorted by ticket, so a goroutine that loses the race for the
  inner mutex is still served in ticket order.
- **`Unlock`** increments `serving`. If no later ticket has been issued it
  is done. Otherwise it takes the inner mutex and, if the holder of the
  ticket now being served has queued, delivers a non-blocking send on its
  channel. If that holder hasn't queued yet, it finds its ticket being
  served when it gets there. No spinning.
- **`LockContext`** adds a `<-ctx.Done()` arm to the block. If `ctx`
  fires before the signal, the caller re-acquires the inner mutex and
  abandons its ticket, returning `context.Cause(ctx)`. An abandoned ticket
  can't just be forgotten, because `Unlock` has to tell it apart from a
  ticket whose holder hasn't queued yet. So it becomes a tombstone that
  `Unlock` skips. Adjacent tombstones merge into ranges, and tombstones for
  the newest tickets are removed by rolling `next` back, so repeated
  timeouts against a long-held mutex don't accumulate state.
- **`TryLock`** is a single compare-and-swap that issues itself the ticket
  being served, which only succeeds if no ticket is outstanding.
- **Every happy path and every cancel path** returns the waiter channel
  to a `sync.Pool` with its buffer empty, so `Lock` and `LockContext` are
  allocation-free in steady state.

Strict ordering has a cost that is inherent to the design: an unlocked
mutex is reserved for the next ticket holder even if that goroutine isn't
currently running, and everyone behind it waits.

Buffered(1) channels (rather than unbuffered) are load-bearing: the
`Unlock` side sends non-blockingly while still holding the inner mutex,
so a racing cancellation on the `LockContext` side cannot strand the
sender. Violating that invariant (enqueuing a non-empty channel)
triggers a panic rather than silently reintroducing a deadlock; this is
enforced in `waiter.signal` (called from `handoff`) and tested by
`TestHandoff_PanicsOnViolatedInvariant`.


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
- Ticket-order tests: a goroutine left blocked on the inner mutex is not
  overtaken by a later caller, goroutines that reach the waiter list out
  of order are still served in ticket order, and a stress test asserts
  that every acquisition is by the ticket being served.
- Cancellation tests: abandoned tickets are skipped, the newest abandoned
  ticket is rolled back, and overlapping timeouts leave a bounded number
  of tombstones.
- Invariant tests: queue ordering under mixed `Lock`/`LockContext`,
  middle-of-queue cancel, cross-goroutine `Unlock`, idempotent
  `list.remove`, and the double-unlock panic message matching
  `sync.Mutex`.
- Branch tests: each rarely-taken path (an in-flight ticket holder being
  handed the mutex, a waiter abandoning the ticket being served, a signal
  beating a cancellation, the ticket counter wrapping around) is forced
  deterministically.
- An exhaustive scenario test: every sequence of `Lock`, `LockContext`,
  `TryLock`, `Unlock` and cancellation up to a fixed length, checked step
  by step against a trivially correct reference model.
- A model checker: the locking protocol restated as a state machine, with
  every interleaving of a few threads explored and mutual exclusion, FIFO
  order, freedom from deadlock and the documented invariants checked in
  every state. It checks the protocol rather than the compiled code, and
  it is itself tested: protocol bugs seeded on purpose must be caught.
- A chaos test: 20 goroutines for 2 seconds, each picking a random
  operation, with an atomic counter asserting at most one holder at a
  time.
- [`go.uber.org/goleak`](https://pkg.go.dev/go.uber.org/goleak) in
  `TestMain`: any goroutine left running after the test binary exits
  fails the run.

No data races or goroutine leaks are tolerated.

The scenario test and the model checker run at small bounds by default and
at larger ones in CI. To go deeper locally:

```shell
FIFOMU_SCENARIO_LEN=8 go test -run '^TestScenarios_' .
FIFOMU_MODEL=2x4,3x2,4x1 go test -run '^TestModel_' .
```

A separate, on-demand [soak workflow](.github/workflows/soak.yml) re-runs
the suite for a set time across architectures, `GOMAXPROCS` values, and
with and without the race detector.


## Benchmarks

The benchmark results below were obtained on a 2021 MacBook Pro (M1 Max)
using Go 1.27.

The benchmarks compare three mutex implementations:

- `stdlib` is `sync.Mutex`
- `fifomu` is `fifomu.Mutex` (this package)
- `semaphoreMu` is a trivial mutex implementation built on top of 
 [`semaphore.Weighted`](https://pkg.go.dev/golang.org/x/sync/semaphore); it
 exists only in the test code, as a comparison baseline.

Uncontended, `fifomu.Mutex` costs about the same as `sync.Mutex`: its
fast path is two atomic operations.

Across contended benchmarks, `fifomu.Mutex.Lock` runs roughly 1.5×–2.6×
slower than `sync.Mutex` (`BenchmarkMutex` at the low end,
`BenchmarkMutexNoSpin` at the high end). The outlier is
`BenchmarkMutexSpin`, where `fifomu` is ~10× slower. `fifomu` is faster
than the baseline `semaphoreMu` implementation in every benchmark, and
unlike that baseline, calls to `fifomu`'s `Lock` and `LockContext` methods
do not allocate.

`LockContext` adds a small per-call overhead over `Lock` on contended
paths — the table below shows roughly 15–20% for `BenchmarkLockContext_Contended`
vs `BenchmarkMutex/fifomu` (the extra `ctx.Done()` arm in the select).
Its cancel handler runs in roughly half the time of a contended `Lock`
and is fully allocation-free — pooled waiter channels are recycled so
cancel cycles don't heap-allocate.

Benchmark your own workload before committing to `fifomu.Mutex`. In many
cases you can design around the need for FIFO lock acquisition — reaching
for it should prompt a pause to check whether a different coordination
primitive (a channel, a bounded work queue, `sync.Cond`) would be a better
fit. But when FIFO semantics are what you genuinely need, `fifomu` is the
most straightforward path.

Benchmark name shapes (inherited from the stdlib `sync/mutex_test.go`):

- `Uncontended` / `FastPath` — parallel workers, each with its own mutex
  (no contention). Measures raw fast-path cost.
- `Mutex` — parallel workers contending on one mutex. The baseline
  contended workload.
- `Slack` — like `Mutex` but with 10× goroutines over GOMAXPROCS, which
  forces heavier queueing.
- `Work` — like `Mutex` but with a short busy-loop in each critical
  section.
- `WorkSlack` — combines both.
- `NoSpin` — models a workload where the stdlib's adaptive spin is
  unprofitable. In practice the stdlib still spins some (hence its
  higher allocation-per-op), and `fifomu`'s forced scheduler handoff
  on every acquire still costs more, so the gap here is larger than
  the basic `BenchmarkMutex` (roughly 2.6× vs 1.5×).
- `Spin` — models a workload where spinning *would* be profitable;
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
BenchmarkLockContext_FastPath-10           867633475          1.444 ns/op    0 B/op   0 allocs/op
BenchmarkLockContext_Contended-10            5860754        210.7   ns/op    0 B/op   0 allocs/op
BenchmarkLockContext_Cancel-10              12342715         95.46  ns/op    0 B/op   0 allocs/op
BenchmarkMutexUncontended/stdlib-10        715136355          1.720 ns/op    0 B/op   0 allocs/op
BenchmarkMutexUncontended/fifomu-10       1000000000          1.135 ns/op    0 B/op   0 allocs/op
BenchmarkMutexUncontended/semaphoreMu-10   343491252          3.753 ns/op    0 B/op   0 allocs/op
BenchmarkMutex/stdlib-10                     9430585        122.5   ns/op    0 B/op   0 allocs/op
BenchmarkMutex/fifomu-10                     5693994        178.5   ns/op    0 B/op   0 allocs/op
BenchmarkMutex/semaphoreMu-10                5071872        230.9   ns/op  176 B/op   3 allocs/op
BenchmarkMutexSlack/stdlib-10               10174064        111.7   ns/op    0 B/op   0 allocs/op
BenchmarkMutexSlack/fifomu-10                6370922        188.6   ns/op    0 B/op   0 allocs/op
BenchmarkMutexSlack/semaphoreMu-10           4715474        249.6   ns/op  175 B/op   2 allocs/op
BenchmarkMutexWork/stdlib-10                 9017374        132.4   ns/op    0 B/op   0 allocs/op
BenchmarkMutexWork/fifomu-10                 5774289        210.3   ns/op    0 B/op   0 allocs/op
BenchmarkMutexWork/semaphoreMu-10            4640755        267.4   ns/op  175 B/op   2 allocs/op
BenchmarkMutexWorkSlack/stdlib-10           10735240        120.3   ns/op    0 B/op   0 allocs/op
BenchmarkMutexWorkSlack/fifomu-10            5727982        211.7   ns/op    0 B/op   0 allocs/op
BenchmarkMutexWorkSlack/semaphoreMu-10       4466762        269.4   ns/op  175 B/op   2 allocs/op
BenchmarkMutexNoSpin/stdlib-10               8453712        150.7   ns/op   12 B/op   0 allocs/op
BenchmarkMutexNoSpin/fifomu-10               2796036        392.2   ns/op   12 B/op   0 allocs/op
BenchmarkMutexNoSpin/semaphoreMu-10          2544576        474.0   ns/op   55 B/op   1 allocs/op
BenchmarkMutexSpin/stdlib-10                 5276641        232.9   ns/op    0 B/op   0 allocs/op
BenchmarkMutexSpin/fifomu-10                  495802       2431     ns/op    0 B/op   0 allocs/op
BenchmarkMutexSpin/semaphoreMu-10             473323       2580     ns/op  175 B/op   2 allocs/op
```

### Continuous benchmarks

CI also runs the benchmark suite on every push to `master`, and charts the
results over time at
[neilotoole.github.io/fifomu/dev/bench](https://neilotoole.github.io/fifomu/dev/bench/).
The raw history lives on the
[`benchmark-data`](https://github.com/neilotoole/fifomu/tree/benchmark-data)
branch.

- These numbers come from shared GitHub-hosted runners (`ubuntu-latest`),
  so they are noisy and not comparable to the M1 figures above. Read them
  as a trend, not as absolute performance.
- Pull requests are not compared against that history, because runners
  differ too much from run to run. Instead, CI benchmarks the PR's base
  and head commits alternately on one runner and reports the difference
  with [`benchstat`](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat)
  in the workflow run summary. PR results are not added to the history.

## Requirements

- **Go 1.26 or later.** The package is generic-free, but the tests and
  benchmarks use `for b.Loop()` (Go 1.24+) and `sync.WaitGroup.Go`
  (Go 1.25+).
- **Non-stdlib dependency (tests only):** [`golang.org/x/sync`](https://pkg.go.dev/golang.org/x/sync),
  used only by the benchmark baselines — `fifomu.Mutex` itself
  depends only on the standard library (`context`, `sync` and `sync/atomic`).
- **Test-only dependency:** [`go.uber.org/goleak`](https://pkg.go.dev/go.uber.org/goleak).


## Related

- [`streamcache`](https://github.com/neilotoole/streamcache) uses `fifomu` to ensure fairness
  for concurrent readers of a stream.
- [`sq`](https://github.com/neilotoole/sq) uses `fifomu` indirectly via `streamcache`.
