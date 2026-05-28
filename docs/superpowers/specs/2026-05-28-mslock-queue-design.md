# Native FIFO Mutex with Lock-Free Waiter Queue — Design Spec

**Date:** 2026-05-28
**Status:** Draft
**Supersedes:** the tombstoned-tickets-with-listMu design in phase 2 (left on the prototype branch for historical reference).

## Motivation

Phase 2's `listMu`-protected waiter list achieved correctness but introduced a serialization bottleneck under high parallelism (`MutexSlack` ~2.5× slower than current fifomu). The user's strict success criterion is: **the new design must beat current fifomu on every benchmark, including the slack workloads.**

Tombstoned tickets with an inner mutex cannot meet this bar — any contended caller must take `listMu` to enqueue, which serializes them. We need a queue that supports concurrent enqueue without a shared lock.

## Design

### Data structures

```go
type Mutex struct {
    _ noCopy

    // bit 0 = locked, bits 1..31 = enqueued waiter count
    state atomic.Uint32

    // Lock-free FIFO queue using a sentinel-based Michael-Scott design.
    // head always points to a sentinel waiter; head.next is the first
    // real waiter (or nil if empty). tail points to the most recently
    // enqueued waiter (or to the sentinel if empty).
    //
    // Lazy-initialized on first contended call.
    head atomic.Pointer[waiter]
    tail atomic.Pointer[waiter]
}

type waiter struct {
    // parked -> won (by Unlock CAS)
    // parked -> cancelled (by LockContext watcher CAS)
    state atomic.Uint32

    // per-waiter sema for runtime_Semacquire/Semrelease
    sema uint32

    // singly-linked list pointer; atomic for lock-free traversal
    next atomic.Pointer[waiter]
}
```

### Why sentinel-based MS queue

Without a sentinel, the first enqueue must atomically set both `head` and `tail` from nil. Go offers no compound atomic, so this would require either a CAS-on-pair (not available) or a spin loop. With a sentinel, enqueue and dequeue use the same algorithm regardless of queue emptiness, and Unlock's "is the queue empty?" check is a clean `head.Load().next.Load() == nil`.

The sentinel is one waiter struct, lazy-allocated on first slow-path call via CAS-init. The cost is one allocation per Mutex lifetime.

### Pooling policy

- **Lock waiters** (non-cancellable path, `ctx.Done() == nil`): drawn from `sync.Pool`, returned to pool by G after Unlock hands off. No cancellation means no late watcher, so no risk of stale Semrelease into a recycled slot.
- **LockContext waiters**: freshly allocated per call. The cancellation watcher's lifetime is not strictly bounded by the LockContext caller's return (ctx can fire after, late watchers can race). Allocating fresh ties the waiter's sema lifetime to the call's GC reachability, eliminating the stale-Semrelease class of bug.

One alloc per LockContext call is acceptable; profiling can revisit later if a high-cancellation workload demands it.

## Operations

### Lock fast path

```go
func (m *Mutex) Lock() {
    if m.state.CompareAndSwap(0, mutexLocked) {
        return
    }
    _ = m.lockSlow(context.Background())
}
```

Unchanged from phase 1: single CAS, no queue access.

### LockContext fast path

```go
func (m *Mutex) LockContext(ctx context.Context) error {
    if m.state.CompareAndSwap(0, mutexLocked) {
        return nil
    }
    return m.lockSlow(ctx)
}
```

### lockSlow

```
1. Best-effort fast-acquire retry: CAS(state, 0, mutexLocked). If succeeds, return.
2. Ensure sentinel: if head.Load() == nil, CAS-init head and tail to a fresh sentinel waiter.
3. Allocate waiter w:
   - From pool if ctx is non-cancellable (ctx.Done() == nil).
   - Fresh otherwise.
4. w.state = parked; w.next = nil.
5. Bump state's waiter count:
   newState := state.Add(mutexWaiterUnit)
   If newState lacks the locked bit (fast-Unlock raced), undo (state.Add(-mutexWaiterUnit)),
   return w to pool if applicable, retry from step 1.
6. Enqueue via two-step MS push:
   a. oldTail := tail.Swap(w)            // atomic exchange
   b. oldTail.next.Store(w)              // link from predecessor
7. Park and handle result:
   - If non-cancellable: Semacquire(&w.sema); _ = w.state.Load() (race-detector sync edge);
     recycle w to pool; return nil.
   - If cancellable: spawn watcher; Semacquire(&w.sema); close(done);
     if cancelled.Load(): return ctx.Cause (do not touch w; Unlock owns recycling — but since
     LockContext waiters are not pooled, "recycling" means letting GC reclaim).
     Else: recycle (which is a no-op for fresh-allocated LockContext waiters); return nil.
```

