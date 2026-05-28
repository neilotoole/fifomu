package native

import (
	"context"
	"runtime"
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

// Waiters are NOT pooled. The MS-queue's "popped head becomes new
// sentinel" invariant requires the popped node's .next pointer to
// remain valid until the next Unlock advances past it. Resetting
// fields for pool reuse would break the chain. The cost is one
// allocation per slow-path call; benchmarks should show that's
// still a net win vs current fifomu's channel+inner-mutex design
// on ns/op, but it does regress B/op from 0 to ~24.
//
// (A pool-friendly design — Unlock recycling the OLD sentinel when
// it advances head — is feasible for Lock waiters but conflicts
// with LockContext watcher lifetimes. Deferred to a future revision.)

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

// lockSlow is the slow path for both Lock (ctx=context.Background())
// and LockContext (ctx with cancellable Done()).
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

		// Allocate fresh waiter per slow-path call (see top-of-file
		// comment for why we cannot pool with the MS-queue layout).
		w := new(waiter)
		w.state.Store(waiterParked)

		// Bump waiter count BEFORE enqueueing. If the lock was
		// freed between our last check and this Add, undo and retry.
		newState := m.state.Add(mutexWaiterUnit)
		if newState&mutexLocked == 0 {
			m.state.Add(^uint32(mutexWaiterUnit - 1))
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
			// (Unlock will walk past it on the next dequeue, finding
			// the tombstone via its CAS failure). Do NOT touch w —
			// it's owned by the queue from now on; GC will reclaim
			// once Unlock advances head past it.
			return context.Cause(ctx)
		}
		// Unlock won. The race-detector synchronization edge:
		_ = w.state.Load()
		return nil
	}
}

// unlockSlow walks the lock-free queue, skipping tombstoned
// (cancelled) entries, and hands off to the first live waiter via
// direct runtime_Semrelease.
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
				runtime.Gosched()
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

// noCopy triggers `go vet -copylocks` on accidental copies of Mutex.
type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}
