# Native Mutex Phase 2: LockContext Integration & In-Place Replacement

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `LockContext` to the native-linkname Mutex, then replace `fifomu.Mutex`'s internals in-place with the native design. The existing public API (`Lock`, `Unlock`, `TryLock`, `LockContext`) stays unchanged. All existing tests must pass, including `TestMutex_MixedQueueLockAndLockContext` which requires Lock and LockContext entries to share one FIFO queue.

**Architecture:**
- The state word from phase 1 stays (`bit 0 = locked, bits 1..31 = waiter count`). Uncontended fast paths are still one CAS each.
- A **shared waiter list** is added, protected by an inner `sync.Mutex` (only touched on the slow path). Both Lock and LockContext callers go through this list — preserving the mixed-queue FIFO invariant.
- Each waiter is a struct with `(atomic.Uint32 state, uint32 sema, *waiter next)`. The `state` field encodes `parked / won / cancelled`. Cancellation atomically marks the entry; `Unlock` skips cancelled entries while walking the list (the **tombstoned-ticket** scheme).
- LockContext spawns a watcher goroutine that flips the waiter's state to `cancelled` on `ctx.Done()` and Semreleases the per-waiter sema. The waiter wakes, checks its state, and either accepts the lock or returns the ctx error.

**Tech Stack:** Go 1.23+. `sync.runtime_Semacquire` / `sync.runtime_Semrelease` via `go:linkname` (already wired up in phase 1). `sync/atomic`. `sync.Mutex` for the inner waiter-list lock (only touched on the slow path; uncontended fast path stays untouched).

