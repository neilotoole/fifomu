package fifomu

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

// This file is a model checker for the locking protocol.
//
// Tests that run real goroutines can only sample the interleavings the
// scheduler happens to produce. Here the protocol is restated as a
// state machine, one atomic step at a time, and every interleaving of
// a few threads is explored exhaustively, checking the properties the
// package promises after every step.
//
// What this does and does not show: it checks the protocol, not the
// Go code. The steps below are a transcription of fifomu.go, kept
// deliberately literal, and each names the code it stands for; a
// change to the locking code must be mirrored here. It also treats
// every atomic operation as sequentially consistent, which is what
// sync/atomic guarantees.
//
// A step contains at most one access to shared lock-free state (next,
// serving, or the inner mutex itself). Work on the waiter list and on
// waiter channels is folded into the step that holds or releases the
// inner mutex, since nothing else can observe it in between.

// Program counters: where a model thread is within an operation.
const (
	pcStart    = iota // about to begin the next operation
	pcFast            // Lock: took a ticket; about to load serving
	pcLockMu          // lockSlow: about to take m.mu
	pcRegister        // lockSlow: holds m.mu; served check, then insert
	pcWait            // blocked on the waiter channel
	pcCancelMu        // lockContextSlow: ctx is done; about to take m.mu
	pcCancel          // lockContextSlow: holds m.mu; tryReceive, or tombstone the element
	pcRollback        // abandon: holds m.mu; CAS next back
	pcTryCAS          // TryLock: loaded serving; about to CAS next
	pcUnlock0         // Unlock: about to load serving
	pcUnlock1         // Unlock: about to load next (unlocked check)
	pcUnlock2         // Unlock: about to CAS serving
	pcUnlock3         // Unlock: about to load next (anyone to tell?)
	pcUnlockMu        // unlockSlow: about to take m.mu
	pcRepair0         // repair: holds m.mu; about to load serving
	pcRepair1         // repair: holds m.mu; about to load next
	pcRepair2         // repair: holds m.mu; about to CAS next up to serving
	pcHandoff         // handoff: holds m.mu; load serving, inspect the front
	pcSkip            // handoff: holds m.mu; CAS serving past a tombstone
	pcDone            // program finished
)

// Mutations that break the protocol on purpose. The checker must
// catch each one; that is the evidence that it can catch anything.
type modelBug int

const (
	bugNone modelBug = iota
	// lockSlow queues without re-checking serving under m.mu.
	bugNoServedRecheck
	// Unlock decides whether to hand off using the value of next it
	// read before advancing serving, instead of reading it again.
	bugStaleNextInUnlock
	// A canceled waiter removes its element instead of leaving a
	// tombstone when its ticket cannot be rolled back.
	bugNoTombstone
	// handoff signals the front waiter without checking that its
	// ticket is the one being served.
	bugHandoffIgnoresServing
	// TryLock succeeds whenever the ticket being served is unissued
	// OR the lock looks free by serving alone (no CAS on next).
	bugTryLockNoCAS
	// handoff leaves a waiter that serving has already passed where it
	// is, as it did before it was hardened against a stray Unlock.
	// Only misuse programs can tell the difference.
	bugHandoffIgnoresStale
	// abandon does not call handoff when it is done. Only misuse
	// programs can tell.
	bugAbandonNoHandoff
	// handoff does not first restore serving <= next. Only misuse
	// programs can tell.
	bugNoRepair
)

type modelElem struct {
	lo, hi int
	owner  int // thread whose waiter this is; -1 for a tombstone
}

type modelThread struct {
	// prog is the thread's operations: L (Lock), C (LockContext), T
	// (TryLock), each followed by Unlock if it acquired, and X (an
	// Unlock with no matching Lock, which is misuse).
	prog   string
	ip     int // index of the current operation in prog
	pc     int
	ticket int
	s      int  // Unlock/TryLock: the value of serving it loaded
	n      int  // Unlock: the value of next it loaded (bugStaleNextInUnlock)
	signal bool // the waiter channel's one-slot buffer
	holds  bool // between acquiring the mutex and Unlock's CAS
}

type modelState struct {
	list          []modelElem
	threads       []modelThread
	next, serving int
	mu            int // thread holding the inner mutex, or -1
	// misuse is set if any thread's program contains a stray Unlock.
	// The mutex then promises much less: see checkMisuse.
	misuse bool
}

