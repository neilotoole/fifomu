# MS-Queue FIFO Mutex Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the `listMu`-based Stage A implementation in `internal/native/mutex.go` with a sentinel-based Michael-Scott lock-free FIFO queue. Keep every existing test passing. Beat current fifomu on every benchmark, including slack workloads.

**Architecture:** Two atomic pointers (`head` for sentinel-based traversal; `tail` for atomic append-via-Swap) replace the inner `sync.Mutex` + linked list. Enqueue is `tail.Swap(w); oldTail.next.Store(w)`. Dequeue is single-threaded (only the lock holder runs it) and uses plain `Store` on head. Cancellation flips an atomic state bit; Unlock skips cancelled nodes on dequeue. Lock waiters are pooled; LockContext waiters are allocated fresh per call to eliminate the stale-Semrelease class of bug.

**Tech Stack:** Go 1.23+. `sync/atomic` (Uint32, Pointer). `sync.Pool` for Lock waiters. The existing `runtime_Semacquire`/`Semrelease` linknames in `internal/native/linkname.go` are reused unchanged.

**Branch:** Continue on `worktree-native-mutex-prototype` (the existing prototype branch). Stage A's commits stay in the history as a record of what was tried.

---

## File Structure

Only `internal/native/mutex.go` changes meaningfully:

- **`internal/native/linkname.go`** — unchanged (phase 1 linkname declarations).
- **`internal/native/mutex.go`** — replaced. The new file holds: `Mutex` struct with `state` + `head` + `tail`; `waiter` struct; sentinel lazy-init; `Lock`, `Unlock`, `TryLock`, `LockContext`; `lockSlow`, `unlockSlow`; the cancellation watcher; pool plumbing.
- **`internal/native/mutex_test.go`** — unchanged. All existing tests (11 of them) act as the regression spec.

No other files in the package are modified. No new files are created.

---

### Task 1: Replace the Mutex struct and stub the slow paths

**Files:**
- Modify: `internal/native/mutex.go` (full rewrite of the struct definition + constants + slow-path bodies)

- [ ] **Step 1: Replace the file**

Open `internal/native/mutex.go` and replace its entire contents with:

```go
package native

import (
	"context"
	"sync"
	"sync/atomic"
)

// Mutex is a FIFO mutex backed by a lock-free Michael-Scott waiter
// queue and the runtime's semaphore primitive (linkname'd in
// linkname.go). The uncontended fast path is a single atomic CAS;
// the slow path enqueues without taking any inner lock.
//
// The zero value is an unlocked mutex. Must not be copied after
// first use.
type Mutex struct {
	_ noCopy

	// state: bit 0 = locked, bits 1..31 = enqueued waiter count.
	// state.waiterCount counts waiters that have completed their
	// state.Add bump; it may briefly exceed the queue's visible
	// length during an in-flight enqueue (between tail.Swap and
	// oldTail.next.Store). unlockSlow handles that window via a
	// bounded Gosched-spin.
	state atomic.Uint32

	// head and tail form a sentinel-based lock-free FIFO. head
	// always points to a sentinel waiter; head.next is the next
	// real waiter (or nil if empty). tail points to the most
	// recently enqueued waiter (or to the sentinel if empty).
	//
	// Lazy-initialized on first slow-path call; see ensureSentinel.
	head atomic.Pointer[waiter]
	tail atomic.Pointer[waiter]
}

const (
	mutexLocked     uint32 = 1
	mutexWaiterUnit uint32 = 2 // increment per parked waiter
)

const (
	waiterParked    uint32 = 0
	waiterWon       uint32 = 1
	waiterCancelled uint32 = 2
)

// waiter is one entry in a Mutex's FIFO queue. Lock and LockContext
// callers both go through this queue; the state field lets Unlock
// skip cancelled (tombstoned) entries without breaking FIFO.
type waiter struct {
	// state transitions: parked -> won (by Unlock CAS) or
	// parked -> cancelled (by LockContext watcher CAS). Whichever
	// side CASes first wins the race; the loser does nothing.
	state atomic.Uint32

	// sema is the per-waiter address used for runtime_Semacquire.
	// LockContext waiters are NOT pooled; the sema's lifetime
	// matches the waiter allocation, so late-firing watchers can
	// never land Semreleases on a recycled slot.
	sema uint32

	// next is the singly-linked list pointer. Lock-free reads via
	// atomic.Load; lock-free writes via atomic.Store. Enqueue uses
	// tail.Swap then oldTail.next.Store; dequeue (single-threaded
	// in Unlock) uses head.Store with a plain Store.
	next atomic.Pointer[waiter]
}

// waiterPool recycles Lock-path waiters. LockContext-path waiters
// bypass the pool (see lockSlow).
var waiterPool = sync.Pool{
	New: func() any { return new(waiter) },
}

// resetForPool returns w to a state safe for pool reuse.
func (w *waiter) resetForPool() {
	w.state.Store(waiterParked)
	w.next.Store(nil)
	w.sema = 0
}

// Lock acquires m, blocking until it is available.
func (m *Mutex) Lock() {
	if m.state.CompareAndSwap(0, mutexLocked) {
		return
	}
	_ = m.lockSlow(context.Background())
}

// LockContext acquires m, blocking until available or ctx is done.
//
// On cancellation, returns context.Cause(ctx) and leaves m
// unchanged. If ctx is already cancelled when LockContext is called
// and the lock is available with no queued waiters, LockContext may
// still succeed without blocking.
//
// If the mutex becomes available concurrently with ctx cancellation,
// LockContext may acquire the mutex and return nil even though ctx
// is done. Callers requiring ctx-strict behavior should re-check
// ctx.Err() after acquiring.
func (m *Mutex) LockContext(ctx context.Context) error {
	if m.state.CompareAndSwap(0, mutexLocked) {
		return nil
	}
	return m.lockSlow(ctx)
}

// TryLock attempts to acquire m without blocking. Unlike
// sync.Mutex.TryLock, this reports failure whenever any waiter is
// queued, even if the lock is momentarily unheld — required to
// preserve FIFO ordering.
func (m *Mutex) TryLock() bool {
	return m.state.CompareAndSwap(0, mutexLocked)
}

// Unlock releases m. Panics if m is not locked on entry.
//
// A locked Mutex is not associated with a particular goroutine; one
// goroutine may Lock and another may Unlock.
func (m *Mutex) Unlock() {
	if m.state.CompareAndSwap(mutexLocked, 0) {
		return
	}
	m.unlockSlow()
}

// ensureSentinel lazily initializes m.head and m.tail to a fresh
// sentinel waiter. Idempotent and safe under concurrent calls — the
// first caller CASes head; later callers wait for tail to be set.
func (m *Mutex) ensureSentinel() {
	panic("phase3: ensureSentinel not yet implemented")
}

// lockSlow is the slow path for both Lock (ctx=context.Background())
// and LockContext (ctx with cancellable Done()).
func (m *Mutex) lockSlow(ctx context.Context) error {
	panic("phase3: lockSlow not yet implemented")
}

// unlockSlow walks the lock-free queue, skipping tombstoned
// (cancelled) entries, and hands off to the first live waiter via
// direct runtime_Semrelease.
func (m *Mutex) unlockSlow() {
	panic("phase3: unlockSlow not yet implemented")
}

// noCopy triggers `go vet -copylocks` on accidental copies of Mutex.
type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}
```

- [ ] **Step 2: Verify it builds**

Run: `go build ./internal/native/`
Expected: success (no output).

Run: `go vet ./internal/native/`
Expected: no output.

- [ ] **Step 3: Run the fast-path-only tests to confirm baseline**

Run: `go test ./internal/native/ -run 'TestMutex_LockUnlock|TestMutex_TryLock'`
Expected: both PASS — these tests exercise only the fast path (no slow-path call → no panic stub hit).

The slow-path tests will fail at this point, which is expected.

- [ ] **Step 4: Commit**

```bash
git add internal/native/mutex.go
git commit -m "phase3: replace Mutex struct with lock-free queue layout"
```

---