**Branch:** Continue on `worktree-native-mutex-prototype` (the prototype's branch). The phase 1 PR is open in draft mode at https://github.com/neilotoole/fifomu/pull/6 — phase 2 commits append to that PR. Mark the PR ready for review only after all phase 2 tasks complete.

**Out of scope:**
- Public API changes — none. `LockContext` already exists in `fifomu`; we're swapping the implementation, not the interface.
- Mutex profiling integration. Documented as a known limitation; not solvable without runtime allowlist changes.
- Spin loop. FIFO cannot use sync.Mutex's spin without violating arrival order.

## File Structure

The final state replaces `fifomu.Mutex` internals with the native design. The plan does this in **two stages**:

- **Stage A (Tasks 1-9):** Extend `internal/native.Mutex` with LockContext, the shared waiter list, and tombstone cancellation. The prototype's tests are extended; the wider fifomu package is untouched.
- **Stage B (Tasks 10-13):** Replace `fifomu.Mutex` internals with the `internal/native` code. Delete `list.go` and the channel-waiter pool. Delete `internal/native` (its content is now in the fifomu package proper).

Files involved:

- `internal/native/mutex.go` — extended with `LockContext`, waiter list, per-waiter waiter struct. (modify in Stage A)
- `internal/native/mutex_test.go` — extended with LockContext tests, cancellation tests, mixed-queue tests, FIFO-under-cancellation tests. (modify in Stage A)
- `fifomu.go` — replaced by the native impl. (modify in Stage B)
- `list.go` — deleted. (Stage B)
- `internal/native/` — entire directory deleted after Stage B migration. (Stage B)
- `export_test.go`, `internal_test.go` — may need adjustment if they reference deleted internals.
- `README.md` — known-limitations section updated (mutex profile, goroutine wait reason, race-detector caveat).
- `docs/superpowers/results/2026-05-28-phase2-bench.txt` + `…-phase2.md` — final benchmark + report.

## Critical correctness invariants

These must hold throughout Stage A. Violating any is a bug.

1. **State/list consistency:** when `listMu` is held, `state.waiterCount == len(waiter list excluding cancelled tombstones)` if and only if no `lockSlow` call is mid-way through CAS'ing state. We achieve this by always doing state update AND list update under `listMu`.
2. **FIFO across Lock and LockContext:** both go through the same `lockSlow` path; both append to the same list tail.
3. **No missed wakeups:** every `Unlock` path either successfully hands off to a non-cancelled waiter via `Semrelease(handoff=true)`, OR confirms (under `listMu`) that no live waiter exists and clears the locked bit.
4. **No lost lock ownership:** when `Unlock` decrements the waiter count, the locked bit stays set. The woken waiter never CASes to acquire — it inherits the bit.
5. **Race-detector synchronization:** every wake path includes an atomic load on a state word touched by the corresponding release.

---

## Stage A: Extend native.Mutex with LockContext

### Task 1: Define the waiter struct and pool

**Files:** Modify `internal/native/mutex.go`.

- [ ] **Step 1: Add the waiter type and pool above the existing Mutex declaration**

Add these declarations to `internal/native/mutex.go`, immediately before the `Mutex` struct definition:

```go
import "sync"
```

(merge into the existing single import block; `mutex.go` currently has `import "sync/atomic"`. After merging it should be:)

```go
import (
	"sync"
	"sync/atomic"
)
```

Then add:

```go
// waiterState values, stored in waiter.state.
const (
	waiterParked    uint32 = 0
	waiterWon       uint32 = 1
	waiterCancelled uint32 = 2
)

// waiter is one entry in a Mutex's FIFO queue. Lock and LockContext
// callers both go through this queue; the state field lets Unlock
// skip cancelled entries (the tombstone) without breaking FIFO.
type waiter struct {
	// state transitions: parked -> won (by Unlock's CAS) or
	// parked -> cancelled (by LockContext's watcher CAS).
	// Whichever side CASes first wins the race.
	state atomic.Uint32

	// sema is the per-waiter address used for runtime_Semacquire.
	// Per-waiter (not shared) so that cancellation can wake
	// exactly this goroutine via runtime_Semrelease(&w.sema, ...).
	sema uint32

	// next is the singly-linked list pointer; protected by Mutex.listMu.
	next *waiter
}

// waiterPool recycles waiters to keep the slow path allocation-free
// in steady state. Every code path that returns a waiter to the pool
// MUST first reset its fields — see the resetForPool method.
var waiterPool = sync.Pool{
	New: func() any { return new(waiter) },
}

// resetForPool returns w to a state safe for the pool: state=parked,
// next=nil, sema=0. Called immediately before waiterPool.Put.
func (w *waiter) resetForPool() {
	w.state.Store(waiterParked)
	w.next = nil
	w.sema = 0
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/native/`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add internal/native/mutex.go
git commit -m "phase2: add waiter struct and pool for shared FIFO queue"
```

---

### Task 2: Add the waiter list to Mutex (no behavior change yet)

**Files:** Modify `internal/native/mutex.go`.

- [ ] **Step 1: Extend the Mutex struct**

Replace the existing `Mutex` definition:

```go
type Mutex struct {
	_ noCopy

	// state: bit 0 = locked, bits 1..31 = waiter count.
	state atomic.Uint32

	// listMu protects head and tail. Touched only on the slow
	// path; uncontended fast paths bypass it entirely.
	listMu sync.Mutex
	head   *waiter
	tail   *waiter
}
```

Note: the old `sema uint32` field is **removed**. Phase 1 parked all waiters on this single address; in phase 2, each waiter has its own sema (for per-waiter cancellation). Lock methods stop calling `runtime_Semacquire(&m.sema)` — we'll re-route them in Task 3.

- [ ] **Step 2: Temporarily stub out `lockSlow` and `unlockSlow` so the package compiles**

Replace the bodies of `lockSlow` and `unlockSlow` with `panic("phase2: not yet implemented")` so the file builds while we develop. The Lock/Unlock fast paths still work because they don't call these.

```go
func (m *Mutex) lockSlow() {
	panic("phase2: lockSlow not yet implemented")
}

func (m *Mutex) unlockSlow() {
	panic("phase2: unlockSlow not yet implemented")
}
```

- [ ] **Step 3: Verify it builds**

Run: `go build ./internal/native/`
Expected: success.

Also run: `go test ./internal/native/ -run TestMutex_LockUnlock` (which only exercises the fast path).
Expected: PASS — the basic lock/unlock test still works.

- [ ] **Step 4: Commit**

```bash
git add internal/native/mutex.go
git commit -m "phase2: extend Mutex with waiter list (slow path stubbed)"
```

---

### Task 3: Implement shared lockSlow (Lock path)

**Files:** Modify `internal/native/mutex.go`.

- [ ] **Step 1: Implement lockSlow as a context-free slow path**

Replace the `lockSlow` stub with this implementation:

```go
// lockSlow is the Lock slow path. The non-cancellable variant calls
// it directly with ctx=nil; LockContext calls it via a wrapper.
// Returns ctx.Err()-style error only when ctx is non-nil and cancelled.
func (m *Mutex) lockSlow(ctx context.Context) error {
	// Acquire listMu first, then re-attempt the fast acquire.
	// This serializes the "claim a free lock" race with Unlock's
	// list-walking, so we never enqueue when the lock is actually
	// free with no live waiters.
	m.listMu.Lock()
	if m.state.CompareAndSwap(0, mutexLocked) {
		m.listMu.Unlock()
		return nil
	}

	// Enqueue: bump waiter count and append to tail.
	w := waiterPool.Get().(*waiter)
	w.state.Store(waiterParked)
	m.state.Add(mutexWaiterUnit)
	if m.tail == nil {
		m.head = w
	} else {
		m.tail.next = w
	}
	m.tail = w
	m.listMu.Unlock()

	if ctx == nil {
		// Non-cancellable: just park.
		runtime_Semacquire(&w.sema)
		// Race-detector synchronization edge.
		_ = w.state.Load()
		w.resetForPool()
		waiterPool.Put(w)
		return nil
	}

	// LockContext: watch for cancellation while parked.
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			if w.state.CompareAndSwap(waiterParked, waiterCancelled) {
				// We tombstoned the entry before Unlock could
				// pick it. Wake the waiter so it exits Semacquire
				// and returns the ctx error.
				runtime_Semrelease(&w.sema, false, 0)
			}
			// If the CAS failed, Unlock already CAS'd to won and
			// is about to Semrelease us — no action needed here;
			// we just let the watcher exit.
		case <-done:
		}
	}()
	runtime_Semacquire(&w.sema)
	close(done)
	// Race-detector synchronization edge.
	state := w.state.Load()
	w.resetForPool()
	waiterPool.Put(w)
	if state == waiterCancelled {
		return context.Cause(ctx)
	}
	return nil
}
```

- [ ] **Step 2: Add the context import**

The file now needs `"context"` in its imports. Final import block should be:

```go
import (
	"context"
	"sync"
	"sync/atomic"
)
```

- [ ] **Step 3: Update Lock to use the new signature**

Change the existing `Lock` method to call `lockSlow(nil)`:

```go
// Lock acquires m, blocking until it is available.
func (m *Mutex) Lock() {
	// Fast path: unlocked, no waiters.
	if m.state.CompareAndSwap(0, mutexLocked) {
		return
	}
	m.lockSlow(nil) //nolint:errcheck // nil ctx cannot error
}
```

- [ ] **Step 4: Verify Lock still works**

Run: `go test -race -count=3 ./internal/native/ -run 'TestMutex_LockUnlock|TestMutex_TryLock|TestMutex_UnlockOfUnlockedPanics'`
Expected: PASS. (ParallelContention and FIFO_Smoke will fail because Unlock is still stubbed — that's expected; we fix it next task.)

- [ ] **Step 5: Commit**

```bash
git add internal/native/mutex.go
git commit -m "phase2: implement shared lockSlow with optional ctx"
```

---

### Task 4: Implement unlockSlow with tombstone-skipping

**Files:** Modify `internal/native/mutex.go`.

- [ ] **Step 1: Replace the unlockSlow stub**

```go
// unlockSlow walks the waiter list, skipping tombstoned (cancelled)
// entries, and hands off to the first live waiter via direct
// runtime_Semrelease. If the list is empty (all entries were
// cancelled, or none enqueued yet), it clears the locked bit.
//
// Must be called only when the fast-path CAS(mutexLocked, 0) failed,
// i.e. when state has a nonzero waiter count.
func (m *Mutex) unlockSlow() {
	m.listMu.Lock()
	for {
		w := m.head
		if w == nil {
			// List is empty. Under listMu, this means
			// state.waiterCount must also be 0 (the invariant
			// holds because every list mutation is paired with
			// a state update under this same lock). So state is
			// exactly mutexLocked. Release it.
			//
			// Use Store(0) rather than CAS because (a) we hold
			// listMu so no other slow path is running, and
			// (b) any concurrent fast-path Lock CAS(0,1)
			// requires state to already be 0 — which it isn't
			// until after this Store.
			if m.state.Load() != mutexLocked {
				m.listMu.Unlock()
				panic("native.Mutex: state/list inconsistency in unlockSlow (empty list, nonzero waiter count)")
			}
			m.state.Store(0)
			m.listMu.Unlock()
			return
		}

		// Pop head before deciding what to do with it. Either we
		// hand it the lock, or we discard it as cancelled — either
		// way it's leaving the list.
		m.head = w.next
		if m.head == nil {
			m.tail = nil
		}
		m.state.Add(^uint32(mutexWaiterUnit - 1)) // subtract mutexWaiterUnit

		if w.state.CompareAndSwap(waiterParked, waiterWon) {
			// We claimed this waiter. Release listMu BEFORE
			// Semrelease so the wake-up doesn't contend on it.
			m.listMu.Unlock()
			runtime_Semrelease(&w.sema, true, 0)
			return
		}
		// w was cancelled (state == waiterCancelled). The
		// cancellation watcher Semreleased it already; it will
		// recycle the waiter itself. Loop to find the next live
		// entry.
	}
}
```

The expression `m.state.Add(^uint32(mutexWaiterUnit - 1))` is the standard idiom for atomic subtraction on `atomic.Uint32` (which only exposes Add). It adds `0xFFFFFFFE`, which under wrap-around equals `-2`.

- [ ] **Step 2: Update Unlock to use the new behavior**

The existing `Unlock` method does not need changes — it already calls `m.unlockSlow()` when the fast path fails. Verify by reading lines around 65-72 of `mutex.go`.

- [ ] **Step 3: Verify full test suite passes under -race**

Run: `go test -race -count=5 ./internal/native/`
Expected: PASS for all tests, including the parallel contention and FIFO smoke tests.

If `TestMutex_FIFO_Smoke` fails: the list ordering is broken. If `TestMutex_ParallelContention` fails under race: the synchronization edge in lockSlow is missing. If `TestMutex_UnlockOfUnlockedPanics` fails: the panic path was lost when we removed the old unlockSlow.

- [ ] **Step 4: Verify panic-on-double-unlock still works**

The old `unlockSlow` had the `if old&mutexLocked == 0 { panic(...) }` check. The new path takes a different shape — the double-unlock now goes through the fast-path CAS, which fails (state is 0, so CAS(mutexLocked, 0) fails), then enters `unlockSlow`. In unlockSlow, the list will be empty AND `state != mutexLocked` (state == 0), so it hits the "state/list inconsistency" panic.

**That's the wrong panic message.** We need to detect double-unlock specifically.

Add at the top of `unlockSlow`, before acquiring listMu:

```go
	// Reject double-unlock cleanly: if state has no locked bit,
	// fail with the canonical message rather than falling into
	// the state/list invariant check below.
	if m.state.Load()&mutexLocked == 0 {
		panic("sync: unlock of unlocked mutex")
	}
