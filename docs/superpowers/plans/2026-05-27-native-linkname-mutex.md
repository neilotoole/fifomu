# Native-Linkname Mutex Prototype — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a prototype `Mutex` for fifomu that replaces the inner-`sync.Mutex` + channel-pool design with an atomic state word and direct goroutine parking via `sync.runtime_Semacquire`/`sync.runtime_Semrelease` (linkname'd), then measure whether it materially beats the current implementation.

**Architecture:** A single `atomic.Uint32` state word encodes the locked bit (bit 0) and the parked-waiter count (bits 1..31). `Lock` fast-paths on `CAS(0, locked)`. The slow path increments the waiter count and parks via `runtime_Semacquire`. `Unlock` fast-paths on `CAS(locked, 0)`; when waiters are present, it decrements the count and calls `runtime_Semrelease(&sema, handoff=true, 1)`, which the runtime turns into a direct goroutine handoff (skipping the scheduler steal window). FIFO is guaranteed by two properties: (a) the runtime's per-address sema wait queue is FIFO when `lifo=false`, which `sync.runtime_Semacquire` hardcodes; (b) `handoff=true` consumes the released permit on the head waiter's behalf via `cansemacquire`, so new arrivals cannot steal it.

**This plan only covers Lock / Unlock / TryLock.** `LockContext` is deferred — see "Out of scope" below. The prototype is gated by a benchmark decision point: if the native impl does not show a meaningful win over the current channel-based impl, work stops and we keep the existing code.