func (st *modelState) clone() *modelState {
	c := *st
	c.list = slices.Clone(st.list)
	c.threads = slices.Clone(st.threads)
	return &c
}

func (st *modelState) key() string {
	b := make([]byte, 0, 8+3*len(st.list)+8*len(st.threads))
	b = append(b, byte(st.next), byte(st.serving), byte(st.mu+1), byte(len(st.list)))
	for _, e := range st.list {
		b = append(b, byte(e.lo), byte(e.hi), byte(e.owner+1))
	}
	for i := range st.threads {
		th := &st.threads[i]
		flags := byte(0)
		if th.signal {
			flags |= 1
		}
		if th.holds {
			flags |= 2
		}
		b = append(b, byte(th.ip), byte(th.pc), byte(th.ticket), byte(th.s), byte(th.n), flags)
	}
	return string(b)
}

// finishOp moves thread i on to its next operation.
func (st *modelState) finishOp(i int) {
	th := &st.threads[i]
	th.holds = false
	th.ip++
	th.ticket, th.s, th.n = 0, 0, 0
	th.pc = pcStart
	if th.ip == len(th.prog) {
		th.pc = pcDone
	}
}

// acquire records that thread i now owns the mutex, and moves it on to
// Unlock. Every successful Lock, LockContext and TryLock ends here.
func (st *modelState) acquire(i int) {
	th := &st.threads[i]
	th.holds = true
	th.pc = pcUnlock0
}

// insert is list.insert.
func (st *modelState) insert(ticket, owner int) {
	at := len(st.list)
	for at > 0 && st.list[at-1].lo > ticket {
		at--
	}
	st.list = slices.Insert(st.list, at, modelElem{lo: ticket, hi: ticket, owner: owner})
}

// tombstone is list.abandon: thread i's element becomes a tombstone,
// merged with its neighbors. It returns the tombstone's index.
func (st *modelState) tombstone(i int) int {
	at := slices.IndexFunc(st.list, func(e modelElem) bool { return e.owner == i })
	if at < 0 {
		panic("model: abandoning thread has no element")
	}
	st.list[at].owner = -1
	if at > 0 && st.list[at-1].owner == -1 && st.list[at-1].hi+1 == st.list[at].lo {
		st.list[at-1].hi = st.list[at].hi
		st.list = slices.Delete(st.list, at, at+1)
		at--
	}
	if at+1 < len(st.list) && st.list[at+1].owner == -1 && st.list[at].hi+1 == st.list[at+1].lo {
		st.list[at].hi = st.list[at+1].hi
		st.list = slices.Delete(st.list, at+1, at+2)
	}
	return at
}