### Cancellation watcher (LockContext only)

```go
var cancelled atomic.Bool
done := make(chan struct{})
go func() {
    select {
    case <-ctx.Done():
        if w.state.CompareAndSwap(waiterParked, waiterCancelled) {
            cancelled.Store(true)
            runtime_Semrelease(&w.sema, false, 0)
        }
    case <-done:
    }
}()
```

The single CAS on `w.state` is the protocol's decision point. Either Unlock wins (state → won, Unlock Semreleases) or watcher wins (state → cancelled, watcher Semreleases). The losing side observes the CAS failure and does nothing. No double-claim, no missed-claim.

### Unlock fast path

```go
func (m *Mutex) Unlock() {
    if m.state.CompareAndSwap(mutexLocked, 0) {
        return
    }
    m.unlockSlow()
}
```

### unlockSlow

```
1. Reject double-unlock: if state lacks locked bit, panic("sync: unlock of unlocked mutex").

2. Loop until we wake a waiter or determine empty:
   a. h := head.Load(); n := h.next.Load().
   b. If n == nil:
      - If state.Load() has waiter bits set, an enqueue is mid-flight between
        tail.Swap and oldTail.next.Store. runtime.Gosched(); continue.
      - Else queue is truly empty. state.Store(0); return.
   c. Single-threaded dequeue (only the current holder unlocks):
        head.Store(n)
        state.Add(^uint32(mutexWaiterUnit - 1))    // atomic subtract
   d. CAS(n.state, parked, won):
      - Success: Semrelease(&n.sema, true, 0); return.
      - Failure (cancelled): continue loop. n is dropped (GC reclaims; LockContext
        waiters are not pooled, so no pool-recycle race).
```

Crucially: the dequeue at step c is single-threaded because only the lock holder calls Unlock and only one holder exists at a time. CAS on head is not needed; a plain Store suffices.

The `Gosched` at step b is the only non-strictly-lock-free element. It is bounded: an in-flight enqueue completes in O(1) atomic ops once tail.Swap returns. In practice 0-1 retries are typical.

## Correctness invariants

1. **Sentinel exists and is non-nil** after first slow-path call. Verified by the lazy-init CAS protocol.
2. **state.waiterCount ≥ count of nodes in queue not-yet-dequeued.** The waiter count is incremented before enqueue; decremented during dequeue. The brief window where count > queue length corresponds to "in-flight enqueue between tail.Swap and oldTail.next.Store".
3. **FIFO across Lock and LockContext.** `tail.Swap` provides total order on enqueue. Dequeue follows links from sentinel.next. Both methods enqueue identically, so arrival order is preserved.
4. **At most one of {Unlock, watcher} claims each waiter.** Single CAS on `w.state` makes this atomic.
5. **No missed wakeups.** Unlock's empty check spins while state.waiterCount > 0 and queue appears empty, until either (a) the enqueue completes (queue non-empty), or (b) the enqueue's undo fires (state.waiterCount drops to 0). The state-add-before-enqueue ordering guarantees this.
6. **No stale Semreleases into pool.** LockContext waiters are not pooled, so the watcher's sema is always tied to a unique allocation that's only GC'd once all references (including the watcher's closure) are gone.

## Race-detector synchronization

The linkname'd `runtime_Semacquire`/`Semrelease` carry no `race.Acquire`/`Release` annotations. We patch this by:
- **Won path**: `_ = w.state.Load()` after Semacquire — atomic load on a field the unlocker just CAS'd, establishes HB via sequential consistency of atomics.
- **Cancellation path**: G reads `cancelled.Load()`, which is an atomic the watcher Stored. Same mechanism.
- **w.next**: G never writes w.next after enqueue. Unlock reads w.next during dequeue. The HB chain is via the atomic `tail.Swap` on enqueue and `head.Store` on dequeue, both observable to the race detector.

## Edge cases

### Lazy sentinel init race

Multiple goroutines may concurrently call `lockSlow` on a fresh Mutex (head == nil). All race on the CAS to set head. Exactly one wins. The losers spin briefly waiting for the winner to also set tail (winner sets head then tail; losers see head non-nil but might see tail still nil for a few cycles). Bounded.

