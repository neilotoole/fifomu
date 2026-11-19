// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package fifomu provides a Mutex whose Lock method grants the lock to
// callers in the order in which they called it. This is in contrast to
// sync.Mutex, where a single goroutine can repeatedly lock and unlock and
// relock the mutex without handing off to other lock waiter goroutines
// (that is, until after a 1ms starvation threshold, at which point
// sync.Mutex enters a FIFO "starvation mode" for those starved waiters,
// but that's too late for some use cases).
//
// fifomu.Mutex is API-compatible with sync.Mutex (it implements sync.Locker
// and provides the same TryLock/Lock/Unlock methods) and adds a
// context-aware Mutex.LockContext method. There are two deliberate
// differences from sync.Mutex: Mutex.TryLock never takes the lock ahead of
// a caller that is already waiting for it, and unlocking an unlocked Mutex
// is a recoverable panic rather than a fatal error. See Mutex.TryLock and
// Mutex.Unlock for details.
//
// # When to use
//
// Use fifomu when the order in which contending goroutines acquire the
// lock matters, and sync.Mutex's willingness to let a running goroutine
// re-take the lock ahead of sleeping waiters is a problem. Typical cases:
// streaming fan-out where consumers must be served in turn, and
// rate-limited queues where sync.Mutex's greedy-relock would starve some
// callers. If your code doesn't care who acquires next, use sync.Mutex
// instead.
//
// # FIFO guarantee
//
// Lock and LockContext begin by taking a ticket from an atomic counter,
// and the mutex is granted strictly in ticket order. A caller's place in
// line is therefore fixed by a single atomic increment at the top of the
// call, which never blocks and cannot be overtaken: once a goroutine holds
// a ticket, no goroutine that takes a later ticket acquires the mutex
// first, however the scheduler treats either of them afterwards.
//
// A ticket abandoned by LockContext (because its ctx is done) is skipped.
//
// Two goroutines that call Lock at almost the same instant are ordered by
// which increments the counter first. A goroutine that is preempted after
// entering Lock but before its increment has not yet taken a place in
// line.
//
// Strict ordering has a cost that is inherent rather than incidental: an
// unlocked mutex is reserved for the next ticket holder even if that
// goroutine is not currently running, and everyone behind it waits.
//
// # How it works
//
// A Mutex is two atomic counters, next (the next ticket to issue) and
// serving (the ticket that owns the mutex), plus a sync.Mutex guarding a
// ticket-sorted list of waiters. Lock takes a ticket; if it is already
// being served, the caller owns the mutex without touching the inner
// lock. Otherwise the caller queues a buffered(1) channel drawn from a
// sync.Pool under its ticket and blocks on it. Unlock increments serving
// and, if a later ticket has been issued, signals that ticket's waiter by
// a non-blocking send. If the holder of that ticket has not queued yet,
// it finds its ticket being served when it gets there.
//
// LockContext adds a select arm on ctx.Done(). A canceled waiter leaves a
// tombstone under its ticket for the unlocker to skip; adjacent tombstones
// merge, and tombstones for the most recently issued tickets are removed
// by rolling the ticket counter back. Lock and LockContext are
// allocation-free in steady state.
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
	"sync/atomic"
)

var _ sync.Locker = (*Mutex)(nil)