### Task 2: Implement sentinel lazy-init

**Files:**
- Modify: `internal/native/mutex.go` (replace the `ensureSentinel` panic stub)

- [ ] **Step 1: Replace ensureSentinel**

Find the `ensureSentinel` function (the one with the `phase3:` panic) and replace its body with:

```go
func (m *Mutex) ensureSentinel() {
	if m.head.Load() != nil {
		return
	}
	sentinel := new(waiter)
	if m.head.CompareAndSwap(nil, sentinel) {
		// We won the head CAS; we own setting tail too.
		m.tail.Store(sentinel)
		return
	}
	// Another goroutine won the CAS. They will set tail; spin
	// briefly waiting for them. The window is O(1) atomic ops on
	// the winner's side, so this loop terminates quickly.
	for m.tail.Load() == nil {
		// brief contention; let the scheduler run another goroutine
	}
}
```

- [ ] **Step 2: Verify the build still works**

Run: `go build ./internal/native/`
Expected: success.

(Note: `ensureSentinel` is not called from anywhere yet, so no test exercises it directly. Task 3 wires it in.)

- [ ] **Step 3: Commit**

```bash
git add internal/native/mutex.go
git commit -m "phase3: lazy sentinel initialization"
```

---

### Task 3: Implement lockSlow

**Files:**
- Modify: `internal/native/mutex.go` (replace the `lockSlow` panic stub)

- [ ] **Step 1: Replace lockSlow**

Find the `lockSlow` function and replace its body with the following. This is the full implementation including the cancellation watcher path; Task 4 follows with `unlockSlow`.

```go
func (m *Mutex) lockSlow(ctx context.Context) error {
	// Lazy-init the sentinel before the first enqueue.
	m.ensureSentinel()

	// Retry loop: covers the race where fast-path Unlock clears
	// the locked bit between our state observation and our
	// state.Add. We undo and retry rather than getting stranded as
	// a phantom waiter with no holder.
	for {
		// One more fast-acquire attempt before allocating a waiter.
		if m.state.CompareAndSwap(0, mutexLocked) {
			return nil
		}

		// Allocate waiter: Lock callers use the pool; LockContext
		// callers use fresh allocations (see spec for rationale on
		// the stale-Semrelease class of bug).
		var w *waiter
		fromPool := ctx.Done() == nil
		if fromPool {
			w = waiterPool.Get().(*waiter)
		} else {
			w = new(waiter)
		}
		w.state.Store(waiterParked)
		w.next.Store(nil)

		// Bump waiter count BEFORE enqueueing. If the lock was
		// freed between our last check and this Add, undo and retry.
		newState := m.state.Add(mutexWaiterUnit)
		if newState&mutexLocked == 0 {
			m.state.Add(^uint32(mutexWaiterUnit - 1))
			if fromPool {
				w.resetForPool()
				waiterPool.Put(w)
			}
			continue
		}

		// Enqueue: atomic swap of tail + link from oldTail.
		// Sentinel guarantees oldTail is non-nil after init.
		oldTail := m.tail.Swap(w)
		oldTail.next.Store(w)

		// Park.
		if ctx.Done() == nil {
			runtime_Semacquire(&w.sema)
			// Race-detector synchronization edge with unlocker's
			// CAS on w.state (sema is linkname'd and lacks
			// race.Acquire/Release annotations).
			_ = w.state.Load()
			w.resetForPool()
			waiterPool.Put(w)
			return nil
		}

		// LockContext: watch ctx.Done in parallel with Semacquire.
		var cancelled atomic.Bool
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				if w.state.CompareAndSwap(waiterParked, waiterCancelled) {
					// We tombstoned the entry before Unlock could
					// pick it. Signal G via the heap variable, then
					// wake G's Semacquire.
					cancelled.Store(true)
					runtime_Semrelease(&w.sema, false, 0)
				}
				// CAS failure → Unlock won; do nothing.
			case <-done:
			}
		}()
		runtime_Semacquire(&w.sema)
		close(done)

		if cancelled.Load() {
			// Watcher won the race. The waiter stays in the queue
			// (Unlock will dequeue it and observe the tombstone via
			// its own CAS failure). Do NOT touch w here — its
			// memory is owned by Unlock from now on. Since we
			// allocated fresh (no pool), there's nothing to return.
			return context.Cause(ctx)
		}
		// Unlock won. We exclusively own w; recycle if it's pooled.
		// (For LockContext-fresh waiters, the resetForPool is a
		// no-op but harmless; we don't put fresh waiters back.)
		if fromPool {
			w.resetForPool()
			waiterPool.Put(w)
		}
		return nil
	}
}
```