### Watcher fires very late

A LockContext call's `ctx` can be cancelled long after the LockContext caller returns. The watcher goroutine may still be alive. Without pool reuse for LockContext waiters, the watcher's CAS attempt against `w.state` is on the original waiter's memory (never reused). CAS either succeeds (if state was somehow still parked, which can't happen because Unlock already claimed it) or fails. Either way, no harm.

In contrast, if we pooled LockContext waiters, a late watcher's CAS could land on a recycled waiter whose state is now `parked` again (set by a new LockContext caller in the new iteration). That would corrupt the new iteration. This is the bug class we encountered in phase 2 Stage A; the fresh-allocation policy avoids it entirely.

### Unlock fast-path race with mid-flight enqueue

```
state = 1 (locked, no waiters)
G:    state.Add(2) → state = 3. About to enqueue.
H:    Unlock fast-CAS(1, 0). FAILS (state = 3). Falls to unlockSlow.
G:    tail.Swap(Gnode), oldTail = sentinel.
G:    sentinel.next.Store(Gnode).
H:    unlockSlow: head.Load() = sentinel. head.next.Load() = Gnode. Dequeue normally.
```

This is the happy path. The state.waiterCount serves as the synchronization signal: as long as it's nonzero, Unlock knows to wait or to find the node.

### Race between fast-Unlock and slow-Lock's state.Add

```
state = 1 (locked, no waiters)
G:    lockSlow: about to state.Add.
H:    Unlock fast-CAS(1, 0). state = 0.
G:    state.Add(2). state = 2. newState lacks locked bit.
G:    undo: state.Add(-2). state = 0. Retry.
G:    On retry, fast-acquire CAS(0, locked) succeeds. G has the lock.
```

This is the same race we handled in phase 2 Stage A. The retry mechanism is preserved.

## Test plan

All phase 2 tests carry over:
- `TestMutex_LockUnlock`
- `TestMutex_ParallelContention`
- `TestMutex_TryLock`
- `TestMutex_UnlockOfUnlockedPanics`
- `TestMutex_FIFO_Smoke`
- `TestLockContext_FastPath`
- `TestLockContext_AlreadyCancelled`
- `TestLockContext_CancelWhileBlocked`
- `TestMutex_MixedFIFO`
- `TestMutex_CancellationPreservesFIFO`
- `TestMutex_CancelUnlockRace` (5000-iteration cancel/unlock stress under -race)

These are the canaries. Any regression on any of them blocks the design.

## Benchmark gates

Beat current fifomu (the phase-0 baseline) on every benchmark. Specifically:
- `MutexUncontended/native` ≤ 0.6× `MutexUncontended/fifomu` (already achieved in phase 1; preserve).
- `MutexSlack/native` ≤ `MutexSlack/fifomu` (177 ns).
- `MutexWorkSlack/native` ≤ `MutexWorkSlack/fifomu` (195 ns).
- `MutexNoSpin/native` ≤ `MutexNoSpin/fifomu` (432 ns; already achieved in phase 1).
- `MutexSpin/native` ≤ `MutexSpin/fifomu` (2417 ns; already achieved in phase 1).
- `LockContext_Cancel/native` ≤ `LockContext_Cancel/fifomu` (85.5 ns).
- `LockContext_Contended/native` ≤ `LockContext_Contended/fifomu` (185 ns).
- `LockContext_FastPath/native` ≤ `LockContext_FastPath/fifomu` (3.5 ns).

If any of these fail by more than 10%, the design is not viable as written. Reassess before implementation continues.

## Known limitations (carried from phase 1)

- **No mutex profile coverage.** `runtime.SetMutexProfileFraction` does not see fifomu contention because `sync.runtime_Semacquire` uses `semaBlockProfile` only. Block profile still works.
- **Goroutine profile wait reason** is `semacquire` rather than `sync.Mutex.Lock`.
- **Linkname dependency** on `sync.runtime_Semacquire`/`Semrelease`. The Go team has stated intent to tighten this surface; phase 2 added a CI check task to recheck after every Go minor-version bump.

## Migration

Replace the Stage A code in `internal/native/mutex.go` with the MS-queue implementation. All existing tests stay. After the bench gate passes, Stage B (migration into `fifomu.go` proper) follows the phase 2 plan.

If the bench gate fails, the prototype branch retains the Stage A code as a record of the negative result.