**Tech Stack:** Go 1.23+ (for the runtime linkname allowlist semantics). `sync.runtime_Semacquire` and `sync.runtime_Semrelease` (both unblocked in `cmd/link/internal/loader/loader.go`'s `blockedLinknames` map; both have "do not change signature" comments in `runtime/sema.go:60-91`). `sync/atomic`. The `internal/` directory of the existing repo for package isolation.

**Out of scope (future plan):**
- `LockContext` — current fifomu has an invariant test (`fifomu_invariants_test.go:21`, `TestMutex_MixedQueueLockAndLockContext`) that requires Lock and LockContext entries to share one FIFO queue. The native design naturally parks `Lock` waiters on a runtime semaphore that cannot be cancelled. Designing a unified cancellable FIFO queue on top of the sema is its own design problem and a separate plan. The prototype here will not provide `LockContext`; the benchmark harness will exclude it for the native impl.
- Mutex profiling (`runtime.SetMutexProfileFraction`) integration — the externally-linkname-able `sync.runtime_Semacquire` uses only `semaBlockProfile`, not `semaMutexProfile`. The native impl will not appear in mutex profiles. Documented as a known limitation.

## File Structure

- `internal/native/mutex.go` — the prototype implementation. Lives under `internal/` so it is only consumable by the fifomu package itself (and its tests). New file.
- `internal/native/linkname.go` — the linkname declarations and the `_ "unsafe"` import. Separated so that the linkname surface is in one short, audit-friendly file. New file.
- `internal/native/mutex_test.go` — unit tests for the prototype: lock/unlock, double-unlock panic, TryLock semantics, simple FIFO smoke test. New file.
- `fifomu_test.go:36-72` — modify `benchmarkEachImpl` and `newMu` to add a `"native"` branch using `*native.Mutex`. The `mutexer` interface (line 25-29) does not include `LockContext`, so this is a small additive change.
- `docs/superpowers/plans/2026-05-27-native-linkname-mutex.md` — this plan.
- `docs/superpowers/results/2026-05-27-native-linkname-mutex.md` — final benchstat comparison report, written at the end as the deliverable. New file.

---

### Task 1: Create the linkname surface

**Files:**
- Create: `internal/native/linkname.go`

- [ ] **Step 1: Write the linkname declarations**

Create `internal/native/linkname.go`:

```go
// Package native is a prototype FIFO mutex built directly on the
// runtime's semaphore primitives (linkname'd from package sync).
// It is intentionally minimal: only Lock, Unlock, and TryLock.
// LockContext is not supported in this prototype.
package native

import (
	_ "unsafe" // for go:linkname
)

// runtime_Semacquire is linkname'd to sync.runtime_Semacquire.
//
// sync.runtime_Semacquire hardcodes lifo=false in the runtime
// (runtime/sema.go:70-72), which gives a FIFO wait queue for
// goroutines parked on the same sema address. The symbol is not in
// cmd/link's blockedLinknames map and the runtime comment at
// runtime/sema.go:60-67 explicitly commits to not changing the
// signature ("Do not remove or change the type signature").
//
//go:linkname runtime_Semacquire sync.runtime_Semacquire
func runtime_Semacquire(s *uint32)

// runtime_Semrelease is linkname'd to sync.runtime_Semrelease.
//
// With handoff=true, the released permit is consumed by the head
// waiter directly via cansemacquire in semrelease1 (runtime/sema.go
// :260-263), so new arrivals racing on the address cannot steal it.
// That property is what makes our FIFO guarantee hold under
// contention.
//
//go:linkname runtime_Semrelease sync.runtime_Semrelease
func runtime_Semrelease(s *uint32, handoff bool, skipframes int)
```

- [ ] **Step 2: Verify the package compiles**

Run: `go build ./internal/native/`
Expected: no output (success). If the linker rejects the linkname, you'll see `invalid reference to sync.runtime_Semacquire` — that would indicate the allowlist analysis was wrong and the project should stop here.

- [ ] **Step 3: Commit**

```bash
git add internal/native/linkname.go
git commit -m "prototype: add runtime sema linkname surface"
```

---

### Task 2: Write the smallest possible Lock/Unlock test (red)

**Files:**
- Create: `internal/native/mutex_test.go`

- [ ] **Step 1: Write a failing test for basic lock/unlock**

Create `internal/native/mutex_test.go`:

```go
package native_test

import (
	"testing"

	"github.com/neilotoole/fifomu/internal/native"
)

func TestMutex_LockUnlock(t *testing.T) {
	var mu native.Mutex
	mu.Lock()
	mu.Unlock()
	mu.Lock()
	mu.Unlock()
}
```

**Note for later tasks:** Tasks 4 and 7 add tests that need extra imports (`"sync"`, `"time"`). When you reach those tasks, add the new package to this single existing `import` block — do not write a second import declaration.

- [ ] **Step 2: Run the test and verify it fails to compile**

Run: `go test ./internal/native/`
Expected: `undefined: native.Mutex`. Compilation failure, as intended for the TDD red step.

---

### Task 3: Implement Mutex with fast-path-only Lock/Unlock (green)

**Files:**
- Create: `internal/native/mutex.go`

- [ ] **Step 1: Write the minimal Mutex struct and methods**

Create `internal/native/mutex.go`:

```go
package native

import "sync/atomic"

// Mutex is a FIFO mutex that parks waiters directly on the runtime
// semaphore primitive. See linkname.go for the runtime hooks.
//
// The zero value is an unlocked mutex. A Mutex must not be copied
// after first use.
type Mutex struct {
	_ noCopy

	// state: bit 0 = locked, bits 1..31 = parked waiter count.
	state atomic.Uint32

	// sema is the address parked waiters block on via
	// runtime_Semacquire. Its value is opaque to us; only the
	// runtime sema implementation touches it.
	sema uint32
}

const (
	mutexLocked    = 1
	mutexWaiterUnit = 2 // increment per parked waiter
)

// Lock acquires m, blocking until it is available.
func (m *Mutex) Lock() {
	// Fast path: unlocked, no waiters.
	if m.state.CompareAndSwap(0, mutexLocked) {
		return
	}
	m.lockSlow()
}

// lockSlow is the parking path. Kept out of Lock so Lock's fast
// path is small enough to inline.
func (m *Mutex) lockSlow() {
	// Add ourselves as a waiter, then park.
	for {
		old := m.state.Load()
		if old == 0 {
			// Lock was just released and there are no other
			// waiters ahead of us. We can take it directly.
			if m.state.CompareAndSwap(0, mutexLocked) {
				return
			}
			continue
		}
		if m.state.CompareAndSwap(old, old+mutexWaiterUnit) {
			runtime_Semacquire(&m.sema)
			// On wake, the unlocker handed us the lock via
			// direct handoff: state already has the locked bit
			// set, and our waiter slot has been decremented.
			return
		}
	}
}

// Unlock releases m. Panics if m is not locked on entry.
func (m *Mutex) Unlock() {
	// Fast path: locked, no waiters.
	if m.state.CompareAndSwap(mutexLocked, 0) {
		return
	}
	m.unlockSlow()
}

// unlockSlow handles the waiter handoff path.
func (m *Mutex) unlockSlow() {
	for {
		old := m.state.Load()
		if old&mutexLocked == 0 {
			panic("sync: unlock of unlocked mutex")
		}
		// Hand off: keep the locked bit set (transferred to the
		// waker), decrement the waiter count.
		if m.state.CompareAndSwap(old, old-mutexWaiterUnit) {
			// handoff=true makes the released permit
			// consumed by the head waiter directly via
			// cansemacquire — no new arrival can steal it.
			runtime_Semrelease(&m.sema, true, 1)
			return
		}
	}
}

// TryLock attempts to acquire m without blocking. Unlike
// sync.Mutex.TryLock, this reports failure when any waiter is
// queued, even if the lock is momentarily unheld — required to
// preserve FIFO ordering.
func (m *Mutex) TryLock() bool {
	return m.state.CompareAndSwap(0, mutexLocked)
}

// noCopy is the standard zero-sized marker that triggers
// `go vet -copylocks` on accidental copies of Mutex.
type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}
```

- [ ] **Step 2: Run the test and verify it passes**

Run: `go test ./internal/native/`
Expected: `ok  github.com/neilotoole/fifomu/internal/native ...s`

- [ ] **Step 3: Commit**

```bash
git add internal/native/mutex.go internal/native/mutex_test.go
git commit -m "prototype: native fifo mutex with state-word fast path"
```

---

### Task 4: Add a parallel-contention test (red, then green by existing code)

**Files:**
- Modify: `internal/native/mutex_test.go`

- [ ] **Step 1: Add a contention test**

First add `"sync"` to the existing `import` block at the top of `internal/native/mutex_test.go` (alongside `"testing"`). Then append:

```go
func TestMutex_ParallelContention(t *testing.T) {
	const goroutines = 20
	const itersPer = 5_000

	var mu native.Mutex
	var counter int

	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range itersPer {
				mu.Lock()
				counter++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	want := goroutines * itersPer
	if counter != want {
		t.Fatalf("counter = %d, want %d (mutex did not serialize)", counter, want)
	}
}
```

- [ ] **Step 2: Run the test**

Run: `go test -race ./internal/native/ -run TestMutex_ParallelContention`
Expected: PASS. If the counter is wrong or the race detector fires, the state-word transitions in `lockSlow`/`unlockSlow` have a bug — investigate before continuing.

- [ ] **Step 3: Commit**

```bash
git add internal/native/mutex_test.go
git commit -m "prototype: contention test for native mutex"
```

---

### Task 5: TryLock semantics test

**Files:**
- Modify: `internal/native/mutex_test.go`

- [ ] **Step 1: Write the TryLock test**

Append:

```go
func TestMutex_TryLock(t *testing.T) {
	var mu native.Mutex

	if !mu.TryLock() {
		t.Fatal("TryLock on unlocked mutex returned false")
	}
	if mu.TryLock() {
		t.Fatal("TryLock on locked mutex returned true")
	}
	mu.Unlock()

	// FIFO TryLock: must fail when a waiter is queued, even if
	// the lock has been momentarily released. We can't deterministically
	// observe that state without instrumentation, but we can at
	// least verify the basic locked/unlocked cases.
	if !mu.TryLock() {
		t.Fatal("TryLock on re-released mutex returned false")
	}
	mu.Unlock()
}
```

- [ ] **Step 2: Run it**

Run: `go test ./internal/native/ -run TestMutex_TryLock`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/native/mutex_test.go
git commit -m "prototype: TryLock semantics test"
```

---

### Task 6: Double-unlock panic test

**Files:**
- Modify: `internal/native/mutex_test.go`

- [ ] **Step 1: Write the double-unlock test**

Append:

```go
func TestMutex_UnlockOfUnlockedPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Unlock of unlocked mutex did not panic")
		}
		got, _ := r.(string)
		if got != "sync: unlock of unlocked mutex" {
			t.Fatalf("panic = %v, want \"sync: unlock of unlocked mutex\"", r)
		}
	}()
	var mu native.Mutex
	mu.Unlock()
}
```

- [ ] **Step 2: Run it**

Run: `go test ./internal/native/ -run TestMutex_UnlockOfUnlockedPanics`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/native/mutex_test.go
git commit -m "prototype: panic on double-unlock"
```