- [ ] **Step 2: Verify it builds**

Run: `go build ./internal/native/`
Expected: success.

- [ ] **Step 3: Note expected test status**

`TestMutex_ParallelContention` and other slow-path tests will still fail or hang because `unlockSlow` is still stubbed. Do NOT run them yet — they may hang (Semacquire never wakes). Continue to Task 4 first.

- [ ] **Step 4: Commit**

```bash
git add internal/native/mutex.go
git commit -m "phase3: lock-free enqueue in lockSlow"
```

---

### Task 4: Implement unlockSlow

**Files:**
- Modify: `internal/native/mutex.go` (replace the `unlockSlow` panic stub)

- [ ] **Step 1: Replace unlockSlow**

Find the `unlockSlow` function and replace its body with:

```go
func (m *Mutex) unlockSlow() {
	// Reject double-unlock cleanly: if state has no locked bit,
	// fail with the canonical message before walking the queue.
	if m.state.Load()&mutexLocked == 0 {
		panic("sync: unlock of unlocked mutex")
	}

	// Dequeue is single-threaded (only the lock holder unlocks,
	// and only one holder exists at a time). No CAS contention on
	// head; a plain Store after the read suffices.
	for {
		h := m.head.Load()
		// Sentinel has been initialized (lockSlow always
		// ensureSentinel's before adding waiters). If head is nil
		// here, somebody Unlocked without any prior Lock — that's
		// the double-unlock case which the panic above catches,
		// so this should be unreachable. Defensive nil-guard:
		if h == nil {
			panic("native.Mutex: unlockSlow saw nil head; impossible if state has locked bit")
		}
		n := h.next.Load()
		if n == nil {
			// Queue appears empty. Either (a) truly empty (all
			// waiters dequeued, decremented count to zero), or
			// (b) an enqueue is mid-flight between tail.Swap and
			// oldTail.next.Store.
			if m.state.Load()&^mutexLocked != 0 {
				// state.waiterCount > 0; enqueue in flight. Yield
				// and retry. The enqueue completes in O(1) atomic
				// ops once it starts, so this terminates quickly.
				runtimeGosched()
				continue
			}
			// Truly empty. Clear the locked bit.
			m.state.Store(0)
			return
		}

		// Dequeue n by advancing head. Single-threaded so no CAS.
		m.head.Store(n)
		m.state.Add(^uint32(mutexWaiterUnit - 1)) // atomic subtract

		if n.state.CompareAndSwap(waiterParked, waiterWon) {
			// We claimed this waiter. Hand off via direct
			// Semrelease with handoff=true.
			runtime_Semrelease(&n.sema, true, 0)
			return
		}
		// n was cancelled (waiterCancelled). The cancellation
		// watcher already Semreleased it; the LockContext caller
		// returned ctx.Err() without touching n. n is fresh-allocated
		// (LockContext waiters are not pooled), so we drop it on
		// the floor and let GC reclaim. Continue the loop to find
		// the next live waiter.
	}
}
```

- [ ] **Step 2: Add `runtimeGosched` wrapper**

The `runtime.Gosched` function lives in package `runtime`. Add the import. Find the imports at the top of `mutex.go` and replace them:

```go
import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
)
```

Then add a thin wrapper near the top of the file (after the constants and before `Lock`):

```go
// runtimeGosched is a thin wrapper around runtime.Gosched used by
// unlockSlow when a queue read sees an in-flight enqueue. Factored
// out only to make this synchronization point easy to grep for.
func runtimeGosched() {
	runtime.Gosched()
}
```