// Mutex is a mutual exclusion lock whose Lock method grants the lock
// to callers in the order in which they called it. See the package
// documentation for the precise guarantee.
//
// A Mutex must not be copied after first use.
//
// The zero value for a Mutex is an unlocked mutex.
//
// Mutex implements the same methodset as sync.Mutex, so it can
// be used as a drop-in replacement. It implements an additional
// method Mutex.LockContext, which provides context-aware locking.
// Note that unlike sync.Mutex.TryLock, Mutex.TryLock never takes
// the lock ahead of a caller that is already waiting for it.
type Mutex struct {
	_ noCopy

	// waiters holds the callers blocked waiting for their ticket to be
	// served, plus tombstones for abandoned tickets, sorted by ticket.
	waiters list

	// mu guards waiters. It is only taken on slow paths: by a caller
	// whose ticket is not yet being served, by a LockContext caller
	// giving up, and by an unlocker that saw a later ticket issued.
	mu sync.Mutex

	// next is the next ticket to issue. Lock and LockContext take a
	// ticket with a single atomic increment; that increment is the
	// caller's place in line.
	//
	// next only decreases when LockContext gives up and its ticket,
	// plus any abandoned tickets directly before it, are the most
	// recently issued: those tickets are then un-issued and will be
	// handed out again. See Mutex.abandon.
	next atomic.Uint64

	// serving is the ticket that owns the mutex. The mutex is unlocked
	// when serving == next, that is, when the ticket being served has
	// not been issued yet. serving never decreases, and never exceeds
	// next.
	serving atomic.Uint64
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
	t := m.next.Add(1) - 1
	if m.serving.Load() == t {
		return
	}
	m.lockSlow(t)
}

