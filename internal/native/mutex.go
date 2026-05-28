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
	mutexLocked     = 1
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