- [ ] **Step 3: Verify it builds**

Run: `go build ./internal/native/`
Expected: success.

Run: `go vet ./internal/native/`
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add internal/native/mutex.go
git commit -m "phase3: lock-free dequeue with tombstone-skip in unlockSlow"
```

---

### Task 5: Run the Lock-only test suite under -race

This is the first end-to-end correctness gate. The non-cancellable paths (Lock, Unlock, TryLock, double-unlock-panic, FIFO smoke) must all pass.

**Files:** none modified — this task is verification only.

- [ ] **Step 1: Run the Lock-only tests under -race**

Run:

```bash
go test -race -count=5 -timeout=5m ./internal/native/ \
  -run 'TestMutex_LockUnlock|TestMutex_ParallelContention|TestMutex_TryLock|TestMutex_UnlockOfUnlockedPanics|TestMutex_FIFO_Smoke'
```

Expected: all 5 tests PASS across all 5 iterations.

If `TestMutex_ParallelContention` fails with a data race: the in-flight-enqueue Gosched window is not synchronized properly — review unlockSlow's loop.

If `TestMutex_FIFO_Smoke` fails any iteration: arrival-order is broken — review the `tail.Swap` + `oldTail.next.Store` ordering.

If `TestMutex_UnlockOfUnlockedPanics` fails: the panic message at the top of unlockSlow doesn't match — verify the literal string.

If any test hangs: a Semrelease is being missed somewhere. Most likely an enqueue completed but state.waiterCount wasn't properly bumped, or unlockSlow's Gosched-retry exited too early.

- [ ] **Step 2: Run the FIFO smoke 20x**

Run:

```bash
go test -race -count=20 -run TestMutex_FIFO_Smoke ./internal/native/
```

Expected: all 20 PASS. Single failures here are red flags — FIFO violations are subtle and intermittent.

- [ ] **Step 3: Commit only if all green**

If everything passes, there's no code change to commit at this step. Skip to Task 6.

If a test failed, STOP and report. Do not attempt to "fix" by modifying tests — investigate the implementation. Most likely the bug is in unlockSlow's Gosched-retry or lockSlow's state.Add-undo path.

---

### Task 6: Run the LockContext test suite under -race

The cancellation-path correctness gate.

**Files:** none modified.

- [ ] **Step 1: Run the LockContext tests under -race**

Run:

```bash
go test -race -count=5 -timeout=5m ./internal/native/ \
  -run 'TestLockContext|TestMutex_MixedFIFO|TestMutex_CancellationPreservesFIFO'
```

Expected: all relevant tests (TestLockContext_FastPath, TestLockContext_AlreadyCancelled, TestLockContext_CancelWhileBlocked, TestMutex_MixedFIFO, TestMutex_CancellationPreservesFIFO) PASS across all 5 iterations.

If `TestMutex_MixedFIFO` fails: Lock and LockContext are not sharing the same queue correctly. Verify both paths go through `tail.Swap`.

If `TestMutex_CancellationPreservesFIFO` fails: the tombstone-skip in unlockSlow is broken or the watcher's CAS races incorrectly. Trace carefully.

- [ ] **Step 2: Run the cancel/unlock race stress test**

Run:

```bash
go test -race -count=5 -timeout=10m -run TestMutex_CancelUnlockRace ./internal/native/
```

Expected: all 5 iterations PASS. The log line should show both `acquired` and `cancelled` are nonzero (the race is being exercised).

If "lost iterations" appears: a wakeup was missed — the watcher CAS / Unlock CAS protocol has a hole.

If a data race fires: check that the cancellation path doesn't touch `w` after the watcher CAS succeeds (cancelled-path leaves w exclusively to Unlock).

- [ ] **Step 3: Commit only if all green**

No code change to commit. Skip to Task 7.

If failures, STOP and report. Do not modify tests.

---

### Task 7: Run the full benchmark gate

The strict success criterion: beat current fifomu on every benchmark.

**Files:**
- Create: `docs/superpowers/results/2026-05-28-phase3-msqueue-bench.txt`

- [ ] **Step 1: Run the full benchmark suite**

Run:

```bash
go test -run=^$ -bench='BenchmarkMutex$|BenchmarkMutexUncontended$|BenchmarkMutexSlack$|BenchmarkMutexWork$|BenchmarkMutexWorkSlack$|BenchmarkMutexNoSpin$|BenchmarkMutexSpin$|BenchmarkLockContext' \
  -benchmem -count=6 -timeout=20m ./... \
  | tee docs/superpowers/results/2026-05-28-phase3-msqueue-bench.txt