func (m *Mutex) lockSlow(t uint64) {
	m.mu.Lock()
	if m.served(t) {
		m.mu.Unlock()
		return
	}

	w := waiterPool.Get().(waiter) //nolint:errcheck
	m.waiters.insert(t, w)
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
// lock is available with no callers waiting for it, LockContext
// may still succeed without blocking.
//
// If the mutex becomes available concurrently with ctx
// cancellation, LockContext may acquire the mutex and return
// nil even though ctx is done. Callers that require ctx-strict
// behavior should re-check ctx.Err() after acquiring.
func (m *Mutex) LockContext(ctx context.Context) error {
	t := m.next.Add(1) - 1
	if m.serving.Load() == t {
		return nil
	}
	return m.lockContextSlow(ctx, t)
}

func (m *Mutex) lockContextSlow(ctx context.Context, t uint64) error {
	m.mu.Lock()
	if m.served(t) {
		m.mu.Unlock()
		return nil
	}

	w := waiterPool.Get().(waiter) //nolint:errcheck
	elem := m.waiters.insert(t, w)
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
			m.abandon(elem)
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

// served reports whether ticket t owns the mutex. Must be called with
// m.mu held by the holder of t, before t is added to m.waiters.
//
// In correct use serving never passes a ticket whose holder has not
// been told, so this is an equality test. The >= is deliberate: if a
// stray Unlock (one with no matching Lock) releases the mutex while
// t's holder is still on its way here, serving moves past t. Treating
// that as "t was served" lets the Lock call return, as it would for a
// sync.Mutex unlocked by another goroutine, instead of queuing a
// waiter that could never be signaled and would block every later
// ticket behind it.
func (m *Mutex) served(t uint64) bool {
	return int64(m.serving.Load()-t) >= 0 //nolint:gosec // wraparound-safe ordering
}

// abandon gives up the ticket of the waiter element e, on behalf of a
// LockContext caller whose ctx is done. Must be called with m.mu held,
// and e must not have been signaled.
//
// A ticket cannot simply be forgotten: the unlocker that eventually
// advances serving onto it must be able to tell an abandoned ticket
// from one whose holder has not reached the waiter list yet. So e
// becomes a tombstone, which Mutex.handoff skips.
//
// If that tombstone covers the most recently issued tickets, they are
// un-issued instead: next is rolled back and the tombstone discarded.
// A lone caller that retries LockContext with a timeout against a
// long-held mutex therefore leaves nothing behind. Tombstones that
// cannot be rolled back are merged into ranges by list.abandon, so the
// waiter list stays proportional to the number of live waiters.
func (m *Mutex) abandon(e *element) {
	e = m.waiters.abandon(e)
	// The CAS fails if a later ticket has been issued. If it succeeds,
	// every ticket from e.lo up is abandoned and nobody holds one, so
	// taking them out of circulation cannot strand anyone.
	if e == m.waiters.back() && m.next.CompareAndSwap(e.hi+1, e.lo) {
		m.waiters.remove(e)
	}
}

// TryLock tries to lock m and reports whether it succeeded.
//
// TryLock succeeds only if m is unlocked and no other caller is in the
// process of acquiring it. Unlike sync.Mutex.TryLock, it can therefore
// never take the lock ahead of a goroutine that called Lock first.
func (m *Mutex) TryLock() bool {
	// If next still equals the serving value just read, no ticket is
	// outstanding, and the CAS issues us the one being served. serving
	// cannot have moved in between: it never exceeds next.
	s := m.serving.Load()
	return m.next.CompareAndSwap(s, s+1)
}

// Unlock unlocks m.
// It is a run-time error if m is not locked on entry to Unlock.
//
// A locked Mutex is not associated with a particular goroutine.
// It is allowed for one goroutine to lock a Mutex and then
// arrange for another goroutine to unlock it.
//
// Unlocking an unlocked Mutex panics with the same message that
// sync.Mutex reports. Unlike sync.Mutex, where that is a fatal
// error that terminates the program, here it is an ordinary panic:
// it can be recovered, and it leaves the Mutex unlocked and usable.
// Callers should not rely on recover to paper over mutex misuse —
// the panic almost always indicates a real logic bug.
//
// Other panic paths (in particular the waiter-pool-invariant
// panic in waiter.signal) are bug-detector panics and not
// intended to be recoverable; they should never fire in correct
// use of the package.
func (m *Mutex) Unlock() {
	var s uint64
	for {
		s = m.serving.Load()
		if s == m.next.Load() {
			panic("sync: unlock of unlocked mutex")
		}
		// The CAS only fails if another goroutine unlocked m at the
		// same time, which is misuse. Go round again: if nobody else
		// is waiting for m, the loser then finds it unlocked and
		// panics. (If later tickets are outstanding, a stray Unlock
		// is indistinguishable from unlocking on their behalf.)
		if m.serving.CompareAndSwap(s, s+1) {
			break
		}
	}

	// The mutex now belongs to ticket s+1. If that ticket has not been
	// issued, m is unlocked and there is nobody to tell: whoever is
	// issued s+1 will find it already being served.
	if m.next.Load() == s+1 {
		return
	}
	m.unlockSlow()
}

func (m *Mutex) unlockSlow() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handoff()
}

// handoff makes sure that the holder of the ticket being served finds
// out that it owns the mutex. Must be called with m.mu held.
//
// The ticket being served is in one of four states:
//
//   - Not issued. The mutex is unlocked; nothing to do.
//   - Issued, but its holder has not reached the waiter list yet. The
//     holder will see that its ticket is being served, either on its
//     fast path or in served under m.mu; nothing to do.
//   - Queued as a waiter. Signal it.
//   - Abandoned. Advance serving past the tombstone and look again.
//
// The first two cases are the same from here: the front of the waiter
// list is not the ticket being served. The list is sorted and holds
// nothing below serving, so only the front needs inspecting.
//
// handoff is idempotent, which matters because unlockers do not hold
// m.mu when they advance serving: by the time one gets here, the
// ticket it handed to may have locked and unlocked again.
func (m *Mutex) handoff() {
	for {
		e := m.waiters.front()
		if e == nil || e.lo != m.serving.Load() {
			return
		}

		if e.abandoned() {
			// Nobody owns an abandoned ticket, so no Unlock can be
			// advancing serving concurrently.
			m.serving.Store(e.hi + 1)
			m.waiters.remove(e)
			continue
		}

		w := e.value
		m.waiters.remove(e)
		w.signal()
		return
	}
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
// handler to distinguish "ctx fired before signal" (false, abandon
// the ticket and return the ctx error) from "signal raced with
// ctx cancel" (true, accept the lock and return nil).
func (w waiter) tryReceive() bool {
	select {
	case <-w:
		return true
	default:
		return false
	}
}