```

- [ ] **Step 5: Re-run to confirm the panic message**

Run: `go test ./internal/native/ -run TestMutex_UnlockOfUnlockedPanics`
Expected: PASS (the test asserts the exact panic string).

- [ ] **Step 6: Commit**

```bash
git add internal/native/mutex.go
git commit -m "phase2: implement unlockSlow with tombstone-skipping handoff"
```

---

### Task 5: Add LockContext public method

**Files:** Modify `internal/native/mutex.go`.

- [ ] **Step 1: Add LockContext above the unlockSlow declaration**

```go
// LockContext acquires m, blocking until available or ctx is done.
// On cancellation, returns context.Cause(ctx) and leaves m unchanged.
//
// If ctx is already cancelled when LockContext is called and the
// lock is available with no queued waiters, LockContext may still
// succeed without blocking.
//
// If the mutex becomes available concurrently with ctx cancellation,
// LockContext may acquire the mutex and return nil even though ctx
// is done. Callers requiring ctx-strict behavior should re-check
// ctx.Err() after acquiring.
func (m *Mutex) LockContext(ctx context.Context) error {
	// Fast path: same as Lock — only attempt if no waiters.
	if m.state.CompareAndSwap(0, mutexLocked) {
		return nil
	}
	return m.lockSlow(ctx)
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/native/`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add internal/native/mutex.go
git commit -m "phase2: add LockContext public method"
```

---

### Task 6: TDD — LockContext basic behavior

**Files:** Modify `internal/native/mutex_test.go`.

- [ ] **Step 1: Add `"context"` and `"errors"` to the import block**

Resulting block:

```go
import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/neilotoole/fifomu/internal/native"
)
```

- [ ] **Step 2: Append the basic LockContext tests**

```go
func TestLockContext_FastPath(t *testing.T) {
	var mu native.Mutex
	if err := mu.LockContext(context.Background()); err != nil {
		t.Fatalf("LockContext on uncontended mutex returned %v", err)
	}
	mu.Unlock()
}

func TestLockContext_AlreadyCancelled(t *testing.T) {
	var mu native.Mutex
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// With no waiters and an unlocked mutex, LockContext may
	// succeed via the fast path even though ctx is already done —
	// this matches the documented behavior.
	if err := mu.LockContext(ctx); err != nil {
		t.Fatalf("LockContext on uncontended mutex with cancelled ctx returned %v (expected nil per docs)", err)
	}
	mu.Unlock()
}

func TestLockContext_CancelWhileBlocked(t *testing.T) {
	var mu native.Mutex
	mu.Lock() // hold the lock so LockContext must block

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- mu.LockContext(ctx)
	}()

	// Give the LockContext goroutine time to enqueue.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("LockContext returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("LockContext did not return within 2s after ctx cancel")
	}

	mu.Unlock() // we still hold the lock; nobody got it
}
```

- [ ] **Step 3: Run under -race**

Run: `go test -race -count=3 ./internal/native/ -run TestLockContext`
Expected: all three PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/native/mutex_test.go
git commit -m "phase2: TDD basic LockContext behavior"
```

---

### Task 7: TDD — mixed Lock + LockContext FIFO invariant

This is the load-bearing test. It mirrors `TestMutex_MixedQueueLockAndLockContext` in `fifomu_invariants_test.go:21`, adapted to the prototype's package.

**Files:** Modify `internal/native/mutex_test.go`.

- [ ] **Step 1: Append the mixed-queue FIFO test**

```go
// TestMutex_MixedFIFO verifies that the waiter queue treats Lock and
// LockContext entries identically: entries wake in FIFO order
// regardless of which entry point queued them.
//
// This invariant exists because users mix the two methods in real
// code; a refactor that branched on the entry point (e.g., parking
// LockContext on a separate sema) would silently break it.
func TestMutex_MixedFIFO(t *testing.T) {
	const N = 8
	var mu native.Mutex
	mu.Lock()

	order := make(chan int, N)
	errCh := make(chan error, N)
	var wg sync.WaitGroup
	ctx := context.Background()

	for i := range N {
		wg.Go(func() {
			// Even i uses Lock, odd i uses LockContext.
			if i%2 == 0 {
				mu.Lock()
			} else if err := mu.LockContext(ctx); err != nil {
				errCh <- err
				return
			}
			order <- i
			mu.Unlock()
		})
		// Stagger so each goroutine reaches the enqueue point in
		// arrival order. The reordering window in lockSlow before
		// the goroutine takes listMu is bounded but real.
		time.Sleep(300 * time.Microsecond)
	}

	mu.Unlock()
	wg.Wait()
	close(order)
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}

	var got []int
	for v := range order {
		got = append(got, v)
	}

	for i, v := range got {
		if v != i {
			t.Fatalf("acquisition order = %v, want sequential (mismatch at position %d)", got, i)
		}
	}
}
```

- [ ] **Step 2: Run repeatedly**

Run: `go test -race -count=20 ./internal/native/ -run TestMutex_MixedFIFO`
Expected: all 20 PASS. If even one fails, FIFO across the two entry points is broken — investigate immediately, do not proceed.

- [ ] **Step 3: Commit**

```bash
git add internal/native/mutex_test.go
git commit -m "phase2: TDD mixed Lock+LockContext FIFO invariant"
```

---

### Task 8: TDD — cancellation does not break FIFO for other waiters

When a LockContext waiter cancels mid-queue, the remaining waiters must still wake in arrival order — the tombstone is invisible to them.

**Files:** Modify `internal/native/mutex_test.go`.

- [ ] **Step 1: Append the cancel-in-middle test**

```go
// TestMutex_CancellationPreservesFIFO verifies that a cancelled
// LockContext entry in the middle of the queue is tombstoned and
// skipped by Unlock — the remaining waiters wake in arrival order
// as if the cancelled entry had never been there.
func TestMutex_CancellationPreservesFIFO(t *testing.T) {
	const N = 6
	var mu native.Mutex
	mu.Lock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	order := make(chan int, N)
	var wg sync.WaitGroup

	// Waiters 0..N-1. Waiter 2 uses LockContext with cancellable
	// ctx; we'll cancel it after everyone has enqueued.
	for i := range N {
		wg.Go(func() {
			if i == 2 {
				if err := mu.LockContext(ctx); err != nil {
					return
				}
			} else {
				mu.Lock()
			}
			order <- i
			mu.Unlock()
		})
		time.Sleep(300 * time.Microsecond)
	}

	// All N goroutines are enqueued. Cancel waiter 2.
	cancel()
	time.Sleep(20 * time.Millisecond) // let the watcher tombstone

	// Now release the lock — Unlock chain should yield 0, 1, 3, 4, 5.
	mu.Unlock()
	wg.Wait()
	close(order)

	var got []int
	for v := range order {
		got = append(got, v)
	}

	want := []int{0, 1, 3, 4, 5}
	if len(got) != len(want) {
		t.Fatalf("got %d acquisitions, want %d (got=%v)", len(got), len(want), got)
	}
	for i, v := range got {
		if v != want[i] {
			t.Fatalf("acquisition order = %v, want %v", got, want)
		}
	}
}
```

- [ ] **Step 2: Run repeatedly under -race**

Run: `go test -race -count=20 ./internal/native/ -run TestMutex_CancellationPreservesFIFO`
Expected: all 20 PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/native/mutex_test.go
git commit -m "phase2: TDD cancellation preserves FIFO for other waiters"
```

