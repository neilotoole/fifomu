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
