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
	// Fast path: same as Lock — only attempt if no waiters and unlocked.
	if m.state.CompareAndSwap(0, mutexLocked) {
		return nil
	}
	return m.lockSlow(ctx)
}

// lockSlow is the Lock slow path. Both Lock and LockContext call it;
// Lock passes context.Background() (whose Done() is nil, signalling
// the non-cancellable path), LockContext passes the caller's ctx.
// Returns context.Cause(ctx) only when ctx becomes cancelled before
// the lock is handed off.
func (m *Mutex) lockSlow(ctx context.Context) error {
	// Try fast-acquire first WITHOUT listMu. Under high parallelism
	// (slack workloads) this matters: listMu becomes a serialization
	// point when every contended caller takes it just to fail and
	// fall through to enqueue. We only need listMu when we actually
	// touch the waiter list.
	for {
		s := m.state.Load()
		if s&mutexLocked == 0 {
			// Lock bit is clear (state=0 or has only phantom waiter
			// bits). Try to acquire by setting the bit.
			if m.state.CompareAndSwap(s, s|mutexLocked) {
				return nil
			}
			// CAS failed, retry.
			continue
		}
		// Lock is held; we must enqueue. Drop into the listMu-protected
		// enqueue path.
		break
	}

	// Enqueue path: take listMu. Under it, re-check state once more —
	// the lock may have been released since our last load, in which case
	// we can fast-acquire directly (still under listMu to serialize with
	// any concurrent unlockSlow).
	m.listMu.Lock()
	for {
		s := m.state.Load()
		if s&mutexLocked == 0 {
			if m.state.CompareAndSwap(s, s|mutexLocked) {
				m.listMu.Unlock()
				return nil
			}
			continue
		}
		// Enqueue: bump waiter count and append to tail.
		//
		// Pool only Lock waiters. LockContext waiters are allocated
		// fresh — their sema's lifetime is tied to that allocation,
		// which prevents a stray watcher Semrelease (firing after the
		// CAS race has resolved against it) from leaking into a future
		// iteration's reuse of the same sema address.
		var w *waiter
		fromPool := ctx.Done() == nil
		if fromPool {
			w = waiterPool.Get().(*waiter)
		} else {
			w = &waiter{}
		}
		w.state.Store(waiterParked)
		newState := m.state.Add(mutexWaiterUnit)
		if newState&mutexLocked == 0 {
			// Race: fast-path Unlock cleared the locked bit between our
			// state.Load above and our state.Add. Undo and retry under
			// listMu.
			m.state.Add(^uint32(mutexWaiterUnit - 1))
			if fromPool {
				w.resetForPool()
				waiterPool.Put(w)
			}
			continue
		}
		// Enqueue.
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
			// fromPool is always true on this branch; recycle.
			w.resetForPool()
			waiterPool.Put(w)
			return nil
		}

		// LockContext: watch for cancellation while parked.
		//
		// `cancelled` is written by the watcher (only on a successful
		// CAS to waiterCancelled) and read by this goroutine after
		// Semacquire returns. It is intentionally a separate variable
		// from w.state because in the cancelled path, w stays in the
		// FIFO list and is exclusively owned by Unlock from the moment
		// the watcher CAS succeeds — Unlock may recycle w at any time
		// after that, so we must not read or write w here.
		var cancelled atomic.Bool
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				if w.state.CompareAndSwap(waiterParked, waiterCancelled) {
					// Tombstone established before Unlock could claim
					// us. Signal G via the heap variable (NOT w),
					// then wake G.
					cancelled.Store(true)
					runtime_Semrelease(&w.sema, false, 0)
				}
				// If the CAS failed, Unlock won the race and will
				// Semrelease us; do nothing here.
			case <-done:
			}
		}()
		runtime_Semacquire(&w.sema)
		close(done)
		if cancelled.Load() {
			// Watcher-wins path: w is still in the list and is owned
			// by Unlock (it will pop and recycle w when it finds the
			// tombstone). DO NOT touch w from here.
			return context.Cause(ctx)
		}
		// Unlock-wins path: the unlocker popped w from the list and
		// CAS'd state to waiterWon; we now exclusively own w.
		//
		// LockContext waiters are not pooled (see allocation above),
		// so just let GC reclaim w. We still need a race-detector-
		// visible HB edge with the unlocker's read of w.next at the
		// list-pop, because the linkname'd sema's HB is invisible to
		// -race. Briefly taking listMu provides that edge.
		m.listMu.Lock()
		m.listMu.Unlock() //nolint:staticcheck // sync edge for -race
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
		// w was cancelled. LockContext waiters are not pooled, so
		// we simply drop w on the floor here — GC will reclaim it.
		// The watcher's Semrelease has already woken the LockContext
		// goroutine, which returned without touching w.
		// Loop to find the next live entry.
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