---

### Task 9: TDD — race between cancel and Unlock (the hardest race)

This task targets the (cancel, unlock, new-arrival) race the final reviewer identified as the hardest sub-problem. We construct it deterministically and run many times.

**Files:** Modify `internal/native/mutex_test.go`.

- [ ] **Step 1: Append a stress test**

```go
// TestMutex_CancelUnlockRace stresses the race between LockContext
// cancellation and Unlock claiming the same waiter. Either the
// cancellation CAS or the Unlock CAS must win; both winning (or
// both losing) is a bug. The test runs a tight Lock/LockContext/cancel/
// Unlock loop and asserts that the lock count and cancellation count
// reconcile: every iteration either acquires the lock once and
// returns no error, or fails to acquire and returns the ctx error.
func TestMutex_CancelUnlockRace(t *testing.T) {
	const iterations = 5_000

	var mu native.Mutex
	var acquired, cancelled int64

	for range iterations {
		mu.Lock()

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		var err error
		go func() {
			err = mu.LockContext(ctx)
			close(done)
		}()

		// Small sleep to let the goroutine reach Semacquire.
		time.Sleep(10 * time.Microsecond)

		// Race the Unlock and the cancel.
		go cancel()
		mu.Unlock()

		<-done
		if err == nil {
			atomic.AddInt64(&acquired, 1)
			mu.Unlock()
		} else if errors.Is(err, context.Canceled) {
			atomic.AddInt64(&cancelled, 1)
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	total := atomic.LoadInt64(&acquired) + atomic.LoadInt64(&cancelled)
	if total != iterations {
		t.Fatalf("lost iterations: acquired=%d cancelled=%d total=%d want=%d",
			acquired, cancelled, total, iterations)
	}
	t.Logf("acquired=%d cancelled=%d (both outcomes valid; both occurring proves the race is exercised)",
		acquired, cancelled)
}
```

