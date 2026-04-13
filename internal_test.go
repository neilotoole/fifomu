package fifomu

import (
	"strings"
	"testing"
)

// TestList_RemoveIsIdempotent locks in the hardening added to
// list.remove: calling remove on an element that is not currently in
// the list must be a safe no-op. Without the early-return guard, a
// double-remove would re-Put the element into elementPool a second
// time, which could cause future Gets to yield the same element
// pointer to two concurrent callers.
//
// This test verifies the observable symptom (list state stays
// consistent); TestList_RemoveDoesNotDoublePool below catches the
// specific pool-poisoning regression.
func TestList_RemoveIsIdempotent(t *testing.T) {
	var l list[waiter]
	w1 := waiter(make(chan struct{}, 1))
	w2 := waiter(make(chan struct{}, 1))
	w3 := waiter(make(chan struct{}, 1))

	e1 := l.pushBackElem(w1)
	e2 := l.pushBackElem(w2)
	e3 := l.pushBackElem(w3)

	if got, want := l.len, 3; got != want {
		t.Fatalf("len after 3 pushes = %d, want %d", got, want)
	}

	// Remove the middle element.
	l.remove(e2)
	if l.len != 2 {
		t.Fatalf("len after remove(e2) = %d, want 2", l.len)
	}
	if e2.list != nil {
		t.Fatal("e2.list was not cleared by remove")
	}
	if l.front() != e1 {
		t.Fatal("front should still be e1 after removing middle")
	}

	// Double-remove must be a no-op (does not decrement len, does not
	// mutate the list).
	l.remove(e2)
	if l.len != 2 {
		t.Fatalf("len after double-remove(e2) = %d, want 2", l.len)
	}

	// Remove of an element that was never inserted is also a no-op.
	orphan := &element[waiter]{}
	l.remove(orphan)
	if l.len != 2 {
		t.Fatalf("len after remove(orphan) = %d, want 2", l.len)
	}

	// Clean up the list.
	l.remove(e1)
	l.remove(e3)
	if l.len != 0 {
		t.Fatalf("len after removing all = %d, want 0", l.len)
	}
	if l.front() != nil {
		t.Fatal("front should be nil after empty")
	}
}

// TestList_RemoveDoesNotDoublePool directly verifies that a
// double-remove does not put the same element into elementPool
// twice. We prime the pool with many sentinel elements, then
// double-remove, then drain; the double-removed element must
// appear at most once.
//
// sync.Pool is not strictly LIFO and may drop items on GC, so the
// "seen == 0" outcome is acceptable (the pool didn't yield our
// element at all). The regression signal is seen > 1.
func TestList_RemoveDoesNotDoublePool(t *testing.T) {
	var l list[waiter]
	w := waiter(make(chan struct{}, 1))
	e := l.pushBackElem(w)

	// First remove: element is legitimately put back into the pool.
	l.remove(e)

	// Prime the pool with many distinct sentinels, to reduce the
	// chance that a later Get happens to miss our element.
	const N = 100
	for range N {
		elementPool.Put(&element[waiter]{})
	}

	// Double-remove: with the hardening, this is a no-op; without it,
	// e gets Put a second time.
	l.remove(e)

	// Drain the pool aggressively and count occurrences of e.
	seen := 0
	for range N + 50 {
		got := elementPool.Get().(*element[waiter])
		if got == e {
			seen++
		}
	}

	if seen > 1 {
		t.Fatalf("element %p appeared in pool %d times after double-remove; expected ≤ 1", e, seen)
	}
}

// TestNotifyWaiters_PanicsOnViolatedInvariant verifies the
// select-with-default safety net in notifyWaiters: if a waiter
// arrives with a non-empty buffer (meaning the pool invariant was
// violated), notifyWaiters panics with a diagnostic message
// instead of silently reintroducing the pre-fix deadlock.
//
// We simulate the invariant violation by manually queuing a waiter
// whose buffer is already full, then triggering Unlock →
// notifyWaiters.
func TestNotifyWaiters_PanicsOnViolatedInvariant(t *testing.T) {
	var mu Mutex
	mu.Lock() // cur = 1, no waiters

	// Manually push a waiter with a pre-filled buffer. In normal
	// operation the pool invariant guarantees this never happens;
	// we're violating it deliberately.
	mu.mu.Lock()
	w := waiter(make(chan struct{}, 1))
	w <- struct{}{} // buffer now full
	mu.waiters.pushBack(w)
	mu.mu.Unlock()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic from notifyWaiters when invariant is violated")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value not a string: %T %v", r, r)
		}
		if !strings.Contains(msg, "invariant violated") {
			t.Fatalf("panic message = %q, want one containing 'invariant violated'", msg)
		}
	}()

	// Trigger notifyWaiters by unlocking.
	mu.Unlock()
}
