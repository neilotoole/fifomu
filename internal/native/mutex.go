package native

import (
	"context"
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
	_ = m.lockSlow(context.Background())
}

// lockSlow is the Lock slow path. Both Lock and LockContext call it;
// Lock passes context.Background() (whose Done() is nil, signalling
// the non-cancellable path), LockContext passes the caller's ctx.
// Returns context.Cause(ctx) only when ctx becomes cancelled before
// the lock is handed off.
func (m *Mutex) lockSlow(ctx context.Context) error {
	// Acquire listMu first, then re-attempt the fast acquire.
	// This serializes the "claim a free lock" race with Unlock's
	// list-walking, so we never enqueue when the lock is actually
	// free with no live waiters.
	m.listMu.Lock()

	// Fast-acquire loop: try to claim the lock (handles both state=0
	// and state with phantom waiter bits from a concurrent Unlock race).
	// The race: fast-path Unlock CAS(locked, 0) can clear the locked bit
	// between our initial CAS check and our state.Add below. We detect
	// this by checking the locked bit after state.Add, and retry here if
	// it was cleared.
	for {
		s := m.state.Load()
		if s&mutexLocked == 0 {
			// Lock bit is clear (state=0, or state has only phantom waiter
			// bits from a previous race). Try to acquire by setting the bit.
			if m.state.CompareAndSwap(s, s|mutexLocked) {
				m.listMu.Unlock()
				return nil
			}
			// CAS failed — state changed concurrently. Retry.
			continue
		}
		// Locked bit is set. Proceed to enqueue.

		// Enqueue: bump waiter count and append to tail.
		w := waiterPool.Get().(*waiter)
		w.state.Store(waiterParked)
		newState := m.state.Add(mutexWaiterUnit)
		if newState&mutexLocked == 0 {
			// Race: fast-path Unlock cleared the locked bit between our
			// check above and our state.Add. Undo the increment and retry
			// the fast-acquire loop.
			m.state.Add(^uint32(mutexWaiterUnit - 1))
			w.resetForPool()
			waiterPool.Put(w)
			continue
		}
		// Locked bit is still set after our Add. Enqueue the waiter.
		if m.tail == nil {
			m.head = w
		} else {
			m.tail.next = w
		}
		m.tail = w
		m.listMu.Unlock()

		if ctx.Done() == nil {
			// Non-cancellable (context.Background / context.TODO):
			// skip the watcher goroutine and just park.
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
}

// Unlock releases m. Panics if m is not locked on entry.
func (m *Mutex) Unlock() {
	// Fast path: locked, no waiters.
	if m.state.CompareAndSwap(mutexLocked, 0) {
		return
	}
	m.unlockSlow()
}

// unlockSlow walks the waiter list, skipping tombstoned (cancelled)
// entries, and hands off to the first live waiter via direct
// runtime_Semrelease. If the list is empty (all entries were
// cancelled, or none enqueued yet), it clears the locked bit.
//
// Must be called only when the fast-path CAS(mutexLocked, 0) failed,
// i.e. when state has a nonzero waiter count.
func (m *Mutex) unlockSlow() {
	// Reject double-unlock cleanly: if state has no locked bit,
	// fail with the canonical message rather than falling into
	// the state/list invariant check below.
	if m.state.Load()&mutexLocked == 0 {
		panic("sync: unlock of unlocked mutex")
	}
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
		m.state.Add(^uint32(mutexWaiterUnit - 1)) // atomic subtract mutexWaiterUnit

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
