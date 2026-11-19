package fifomu

import (
	"context"
	"math/rand/v2"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor polls cond until it reports true, failing the test after a
// generous timeout.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		runtime.Gosched()
	}
}

// requireUnlocked asserts that m is unlocked with nothing left behind:
// no ticket outstanding and no waiters or tombstones queued.
func requireUnlocked(t *testing.T, m *Mutex) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, n := m.serving.Load(), m.next.Load(); s != n {
		t.Fatalf("mutex not unlocked: serving=%d next=%d", s, n)
	}
	if m.waiters.len != 0 {
		t.Fatalf("waiter list not empty: len=%d", m.waiters.len)
	}
}

// tickets returns the ticket ranges in l, front to back, as [lo, hi]
// pairs; live reports which of them are live waiters.
func tickets(l *list) (ranges [][2]uint64, live []bool) {
	for e := l.front(); e != nil && e != &l.root; e = e.next {
		ranges = append(ranges, [2]uint64{e.lo, e.hi})
		live = append(live, !e.abandoned())
	}
	return ranges, live
}

func newWaiter() waiter { return make(chan struct{}, 1) }

// TestList_InsertKeepsTicketOrder verifies that elements end up sorted
// by ticket whatever order they are inserted in. This is what lets a
// goroutine that was overtaken on the inner mutex still be served in
// ticket order.
func TestList_InsertKeepsTicketOrder(t *testing.T) {
	var l list
	for _, ticket := range []uint64{5, 2, 9, 1, 7, 3} {
		l.insert(ticket, newWaiter())
	}

	got, _ := tickets(&l)
	want := [][2]uint64{{1, 1}, {2, 2}, {3, 3}, {5, 5}, {7, 7}, {9, 9}}
	if !slices.Equal(got, want) {
		t.Fatalf("tickets = %v, want %v", got, want)
	}
	if l.back().lo != 9 {
		t.Fatalf("back = %d, want 9", l.back().lo)
	}
}

// TestList_AbandonMergesAdjacentTombstones verifies that tombstones
// for consecutive tickets collapse into a single range, and that
// tombstones separated by a live or missing ticket do not.
func TestList_AbandonMergesAdjacentTombstones(t *testing.T) {
	var l list
	elems := map[uint64]*element{}
	for _, ticket := range []uint64{1, 2, 3, 4, 6, 7} { // 5 is "in flight"
		elems[ticket] = l.insert(ticket, newWaiter())
	}

	check := func(step string, wantRanges [][2]uint64, wantLive []bool) {
		t.Helper()
		gotRanges, gotLive := tickets(&l)
		if !slices.Equal(gotRanges, wantRanges) || !slices.Equal(gotLive, wantLive) {
			t.Fatalf("%s: ranges=%v live=%v, want ranges=%v live=%v",
				step, gotRanges, gotLive, wantRanges, wantLive)
		}
	}

	// No neighbor is a tombstone: stands alone.
	if got := l.abandon(elems[2]); got != elems[2] {
		t.Fatal("abandon(2) should return the element itself")
	}
	check("abandon 2",
		[][2]uint64{{1, 1}, {2, 2}, {3, 3}, {4, 4}, {6, 6}, {7, 7}},
		[]bool{true, false, true, true, true, true})

	// Not adjacent to tombstone 2 (3 is live): stands alone.
	l.abandon(elems[4])
	check("abandon 4",
		[][2]uint64{{1, 1}, {2, 2}, {3, 3}, {4, 4}, {6, 6}, {7, 7}},
		[]bool{true, false, true, false, true, true})

	// Adjacent on both sides: all three merge into the predecessor.
	if got := l.abandon(elems[3]); got != elems[2] {
		t.Fatal("abandon(3) should merge into the tombstone for 2")
	}
	check("abandon 3",
		[][2]uint64{{1, 1}, {2, 4}, {6, 6}, {7, 7}},
		[]bool{true, false, true, true})

	// Ticket 5 is missing from the list, so 6 must not merge with 2-4:
	// 5's holder may yet arrive.
	l.abandon(elems[6])
	check("abandon 6",
		[][2]uint64{{1, 1}, {2, 4}, {6, 6}, {7, 7}},
		[]bool{true, false, false, true})

	// Merge with a successor only.
	l.abandon(elems[7])
	check("abandon 7",
		[][2]uint64{{1, 1}, {2, 4}, {6, 7}},
		[]bool{true, false, false})
}

