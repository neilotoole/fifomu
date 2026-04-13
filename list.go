package fifomu

import (
	"sync"
)

var elementPool = sync.Pool{New: func() any { return new(element) }}

// list is a doubly-linked list of waiter elements. It is not
// thread-safe; callers hold Mutex.mu while manipulating the list.
type list struct {
	root element
	len  int
}

// lazyInit initializes the root sentinel the first time the list is
// used. Sharing a zero-value list is allowed; the first push triggers
// the init, subsequent pushes are no-ops on this path.
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

// pushBackElem inserts a new element e with value v at
// the back of list l and returns e.
func (l *list) pushBackElem(v waiter) *element {
	l.lazyInit()

	e := elementPool.Get().(*element) //nolint:errcheck
	e.value = v
	l.insert(e, l.root.prev)
	return e
}

// pushBack inserts a new element with value v at the back of list l.
//
// Callers that may need to later remove the element (e.g., LockContext
// on ctx.Done) should use pushBackElem instead to obtain the element
// pointer; pushBack is for callers that only ever dequeue from the
// front (e.g., Lock, which waits for its signal and never cancels).
func (l *list) pushBack(v waiter) {
	l.pushBackElem(v)
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
	l.len--
	elementPool.Put(e)
}

// insert inserts e after at.
func (l *list) insert(e, at *element) {
	e.prev = at
	e.next = at.next
	e.prev.next = e
	e.next.prev = e
	e.list = l
	l.len++
}

// element is a node of a linked list of waiters.
type element struct {
	next, prev *element

	// list is a back-pointer to the owning list. remove compares
	// e.list to its argument: if they differ (element never inserted,
	// or already removed), remove is a no-op, preventing a double-Put
	// into elementPool. Cleared by remove.
	list *list

	value waiter
}
