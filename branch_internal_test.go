package fifomu

import (
	"context"
	"math"
	"slices"
	"testing"
	"time"
)

// This file pins down, one at a time, the branches of the lock that
// the scheduler only reaches now and then. Each test holds the inner
// mutex, or writes a counter directly, to park the goroutines involved
// at the exact point of interest.

// ctxWaiter is a LockContext call running in its own goroutine.
type ctxWaiter struct {
	cancel context.CancelFunc
	done   chan error
}

// queueLockContext starts a LockContext call and returns once it is
// queued as a waiter.
func queueLockContext(t *testing.T, m *Mutex) ctxWaiter {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	w := ctxWaiter{cancel: cancel, done: make(chan error, 1)}
	before := WaitersLen(m)
	go func() { w.done <- m.LockContext(ctx) }()
	waitFor(t, "LockContext to queue", func() bool { return WaitersLen(m) == before+1 })
	return w
}

// queueLock starts a Lock call and returns once it is queued as a
// waiter. The returned channel is closed when Lock returns.
func queueLock(t *testing.T, m *Mutex) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	before := WaitersLen(m)
	go func() {
		m.Lock()
		close(done)
	}()
	waitFor(t, "Lock to queue", func() bool { return WaitersLen(m) == before+1 })
	return done
}

func requireAcquires(t *testing.T, what string, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not acquire the mutex", what)
	}
}

func requireStillWaiting(t *testing.T, what string, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
		t.Fatalf("%s acquired the mutex out of turn", what)
	case <-time.After(20 * time.Millisecond):
	}
}