// step returns the states thread i can move to from st in one atomic
// step. No successors means the thread is blocked (or finished). More
// than one means a choice the scheduler or the environment makes: a
// LockContext waiter may be signaled, or have its ctx fire.
func (st *modelState) step(i int, bug modelBug) ([]*modelState, error) {
	th := &st.threads[i]
	next := func() (*modelState, *modelThread) {
		c := st.clone()
		return c, &c.threads[i]
	}

	switch th.pc {
	case pcStart:
		c, t := next()
		switch t.prog[t.ip] {
		case 'X': // a stray Unlock
			t.pc = pcUnlock0
			return []*modelState{c}, nil
		case 'T':
			t.s = c.serving // TryLock: s := m.serving.Load()
			t.pc = pcTryCAS
		default:
			t.ticket = c.next // t := m.next.Add(1) - 1
			c.next++
			t.pc = pcFast
		}
		return []*modelState{c}, nil

	case pcFast: // if m.serving.Load() == t { return }
		c, t := next()
		if c.serving == t.ticket {
			c.acquire(i)
		} else {
			t.pc = pcLockMu
		}
		return []*modelState{c}, nil

	case pcLockMu, pcCancelMu, pcUnlockMu: // m.mu.Lock()
		if st.mu != -1 {
			return nil, nil
		}
		c, t := next()
		c.mu = i
		t.pc++ // each *Mu pc is followed by the pc that runs under m.mu
		return []*modelState{c}, nil

	case pcRegister: // if m.served(t) {...}; m.waiters.insert(t, w); m.mu.Unlock()
		c, t := next()
		c.mu = -1
		if bug != bugNoServedRecheck && c.serving >= t.ticket {
			c.acquire(i)
		} else {
			c.insert(t.ticket, i)
			t.pc = pcWait
		}
		return []*modelState{c}, nil

	case pcWait: // select { case <-ctx.Done(): ...; case <-w: ... }
		var out []*modelState
		if th.signal {
			c, t := next()
			t.signal = false
			c.acquire(i)
			out = append(out, c)
		}
		if th.prog[th.ip] == 'C' {
			c, t := next()
			t.pc = pcCancelMu
			out = append(out, c)
		}
		return out, nil

	case pcCancel: // if w.tryReceive() { err = nil } else { m.abandon(elem) }
		c, t := next()
		if t.signal {
			t.signal = false
			c.mu = -1 // m.mu.Unlock()
			c.acquire(i)
			return []*modelState{c}, nil
		}
		if bug == bugNoTombstone {
			at := slices.IndexFunc(c.list, func(e modelElem) bool { return e.owner == i })
			e := c.list[at]
			c.list = slices.Delete(c.list, at, at+1)
			if c.next == e.hi+1 {
				c.next = e.lo
			}
			c.mu = -1
			c.finishOp(i)
			return []*modelState{c}, nil
		}
		at := c.tombstone(i)     // e = m.waiters.abandon(e)
		t.ticket = c.list[at].lo // which tombstone pcRollback works on
		t.pc = pcRollback
		return []*modelState{c}, nil

	case pcRollback: // if e == back && m.next.CompareAndSwap(e.hi+1, e.lo) { remove(e) }; m.handoff()
		c, t := next()
		at := slices.IndexFunc(c.list, func(e modelElem) bool { return e.lo == t.ticket })
		e := c.list[at]
		// The real code also tests e == back first. That is a shortcut,
		// not a condition: if next == e.hi+1, nothing above e.hi has been
		// issued, so e is necessarily the back.
		if c.next == e.hi+1 {
			c.next = e.lo
			c.list = slices.Delete(c.list, at, at+1)
		}
		if bug == bugAbandonNoHandoff {
			c.mu = -1
			c.finishOp(i)
			return []*modelState{c}, nil
		}
		t.pc = pcRepair0
		return []*modelState{c}, nil

	case pcTryCAS: // return m.next.CompareAndSwap(s, s+1)
		c, t := next()
		ok := c.next == t.s
		if bug == bugTryLockNoCAS {
			ok = c.serving == t.s && c.next <= t.s+1
		}
		if ok {
			c.next = t.s + 1
			t.ticket = t.s
			c.acquire(i)
		} else {
			c.finishOp(i)
		}
		return []*modelState{c}, nil

	case pcUnlock0, pcUnlock1, pcUnlock2, pcUnlock3:
		return st.stepUnlock(i, bug)

	case pcRepair0, pcRepair1, pcRepair2, pcHandoff, pcSkip:
		return st.stepHandoff(i, bug)
	}
	return nil, nil // pcDone
}

// stepUnlock is step for a thread in Unlock, up to the point where it
// decides whether to hand off.
func (st *modelState) stepUnlock(i int, bug modelBug) ([]*modelState, error) {
	th := &st.threads[i]
	next := func() (*modelState, *modelThread) {
		c := st.clone()
		return c, &c.threads[i]
	}

	switch th.pc {
	case pcUnlock0: // s = m.serving.Load()
		c, t := next()
		t.s = c.serving
		t.pc = pcUnlock1
		return []*modelState{c}, nil

	case pcUnlock1: // if !before(s, m.next.Load()) { panic(...) }
		if st.next <= th.s {
			if th.holds && !st.misuse {
				return nil, fmt.Errorf("thread %d: Unlock by the holder panicked: unlock of unlocked mutex", i)
			}
			c, _ := next() // the panic, recovered by the caller
			c.finishOp(i)
			return []*modelState{c}, nil
		}
		c, t := next()
		t.n = c.next
		t.pc = pcUnlock2
		return []*modelState{c}, nil

	case pcUnlock2: // if m.serving.CompareAndSwap(s, s+1) { break }
		if st.serving != th.s {
			if !st.misuse {
				return nil, fmt.Errorf("thread %d: Unlock's CAS failed: serving moved under the holder", i)
			}
			c, t := next()
			t.pc = pcUnlock0 // go round again
			return []*modelState{c}, nil
		}
		c, t := next()
		c.serving = t.s + 1
		t.holds = false
		t.pc = pcUnlock3
		return []*modelState{c}, nil

	case pcUnlock3: // if m.next.Load() == s+1 { return }
		c, t := next()
		n := c.next
		if bug == bugStaleNextInUnlock {
			n = t.n
		}
		if n == t.s+1 {
			c.finishOp(i)
		} else {
			t.pc = pcUnlockMu
		}
		return []*modelState{c}, nil
	}
	return nil, nil
}