```

Expected: completes in a few minutes. Output includes `native-10` rows for each benchmark.

- [ ] **Step 2: Compare against current fifomu baseline with benchstat**

Run:

```bash
benchstat \
  docs/superpowers/results/2026-05-27-current-impl-baseline-bench.txt \
  docs/superpowers/results/2026-05-28-phase3-msqueue-bench.txt
```

Read each line carefully. Note the `vs base` column.

- [ ] **Step 3: Apply the strict gate**

Per the spec's "Benchmark gates" section, native must NOT regress against fifomu on any of these:

| Benchmark | fifomu baseline (ns) | Allowed max (≤ 1.1× baseline) |
|---|---|---|
| MutexUncontended | 3.5 | 3.85 |
| Mutex | 158 | 174 |
| MutexSlack | 177 | 195 |
| MutexWork | 188 | 207 |
| MutexWorkSlack | 195 | 215 |
| MutexNoSpin | 432 | 475 |
| MutexSpin | 2417 | 2659 |
| LockContext_FastPath | 3.5 | 3.85 |
| LockContext_Contended | 185 | 204 |
| LockContext_Cancel | 85.5 | 94 |

If any `native-*` benchmark exceeds its allowed max: the design has not met the strict bar. STOP and report with the failing benchmark numbers. Do not optimize speculatively without a plan.

If everything is within bounds, proceed.

- [ ] **Step 4: Commit results**

```bash
git add docs/superpowers/results/2026-05-28-phase3-msqueue-bench.txt
git commit -m "phase3: full benchmark results vs current fifomu baseline"
```

---

### Task 8: Write the final results report

**Files:**
- Create: `docs/superpowers/results/2026-05-28-phase3-msqueue.md`

- [ ] **Step 1: Write the report**

Create `docs/superpowers/results/2026-05-28-phase3-msqueue.md` with this template (fill in actual numbers from the benchstat run):

```markdown
# Phase 3 MS-Queue FIFO Mutex — Results

**Date:** 2026-05-28
**Go version:** [output of `go version`]
**CPU:** Apple M1 Max, 10 cores
**Plan:** [`docs/superpowers/plans/2026-05-28-mslock-queue-impl.md`](../plans/2026-05-28-mslock-queue-impl.md)
**Spec:** [`docs/superpowers/specs/2026-05-28-mslock-queue-design.md`](../specs/2026-05-28-mslock-queue-design.md)

## Verdict

[Choose: SHIPS / DOES NOT SHIP, plus one sentence why.]

## Headline numbers vs current fifomu baseline

| Benchmark | fifomu | native (phase 3) | native/fifomu |
|---|---|---|---|
| MutexUncontended | … | … | … |
| Mutex | … | … | … |
| MutexSlack | … | … | … |
| MutexWork | … | … | … |
| MutexWorkSlack | … | … | … |
| MutexNoSpin | … | … | … |
| MutexSpin | … | … | … |
| LockContext_FastPath | … | … | … |
| LockContext_Contended | … | … | … |
| LockContext_Cancel | … | … | … |

## Correctness

All 11 existing tests pass under -race -count=5. The cancel/unlock race stress test passes 5000 iterations × 5 counts. No regressions vs Stage A.

## Known limitations carried from earlier phases

- Mutex profile blindness (`runtime.SetMutexProfileFraction` does not see fifomu contention).
- Goroutine wait reason appears as `semacquire`.
- Linkname dependency on `sync.runtime_Semacquire`/`Semrelease`; CI should re-verify on every Go minor-version bump.
- LockContext callers pay one allocation per call (waiter is not pooled).

