// Package native is a prototype FIFO mutex built directly on the
// runtime's semaphore primitives (linkname'd from package sync).
// It is intentionally minimal: only Lock, Unlock, and TryLock.
// LockContext is not supported in this prototype.
package native

import (
	_ "unsafe" // for go:linkname
)

// runtime_Semacquire is linkname'd to sync.runtime_Semacquire.
//
// sync.runtime_Semacquire hardcodes lifo=false in the runtime
// (runtime/sema.go:70-72), which gives a FIFO wait queue for
// goroutines parked on the same sema address. The symbol is not in
// cmd/link's blockedLinknames map and the runtime comment at
// runtime/sema.go:60-67 explicitly commits to not changing the
// signature ("Do not remove or change the type signature").
//
//go:linkname runtime_Semacquire sync.runtime_Semacquire
func runtime_Semacquire(s *uint32)

// runtime_Semrelease is linkname'd to sync.runtime_Semrelease.
//
// With handoff=true, the released permit is consumed by the head
// waiter directly via cansemacquire in semrelease1 (runtime/sema.go
// :260-263), so new arrivals racing on the address cannot steal it.
// That property is what makes our FIFO guarantee hold under
// contention.
//
//go:linkname runtime_Semrelease sync.runtime_Semrelease
func runtime_Semrelease(s *uint32, handoff bool, skipframes int)
