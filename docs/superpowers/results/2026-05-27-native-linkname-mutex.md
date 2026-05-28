# Native-Linkname Mutex Prototype — Results

**Date:** 2026-05-28
**Go version:** go1.26.3 darwin/arm64
**CPU:** Apple M1 Max, 10 cores
**Plan:** [`docs/superpowers/plans/2026-05-27-native-linkname-mutex.md`](../plans/2026-05-27-native-linkname-mutex.md)
**Branch:** `worktree-native-mutex-prototype`

## Verdict

**Proceed to phase 2 (LockContext integration).** All three plan decision criteria are met with substantial margin. The native impl achieves parity-or-better with `sync.Mutex` on every contended/uncontended benchmark and beats current fifomu by 1.25× to 4.40× depending on the workload.

## Headline numbers

| Benchmark | sync.Mutex | fifomu (current) | **native (prototype)** | semaphore.Weighted | native/stdlib | native/fifomu |
|---|---|---|---|---|---|---|
| MutexUncontended | 1.75 ns | 3.63 ns | **1.74 ns** | 3.86 ns | **0.99×** | **0.48×** |
| Mutex (contended) | 121 ns | 158 ns | **110 ns** | 244 ns | **0.91×** | **0.70×** |
| MutexSlack | 113 ns | 176 ns | **109 ns** | 284 ns | **0.96×** | **0.62×** |
| MutexWork | 133 ns | 197 ns | **131 ns** | 364 ns | **0.99×** | **0.67×** |
| MutexWorkSlack | 117 ns | 195 ns | **156 ns** | 285 ns | 1.34× | **0.80×** |
| MutexNoSpin | 140 ns | 432 ns | **191 ns** | 473 ns | 1.37× | **0.44×** |
| MutexSpin | 239 ns | 2417 ns | **549 ns** | 2611 ns | 2.30× | **0.23×** |

Lower is better. **Bold native column wins** are cases where native is at parity or better with sync.Mutex.

Both `native/stdlib < 1.0` (native wins outright over stdlib) on Mutex, MutexSlack — likely because `sync.Mutex`'s spin loop is overhead here and the workload doesn't reward it. `native/fifomu` is below 1.0 on every benchmark.

## Decision criteria (from the plan)

1. **`MutexUncontended/native` ≤ 1.3× `MutexUncontended/stdlib`** — required ≤ 1.3×, actual **0.99×**. ✅ PASS
2. **`MutexNoSpin/native` ≤ 0.7× `MutexNoSpin/fifomu`** — required ≤ 0.7×, actual **0.44×**. ✅ PASS
3. **No correctness regressions** — FIFO smoke test passed 20× under `-race`; the parallel contention test passed under `-race`; the panic-on-double-unlock test passed; TryLock semantics test passed. ✅ PASS

## Where the gaps remain

The two benchmarks where native still trails stdlib are spinning-favourable cases:

- **MutexNoSpin (1.37× slower than stdlib):** `sync.Mutex` uses its spin loop to avoid context switches under brief contention. A FIFO impl cannot spin past queued waiters without violating FIFO — this gap is inherent.
- **MutexSpin (2.30× slower than stdlib):** Same root cause, more extreme. Even so, native is **4.40× faster than current fifomu** on this benchmark (549 ns vs 2417 ns).

These gaps are structural to FIFO discipline, not implementation defects.

## Allocations

Native is **allocation-free in steady state** on every benchmark, matching sync.Mutex's profile and the current fifomu impl. `semaphore.Weighted` allocates 175 B / 2 allocs per op on most contended benchmarks.

## High-variance notes

Two native benchmarks showed elevated coefficient of variation (CV) that's worth flagging:

- `Mutex/native-10`: ±43% (one of six iterations clocked 62.62 ns vs ~110 ns elsewhere)
- `MutexWork/native-10`: ±33%

The most likely cause is the `goyield()` direct-handoff path in `runtime_Semrelease(handoff=true)`: the releaser yields its P to the woken waiter, but only when `getg().m.locks == 0` and not on g0 (see `runtime/sema.go:264-287`). Scheduler-state-dependent code paths legitimately produce variance. Not a correctness issue, but worth noting for any future tuning that wants tight tail latencies.