// stepHandoff is step for a thread in handoff (including the repair
// it begins with), which it runs holding m.mu.
func (st *modelState) stepHandoff(i int, bug modelBug) ([]*modelState, error) {
	th := &st.threads[i]
	next := func() (*modelState, *modelThread) {
		c := st.clone()
		return c, &c.threads[i]
	}

	switch th.pc {
	case pcRepair0: // repair: s := m.serving.Load()
		c, t := next()
		t.s = c.serving
		t.pc = pcRepair1
		return []*modelState{c}, nil

	case pcRepair1: // repair: n := m.next.Load(); if !before(n, s) { return }
		c, t := next()
		t.n = c.next
		if t.n >= t.s || bug == bugNoRepair {
			t.pc = pcHandoff
		} else {
			t.pc = pcRepair2
		}
		return []*modelState{c}, nil

	case pcRepair2: // repair: m.next.CompareAndSwap(n, s), and round again
		c, t := next()
		if c.next == t.n {
			c.next = t.s
		}
		t.pc = pcRepair0
		return []*modelState{c}, nil

	case pcHandoff: // one iteration of the loop in handoff
		c, t := next()
		done := func() ([]*modelState, error) {
			c.mu = -1
			c.finishOp(i)
			return []*modelState{c}, nil
		}
		if len(c.list) == 0 {
			return done()
		}
		front := c.list[0]
		s := c.serving // s := m.serving.Load()
		if bug == bugHandoffIgnoresServing {
			s = max(s, front.hi) // pretend the front is always due
		}
		if bug == bugHandoffIgnoresStale && front.owner != -1 && front.lo < s {
			return done()
		}
		if s < front.lo {
			return done()
		}
		if front.owner == -1 {
			if front.hi >= s {
				t.s = c.serving
				t.pc = pcSkip
				return []*modelState{c}, nil
			}
			c.list = c.list[1:]
			return []*modelState{c}, nil // continue
		}
		if c.threads[front.owner].signal {
			return nil, fmt.Errorf("thread %d: waiter pool invariant violated: double signal", i)
		}
		c.threads[front.owner].signal = true // w.signal()
		c.list = c.list[1:]
		if front.lo == s || bug == bugHandoffIgnoresServing {
			return done()
		}
		return []*modelState{c}, nil // continue

	case pcSkip: // m.serving.CompareAndSwap(s, e.hi+1); continue
		c, t := next()
		if c.serving == t.s {
			c.serving = c.list[0].hi + 1
		}
		t.pc = pcHandoff
		return []*modelState{c}, nil
	}
	return nil, nil
}