---

### Task 7: FIFO ordering smoke test

This test queues N goroutines that must each acquire and record their position. Strict-FIFO is hard to assert deterministically (the docs explicitly carve out a reordering window), but a high-N test with sequential queueing usually catches gross violations.

**Files:**
- Modify: `internal/native/mutex_test.go`

- [ ] **Step 1: Write the FIFO smoke test**

First add `"time"` to the existing `import` block at the top of `internal/native/mutex_test.go`. Then append:

```go
// TestMutex_FIFO_Smoke is a probabilistic FIFO check. It cannot
// guarantee strict FIFO (the runtime sema queue ordering across
// the gap between "atomic state CAS" and "actually parked" is not
// strictly bounded), but a high-N test with deliberate stagger
// between Lock calls reliably catches gross reorderings.
func TestMutex_FIFO_Smoke(t *testing.T) {
	const N = 32
	var mu native.Mutex
	mu.Lock() // hold the lock so everyone queues

	order := make(chan int, N)
	var wg sync.WaitGroup
	for i := range N {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.Lock()
			order <- i
			mu.Unlock()
		}()
		// Stagger to ensure goroutines reach the parking point in
		// arrival order. Without this, the test sometimes fires
		// even for a correct FIFO impl.
		time.Sleep(200 * time.Microsecond)
	}

	mu.Unlock()
	wg.Wait()
	close(order)

	var got []int
	for v := range order {
		got = append(got, v)
	}

	for i, v := range got {
		if v != i {
			t.Fatalf("acquisition order = %v, want %v (mismatch at position %d)", got, want(N), i)
		}
	}
}

func want(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}
```