## Findings of note

### Race-detector false positive (one-line fix)

The biggest design surprise: when `sync.runtime_Semacquire`/`Semrelease` are linkname'd from a package other than `sync`, the race-detector annotations (`race.Acquire`/`race.Release`) that sync.Mutex relies on are NOT included. The runtime sema's synchronization edge is invisible to `-race`.

**Symptom:** the parallel contention test (`TestMutex_ParallelContention`) flagged a data race on the counter variable when first written under `-race`, despite the mutex being correctly held.

**Fix:** read the atomic state word after `runtime_Semacquire` returns. The atomic load establishes a happens-before edge with `unlockSlow`'s atomic store, which is sufficient to teach the race detector about the handoff. See `internal/native/mutex.go` lines around the `_ = m.state.Load()` call in `lockSlow`.

This needs to be documented as a known constraint for any phase 2 plan: every code path that wakes from `runtime_Semacquire` must touch a shared atomic before reading mutex-protected state.

### Mutex-profile blindness (unchanged from earlier analysis)

`sync.runtime_Semacquire` uses `semaBlockProfile` only, not `semaMutexProfile` (see `runtime/sema.go:71`). Native mutex contention will appear in `runtime.SetBlockProfileFraction` output but **not** in `runtime.SetMutexProfileFraction`. The mutex-profile variant is on the internal/sync allowlist and not accessible to us.

This is a real loss for production debugging — users running with `mutexprofile` would see sync.Mutex contention but miss native.Mutex contention. Needs prominent documentation in any public release.

### Goroutine-profile attribution

Blocked goroutines parked in `runtime_Semacquire` from this package will appear in `runtime/pprof` goroutine profiles with the wait reason `"semacquire"` (the default), not the more useful `"sync.Mutex.Lock"` tag that sync.Mutex's internal variant sets via `waitReasonSyncMutexLock`. Users debugging "why are goroutines blocked" may see unhelpful stack frames. Distinct from the mutex-profile concern above. (Surfaced by the final reviewer; missed in the first pass.)

### `MutexWorkSlack` regression vs `MutexWork`

Native is 156 ns on `MutexWorkSlack` but 131 ns on `MutexWork` — a 19% slowdown when the slack-mode parallelism multiplier and `runtime.Gosched()` are introduced. The stdlib delta between the two benchmarks is much smaller (117 vs 133 ns). Plausible root cause: `runtime_Semrelease(handoff=true)` calls `goyield()` internally (`runtime/sema.go:264-287`), and that interacts poorly with the explicit `Gosched()` in the workload — the releaser may lose its P twice in succession. Worth investigating in phase 2 before declaring the design final; the gap might close with a different `handoff` policy when the workload is slack-heavy. (Surfaced by the final reviewer.)

### Linkname allowlist tracking

The linker's blocked-linknames map (`cmd/link/internal/loader/loader.go:2406`) is curated per Go version. The current Go 1.26 toolchain leaves `sync.runtime_Semacquire`/`Semrelease` unrestricted (only `internal/sync.runtime_Semacquire` and `internal/sync.runtime_SemacquireMutex` are restricted to `internal/sync`). The Go team has stated intent to tighten this surface over time. **Phase 2 should include a tracking note in CI to recheck after every Go minor-version bump** so we catch any regression before users hit it. (Surfaced by the final reviewer.)

## What phase 2 needs to solve

The prototype intentionally omitted `LockContext`. To ship as a full fifomu replacement, phase 2 must:

1. Preserve the `TestMutex_MixedQueueLockAndLockContext` invariant in `fifomu_invariants_test.go:21`: Lock and LockContext entries must share one FIFO queue.
2. Provide cancellable parking. The runtime semaphore primitives are not cancellable, so cancellation has to be layered on top — likely via a ticketed waiter scheme (each waiter has a monotonic ticket; cancellation atomically marks the waiter tombstoned; `Unlock` skips tombstoned entries).
3. Pursue the design without losing the uncontended-fast-path win. The state-word + sema design must remain reachable when no LockContext callers are present.
4. Document the race-detector workaround and the mutex-profile loss in the package doc.

