// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package fifomu provides a Mutex whose Lock method returns the lock to
// queued callers in FIFO call order. This is in contrast to sync.Mutex, where
// a single goroutine can repeatedly lock and unlock and relock the mutex
// without handing off to other lock waiter goroutines (that is, until after
// a 1ms starvation threshold, at which point sync.Mutex enters a FIFO
// "starvation mode" for those starved waiters, but that's too late for some
// use cases).
//
// fifomu.Mutex is API-compatible with sync.Mutex (it implements sync.Locker
// and provides the same TryLock/Lock/Unlock methods) and adds a
// context-aware Mutex.LockContext method. Note one deliberate semantic
// difference from sync.Mutex: Mutex.TryLock fails whenever the waiter queue
// is non-empty, so that TryLock cannot jump ahead of FIFO-queued waiters.
// See Mutex.TryLock for details.
//
// # When to use
//
// Use fifomu when arrival-order fairness among contending goroutines is a
// correctness property, not just a performance one. Typical cases:
// streaming fan-out where each consumer must see chunks in arrival order,
// rate-limited queues where sync.Mutex's greedy-relock would starve some
// callers, and test scaffolding that needs deterministic acquire order.
// If your code doesn't care who acquires next, use sync.Mutex instead.
//
// # FIFO guarantee
//
// FIFO ordering applies to goroutines that have been queued as waiters.
// Arrival order across goroutines simultaneously contending for the internal
// sync.Mutex that protects the waiter queue is not guaranteed: a goroutine
// that called Lock slightly later may enqueue slightly earlier. The
// reordering window is bounded by the duration of the critical section
// that adds a waiter to the queue.
//
// # How it works
//
// Internally, a Mutex is a sync.Mutex plus a FIFO queue of waiters. Each
// waiter is a buffered(1) channel drawn from a sync.Pool. An unlocker walks
// the queue head, flips its own locked flag, and signals exactly one
// waiter by a non-blocking send. No spinning. LockContext adds a select arm
// on ctx.Done() and a cancel handler that walks the list to dequeue.
// Every happy path and every cancel path returns the waiter to the pool
// empty, so Lock and LockContext are allocation-free in steady state.
//
// # When to prefer sync.Mutex
//
// Unless you need the FIFO behavior, prefer sync.Mutex. For typical
// workloads, its "greedy-relock" behavior requires less goroutine
// switching and yields better performance.
package fifomu

import (
	"context"
	"sync"
)

var _ sync.Locker = (*Mutex)(nil)

// Mutex is a mutual exclusion lock whose Lock method returns
// the lock to queued callers in FIFO call order. See the package
// documentation for the precise guarantee and its caveats.
//
// A Mutex must not be copied after first use.
//
// The zero value for a Mutex is an unlocked mutex.
//
// Mutex implements the same methodset as sync.Mutex, so it can
// be used as a drop-in replacement. It implements an additional
// method Mutex.LockContext, which provides context-aware locking.
// Note that unlike sync.Mutex.TryLock, Mutex.TryLock deliberately
// reports failure whenever the waiter queue is non-empty, so that
// TryLock cannot jump ahead of FIFO-queued waiters.
type Mutex struct {
	_ noCopy

	waiters list
	locked  bool
	mu      sync.Mutex
}

// noCopy may be embedded into structs which must not be copied
// after the first use. It is a zero-sized marker type with Lock
// and Unlock methods so that `go vet -copylocks` detects accidental
// copies. See https://golang.org/issues/8005#issuecomment-190753527.
type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}

// Lock locks m.
//
// If the lock is already in use, the calling goroutine
// blocks until the mutex is available.
func (m *Mutex) Lock() {
	m.mu.Lock()
	if !m.locked && m.waiters.len == 0 {
		m.locked = true
		m.mu.Unlock()
		return
	}

	w := waiterPool.Get().(waiter) //nolint:errcheck
	m.waiters.pushBackElem(w)
	m.mu.Unlock()

	w.wait()
	waiterPool.Put(w)
}

// LockContext locks m.
//
// If the lock is already in use, the calling goroutine
// blocks until the mutex is available or ctx is done.
//
// On failure, LockContext returns context.Cause(ctx) and
// leaves the mutex unchanged.
//
// If ctx is already done when LockContext is called and the
// lock is available with no queued waiters, LockContext may
// still succeed without blocking.
//
// If the mutex becomes available concurrently with ctx
// cancellation, LockContext may acquire the mutex and return
// nil even though ctx is done. Callers that require ctx-strict
// behavior should re-check ctx.Err() after acquiring.
func (m *Mutex) LockContext(ctx context.Context) error {
	m.mu.Lock()
	if !m.locked && m.waiters.len == 0 {
		m.locked = true
		m.mu.Unlock()
		return nil
	}

	w := waiterPool.Get().(waiter) //nolint:errcheck
	elem := m.waiters.pushBackElem(w)
	m.mu.Unlock()

	select {
	case <-ctx.Done():
		err := context.Cause(ctx)
		m.mu.Lock()
		if w.tryReceive() {
			// Acquired the lock after we were canceled. Rather than
			// trying to fix up the queue, just pretend we didn't notice
			// the cancellation.
			err = nil
		} else {
			m.waiters.remove(elem)
		}
		m.mu.Unlock()
		// Put w back outside the critical section — sync.Pool is
		// concurrency-safe, and both branches above leave w with an
		// empty buffer, so holding m.mu here would serialize pool
		// ops against other Lock/Unlock callers for no benefit.
		waiterPool.Put(w)
		return err

	case <-w:
		waiterPool.Put(w)
		return nil
	}
}