// requireAbandoned cancels w and asserts that its LockContext fails.
func requireAbandoned(t *testing.T, what string, w ctxWaiter) {
	t.Helper()
	w.cancel()
	select {
	case err := <-w.done:
		if err == nil {
			t.Fatalf("%s acquired the mutex; want it to give up", what)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not return after cancel", what)
	}
}

func requireUnlockPanics(t *testing.T, m *Mutex) {
	t.Helper()
	defer func() {
		if r := recover(); r != "sync: unlock of unlocked mutex" {
			t.Fatalf("Unlock of unlocked mutex: recovered %v", r)
		}
	}()
	m.Unlock()
}

// TestLock_InFlightHolderFindsTicketServed covers the caller that has
// taken a ticket but not yet reached the waiter list when the mutex is
// handed to it. Nobody signals it; it must notice, under the inner
// mutex, that its ticket is being served.
func TestLock_InFlightHolderFindsTicketServed(t *testing.T) {
	lockers := map[string]func(t *testing.T, m *Mutex){
		"Lock": func(_ *testing.T, m *Mutex) { m.Lock() },
		"LockContext": func(t *testing.T, m *Mutex) {
			if err := m.LockContext(context.Background()); err != nil {
				t.Error(err)
			}
		},
	}
	for name, lock := range lockers {
		t.Run(name, func(t *testing.T) {
			for range 100 {
				var m Mutex
				m.Lock() // ticket 0

				m.mu.Lock()
				acquired := make(chan struct{})
				go func() { // ticket 1, blocks on m.mu
					lock(t, &m)
					close(acquired)
				}()
				waitFor(t, "ticket 1 to be taken", func() bool { return m.next.Load() == 2 })

				unlocked := make(chan struct{})
				go func() {
					m.Unlock() // advances serving, then blocks on m.mu
					close(unlocked)
				}()
				waitFor(t, "serving to advance", func() bool { return m.serving.Load() == 1 })
				m.mu.Unlock()

				requireAcquires(t, "in-flight holder", acquired)
				<-unlocked
				if n := WaitersLen(&m); n != 0 {
					t.Fatalf("waiters = %d, want 0", n)
				}
				m.Unlock()
				requireUnlocked(t, &m)
			}
		})
	}
}

// TestHandoff_LeavesWaitersBehindInFlightHolder verifies that Unlock
// does not signal a queued waiter when an earlier ticket's holder has
// yet to reach the waiter list. The front of the list is then not the
// ticket being served, and must be left alone.
func TestHandoff_LeavesWaitersBehindInFlightHolder(t *testing.T) {
	var m Mutex
	m.Lock() // ticket 0

	inFlight := m.next.Add(1) - 1 // a Lock call that has only taken ticket 1
	later := queueLock(t, &m)     // ticket 2, queued

	m.Unlock() // serving -> 1; front of the list is ticket 2
	requireStillWaiting(t, "ticket 2", later)
	if n := WaitersLen(&m); n != 1 {
		t.Fatalf("waiters = %d, want 1", n)
	}

	m.lockSlow(inFlight) // the in-flight Lock arrives and finds itself served
	requireStillWaiting(t, "ticket 2", later)

	m.Unlock()
	requireAcquires(t, "ticket 2", later)
	m.Unlock()
	requireUnlocked(t, &m)
}

// TestHandoff_IsIdempotent verifies that a handoff which arrives late,
// after the ticket it was for has been dealt with, does nothing. This
// happens for real: an unlocker advances serving before it takes the
// inner mutex, and can be overtaken there.
func TestHandoff_IsIdempotent(t *testing.T) {
	var m Mutex

	// On an unlocked mutex.
	m.unlockSlow()
	m.unlockSlow()
	requireUnlocked(t, &m)

	// After the waiter has already been signaled: a second signal
	// would trip the waiter-pool invariant panic.
	m.Lock()
	waiter := queueLock(t, &m)
	m.Unlock()
	m.unlockSlow()
	m.unlockSlow()
	requireAcquires(t, "waiter", waiter)

	// While the mutex is held with nobody waiting.
	m.unlockSlow()
	if m.TryLock() {
		t.Fatal("TryLock succeeded on a held mutex")
	}
	m.Unlock()
	requireUnlocked(t, &m)
}

// TestHandoff_SkipsTombstoneRange verifies that several abandoned
// tickets, merged into one tombstone, are skipped in a single step.
func TestHandoff_SkipsTombstoneRange(t *testing.T) {
	var m Mutex
	m.Lock() // ticket 0

	a := queueLockContext(t, &m) // ticket 1
	b := queueLockContext(t, &m) // ticket 2
	c := queueLockContext(t, &m) // ticket 3
	d := queueLock(t, &m)        // ticket 4

	requireAbandoned(t, "b", b)
	requireAbandoned(t, "a", a)
	requireAbandoned(t, "c", c)

	m.mu.Lock()
	gotRanges, gotLive := tickets(&m.waiters)
	m.mu.Unlock()
	if !slices.Equal(gotRanges, [][2]uint64{{1, 3}, {4, 4}}) || !slices.Equal(gotLive, []bool{false, true}) {
		t.Fatalf("waiter list: ranges=%v live=%v", gotRanges, gotLive)
	}

	m.Unlock()
	requireAcquires(t, "d", d)
	if s := m.serving.Load(); s != 4 {
		t.Fatalf("serving = %d, want 4", s)
	}
	m.Unlock()
	requireUnlocked(t, &m)
}

// TestLockContext_AbandonTicketBeingServed covers a waiter that gives
// up in the window after an unlocker has advanced serving onto its
// ticket, but before that unlocker has taken the inner mutex to signal
// it. The mutex momentarily belongs to a ticket nobody wants, and the
// waiter that is giving up passes it on itself.
func TestLockContext_AbandonTicketBeingServed(t *testing.T) {
	t.Run("newest ticket: mutex ends up unlocked", func(t *testing.T) {
		var m Mutex
		m.Lock() // ticket 0

		w := queueLockContext(t, &m) // ticket 1

		// The first half of Unlock.
		if !m.serving.CompareAndSwap(0, 1) {
			t.Fatal("setup: CAS failed")
		}
		requireAbandoned(t, "waiter", w)

		// Ticket 1 was the newest, so it was un-issued: unlocked.
		requireUnlocked(t, &m)

		// The second half of Unlock finds nothing to do.
		m.unlockSlow()
		requireUnlocked(t, &m)

		if !m.TryLock() {
			t.Fatal("TryLock failed on an unlocked mutex")
		}
		m.Unlock()
		requireUnlocked(t, &m)
	})

	t.Run("later ticket queued: it is served instead", func(t *testing.T) {
		var m Mutex
		m.Lock() // ticket 0

		w := queueLockContext(t, &m) // ticket 1
		later := queueLock(t, &m)    // ticket 2

		if !m.serving.CompareAndSwap(0, 1) {
			t.Fatal("setup: CAS failed")
		}
		requireAbandoned(t, "waiter", w)

		// Giving up skipped the tombstone for ticket 1 there and then,
		// rather than leaving ticket 2 to wait for the unlocker.
		requireAcquires(t, "ticket 2", later)

		// The second half of Unlock finds nothing left to do.
		m.unlockSlow()
		if m.TryLock() {
			t.Fatal("mutex not held by ticket 2 after the late handoff")
		}
		m.Unlock()
		requireUnlocked(t, &m)
	})
}

// TestLockContext_SignalBeatsCancel covers the cancel handler finding
// that its waiter was signaled after all: ctx fired, but before the
// handler got the inner mutex, an unlocker handed it the mutex.
// LockContext must then report success, because it owns the mutex.
func TestLockContext_SignalBeatsCancel(t *testing.T) {
	for range 200 {
		var m Mutex
		m.Lock() // ticket 0

		w := queueLockContext(t, &m) // ticket 1

		m.mu.Lock()
		w.cancel()
		// Give the waiter time to wake on ctx.Done and block on m.mu.
		// If it hasn't, its select sees both arms ready and may take
		// either; the assertions below hold both ways.
		time.Sleep(200 * time.Microsecond)

		// Unlock, by hand, since we already hold m.mu.
		if !m.serving.CompareAndSwap(0, 1) {
			t.Fatal("setup: CAS failed")
		}
		m.handoff()
		m.mu.Unlock()

		if err := <-w.done; err != nil {
			t.Fatalf("LockContext = %v, want nil: it was handed the mutex", err)
		}
		if m.TryLock() {
			t.Fatal("mutex not held after LockContext returned nil")
		}
		m.Unlock()
		requireUnlocked(t, &m)
	}
}

// TestTicket_ReissuedAfterRollback verifies that a ticket number that
// was un-issued is handed out again and served normally.
func TestTicket_ReissuedAfterRollback(t *testing.T) {
	var m Mutex
	m.Lock() // ticket 0

	requireAbandoned(t, "first holder of ticket 1", queueLockContext(t, &m))
	if n := m.next.Load(); n != 1 {
		t.Fatalf("next = %d after rollback, want 1", n)
	}

	second := queueLock(t, &m)
	m.mu.Lock()
	gotRanges, gotLive := tickets(&m.waiters)
	m.mu.Unlock()
	if !slices.Equal(gotRanges, [][2]uint64{{1, 1}}) || !slices.Equal(gotLive, []bool{true}) {
		t.Fatalf("waiter list: ranges=%v live=%v, want ticket 1 reissued", gotRanges, gotLive)
	}

	m.Unlock()
	requireAcquires(t, "second holder of ticket 1", second)
	m.Unlock()
	requireUnlocked(t, &m)
}

// TestUnlock_PanicsWhenUnlocked checks the unlock-of-unlocked panic
// in the states where the two counters got to be equal by different
// routes, and that the panic leaves the mutex untouched.
func TestUnlock_PanicsWhenUnlocked(t *testing.T) {
	var m Mutex
	requireUnlockPanics(t, &m) // zero value
	requireUnlocked(t, &m)

	m.Lock()
	m.Unlock()                 //nolint:staticcheck // a lock/unlock cycle is the setup
	requireUnlockPanics(t, &m) // after a lock/unlock cycle
	requireUnlocked(t, &m)

	m.Lock()
	requireAbandoned(t, "waiter", queueLockContext(t, &m))
	m.Unlock()
	requireUnlockPanics(t, &m) // after a ticket was rolled back
	requireUnlocked(t, &m)

	if !m.TryLock() {
		t.Fatal("mutex unusable after recovered panics")
	}
	m.Unlock()
	requireUnlocked(t, &m)
}

// TestTicket_Wraparound runs the mutex across the point where the
// ticket counter overflows. Nobody will issue 2^64 tickets, but the
// arithmetic is written to survive it, and this keeps it that way.
func TestTicket_Wraparound(t *testing.T) {
	const start = math.MaxUint64 - 2

	var m Mutex
	m.next.Store(start)
	m.serving.Store(start)

	m.Lock() // ticket MaxUint64-2

	a := queueLock(t, &m)        // MaxUint64-1
	b := queueLockContext(t, &m) // MaxUint64
	c := queueLockContext(t, &m) // 0
	d := queueLock(t, &m)        // 1
	e := queueLockContext(t, &m) // 2: abandoned as the newest, so rolled back

	requireAbandoned(t, "e", e)
	if n := m.next.Load(); n != 2 {
		t.Fatalf("next = %d after rollback across the wrap, want 2", n)
	}

	// b and c straddle the wrap; their tombstones must merge.
	requireAbandoned(t, "c", c)
	requireAbandoned(t, "b", b)
	m.mu.Lock()
	gotRanges, gotLive := tickets(&m.waiters)
	m.mu.Unlock()
	wantRanges := [][2]uint64{{math.MaxUint64 - 1, math.MaxUint64 - 1}, {math.MaxUint64, 0}, {1, 1}}
	if !slices.Equal(gotRanges, wantRanges) || !slices.Equal(gotLive, []bool{true, false, true}) {
		t.Fatalf("waiter list: ranges=%v live=%v", gotRanges, gotLive)
	}

	m.Unlock()
	requireAcquires(t, "a", a)
	requireStillWaiting(t, "d", d)

	m.Unlock() // skips the tombstone spanning the wrap
	requireAcquires(t, "d", d)
	if s := m.serving.Load(); s != 1 {
		t.Fatalf("serving = %d, want 1", s)
	}

	m.Unlock()
	requireUnlocked(t, &m)
	if !m.TryLock() {
		t.Fatal("TryLock failed after the wrap")
	}
	m.Unlock()
}

// TestList_InsertOrdersAcrossWraparound verifies that insert keeps
// ticket order when the tickets straddle the counter's overflow.
func TestList_InsertOrdersAcrossWraparound(t *testing.T) {
	var l list
	for _, ticket := range []uint64{1, math.MaxUint64, 0, math.MaxUint64 - 1} {
		l.insert(ticket, newWaiter())
	}
	got, _ := tickets(&l)
	want := [][2]uint64{
		{math.MaxUint64 - 1, math.MaxUint64 - 1},
		{math.MaxUint64, math.MaxUint64},
		{0, 0},
		{1, 1},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("tickets = %v, want %v", got, want)
	}
}

// TestList_EmptyAccessors covers front and back on a list that has
// never been used, and on one that has been emptied.
func TestList_EmptyAccessors(t *testing.T) {
	var l list
	if l.front() != nil || l.back() != nil {
		t.Fatal("zero-value list should have no front or back")
	}
	e := l.insert(7, newWaiter())
	if l.front() != e || l.back() != e {
		t.Fatal("single element should be both front and back")
	}
	l.remove(e)
	if l.front() != nil || l.back() != nil {
		t.Fatal("emptied list should have no front or back")
	}
}

// TestNoCopy exercises the marker methods that exist only so that
// `go vet -copylocks` flags a copied Mutex. They must be callable
// no-ops.
func TestNoCopy(_ *testing.T) {
	var nc noCopy
	nc.Lock()
	nc.Unlock() //nolint:staticcheck // calling the no-ops is the test
}