- [ ] **Step 2: Add `"sync/atomic"` to imports**

Now the test file needs `"sync/atomic"`. Update the import block:

```go
import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/neilotoole/fifomu/internal/native"
)
```

- [ ] **Step 3: Run under -race**

Run: `go test -race ./internal/native/ -run TestMutex_CancelUnlockRace -count=5 -timeout=3m`
Expected: PASS across all 5 runs, with the log line showing both `acquired` and `cancelled` are nonzero (the race is being exercised).

If the test reports "lost iterations": there's a missed wakeup or a duplicate wakeup — the cancel/unlock CAS race is buggy.

- [ ] **Step 4: Commit**

```bash
git add internal/native/mutex_test.go
git commit -m "phase2: TDD cancel/unlock CAS race"
```

---

### Task 9.5: Stage A benchmark gate

Before migrating into the fifomu package, capture phase 2 benchmarks against phase 1's baseline. If LockContext integration regressed the uncontended fast path by more than 10%, fix before continuing.

**Files:** Create `docs/superpowers/results/2026-05-28-phase2-stageA-bench.txt`.

- [ ] **Step 1: Run benchmarks**

Run:

```bash
go test -run=^$ -bench='BenchmarkMutex$|BenchmarkMutexUncontended$|BenchmarkMutexSlack$|BenchmarkMutexWork$|BenchmarkMutexWorkSlack$|BenchmarkMutexNoSpin$|BenchmarkMutexSpin$' \
  -benchmem -count=6 -timeout=20m ./... \
  | tee docs/superpowers/results/2026-05-28-phase2-stageA-bench.txt
```