- [ ] **Step 2: Run it**

Run: `go test ./internal/native/ -run TestMutex_FIFO_Smoke -count=20`
Expected: PASS across all 20 runs. If even one fails, FIFO is broken.

- [ ] **Step 3: Commit**

```bash
git add internal/native/mutex_test.go
git commit -m "prototype: FIFO ordering smoke test"
```

---

### Task 8: Wire the native impl into the benchmark harness

**Files:**
- Modify: `fifomu_test.go` (specifically lines 25-72; the `mutexer` interface, the `newMu` variable, the `newFifoMu`/`newStdlibMu`/`newSemaphoreMu` helpers, and `benchmarkEachImpl`)

- [ ] **Step 1: Read the current harness**

Run: `sed -n '20,75p' fifomu_test.go` to confirm the exact lines before editing. The harness expects each impl to satisfy the `mutexer` interface (Lock, Unlock, TryLock). `*native.Mutex` already does.

- [ ] **Step 2: Add the native branch**

In `fifomu_test.go`, find the import block and add:

```go
	"github.com/neilotoole/fifomu/internal/native"
```

Then find the `_ mutexer = (*fifomu.Mutex)(nil)` block (around line 31) and add:

```go
	_ mutexer = (*native.Mutex)(nil)
```

Then add a constructor near the existing `newFifoMu`/`newStdlibMu`/`newSemaphoreMu`:

```go
func newNativeMu() mutexer {
	return &native.Mutex{}
}
```

Finally, in `benchmarkEachImpl`, add a fourth `b.Run` block:

```go
	b.Run("native", func(b *testing.B) {
		newMu = newNativeMu
		fn(b)
		newMu = newFifoMu
	})
```

(Add it after the existing `"semaphoreMu"` branch.)

- [ ] **Step 3: Verify the benchmarks compile and one runs**

Run: `go test -run=^$ -bench=BenchmarkMutexUncontended -benchtime=100ms ./...`
Expected: Output includes a `BenchmarkMutexUncontended/native-10` line.

- [ ] **Step 4: Commit**

```bash
git add fifomu_test.go
git commit -m "prototype: add native impl to benchmark harness"
```

---

### Task 9: Run the full benchmark comparison

**Files:**
- Create: `docs/superpowers/results/2026-05-27-native-linkname-mutex-bench.txt` (raw benchstat input)
- Create: `docs/superpowers/results/2026-05-27-native-linkname-mutex.md` (the report)

- [ ] **Step 1: Run the full suite, same args as the baseline**

Run:

```bash
go test -run=^$ \
  -bench='BenchmarkMutex$|BenchmarkMutexUncontended$|BenchmarkMutexSlack$|BenchmarkMutexWork$|BenchmarkMutexWorkSlack$|BenchmarkMutexNoSpin$|BenchmarkMutexSpin$' \
  -benchmem -count=6 -timeout=20m ./... \
  | tee docs/superpowers/results/2026-05-27-native-linkname-mutex-bench.txt
```

Expected: completes in a few minutes; produces output with `stdlib`/`fifomu`/`semaphoreMu`/`native` subbenchmarks for each parent.

- [ ] **Step 2: Summarize with benchstat**

Run:

```bash
benchstat docs/superpowers/results/2026-05-27-native-linkname-mutex-bench.txt
```

Capture the output and write `docs/superpowers/results/2026-05-27-native-linkname-mutex.md` with this structure:

```markdown
# Native-Linkname Mutex Prototype — Results

**Date:** 2026-05-27
**Go version:** [output of `go version`]
**CPU:** Apple M1 Max, 10 cores

## Headline numbers

| Benchmark | sync.Mutex | fifomu (current) | native (prototype) | semaphoreMu |
|---|---|---|---|---|
| MutexUncontended | [n]ns | [n]ns | [n]ns | [n]ns |
| Mutex | [n]ns | [n]ns | [n]ns | [n]ns |
| MutexSlack | ... | ... | ... | ... |
| MutexWork | ... | ... | ... | ... |
| MutexWorkSlack | ... | ... | ... | ... |
| MutexNoSpin | ... | ... | ... | ... |
| MutexSpin | ... | ... | ... | ... |

## Verdict

[Fill in: did native beat current fifomu? By how much? On which benchmarks?]

## Decision

[Choose: (a) proceed to phase 2 (LockContext integration) — write a follow-up plan, (b) stop — current impl is good enough, (c) revisit with a different design]

## Full benchstat output

[Paste the full benchstat output here in a fenced block.]
```

- [ ] **Step 3: Commit**

```bash
git add docs/superpowers/results/2026-05-27-native-linkname-mutex-bench.txt \
        docs/superpowers/results/2026-05-27-native-linkname-mutex.md
git commit -m "prototype: native-linkname mutex benchmark results"
```

---

### Task 10: Decision review

This is not a code task — it is the explicit checkpoint that gates further work.

- [ ] **Step 1: Read the results report**

Read `docs/superpowers/results/2026-05-27-native-linkname-mutex.md`.

- [ ] **Step 2: Apply the decision criteria**

Proceed to a follow-up plan covering LockContext integration **only if all three are true:**

1. `MutexUncontended/native` is ≤ 1.3× `MutexUncontended/stdlib` (i.e. native gets close to sync.Mutex's uncontended fast path — current fifomu is 1.87×).
2. `MutexNoSpin/native` is ≤ 0.7× `MutexNoSpin/fifomu` (i.e. native is at least 30% faster than current fifomu on the high-contention path where current fifomu loses 3× to stdlib).
3. No correctness regressions (the FIFO smoke test passed reliably across many runs).

If any criterion fails: stop. Document the reason in the report and close out the prototype. The current channel-based impl wins; the linkname tax is not justified.

If all pass: write a separate plan covering LockContext integration. That plan must address the `TestMutex_MixedQueueLockAndLockContext` invariant — Lock and LockContext entries must share one FIFO queue.

- [ ] **Step 3: Update the report's "Decision" section accordingly and commit**

```bash
git add docs/superpowers/results/2026-05-27-native-linkname-mutex.md
git commit -m "prototype: decision logged"
```

---

## Self-Review

**Spec coverage:**
- "Use sync.runtime_Semacquire and sync.runtime_Semrelease via linkname" → Task 1.
- "State-word fast path matching sync.Mutex shape" → Task 3.
- "FIFO preserved" → Tasks 3 (handoff=true) and 7 (smoke test).
- "Add to benchmark harness, compare against current fifomu / sync.Mutex / semaphore.Weighted" → Tasks 8, 9.
- "Decision point" → Task 10.
- "LockContext explicitly out of scope" → documented in header.

**Placeholder scan:** No "TBD", no "add error handling", no "implement later". The only deliberate gaps are the dollar-value benchmark numbers in Task 9's report template — those cannot be known until the bench runs.

**Type consistency:** `mutexer` interface, `*native.Mutex`, and the helper function names (`newNativeMu`) are used consistently. `mutexLocked` and `mutexWaiterUnit` constants are used both in `lockSlow` and `unlockSlow`. The `noCopy` type is defined locally in `mutex.go` and used by `Mutex`.

**Cross-Phase coherence:** Phase 2 (LockContext) is explicitly deferred to a separate plan. The `mutexer` interface in the benchmark harness already excludes LockContext, so adding the native impl to the harness does not require touching the `LockContext` test path.

---

## Notes for the implementer

- Run `go vet ./...` after each task — `noCopy` should keep flagging any accidental Mutex copies. If it ever stops working, check the marker type was defined correctly.
- The `-race` flag is cheap on these tests — use it whenever you run them. State-word races are exactly what it's there to catch.
- If a benchmark produces wildly variable results, re-run with `-count=10` and `benchstat -confidence 0.95` to get tighter intervals before drawing conclusions. Wall-clock benchmarks on a laptop with other workloads can be noisy.