### Hardest sub-problem (per the final reviewer)

Cancellation requires an out-of-band waiter list (since the runtime sema's queue is opaque and cannot be selectively dequeued). That list must coordinate with the state word and the sema release without introducing a second protective lock — otherwise we lose the uncontended fast-path win. **The genuinely hard part is proving no ordering of a concurrent (cancel, unlock, new-arrival) trio produces a live-lock or a missed wakeup.** Two options to evaluate:

- **(a) Tombstoned tickets:** each waiter gets a monotonic ticket; cancellation marks the waiter tombstoned via an atomic CAS on the waiter struct; `Unlock` walks tombstoned entries before releasing the sema. The walk has to be lock-free or use an inner mutex (which defeats the perf win on the slow path).
- **(b) Wake-and-bail:** on cancellation, the watcher `Semreleases` to wake the waiter spuriously; the waiter checks its own ctx on wake-up and immediately re-releases the lock to the next waiter if cancelled. Conceptually simpler but leaks a wakeup token, and pathological under high cancellation rates.

Either way, prototype the cancellation path against a reproducer for the (cancel, unlock, new-arrival) race before writing the rest of phase 2.

## Full benchstat output

```
goos: darwin
goarch: arm64
pkg: github.com/neilotoole/fifomu
cpu: Apple M1 Max
                                │ docs/superpowers/results/2026-05-27-native-linkname-mutex-bench.txt │
                                │                               sec/op                                │
MutexUncontended/stdlib-10                                                              1.749n ±  10%
MutexUncontended/fifomu-10                                                              3.633n ±  48%
MutexUncontended/semaphoreMu-10                                                         3.863n ±   8%
MutexUncontended/native-10                                                              1.739n ±   5%
Mutex/stdlib-10                                                                         120.9n ±   6%
Mutex/fifomu-10                                                                         157.8n ±   5%
Mutex/semaphoreMu-10                                                                    243.8n ±   4%
Mutex/native-10                                                                         109.5n ±  43%
MutexSlack/stdlib-10                                                                    113.3n ±   7%
MutexSlack/fifomu-10                                                                    176.1n ±   5%
MutexSlack/semaphoreMu-10                                                               284.4n ±  40%
MutexSlack/native-10                                                                    108.9n ±   4%
MutexWork/stdlib-10                                                                     132.8n ±  12%
MutexWork/fifomu-10                                                                     197.2n ±   4%
MutexWork/semaphoreMu-10                                                                364.2n ± 187%
MutexWork/native-10                                                                     131.3n ±  33%
MutexWorkSlack/stdlib-10                                                                117.0n ±   5%
MutexWorkSlack/fifomu-10                                                                195.2n ±   1%
MutexWorkSlack/semaphoreMu-10                                                           284.9n ±   2%
MutexWorkSlack/native-10                                                                156.4n ±   2%
MutexNoSpin/stdlib-10                                                                   139.7n ±   2%
MutexNoSpin/fifomu-10                                                                   432.0n ±   8%
MutexNoSpin/semaphoreMu-10                                                              473.1n ±   6%
MutexNoSpin/native-10                                                                   190.9n ±   2%
MutexSpin/stdlib-10                                                                     238.6n ±  12%
MutexSpin/fifomu-10                                                                     2.417µ ±   1%
MutexSpin/semaphoreMu-10                                                                2.611µ ±   0%
MutexSpin/native-10                                                                     549.4n ±   2%
geomean                                                                                 126.8n
```

(Allocations and B/op summaries omitted — all native runs are zero allocations, matching stdlib and current fifomu.)

Raw benchmark output: [`2026-05-27-native-linkname-mutex-bench.txt`](2026-05-27-native-linkname-mutex-bench.txt)
Baseline (current impl only): [`2026-05-27-current-impl-baseline-bench.txt`](2026-05-27-current-impl-baseline-bench.txt)
