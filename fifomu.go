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
// fifomu.Mutex implements the exported methods of sync.Mutex and thus is
// a drop-in replacement (and by extension also implements sync.Locker).
// It also provides a bonus context-aware Mutex.LockContext method.
//
// FIFO ordering applies to goroutines that have been queued as waiters.
// Arrival order across goroutines simultaneously contending for the internal
// state protecting the waiter queue is not guaranteed: a goroutine that
// called Lock slightly later may enqueue slightly earlier. The reordering
// window is bounded by the duration of the critical section that adds a
// waiter to the queue.
//
// Note: unless you need the FIFO behavior, you should prefer sync.Mutex.
// For typical workloads, its "greedy-relock" behavior requires less goroutine
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
	waiters list[waiter]
	locked  bool
	mu      sync.Mutex
}

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
	m.waiters.pushBack(w)
	m.mu.Unlock()

	<-w
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
		select {
		case <-w:
			// Acquired the lock after we were canceled. Rather than
			// trying to fix up the queue, just pretend we didn't notice
			// the cancellation.
			err = nil
		default:
			m.waiters.remove(elem)
		}
		m.mu.Unlock()
		// Put w back outside the critical section — both inner
		// branches leave w in the pool-safe "empty buffer, no other
		// referrer" state, and sync.Pool is concurrency-safe, so
		// holding m.mu here would only serialize the pool op
		// against other Lock/Unlock callers for no benefit.
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
func (m *Mutex) Unlock() {
	m.mu.Lock()
	if !m.locked {
		m.mu.Unlock()
		panic("sync: unlock of unlocked mutex")
	}
	m.locked = false
	m.notifyWaiters()
	m.mu.Unlock()
}

// notifyWaiters signals the next queued waiter, if any, that it has
// acquired the mutex. Must be called with m.mu held.
//
// Only a single waiter is signaled per call. A binary mutex can release
// at most one holder at a time, so there is no point looping. (The
// upstream semaphore.Weighted does loop because a single Release can
// satisfy several smaller Acquire calls; that does not apply here.)
func (m *Mutex) notifyWaiters() {
	next := m.waiters.front()
	if next == nil || m.locked {
		return
	}

	w := next.Value
	m.locked = true
	m.waiters.remove(next)

	// Every pooled waiter channel enters the pool with an empty buffer:
	// every success path receives from it before returning it, and the
	// LockContext cancel-default path removes the waiter from the queue
	// without ever signaling it. While we hold m.mu here, no other
	// goroutine can signal w either. So the buffered(1) send below must
	// succeed without blocking.
	//
	// We do this as a select-with-default so that any future change
	// that accidentally violates the invariant fails loudly instead of
	// reintroducing the pre-fix deadlock: on master, notifyWaiters sent
	// on an unbuffered channel, and a LockContext caller whose ctx
	// fired concurrently would commit to the ctx.Done branch, leave
	// us parked on the send, and then block on m.mu.Lock(). Deadlock
	// between the parked sender and the canceling receiver — with
	// every subsequent Lock caller piling up behind m.mu.
	select {
	case w <- struct{}{}:
	default:
		panic("fifomu: waiter pool invariant violated (channel buffer not empty)")
	}
}

var waiterPool = sync.Pool{New: func() any { return waiter(make(chan struct{}, 1)) }}

type waiter chan struct{}