- [ ] **Step 2: Compare against phase 1 with benchstat**

Run:

```bash
benchstat \
  docs/superpowers/results/2026-05-27-native-linkname-mutex-bench.txt \
  docs/superpowers/results/2026-05-28-phase2-stageA-bench.txt
```

- [ ] **Step 3: Apply the gate**

If `MutexUncontended/native` regressed by more than 10% between phase 1 and stage A: stop. The slow-path additions should not be touching the fast path. Investigate before continuing. Otherwise proceed.

If contended benchmarks (`Mutex`, `MutexSlack`, `MutexWork`, `MutexWorkSlack`) regressed by more than 25% between phase 1 and stage A: investigate but do not block — some regression is expected because the slow path now includes a `sync.Mutex` acquisition. The acceptable bound depends on whether stage A is still beating current fifomu.

- [ ] **Step 4: Commit benchmark results**

```bash
git add docs/superpowers/results/2026-05-28-phase2-stageA-bench.txt
git commit -m "phase2: stage A benchmark results"
```

---

## Stage B: Replace fifomu.Mutex with the native impl

### Task 10: Move native.Mutex into the fifomu package

**Files:** Replace `fifomu.go` content. Delete `list.go`. Delete `internal/native/` entirely.

- [ ] **Step 1: Read the existing fifomu.go to identify what must be preserved**

Run: `head -90 fifomu.go`
Capture: the package doc comment (lines 1-53), and the public type doc on Mutex (lines 62-75). These are the user-visible documentation; we'll preserve them on the new implementation. Also note the noCopy declaration (lines 84-91) — we already have one in internal/native; we'll keep the fifomu copy and delete the internal/native one.

- [ ] **Step 2: Rewrite fifomu.go**

Replace the entire contents of `fifomu.go` with:

