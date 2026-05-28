package native

import (
	"sync"
	"sync/atomic"
)

// Mutex is a FIFO mutex that parks waiters directly on the runtime
// semaphore primitive. See linkname.go for the runtime hooks.
//
// The zero value is an unlocked mutex. A Mutex must not be copied
// after first use.
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

const (
	mutexLocked     = 1
	mutexWaiterUnit = 2 // increment per parked waiter
)

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
	panic("phase2: lockSlow not yet implemented")
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
	panic("phase2: unlockSlow not yet implemented")
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
