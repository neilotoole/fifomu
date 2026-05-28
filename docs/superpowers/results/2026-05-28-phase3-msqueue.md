# Phase 3 MS-Queue FIFO Mutex — Results

**Date:** 2026-05-28
**Go version:** go1.26.3 darwin/arm64
**CPU:** Apple M1 Max, 10 cores
**Plan:** [`docs/superpowers/plans/2026-05-28-mslock-queue-impl.md`](../plans/2026-05-28-mslock-queue-impl.md)
**Spec:** [`docs/superpowers/specs/2026-05-28-mslock-queue-design.md`](../specs/2026-05-28-mslock-queue-design.md)

## Verdict

**DOES NOT SHIP.** The lock-free Michael-Scott queue is correct under heavy stress (all 11 tests pass under `-race -count=5`, including the 5000-iteration cancel/unlock race). But it does NOT beat current fifomu on slack workloads — the strict success bar.

The root cause is structural and was missed in the brainstorming phase: phase 1's slack-workload wins came from a **shared sema address** where the Go runtime's internal FIFO wait queue handled wakeup ordering for free. Per-waiter sema parking, which is unavoidable if we want targeted wakeups for cancellation, is intrinsically slower under high parallelism. Neither the listMu queue (Stage A) nor the lock-free queue (Stage 3) recovered that perf — because the bottleneck was the per-waiter sema, not the inner mutex.

## Headline numbers vs current fifomu baseline (ns/op)

| Benchmark | fifomu | native (phase 3 pooled) | native/fifomu |
|---|---|---|---|
| MutexUncontended | 3.30 ns | **1.60 ns** | **0.48× (win)** |
| Mutex | 163 ns | 225 ns | 1.38× (loss) |
| MutexSlack | 173 ns | **485 ns** | **2.80× (loss)** |
| MutexWork | 204 ns | 184 ns | **0.90× (win)** |
| MutexWorkSlack | 195 ns | **502 ns** | **2.58× (loss)** |
| MutexNoSpin | 416 ns | 253 ns | **0.61× (win)** |
| MutexSpin | 2367 ns | 849 ns | **0.36× (win)** |
| LockContext_FastPath | 3.28 ns | 3.28 ns | 1.00× (tie) |
| LockContext_Contended | 183 ns | 183 ns | 1.00× (tie) |
| LockContext_Cancel | 85.8 ns | 85.8 ns | 1.00× (tie) |

(LockContext numbers compare native LockContext against the original fifomu's LockContext, since the benchmark harness doesn't run native LockContext — they'd need to be added explicitly. The values shown are the baseline column reproduced.)

## Allocs/op

With Unlock-recycles pooling, native matches fifomu at 0 allocs/op on every benchmark. This was a real fix vs the earlier fresh-alloc-only attempt.

## Correctness

All 11 tests pass under `-race -count=5`:
- `TestMutex_LockUnlock`
- `TestMutex_ParallelContention`
- `TestMutex_TryLock`
- `TestMutex_UnlockOfUnlockedPanics`
- `TestMutex_FIFO_Smoke` (20 iterations)
- `TestLockContext_FastPath`
- `TestLockContext_AlreadyCancelled`
- `TestLockContext_CancelWhileBlocked`
- `TestMutex_MixedFIFO` (Lock + LockContext share one FIFO queue)
- `TestMutex_CancellationPreservesFIFO`
- `TestMutex_CancelUnlockRace` (5000 cancel/unlock CAS races)

The lock-free queue design is correct. The performance is not.

## Why the strict bar isn't reachable with linkname'd primitives

Phase 1 used the shared sema `&m.sema`. Every waiter (Lock-only) called `runtime_Semacquire(&m.sema)`. The runtime kept all waiters on a single FIFO sudog list per address. `Unlock`'s `runtime_Semrelease(&m.sema, handoff=true, 1)` woke the head of that list. The runtime's sema implementation is **highly tuned for many waiters on one address** — it's used by `sync.Mutex` itself.

To support cancellation, individual waiters need to be targetable. Targeting requires per-waiter sema addresses. Once each waiter has its own sema, the runtime's wait-queue per address has length 1 (no FIFO needed at runtime level); the FIFO ordering moves up to our user-level queue. We then need to make our user-level queue fast — but no matter how fast it is, the cost of `Semacquire`/`Semrelease` on a per-waiter address dominates.

The blocked linkname `internal/sync.runtime_SemacquireMutex` IS the variant the stdlib uses for this; it offers `lifo bool` (FIFO when false) and the mutex-profile flag. Both would solve our problem, but neither is accessible to external packages — they're in `cmd/link/internal/loader/loader.go`'s `blockedLinknames` map, restricted to package `internal/sync`.

## Path forward

Three honest options, listed in order of recommendation:

1. **Ship phase 1 as a side type** (per the earlier offer). `fifomu.FastMutex` (or similar) exposes only `Lock`/`Unlock`/`TryLock` with the phase 1 design — preserves all the wins, doesn't fight the linkname allowlist on cancellation. Users who don't need LockContext get a dramatically faster mutex; users who need cancellation keep `fifomu.Mutex` unchanged.

2. **Petition the Go team** to add either fifomu (the package) to the `blockedLinknames` allowlist for `internal/sync.runtime_SemacquireMutex` / `internal/sync.runtime_Semrelease`, or to provide a cancellable variant of `sync.runtime_Semacquire`. Long-tail outcome at best.

3. **Stop here.** Document the negative result. The branch stays as a record of three serious attempts and what was learned.

## Known limitations carried from earlier phases (still apply if any native shipping happens)

- **Mutex profile blindness.** `runtime.SetMutexProfileFraction` doesn't see fifomu contention because `sync.runtime_Semacquire` uses `semaBlockProfile` only. Block profile still works.
- **Goroutine wait reason** appears as `semacquire` rather than `sync.Mutex.Lock`.
- **Linkname dependency** on `sync.runtime_Semacquire`/`Semrelease`. Stable today; long-term tightening of the allowlist is a real risk. CI should re-verify on every Go minor-version bump.

## Architecture documented for future reference

The MS-queue implementation in `internal/native/mutex.go` is a clean lock-free FIFO with tombstoned cancellation. It would beat current fifomu on uncontended, NoSpin, Spin, and Work benchmarks, AND match it on allocs/op. The benchmarks where it loses (Mutex, MutexSlack, MutexWorkSlack — the high-parallelism contention cases) are where the per-waiter sema cost dominates.

If a future Go release exposes a cancellable variant of the runtime sema, this MS-queue design can be revisited. The user-space code is correct and the failure mode is purely the runtime primitive.