```go
// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// [PRESERVE EXISTING PACKAGE DOC COMMENT FROM lines 1-53, with the
// "# How it works" section updated to describe the state-word +
// shared-list design. Specifically replace the paragraph about
// "Internally, a Mutex is a sync.Mutex plus a FIFO queue of waiters.
// Each waiter is a buffered(1) channel..." with:]
//
// # How it works
//
// Internally, a Mutex packs (locked-bit, parked-waiter-count) into
// a single atomic.Uint32 state word. The uncontended Lock and Unlock
// fast paths are a single compare-and-swap each. Slow-path callers
// queue on a shared FIFO list of waiters (protected by an inner
// sync.Mutex that the fast paths never touch). Each waiter parks on
// its own per-waiter semaphore via go:linkname'd runtime primitives;
// Unlock walks the list head, skipping any tombstoned (cancelled)
// LockContext entries, and hands off via runtime_Semrelease with
// handoff=true — which is what gives the FIFO guarantee. No
// spinning. LockContext adds a watcher goroutine that races the
// unlocker's claim CAS against the cancellation tombstone CAS.
//
// [REMAINING DOC SECTIONS PRESERVED FROM ORIGINAL]
package fifomu

import (
	"context"
	"sync"
	"sync/atomic"
	_ "unsafe" // for go:linkname
)

var _ sync.Locker = (*Mutex)(nil)

// Mutex is a mutual exclusion lock whose Lock method returns
// the lock to queued callers in FIFO call order. See the package
// documentation for the precise guarantee and its caveats.
//
// A Mutex must not be copied after first use.
//
// The zero value for a Mutex is an unlocked mutex.
//
// Mutex implements the same methodset as sync.Mutex, so it can
// be used as a drop-in replacement. It implements an additional
// method Mutex.LockContext, which provides context-aware locking.
// Note that unlike sync.Mutex.TryLock, Mutex.TryLock deliberately
// reports failure whenever the waiter queue is non-empty, so that
// TryLock cannot jump ahead of FIFO-queued waiters.
//
// # Known limitations
//
// fifomu.Mutex contention does NOT appear in mutex profiles
// (runtime.SetMutexProfileFraction). Block profiles still work.
// This is because the runtime exposes mutex-profile-tagged sema
// primitives only to the standard library's sync package. See
// the package documentation for details.
//
// Blocked goroutines parked inside fifomu.Mutex appear in
// runtime/pprof goroutine profiles with the wait reason
// "semacquire" rather than "sync.Mutex.Lock".
type Mutex struct {
	_ noCopy

	state atomic.Uint32

	listMu sync.Mutex
	head   *waiter
	tail   *waiter
}

const (
	mutexLocked     uint32 = 1
	mutexWaiterUnit uint32 = 2
)

const (
	waiterParked    uint32 = 0
	waiterWon       uint32 = 1
	waiterCancelled uint32 = 2
)

type waiter struct {
	state atomic.Uint32
	sema  uint32
	next  *waiter
}

var waiterPool = sync.Pool{
	New: func() any { return new(waiter) },
}

func (w *waiter) resetForPool() {
	w.state.Store(waiterParked)
	w.next = nil
	w.sema = 0
}

// Lock acquires m, blocking until it is available.
func (m *Mutex) Lock() {
	if m.state.CompareAndSwap(0, mutexLocked) {
		return
	}
	_ = m.lockSlow(nil)
}

// LockContext acquires m, blocking until available or ctx is done.
// On cancellation, returns context.Cause(ctx) and leaves m unchanged.
//
// If ctx is already cancelled when LockContext is called and the
// lock is available with no queued waiters, LockContext may still
// succeed without blocking.
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

// TryLock attempts to acquire m without blocking.
//
// Unlike sync.Mutex.TryLock, this reports failure when any waiter
// is queued, even if the lock is momentarily unheld — required to
// preserve FIFO ordering.
func (m *Mutex) TryLock() bool {
	return m.state.CompareAndSwap(0, mutexLocked)
}

// Unlock releases m. Panics if m is not locked on entry.
//
// A locked Mutex is not associated with a particular goroutine.
// It is allowed for one goroutine to lock a Mutex and then arrange
// for another goroutine to unlock it.
func (m *Mutex) Unlock() {
	if m.state.CompareAndSwap(mutexLocked, 0) {
		return
	}
	m.unlockSlow()
}

func (m *Mutex) lockSlow(ctx context.Context) error {
	m.listMu.Lock()
	if m.state.CompareAndSwap(0, mutexLocked) {
		m.listMu.Unlock()
		return nil
	}

	w := waiterPool.Get().(*waiter)
	w.state.Store(waiterParked)
	m.state.Add(mutexWaiterUnit)
	if m.tail == nil {
		m.head = w
	} else {
		m.tail.next = w
	}
	m.tail = w
	m.listMu.Unlock()

	if ctx == nil {
		runtime_Semacquire(&w.sema)
		_ = w.state.Load()
		w.resetForPool()
		waiterPool.Put(w)
		return nil
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			if w.state.CompareAndSwap(waiterParked, waiterCancelled) {
				runtime_Semrelease(&w.sema, false, 0)
			}
		case <-done:
		}
	}()
	runtime_Semacquire(&w.sema)
	close(done)
	state := w.state.Load()
	w.resetForPool()
	waiterPool.Put(w)
	if state == waiterCancelled {
		return context.Cause(ctx)
	}
	return nil
}

func (m *Mutex) unlockSlow() {
	if m.state.Load()&mutexLocked == 0 {
		panic("sync: unlock of unlocked mutex")
	}
	m.listMu.Lock()
	for {
		w := m.head
		if w == nil {
			if m.state.Load() != mutexLocked {
				m.listMu.Unlock()
				panic("fifomu: state/list inconsistency in unlockSlow")
			}
			m.state.Store(0)
			m.listMu.Unlock()
			return
		}
		m.head = w.next
		if m.head == nil {
			m.tail = nil
		}
		m.state.Add(^uint32(mutexWaiterUnit - 1))
		if w.state.CompareAndSwap(waiterParked, waiterWon) {
			m.listMu.Unlock()
			runtime_Semrelease(&w.sema, true, 0)
			return
		}
	}
}

// runtime_Semacquire is linkname'd to sync.runtime_Semacquire.
//
//go:linkname runtime_Semacquire sync.runtime_Semacquire
func runtime_Semacquire(s *uint32)

// runtime_Semrelease is linkname'd to sync.runtime_Semrelease.
//
//go:linkname runtime_Semrelease sync.runtime_Semrelease
func runtime_Semrelease(s *uint32, handoff bool, skipframes int)

type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}
```

(Wherever this task says "preserve" a section from the original `fifomu.go`, copy the exact lines from the current file. They contain user-visible documentation that should not be regressed.)

- [ ] **Step 3: Delete list.go**

```bash
rm list.go
```

- [ ] **Step 4: Delete internal/native/ entirely**

```bash
rm -rf internal/native/
```

- [ ] **Step 5: Verify everything builds**

Run: `go build ./...`
Expected: success. Any compile errors are likely in `export_test.go` or `internal_test.go` referencing the old `list` type. Fix those by deleting any references to the channel-waiter / list types.

- [ ] **Step 6: Run the FULL test suite under -race**

Run: `go test -race -count=2 ./...`
Expected: PASS for everything, including `TestMutex_MixedQueueLockAndLockContext` and all other invariants in `fifomu_invariants_test.go`.

If ANY existing test fails, stop and investigate. The migration must be drop-in.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "phase2: replace fifomu.Mutex internals with native impl"
```

---

### Task 11: Update README and package docs

**Files:** Modify `README.md`.

- [ ] **Step 1: Read the current README**

Run: `cat README.md | head -100`. The README has a "How it works" / "Benchmarks" section.

- [ ] **Step 2: Update the "How it works" section**

Replace the description of the channel-based design with a brief explanation of the state-word + shared-list approach. Reference the same details as the package doc.

- [ ] **Step 3: Add a "Known limitations" section if not already present**

Document:
- No mutex-profile coverage (block profile still works).
- Goroutine wait reason appears as `semacquire`.
- Linkname dependency on `sync.runtime_Semacquire`/`Semrelease`; signatures committed-not-to-change by the Go team for gvisor compatibility, but monitor each Go minor version.

- [ ] **Step 4: Update the benchmark numbers**

Replace any quoted benchmark numbers in the README with the phase 2 stage B benchmarks (which Task 12 will produce). For now, mark them as TBD.

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "phase2: update README for native impl"
```

