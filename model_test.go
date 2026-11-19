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
	pcCancel          // lockContextSlow: holds m.mu; tryReceive or abandon
	pcTryCAS          // TryLock: loaded serving; about to CAS next
	pcUnlock0         // Unlock: about to load serving
	pcUnlock1         // Unlock: about to load next (unlocked check)
	pcUnlock2         // Unlock: about to CAS serving
	pcUnlock3         // Unlock: about to load next (anyone to tell?)
	pcUnlockMu        // unlockSlow: about to take m.mu
	pcHandoff         // handoff: holds m.mu; inspect the front
	pcSkip            // handoff: holds m.mu; store serving past a tombstone
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
)

type modelElem struct {
	lo, hi int
	owner  int // thread whose waiter this is; -1 for a tombstone
}

type modelThread struct {
	prog   string // operations: L (Lock), C (LockContext), T (TryLock)
	ip     int    // index of the current operation in prog
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

// abandon is Mutex.abandon, including list.abandon.
func (st *modelState) abandon(i int, bug modelBug) {
	at := slices.IndexFunc(st.list, func(e modelElem) bool { return e.owner == i })
	if at < 0 {
		panic("model: abandoning thread has no element")
	}
	if bug == bugNoTombstone {
		e := st.list[at]
		st.list = slices.Delete(st.list, at, at+1)
		if st.next == e.hi+1 {
			st.next = e.lo
		}
		return
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

	e := st.list[at]
	// The real code also tests e == back first. That is a shortcut,
	// not a condition: if next == e.hi+1, nothing above e.hi has been
	// issued, so e is necessarily the back.
	if st.next == e.hi+1 { // next.CompareAndSwap(e.hi+1, e.lo)
		st.next = e.lo
		st.list = slices.Delete(st.list, at, at+1)
	}
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
		if t.prog[t.ip] == 'T' {
			t.s = c.serving // TryLock: s := m.serving.Load()
			t.pc = pcTryCAS
		} else {
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

	case pcCancel: // if w.tryReceive() { err = nil } else { m.abandon(elem) }; m.mu.Unlock()
		c, t := next()
		c.mu = -1
		if t.signal {
			t.signal = false
			c.acquire(i)
		} else {
			c.abandon(i, bug)
			c.finishOp(i)
		}
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

	case pcUnlock0: // s = m.serving.Load()
		c, t := next()
		t.s = c.serving
		t.pc = pcUnlock1
		return []*modelState{c}, nil

	case pcUnlock1: // if s == m.next.Load() { panic(...) }
		if st.next == th.s {
			return nil, fmt.Errorf("thread %d: Unlock by the holder panicked: unlock of unlocked mutex", i)
		}
		c, t := next()
		t.n = c.next
		t.pc = pcUnlock2
		return []*modelState{c}, nil

	case pcUnlock2: // if m.serving.CompareAndSwap(s, s+1) { break }
		if st.serving != th.s {
			return nil, fmt.Errorf("thread %d: Unlock's CAS failed: serving moved under the holder", i)
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

	case pcHandoff: // one iteration of the loop in handoff
		c, t := next()
		if len(c.list) == 0 || (c.list[0].lo != c.serving && bug != bugHandoffIgnoresServing) {
			c.mu = -1
			c.finishOp(i)
			return []*modelState{c}, nil
		}
		front := c.list[0]
		if front.owner == -1 {
			if front.lo != c.serving { // only reachable with bugHandoffIgnoresServing
				c.mu = -1
				c.finishOp(i)
				return []*modelState{c}, nil
			}
			t.pc = pcSkip
			return []*modelState{c}, nil
		}
		if c.threads[front.owner].signal {
			return nil, fmt.Errorf("thread %d: waiter pool invariant violated: double signal", i)
		}
		c.threads[front.owner].signal = true // w.signal()
		c.list = c.list[1:]
		c.mu = -1
		c.finishOp(i)
		return []*modelState{c}, nil

	case pcSkip: // m.serving.Store(e.hi + 1); m.waiters.remove(e); continue
		c, t := next()
		c.serving = c.list[0].hi + 1
		c.list = c.list[1:]
		t.pc = pcHandoff
		return []*modelState{c}, nil
	}
	return nil, nil // pcDone
}

// check verifies the properties that must hold in every reachable
// state: mutual exclusion, FIFO, and the invariants from fifomu.go.
func (st *modelState) check() error {
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
	var progs []string
	var gen func(prefix string)
	gen = func(prefix string) {
		if prefix != "" {
			progs = append(progs, prefix)
		}
		if len(prefix) == maxOps {
			return
		}
		for _, op := range "LCT" {
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
// Cost grows steeply. As a guide, on a 2021 laptop: 2x4 is 47M states
// and 15s; 3x2 is 24M and 12s; 5x1 is 156M and over 3 minutes, with
// several GB of memory; 4x2 did not finish in 5 minutes.
func TestModel_ExhaustiveInterleavings(t *testing.T) {
	spec := "2x3,3x1"
	if testing.Short() {
		spec = "2x2,3x1"
	}
	if v := os.Getenv("FIFOMU_MODEL"); v != "" {
		spec = v
	}
	var bounds [][2]int
	for _, part := range strings.Split(spec, ",") {
		var b [2]int
		if _, err := fmt.Sscanf(part, "%dx%d", &b[0], &b[1]); err != nil {
			t.Fatalf("FIFOMU_MODEL: bad bound %q: %v", part, err)
		}
		bounds = append(bounds, b)
	}

	for _, b := range bounds {
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
			for _, progs := range configs {
				if _, err := explore(progs, bug); err != nil {
					t.Logf("caught with threads %s: %v", strings.Join(progs, " | "), err)
					return
				}
			}
			t.Fatal("the checker did not catch this bug")
		})
	}
}