// check verifies the properties that must hold in every reachable
// state: mutual exclusion, FIFO, and the invariants from fifomu.go.
func (st *modelState) check() error {
	if st.misuse {
		return st.checkMisuse()
	}
	holder := -1
	for i := range st.threads {
		if !st.threads[i].holds {
			continue
		}
		if holder != -1 {
			return fmt.Errorf("mutual exclusion: threads %d and %d both hold the mutex", holder, i)
		}
		holder = i
		// FIFO: the holder's ticket is the one being served. Tickets
		// are unique and serving only goes up, so this is strict
		// ticket order.
		if st.threads[i].ticket != st.serving {
			return fmt.Errorf("FIFO: thread %d holds the mutex with ticket %d, but serving is %d",
				i, st.threads[i].ticket, st.serving)
		}
	}

	if st.serving > st.next { // invariant 1
		return fmt.Errorf("serving %d > next %d", st.serving, st.next)
	}

	if st.mu != -1 {
		return nil // the list is mid-update, and nobody can see it
	}
	for j, e := range st.list {
		if e.lo < st.serving { // invariant 2
			return fmt.Errorf("list element [%d,%d] is below serving %d", e.lo, e.hi, st.serving)
		}
		if e.owner == -1 && st.next < e.hi+2 { // invariant 3
			return fmt.Errorf("tombstone [%d,%d] covers the newest ticket (next=%d)", e.lo, e.hi, st.next)
		}
		if e.lo == st.serving && !st.handoffPending() { // invariant 4
			return fmt.Errorf("lost wakeup: ticket %d is being served and is queued as [%d,%d], but no handoff is pending",
				st.serving, e.lo, e.hi)
		}
		if j == 0 {
			continue
		}
		prev := st.list[j-1]
		if prev.hi >= e.lo {
			return fmt.Errorf("list out of order: [%d,%d] before [%d,%d]", prev.lo, prev.hi, e.lo, e.hi)
		}
		if prev.owner == -1 && e.owner == -1 && prev.hi+1 == e.lo {
			return fmt.Errorf("adjacent tombstones [%d,%d] and [%d,%d] not merged", prev.lo, prev.hi, e.lo, e.hi)
		}
	}
	return nil
}

// handoffPending reports whether some thread is committed to calling
// handoff, or might be: it has advanced serving and not yet decided
// whether anyone needs telling, or it is on its way to m.mu to tell
// them. (If a thread at pcUnlock3 then wrongly decides nobody does,
// it finishes, and the state after that step fails the check.)
func (st *modelState) handoffPending() bool {
	for i := range st.threads {
		switch st.threads[i].pc {
		case pcUnlock3, pcUnlockMu, pcRepair0, pcRepair1, pcRepair2, pcHandoff, pcSkip:
			return true
		default:
		}
	}
	return false
}

// checkMisuse verifies the little that still holds once a program
// contains a stray Unlock. Mutual exclusion and FIFO are gone: the
// stray Unlock releases a mutex someone else holds, exactly as it
// would with a sync.Mutex. What must survive is that the mutex keeps
// working: nobody is left waiting for a signal that will never come,
// and the waiter list stays well formed. checkFinal adds that the
// mutex comes to rest unlocked and consistent.
func (st *modelState) checkMisuse() error {
	if st.mu != -1 {
		return nil
	}
	for j, e := range st.list {
		if e.lo <= st.serving && !st.handoffPending() {
			return fmt.Errorf("stranded: [%d,%d] (owner %d) is at or behind serving %d, but no handoff is pending",
				e.lo, e.hi, e.owner, st.serving)
		}
		if j > 0 && st.list[j-1].hi >= e.lo {
			return fmt.Errorf("list out of order: %v", st.list)
		}
	}
	return nil
}

// checkFinal verifies a state from which no thread can move.
func (st *modelState) checkFinal() error {
	for i := range st.threads {
		if st.threads[i].pc != pcDone {
			return fmt.Errorf("deadlock: thread %d is stuck at pc %d (ticket %d); next=%d serving=%d list=%v",
				i, st.threads[i].pc, st.threads[i].ticket, st.next, st.serving, st.list)
		}
	}
	if st.serving != st.next || len(st.list) != 0 || st.mu != -1 {
		return fmt.Errorf("not unlocked at rest: next=%d serving=%d list=%v", st.next, st.serving, st.list)
	}
	return nil
}

// explore visits every state reachable from the given programs, one
// per thread, and returns the first property violation, if any.
func explore(progs []string, bug modelBug) (states int, err error) {
	init := &modelState{mu: -1}
	for _, p := range progs {
		init.misuse = init.misuse || strings.Contains(p, "X")
		init.threads = append(init.threads, modelThread{prog: p})
	}

	seen := map[string]struct{}{init.key(): {}}
	stack := []*modelState{init}
	for len(stack) > 0 {
		st := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if err := st.check(); err != nil {
			return len(seen), err
		}

		moved := false
		for i := range st.threads {
			succ, err := st.step(i, bug)
			if err != nil {
				return len(seen), err
			}
			for _, c := range succ {
				moved = true
				k := c.key()
				if _, ok := seen[k]; !ok {
					seen[k] = struct{}{}
					stack = append(stack, c)
				}
			}
		}
		if !moved {
			if err := st.checkFinal(); err != nil {
				return len(seen), err
			}
		}
	}
	return len(seen), nil
}