---

### Task 12: Final benchmark and report

**Files:** Create `docs/superpowers/results/2026-05-28-phase2-stageB-bench.txt` and `docs/superpowers/results/2026-05-28-phase2-final.md`.

- [ ] **Step 1: Run benchmarks**

Run:

```bash
go test -run=^$ \
  -bench='BenchmarkMutex$|BenchmarkMutexUncontended$|BenchmarkMutexSlack$|BenchmarkMutexWork$|BenchmarkMutexWorkSlack$|BenchmarkMutexNoSpin$|BenchmarkMutexSpin$|BenchmarkLockContext' \
  -benchmem -count=6 -timeout=20m ./... \
  | tee docs/superpowers/results/2026-05-28-phase2-stageB-bench.txt
```

This includes the LockContext benchmarks that were excluded from phase 1's run.

- [ ] **Step 2: benchstat against baseline (current fifomu pre-rewrite)**

The baseline is `docs/superpowers/results/2026-05-27-current-impl-baseline-bench.txt` from phase 0.

```bash
benchstat \
  docs/superpowers/results/2026-05-27-current-impl-baseline-bench.txt \
  docs/superpowers/results/2026-05-28-phase2-stageB-bench.txt
```

- [ ] **Step 3: Write the final report**

Create `docs/superpowers/results/2026-05-28-phase2-final.md` with a structure modeled on the phase 1 report:

- Verdict (ship / hold / revert)
- Headline numbers (table: stdlib, baseline-fifomu, native-phase-2)
- LockContext numbers vs current impl
- Known limitations recap
- Decision: ready for non-draft PR? merge?

- [ ] **Step 4: Update README with the final benchmark numbers**

Replace the TBDs in README.md with the actual numbers from the report.

- [ ] **Step 5: Commit**

```bash
git add docs/superpowers/results/ README.md
git commit -m "phase2: final benchmarks and report"
```

---

### Task 13: Mark the PR ready for review

**Files:** none.

- [ ] **Step 1: Verify branch state**

Run: `git log --oneline ^master HEAD | wc -l`
Confirm a reasonable number of commits (should be ~30 between phase 1 and phase 2). Phase 1 was 14; phase 2 should be roughly the same.

- [ ] **Step 2: Push**

```bash
git push origin worktree-native-mutex-prototype
```

- [ ] **Step 3: Mark the PR ready**

```bash
gh pr ready 6
```

Then optionally update the PR title to drop "Prototype:" and update the description to reflect the full scope.

- [ ] **Step 4: Hand off**

The PR is ready for code review. Stop here — do not merge without explicit user approval.

---

## Self-Review

**Spec coverage:**
- LockContext implementation: Tasks 3, 5
- Cancellation via tombstoned tickets: Tasks 4, 8, 9
- Mixed Lock+LockContext FIFO invariant: Task 7 + Task 10 Step 6 (the existing TestMutex_MixedQueueLockAndLockContext test must pass after migration)
- In-place replacement of fifomu.Mutex: Task 10
- Known limitations documented: Task 11
- Final benchmark + decision: Task 12

**Placeholder scan:** Task 10 says "PRESERVE EXISTING PACKAGE DOC COMMENT" in a quote-block — that's a directive to the implementer, not a placeholder. Every code step contains real code. No TBDs except in Task 12 where the implementer is told to produce the actual numbers.

**Type consistency:** `waiterParked` / `waiterWon` / `waiterCancelled` are used in lockSlow (Task 3), unlockSlow (Task 4), tests (Tasks 6-9), and the final migration (Task 10). `mutexLocked` / `mutexWaiterUnit` carry over from phase 1.

**Cross-Stage coherence:** Stage A finishes with a working `internal/native.Mutex` that has LockContext. Stage B copies the same code into fifomu.go, then deletes the old internals. The two stages share constants and structure exactly.

---

## Notes for the implementer

- Run with `-race -count=10` whenever touching slow-path code. Race conditions in this kind of code don't reproduce reliably without high iteration counts.
- The Task 9 race test is the canary. If it ever fails, the cancellation protocol is broken — do not paper over it with retries.
- Don't add a panic-on-misuse "safety" check that depends on race-free observation of state. The slow paths intentionally observe state inconsistently between CAS attempts; only the invariants asserted under listMu are reliable.
- If Task 10's migration breaks an existing test in `fifomu_invariants_test.go` or `fifomu_chaos_test.go`, do NOT modify the test. The test is the spec. Fix the implementation.
- The watcher goroutine in `lockSlow` allocates a `chan struct{}` per LockContext call. That's one allocation per LockContext slow path; phase 1's benchmark already showed LockContext_Contended at 0 B/op for the current impl, so we must hit that target too. If benchmarks reveal an allocation regression on LockContext, replace `done := make(chan struct{})` with an `atomic.Bool` flag the watcher polls (but that adds polling overhead — measure both).