## Next step

[If SHIPS: write the Stage B migration plan to migrate this impl into fifomu.Mutex in-place per the user's selection.]
[If DOES NOT SHIP: document the regressing benchmarks and stop. The prototype branch remains as a record.]
```

- [ ] **Step 2: Commit**

```bash
git add docs/superpowers/results/2026-05-28-phase3-msqueue.md
git commit -m "phase3: results report"
```

---

### Task 9: Decision review

This is the gate that determines whether to proceed to Stage B (migration into `fifomu.go`) or to stop.

- [ ] **Step 1: Read the report**

Read `docs/superpowers/results/2026-05-28-phase3-msqueue.md`.

- [ ] **Step 2: Apply the decision**

Proceed to write a Stage B migration plan ONLY if ALL of these hold:

1. All 11 existing tests pass under -race -count=5.
2. The cancel/unlock race stress test passes (no lost iterations).
3. Every native benchmark is within 1.1× of its fifomu baseline (i.e., no regression worse than 10%).
4. At least one benchmark beats fifomu by a meaningful margin (≥ 20%) — otherwise the rewrite isn't justifying its complexity.

If all four hold: write a Stage B plan that migrates the native code into `fifomu.go`, deletes `list.go` and the old channel-waiter pool, and runs the existing fifomu test suite against the new internals.

If any fail: stop. Document the failure in the report. The prototype branch is the record.

- [ ] **Step 3: Commit decision**

```bash
git add docs/superpowers/results/2026-05-28-phase3-msqueue.md
git commit -m "phase3: decision logged"
```

---

## Self-Review

**Spec coverage:**
- "Sentinel-based MS queue" → Task 2.
- "Pooling policy: Lock pooled, LockContext fresh" → Task 3 (fromPool branch).
- "Lock fast path" → Task 1 (Lock function).
- "Lock/LockContext slow path with state.Add + tail.Swap + oldTail.next.Store" → Task 3 (lockSlow).
- "Cancellation watcher (LockContext only)" → Task 3 (watcher goroutine in lockSlow).
- "Unlock slow path with Gosched-spin for in-flight enqueue + tombstone skip" → Task 4 (unlockSlow).
- "Race-detector synchronization" → Task 3 (the `_ = w.state.Load()` in won path).
- "All correctness invariants" → exercised by existing tests in Tasks 5 and 6.
- "Bench gate" → Task 7.
- "Decision criteria" → Task 9.

**Placeholder scan:** No "TBD" / "implement later" / "add error handling" phrases. Every step has the exact code or command. Task 8's report template has `…` cells the executor fills with actual numbers (this is intentional — the numbers cannot be predicted).

**Type consistency:**
- `Mutex` fields (`state`, `head`, `tail`) used consistently across all tasks.
- `waiter` fields (`state`, `sema`, `next`) used consistently.
- Constants (`mutexLocked`, `mutexWaiterUnit`, `waiterParked`, `waiterWon`, `waiterCancelled`) used as defined.
- Function names (`ensureSentinel`, `lockSlow`, `unlockSlow`, `resetForPool`, `runtimeGosched`) used consistently.

---

## Notes for the implementer

- **Trust the existing tests.** This is a refactor — the 11 tests in `mutex_test.go` define the spec. Do NOT modify any test. If a test fails, the implementation is wrong.
- **The Gosched in unlockSlow is the only non-strictly-lock-free element.** It is bounded and correct. Do not try to remove it without a clear alternative — phase 2's listMu was the alternative and it was slower.
- **The retry loop in lockSlow handles the fast-Unlock-vs-state.Add race.** This was discovered the hard way in phase 2 Stage A. Don't remove it.
- **LockContext waiters must NOT be pooled.** This was also discovered the hard way (stale-Semrelease bug). The fresh-alloc cost is one heap allocation per `LockContext` call; that's the price of correctness here.
- **Run `-race` always.** Atomic ordering bugs in this kind of code rarely reproduce without the race detector.