// TryLock tries to lock m and reports whether it succeeded.
//
// Unlike sync.Mutex.TryLock, Mutex.TryLock reports failure whenever
// the waiter queue is non-empty, even if the mutex is momentarily
// unheld. This preserves FIFO ordering: TryLock must not jump ahead
// of queued waiters.
func (m *Mutex) TryLock() bool {
	m.mu.Lock()
	success := !m.locked && m.waiters.len == 0
	if success {
		m.locked = true
	}
	m.mu.Unlock()
	return success
}

// Unlock unlocks m.
// It is a run-time error if m is not locked on entry to Unlock.
//
// A locked Mutex is not associated with a particular goroutine.
// It is allowed for one goroutine to lock a Mutex and then
// arrange for another goroutine to unlock it.
//
// Recover-safety for the double-unlock panic: unlike sync.Mutex,
// Unlock checks its state before mutating it. A Mutex therefore
// survives recover from the "sync: unlock of unlocked mutex"
// panic as a normally-unlocked mutex. (sync.Mutex decrements
// before checking and leaves the mutex in a permanently corrupt
// state after recovery.) Callers should not rely on recover to
// paper over mutex misuse — the panic almost always indicates a
// real logic bug — but the specific case of recovering from a
// stray Unlock leaves the Mutex in a usable state.
//
// Other panic paths (in particular the waiter-pool-invariant
// panic in waiter.signal) are bug-detector panics and not
// intended to be recoverable; they should never fire in correct
// use of the package.
func (m *Mutex) Unlock() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.locked {
		panic("sync: unlock of unlocked mutex")
	}
	m.locked = false
	m.notifyWaiters()
}

// notifyWaiters signals the next queued waiter, if any, that it has
// acquired the mutex. Must be called with m.mu held and m.locked == false
// (the sole caller is Unlock, which clears m.locked immediately before).
//
// Only a single waiter is signaled per call. A binary mutex can release
// at most one holder at a time, so there is no point looping. (The
// upstream semaphore.Weighted does loop because a single Release can
// satisfy several smaller Acquire calls; that does not apply here.)
func (m *Mutex) notifyWaiters() {
	next := m.waiters.front()
	if next == nil {
		return
	}
	w := next.value
	m.locked = true
	m.waiters.remove(next)
	w.signal()
}

var waiterPool = sync.Pool{New: func() any { return waiter(make(chan struct{}, 1)) }}

// waiter is a single-slot handoff channel used to notify a queued
// goroutine that it has acquired the mutex. waiterPool produces and
// recycles these; every code path that returns a waiter to the pool
// guarantees its buffer is empty.
type waiter chan struct{}

// signal delivers a lock-acquired handoff to w. Must be called with
// Mutex.mu held, and w's buffer must be empty at call time — both
// invariants are maintained by the mutex protocol and every
// waiterPool return path.
//
// signal uses a non-blocking send (select-with-default) so that any
// future change which violates the pool invariant fails loudly with
// a panic, rather than reintroducing a historical deadlock.
//
// The history: an earlier version of this package used an unbuffered
// channel for the handoff. A LockContext caller whose ctx fired
// concurrently with the send would commit to the ctx.Done branch of
// its outer select, leaving this sender parked on w while still
// holding Mutex.mu — deadlocking the canceling receiver (which needs
// Mutex.mu to inspect w) and every subsequent Lock caller piling up
// behind Mutex.mu.
func (w waiter) signal() {
	select {
	case w <- struct{}{}:
	default:
		panic("fifomu: waiter pool invariant violated (channel buffer not empty)")
	}
}

// wait blocks until signal is called on w. Used by Lock, which has
// no cancellation channel to watch. LockContext cannot use this
// directly — it needs to select on ctx.Done too — so it writes
// `case <-w:` inline instead.
func (w waiter) wait() {
	<-w
}

// tryReceive non-blockingly receives any pending signal on w,
// returning true if one was consumed. Used by LockContext's cancel
// handler to distinguish "ctx fired before signal" (false, remove
// from queue and return the ctx error) from "signal raced with
// ctx cancel" (true, accept the lock and return nil).
func (w waiter) tryReceive() bool {
	select {
	case <-w:
		return true
	default:
		return false
	}
}
