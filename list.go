package fifomu

import (
	"sync"
)

var elementPool = sync.Pool{New: func() any { return new(element) }}

// list is a doubly-linked list of elements, kept sorted by ticket.
// It is not thread-safe; callers hold Mutex.mu while manipulating
// the list.
//
// Each element is either a live waiter (value != nil, lo == hi == the
// waiter's ticket) or a tombstone (value == nil) covering the
// inclusive range [lo, hi] of abandoned tickets. Element ranges never
// overlap, and two tombstones are never adjacent in ticket space:
// abandon merges them. That bounds the list length by roughly twice
// the number of live waiters, however many tickets are abandoned.
type list struct {
	root element
	len  int
}

// lazyInit initializes the root sentinel the first time the list is
// used. Sharing a zero-value list is allowed; the first insert triggers
// the init, subsequent inserts are no-ops on this path.
func (l *list) lazyInit() {
	if l.root.next == nil {
		l.root.next = &l.root
		l.root.prev = &l.root
		l.len = 0
	}
}

// front returns the first element of list l or nil.
func (l *list) front() *element {
	if l.len == 0 {
		return nil
	}
	return l.root.next
}

// back returns the last element of list l or nil.
func (l *list) back() *element {
	if l.len == 0 {
		return nil
	}
	return l.root.prev
}

// insert adds a live waiter element for ticket, keeping l sorted by
// ticket, and returns the new element.
//
// Tickets usually reach the list in increasing order, so the scan
// from the back normally stops at once. It only walks further when
// the caller was overtaken on Mutex.mu by holders of later tickets,
// and then only past those overtakers and any tombstones they left.
func (l *list) insert(ticket uint64, v waiter) *element {
	l.lazyInit()

	// The comparison is on the signed difference, so that ordering
	// survives the ticket counter wrapping around.
	at := l.root.prev
	for at != &l.root && int64(at.lo-ticket) > 0 { //nolint:gosec // wraparound-safe ordering
		at = at.prev
	}

	e := elementPool.Get().(*element) //nolint:errcheck
	e.lo, e.hi = ticket, ticket
	e.value = v
	e.prev = at
	e.next = at.next
	e.prev.next = e
	e.next.prev = e
	e.list = l
	l.len++
	return e
}

// abandon converts the live waiter element e into a tombstone for its
// ticket, merging it with any tombstone that is adjacent in ticket
// space. It returns the tombstone that now covers e's ticket, which is
// e itself or the predecessor it was merged into.
func (l *list) abandon(e *element) *element {
	e.value = nil

	if p := e.prev; p != &l.root && p.abandoned() && p.hi+1 == e.lo {
		p.hi = e.hi
		l.remove(e)
		e = p
	}
	if n := e.next; n != &l.root && n.abandoned() && e.hi+1 == n.lo {
		e.hi = n.hi
		l.remove(n)
	}
	return e
}

// remove removes e from l if e is an element of list l,
// and returns e to the element pool. If e is not an
// element of l, remove is a no-op — in particular, it
// does not re-pool e, so an accidental double-remove
// cannot double-Put the element (which would otherwise
// cause the pool to yield the same element to two
// future Get calls).
func (l *list) remove(e *element) {
	if e.list != l {
		return
	}
	e.prev.next = e.next
	e.next.prev = e.prev
	e.next = nil
	e.prev = nil
	e.list = nil
	e.value = nil
	e.lo, e.hi = 0, 0
	l.len--
	elementPool.Put(e)
}

// element is a node of a linked list of waiters.
type element struct {
	next, prev *element

	// list is a back-pointer to the owning list. remove compares
	// e.list to its argument: if they differ (element never inserted,
	// or already removed), remove is a no-op, preventing a double-Put
	// into elementPool. Cleared by remove.
	list *list

	// value is the waiter to signal, or nil if the element is a
	// tombstone.
	value waiter

	// lo and hi are the inclusive range of tickets the element covers.
	// A live waiter covers exactly its own ticket; a tombstone covers
	// one or more consecutive abandoned tickets.
	lo, hi uint64
}

// abandoned reports whether e, an element of a list, is a tombstone
// rather than a live waiter.
func (e *element) abandoned() bool {
	return e.value == nil
}