// modelConfigs returns every way to give each of `threads` threads a
// program of 1 to maxOps operations, up to reordering of the threads
// (which are interchangeable).
func modelConfigs(threads, maxOps int) [][]string {
	return modelConfigsOver("LCT", threads, maxOps)
}

// modelMisuseConfigs is modelConfigs with stray Unlocks added to the
// operations, keeping only the configurations that contain one.
func modelMisuseConfigs(threads, maxOps int) [][]string {
	all := modelConfigsOver("LCTX", threads, maxOps)
	return slices.DeleteFunc(all, func(progs []string) bool {
		return !strings.Contains(strings.Join(progs, ""), "X")
	})
}

func modelConfigsOver(ops string, threads, maxOps int) [][]string {
	var progs []string
	var gen func(prefix string)
	gen = func(prefix string) {
		if prefix != "" {
			progs = append(progs, prefix)
		}
		if len(prefix) == maxOps {
			return
		}
		for _, op := range ops {
			gen(prefix + string(op))
		}
	}
	gen("")
	slices.Sort(progs)

	var out [][]string
	var pick func(from int, cur []string)
	pick = func(from int, cur []string) {
		if len(cur) == threads {
			out = append(out, slices.Clone(cur))
			return
		}
		for i := from; i < len(progs); i++ {
			pick(i, append(cur, progs[i]))
		}
	}
	pick(0, nil)
	return out
}

// TestModel_ExhaustiveInterleavings explores every interleaving of
// small sets of threads, each running a short program of Lock,
// LockContext (whose ctx may fire at any moment while it waits) and
// TryLock calls, each followed by Unlock if it acquired.
//
// Bounds are written THREADSxOPS, e.g. "3x2" for every combination of
// three threads running up to two operations each. The default set
// takes about a second. Set FIFOMU_MODEL to a comma-separated list to
// choose others; CI runs a deeper set (see the workflow).
//
// Cost grows steeply. As a guide, on a 2021 laptop: 2x4 is 98M states
// and 3x2 is 59M, about half a minute each. An earlier, smaller
// version of the model needed over 3 minutes and several GB of memory
// for 5x1, and did not finish 4x2 in 5 minutes.
func TestModel_ExhaustiveInterleavings(t *testing.T) {
	spec := "2x3,3x1"
	if testing.Short() {
		spec = "2x2,3x1"
	}
	if v := os.Getenv("FIFOMU_MODEL"); v != "" {
		spec = v
	}
	for _, b := range parseModelBounds(t, spec) {
		configs := modelConfigs(b[0], b[1])
		total := 0
		for _, progs := range configs {
			states, err := explore(progs, bugNone)
			total += states
			if err != nil {
				t.Fatalf("threads %s: %v", strings.Join(progs, " | "), err)
			}
		}
		t.Logf("%d threads x up to %d ops: %d configurations, %d states, no violations",
			b[0], b[1], len(configs), total)
	}
}

// TestModel_Misuse explores programs that contain a stray Unlock (an
// Unlock with no matching Lock) racing the other operations. No mutex
// can keep its promises to callers that do this, but it must not be
// left broken for everyone else: see checkMisuse.
//
// Set FIFOMU_MODEL_MISUSE to choose the bounds, as for FIFOMU_MODEL.
// Misuse programs have one more operation to choose from, so the same
// bounds cost several times as much: 2x4,3x2,4x1 is 650M states and
// under 5 minutes on the same laptop.
func TestModel_Misuse(t *testing.T) {
	spec := "2x3,3x1"
	if testing.Short() {
		spec = "2x2,3x1"
	}
	if v := os.Getenv("FIFOMU_MODEL_MISUSE"); v != "" {
		spec = v
	}
	for _, b := range parseModelBounds(t, spec) {
		configs := modelMisuseConfigs(b[0], b[1])
		total := 0
		for _, progs := range configs {
			states, err := explore(progs, bugNone)
			total += states
			if err != nil {
				t.Fatalf("threads %s: %v", strings.Join(progs, " | "), err)
			}
		}
		t.Logf("%d threads x up to %d ops: %d configurations, %d states, no violations",
			b[0], b[1], len(configs), total)
	}
}

