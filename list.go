package fifomu

import (
	"sync"
)

var elementPool = sync.Pool{New: func() any { return new(element[waiter]) }}

// list is a doubly-linked list of type T.
type list[T any] struct {
	root element[T]
	len  int
}

func (l *list[T]) lazyInit() {
	if l.root.next == nil {
		l.root.next = &l.root
		l.root.prev = &l.root
		l.len = 0
	}
}

// front returns the first element of list l or nil.
func (l *list[T]) front() *element[T] {
	if l.len == 0 {
		return nil
	}

	return l.root.next
}

// pushBackElem inserts a new element e with value v at
// the back of list l and returns e.
func (l *list[T]) pushBackElem(v T) *element[T] {
	l.lazyInit()

	e := elementPool.Get().(*element[T]) //nolint:errcheck
	e.value = v
	l.insert(e, l.root.prev)
	return e
}

// pushBack inserts a new element e with value v at
// the back of list l.
func (l *list[T]) pushBack(v T) {
	l.pushBackElem(v)
}

// remove removes e from l if e is an element of list l,
// and returns e to the element pool. If e is not an
// element of l, remove is a no-op — in particular, it
// does not re-pool e, so an accidental double-remove
// cannot double-Put the element (which would otherwise
// cause the pool to yield the same element to two
// future Get calls).
func (l *list[T]) remove(e *element[T]) {
	if e.list != l {
		return
	}
	e.prev.next = e.next
	e.next.prev = e.prev
	e.next = nil
	e.prev = nil
	e.list = nil
	var zero T
	e.value = zero
	l.len--
	elementPool.Put(e)
}

// insert inserts e after at.
func (l *list[T]) insert(e, at *element[T]) {
	e.prev = at
	e.next = at.next
	e.prev.next = e
	e.next.prev = e
	e.list = l
	l.len++
}

// element is a node of a linked list.
type element[T any] struct {
	next, prev *element[T]

	list *list[T]

	value T
}