// TestTicket_NoBargeWhileBlockedOnInner is the regression test for
// the unfairness the ticket design exists to remove.
//
// G2 calls Lock and is left parked on the inner sync.Mutex, as it
// would be if it collided with another goroutine's critical section.
// G1 then unlocks and immediately relocks. Before tickets, G1 would
// re-acquire the mutex hundreds of times before G2 woke up and
// reached the waiter queue. Now G2 took its ticket before blocking,
// so G1's relock must wait behind it.
func TestTicket_NoBargeWhileBlockedOnInner(t *testing.T) {
	for range 200 {
		var m Mutex
		m.Lock() // G1 (this goroutine) holds ticket 0.

		m.mu.Lock() // stand in for some other goroutine's critical section
		var g2Acquired atomic.Bool
		done := make(chan struct{})
		go func() {
			defer close(done)
			m.Lock() // G2: takes ticket 1, then blocks on m.mu
			g2Acquired.Store(true)
			m.Unlock()
		}()
		waitFor(t, "G2 to take its ticket", func() bool { return m.next.Load() == 2 })
		m.mu.Unlock()

		m.Unlock()
		m.Lock() // ticket 2: must be served after G2's ticket 1
		if !g2Acquired.Load() {
			t.Fatal("G1 re-acquired the mutex ahead of G2, which called Lock first")
		}
		m.Unlock()
		<-done
		requireUnlocked(t, &m)
	}
}

// TestTicket_OrderFixedByTicketNotQueueArrival starts goroutines one
// at a time, each taking its ticket and then blocking on the inner
// mutex. When the inner mutex is released they race to reach the
// waiter list, in whatever order sync.Mutex wakes them. They must
// still acquire in ticket order.
func TestTicket_OrderFixedByTicketNotQueueArrival(t *testing.T) {
	const N = 16
	for range 50 {
		var m Mutex
		m.Lock()

		m.mu.Lock()
		order := make(chan int, N)
		var wg sync.WaitGroup
		for i := range N {
			wg.Go(func() {
				m.Lock()
				order <- i
				m.Unlock()
			})
			waitFor(t, "ticket to be taken", func() bool { return m.next.Load() == uint64(i+2) })
		}
		m.mu.Unlock()

		m.Unlock()
		wg.Wait()
		close(order)

		var got []int
		for v := range order {
			got = append(got, v)
		}
		want := make([]int, N)
		for i := range want {
			want[i] = i
		}
		if !slices.Equal(got, want) {
			t.Fatalf("acquired out of ticket order: got %v", got)
		}
		requireUnlocked(t, &m)
	}
}

// TestTryLock_FailsWhileTicketInFlight verifies that TryLock cannot
// take an unlocked mutex ahead of a caller that has taken a ticket but
// not yet noticed that it owns the mutex.
func TestTryLock_FailsWhileTicketInFlight(t *testing.T) {
	var m Mutex

	// A Lock call that has taken its ticket and gone no further.
	ticket := m.next.Add(1) - 1

	if m.TryLock() {
		t.Fatal("TryLock succeeded ahead of an in-flight Lock")
	}

	// The in-flight Lock resumes: its ticket is being served.
	if m.serving.Load() != ticket {
		t.Fatalf("serving = %d, want %d", m.serving.Load(), ticket)
	}
	m.Unlock()

	if !m.TryLock() {
		t.Fatal("TryLock failed on an unlocked mutex")
	}
	m.Unlock()
	requireUnlocked(t, &m)
}

// TestLockContext_CancelNewestTicketRollsBack verifies that a caller
// repeatedly timing out against a held mutex leaves nothing behind:
// its abandoned ticket is the newest, so it is un-issued rather than
// left as a tombstone.
func TestLockContext_CancelNewestTicketRollsBack(t *testing.T) {
	var m Mutex
	m.Lock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for i := range 1000 {
		if err := m.LockContext(ctx); err == nil {
			t.Fatal("LockContext succeeded on a held mutex")
		}
		if n, l := m.next.Load(), m.waiters.len; n != 1 || l != 0 {
			t.Fatalf("iteration %d: next=%d waiters.len=%d, want 1 and 0", i, n, l)
		}
	}

	m.Unlock()
	requireUnlocked(t, &m)
}

// TestLockContext_TombstonesStayBounded drives the pattern that makes
// rollback impossible: two callers whose attempts overlap, so that
// whenever one gives up, the other holds a newer ticket. The waiter
// list must not grow with the number of abandoned attempts.
func TestLockContext_TombstonesStayBounded(t *testing.T) {
	var m Mutex
	m.Lock()

	// queue starts a LockContext call and returns once it is queued.
	queue := func() (cancel context.CancelFunc, done <-chan error) {
		ctx, cancel := context.WithCancel(context.Background())
		ch := make(chan error, 1)
		before := WaitersLen(&m)
		go func() { ch <- m.LockContext(ctx) }()
		waitFor(t, "LockContext to queue", func() bool { return WaitersLen(&m) == before+1 })
		return cancel, ch
	}

	giveUp := func(cancel context.CancelFunc, done <-chan error) {
		t.Helper()
		cancel()
		if err := <-done; err == nil {
			t.Fatal("LockContext succeeded on a held mutex")
		}
	}

	cancelA, doneA := queue()
	for range 1000 {
		cancelB, doneB := queue()
		giveUp(cancelA, doneA) // B is newer: A's ticket becomes a tombstone
		cancelA, doneA = queue()
		giveUp(cancelB, doneB) // A is newer: B's ticket merges with A's old one

		m.mu.Lock()
		l := m.waiters.len
		m.mu.Unlock()
		if l > 2 {
			t.Fatalf("waiter list grew to %d elements", l)
		}
	}

	// The survivor is served once the tombstones ahead of it are skipped.
	m.Unlock()
	if err := <-doneA; err != nil {
		t.Fatalf("surviving LockContext failed: %v", err)
	}
	cancelA()
	m.Unlock()
	requireUnlocked(t, &m)
}

