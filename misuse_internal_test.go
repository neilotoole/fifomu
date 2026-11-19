package fifomu

import (
	"context"
	"testing"
)

// This file covers misuse: an Unlock with no matching Lock, and a nil
// Context. No mutex can keep its promises to a caller that does
// either, and fifomu does not try to. Like a sync.Mutex, it lets a
// stray Unlock release a mutex that someone else holds. What these
// tests pin down is that the damage stops there: nobody is left
// waiting for a signal that will never come, and the mutex goes on
// working for everyone else.
//
// TestModel_Misuse checks the same thing over every interleaving of a
// model of the code. These tests check the code itself, on the
// interleavings that used to go wrong.

// requireUsable asserts that m is unlocked and consistent, and that a
// waiter queued behind a holder is still handed the mutex.
func requireUsable(t *testing.T, m *Mutex) {
	t.Helper()
	requireUnlocked(t, m)
	if m.waiters.len != 0 {
		t.Fatalf("waiter list has %d elements at rest", m.waiters.len)
	}

	m.Lock()
	w := queueLockContext(t, m)
	m.Unlock()
	if err := <-w.done; err != nil {
		t.Fatalf("a waiter queued after the misuse was not served: %v", err)
	}
	m.Unlock()
	requireUnlocked(t, m)
}

// startUnlock runs m.Unlock in a goroutine and returns once it has
// advanced serving to want. The caller holds m.mu, so an Unlock that
// needs to hand off is parked there, between its two halves.
func startUnlock(t *testing.T, m *Mutex, want uint64) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		m.Unlock()
	}()
	waitFor(t, "Unlock to advance serving", func() bool { return m.serving.Load() == want })
	return done
}

// TestMisuse_RacingUnlocksDoNotStrandWaiters is the case that used to
// break the mutex for good. One Lock, two Unlocks, two waiters: the
// second Unlock advances serving again before the first has handed
// off. Both then looked at the front of the list, saw a ticket that
// was no longer the one being served, and left. The waiters were never
// signaled, and the stale front blocked every later ticket.
func TestMisuse_RacingUnlocksDoNotStrandWaiters(t *testing.T) {
	var m Mutex
	m.Lock() // ticket 0

	b := queueLock(t, &m) // ticket 1
	c := queueLock(t, &m) // ticket 2

	m.mu.Lock()
	u1 := startUnlock(t, &m, 1)
	u2 := startUnlock(t, &m, 2) // the stray one
	m.mu.Unlock()
	<-u1
	<-u2

	// The stray Unlock released ticket 1's hold before it began, so
	// both waiters are let through, as with a sync.Mutex.
	requireAcquires(t, "the waiter serving had passed", b)
	requireAcquires(t, "the waiter being served", c)

	// Two holders, three tickets, and one Unlock already spent: the
	// first to unlock succeeds, the second finds the mutex unlocked.
	m.Unlock()
	requireUnlockPanics(t, &m)
	requireUsable(t, &m)
}

// TestMisuse_StrayUnlockReturnsBeforeHandoff is the same race with a
// single waiter, where the stray Unlock sees no later ticket and
// returns without calling handoff at all. The first unlocker is then
// the only one who can wake the waiter.
func TestMisuse_StrayUnlockReturnsBeforeHandoff(t *testing.T) {
	var m Mutex
	m.Lock() // ticket 0

	b := queueLock(t, &m) // ticket 1

	m.mu.Lock()
	u1 := startUnlock(t, &m, 1)
	u2 := startUnlock(t, &m, 2)
	<-u2 // it had nobody to tell, so it never needed m.mu
	m.mu.Unlock()
	<-u1

	requireAcquires(t, "the waiter serving had passed", b)
	requireUnlockPanics(t, &m) // the waiter's own Unlock: already spent
	requireUsable(t, &m)
}