// TestModel_CatchesSeededBugs breaks the protocol in ways that are
// easy to get wrong, and requires the checker to notice each one. A
// checker that passes everything proves nothing.
func TestModel_CatchesSeededBugs(t *testing.T) {
	bugs := map[string]modelBug{
		"lockSlow does not re-check serving under mu": bugNoServedRecheck,
		"Unlock reuses a stale read of next":          bugStaleNextInUnlock,
		"canceled waiter leaves no tombstone":         bugNoTombstone,
		"handoff signals a ticket not being served":   bugHandoffIgnoresServing,
		"TryLock does not CAS next":                   bugTryLockNoCAS,
	}
	configs := append(modelConfigs(2, 2), modelConfigs(3, 2)...)

	for name, bug := range bugs {
		t.Run(name, func(t *testing.T) {
			requireCaught(t, configs, bug)
		})
	}

	// A stranded LockContext waiter can always get itself out by
	// having its ctx fire, so it never shows up as a deadlock. The
	// bugs that lose a wakeup must still be caught when every waiter
	// is one of those, which takes the pending-handoff check.
	contextOnly := slices.DeleteFunc(slices.Clone(configs), func(progs []string) bool {
		return strings.Contains(strings.Join(progs, ""), "L")
	})
	lostWakeups := map[string]modelBug{
		"lockSlow does not re-check serving under mu": bugNoServedRecheck,
		"Unlock reuses a stale read of next":          bugStaleNextInUnlock,
	}
	for name, bug := range lostWakeups {
		t.Run("LockContext waiters only/"+name, func(t *testing.T) {
			requireCaught(t, contextOnly, bug)
		})
	}

	// Bugs that only a stray Unlock exposes.
	misuse := map[string]modelBug{
		"handoff ignores a waiter behind serving": bugHandoffIgnoresStale,
		"abandon does not call handoff":           bugAbandonNoHandoff,
		"handoff does not repair next":            bugNoRepair,
	}
	misuseConfigs := append(modelMisuseConfigs(3, 1), modelMisuseConfigs(2, 3)...)
	for name, bug := range misuse {
		t.Run("misuse/"+name, func(t *testing.T) {
			requireCaught(t, misuseConfigs, bug)
			// And correct use must not be able to tell.
			for _, progs := range append(modelConfigs(2, 2), modelConfigs(3, 1)...) {
				if _, err := explore(progs, bug); err != nil {
					t.Fatalf("correct use distinguishes this bug: threads %s: %v", strings.Join(progs, " | "), err)
				}
			}
		})
	}
}

func requireCaught(t *testing.T, configs [][]string, bug modelBug) {
	t.Helper()
	for _, progs := range configs {
		if _, err := explore(progs, bug); err != nil {
			t.Logf("caught with threads %s: %v", strings.Join(progs, " | "), err)
			return
		}
	}
	t.Fatal("the checker did not catch this bug")
}

// TestModel_CheckRejectsLostWakeup pins the pending-handoff check on
// the smallest state that needs it: ticket 1 is being served, its
// LockContext waiter is queued and unsignaled, and the unlocker has
// gone. Nothing else is wrong with this state, and the waiter's ctx
// could still get it out, so only that check can reject it.
func TestModel_CheckRejectsLostWakeup(t *testing.T) {
	st := &modelState{
		mu:      -1,
		next:    2,
		serving: 1,
		list:    []modelElem{{lo: 1, hi: 1, owner: 0}},
		threads: []modelThread{
			{prog: "C", pc: pcWait, ticket: 1},
			{prog: "L", ip: 1, pc: pcDone},
		},
	}
	err := st.check()
	if err == nil || !strings.Contains(err.Error(), "lost wakeup") {
		t.Fatalf("check() = %v, want a lost wakeup", err)
	}
}

func parseModelBounds(t *testing.T, spec string) [][2]int {
	t.Helper()
	var bounds [][2]int
	for _, part := range strings.Split(spec, ",") {
		var b [2]int
		if _, err := fmt.Sscanf(part, "%dx%d", &b[0], &b[1]); err != nil {
			t.Fatalf("bad model bound %q: %v", part, err)
		}
		bounds = append(bounds, b)
	}
	return bounds
}