// TestLockContext_AbandonedFrontIsSkipped verifies that Unlock skips
// an abandoned ticket at the front of the line and hands the mutex to
// the next live waiter, and that abandoning every waiter leaves the
// mutex unlocked once the holder releases it.
func TestLockContext_AbandonedFrontIsSkipped(t *testing.T) {
	var m Mutex
	m.Lock()

	ctxA, cancelA := context.WithCancel(context.Background())
	doneA := make(chan error, 1)
	go func() { doneA <- m.LockContext(ctxA) }()
	waitFor(t, "A to queue", func() bool { return WaitersLen(&m) == 1 })

	doneB := make(chan struct{})
	go func() {
		m.Lock()
		close(doneB)
	}()
	waitFor(t, "B to queue", func() bool { return WaitersLen(&m) == 2 })

	cancelA()
	if err := <-doneA; err == nil {
		t.Fatal("A acquired a held mutex")
	}

	m.Unlock()
	select {
	case <-doneB:
	case <-time.After(5 * time.Second):
		t.Fatal("B was not served after the abandoned ticket ahead of it")
	}
	m.Unlock()
	requireUnlocked(t, &m)
}

// TestLock_SurvivesStrayUnlock covers the misuse that served guards
// against: an Unlock with no matching Lock that releases the mutex
// while a Lock call holding the next ticket is still on its way to
// the waiter list. That Lock must return rather than queue a waiter
// nobody will ever signal.
func TestLock_SurvivesStrayUnlock(t *testing.T) {
	var m Mutex
	m.Lock() // ticket 0

	ticket := m.next.Add(1) - 1 // an in-flight Lock holding ticket 1
	m.Unlock()                  // legitimate: serving -> 1
	m.Unlock()                  // stray: serving -> 2, past the in-flight ticket

	done := make(chan struct{})
	go func() {
		m.lockSlow(ticket)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Lock deadlocked after a stray Unlock passed its ticket")
	}
	requireUnlocked(t, &m)
}

// TestTicket_StressServedInTicketOrder hammers the mutex with Lock,
// TryLock and LockContext calls, many of which time out, and checks
// the property the package exists for: every acquisition is by the
// ticket being served, and those tickets only ever increase.
//
// The lock paths are inlined here so that each goroutine knows its
// own ticket. guarded is deliberately a plain int: the race detector
// turns any breach of mutual exclusion into a failure.
func TestTicket_StressServedInTicketOrder(t *testing.T) {
	workers, dur := 32, 2*time.Second
	if testing.Short() {
		dur = 200 * time.Millisecond
	}

	var m Mutex
	var guarded int
	last := int64(-1)
	var acquired, abandoned atomic.Int64

	// critical runs with the mutex held by ticket.
	critical := func(ticket uint64) {
		if s := m.serving.Load(); s != ticket {
			t.Errorf("holder has ticket %d but serving is %d", ticket, s)
		}
		if int64(ticket) <= last {
			t.Errorf("ticket %d acquired after ticket %d", ticket, last)
		}
		last = int64(ticket)
		guarded++
		acquired.Add(1)
	}

	deadline := time.Now().Add(dur)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for time.Now().Before(deadline) {
				switch rand.IntN(4) {
				case 0: // Lock
					ticket := m.next.Add(1) - 1
					if m.serving.Load() != ticket {
						m.lockSlow(ticket)
					}
					critical(ticket)
					m.Unlock()

				case 1: // TryLock
					s := m.serving.Load()
					if m.next.CompareAndSwap(s, s+1) {
						critical(s)
						m.Unlock()
					}

				default: // LockContext, usually timing out while queued
					ctx, cancel := context.WithTimeout(context.Background(),
						time.Duration(rand.IntN(20_000))*time.Nanosecond)
					ticket := m.next.Add(1) - 1
					var err error
					if m.serving.Load() != ticket {
						err = m.lockContextSlow(ctx, ticket)
					}
					cancel()
					if err != nil {
						abandoned.Add(1)
						continue
					}
					critical(ticket)
					m.Unlock()
				}
			}
		})
	}
	wg.Wait()

	requireUnlocked(t, &m)
	if int64(guarded) != acquired.Load() {
		t.Fatalf("guarded=%d, acquired=%d", guarded, acquired.Load())
	}
	if acquired.Load() == 0 || abandoned.Load() == 0 {
		t.Fatalf("stress did not exercise both outcomes: acquired=%d abandoned=%d",
			acquired.Load(), abandoned.Load())
	}
	t.Logf("acquired=%d abandoned=%d", acquired.Load(), abandoned.Load())
}