// TestMisuse_HandoffDropsTombstoneBehindServing covers a stray Unlock
// that moves serving past a tombstone before the first unlocker has
// skipped it.
func TestMisuse_HandoffDropsTombstoneBehindServing(t *testing.T) {
	var m Mutex
	m.Lock() // ticket 0

	a := queueLockContext(t, &m) // ticket 1
	c := queueLock(t, &m)        // ticket 2
	requireAbandoned(t, "ticket 1", a)

	m.mu.Lock()
	u1 := startUnlock(t, &m, 1) // onto the tombstone
	u2 := startUnlock(t, &m, 2) // stray: past it
	m.mu.Unlock()
	<-u1
	<-u2

	requireAcquires(t, "the waiter behind the tombstone", c)
	m.Unlock()
	requireUsable(t, &m)
}

// TestMisuse_AbandonBehindServing covers a LockContext caller that
// gives up after a stray Unlock has already moved serving past its
// ticket and gone. Nobody else is coming to clear its tombstone, and
// rolling next back to it would leave next behind serving.
func TestMisuse_AbandonBehindServing(t *testing.T) {
	var m Mutex
	m.Lock() // ticket 0

	a := queueLockContext(t, &m) // ticket 1
	b := queueLock(t, &m)        // ticket 2

	// Two Unlocks have advanced serving, and neither has handed off.
	m.serving.Store(2)

	requireAbandoned(t, "the ticket serving had passed", a)
	// Giving up ran handoff, which found ticket 2 due.
	requireAcquires(t, "the waiter being served", b)
	m.Unlock()
	requireUsable(t, &m)
}

// TestMisuse_RollbackBehindServingIsRepaired covers the newest ticket
// being abandoned, and so rolled back, after a stray Unlock has moved
// serving past it. That leaves next behind serving, which handoff
// must put right, or the next Lock would be issued a ticket that has
// already been passed and its Unlock would panic.
func TestMisuse_RollbackBehindServingIsRepaired(t *testing.T) {
	var m Mutex
	m.Lock() // ticket 0

	a := queueLockContext(t, &m) // ticket 1, the newest

	m.serving.Store(2) // two Unlocks, as above
	requireAbandoned(t, "the ticket serving had passed", a)

	if next, serving := m.next.Load(), m.serving.Load(); next != 2 || serving != 2 {
		t.Fatalf("next=%d serving=%d, want both 2", next, serving)
	}
	requireUsable(t, &m)
}

// TestMisuse_UnlockPanicsWhenServingIsAheadOfNext covers Unlock's
// check on a mutex whose serving has got ahead of next. That state
// only exists for a moment, between a rollback and the repair; an
// Unlock that sees it must treat the mutex as unlocked rather than
// advance serving further.
func TestMisuse_UnlockPanicsWhenServingIsAheadOfNext(t *testing.T) {
	var m Mutex
	m.serving.Store(1)
	requireUnlockPanics(t, &m)
	if got := m.serving.Load(); got != 1 {
		t.Fatalf("serving = %d after the panic, want 1: Unlock must not have advanced it", got)
	}
}

// TestLockContext_NilContextPanicsBeforeTakingATicket verifies that a
// nil Context is rejected up front. If the panic came from ctx.Done(),
// after the caller had taken a ticket, that ticket would never be
// released and every later caller would wait behind it.
func TestLockContext_NilContextPanicsBeforeTakingATicket(t *testing.T) {
	lockNil := func(m *Mutex) (recovered any) {
		defer func() { recovered = recover() }()
		var ctx context.Context
		_ = m.LockContext(ctx)
		return nil
	}

	t.Run("unlocked", func(t *testing.T) {
		var m Mutex
		if r := lockNil(&m); r == nil {
			t.Fatal("LockContext(nil) did not panic")
		}
		requireUsable(t, &m)
	})

	t.Run("held", func(t *testing.T) {
		var m Mutex
		m.Lock()
		if r := lockNil(&m); r == nil {
			t.Fatal("LockContext(nil) did not panic")
		}
		if got := m.next.Load(); got != 1 {
			t.Fatalf("next = %d, want 1: the nil-Context call must not have taken a ticket", got)
		}
		m.Unlock()
		requireUsable(t, &m)
	})
}
