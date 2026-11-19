package fifomu

import (
	"context"
	"iter"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"
)

// scenarioOps are the moves available to a scenario. Each is run to
// completion before the next begins, so a scenario is deterministic:
// what it explores is every ordering of calls, cancellations and
// unlocks, not goroutine interleavings (see model_test.go for those).
//
//	L  Lock: acquires if the mutex is unlocked, else queues a waiter.
//	C  LockContext with a live ctx: likewise.
//	D  LockContext with a ctx that is already done.
//	T  TryLock.
//	U  Unlock.
//	F  Cancel the earliest queued LockContext.
//	B  Cancel the latest queued LockContext.
const scenarioOps = "LCDTUFB"

// scenarioWaiter is a queued Lock or LockContext call, as the
// reference model sees it.
type scenarioWaiter struct {
	cancel context.CancelFunc // nil for a Lock waiter
	done   chan error
}

// TestScenarios_Exhaustive runs every sequence of scenarioOps up to a
// given length against a Mutex and, alongside it, a trivially correct
// reference model: a "held" flag and a FIFO slice of waiters. After
// every step the two must agree on who holds the mutex, who is
// waiting, and in what order waiters are served.
//
// The default length keeps the test to a second or two under the race
// detector. Set FIFOMU_SCENARIO_LEN to go deeper; CI does (see the
// workflow). There are 7^n scenarios of length n.
func TestScenarios_Exhaustive(t *testing.T) {
	maxLen := 5
	if testing.Short() {
		maxLen = 4
	}
	if v := os.Getenv("FIFOMU_SCENARIO_LEN"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			t.Fatalf("FIFOMU_SCENARIO_LEN: %v", err)
		}
		maxLen = n
	}

	count := 0
	script := make([]byte, 0, maxLen)
	var walk func()
	walk = func() {
		if len(script) == maxLen {
			// Shorter scripts are prefixes of these, and every step
			// of a script is checked, so only full-length ones run.
			runScenario(t, string(script))
			count++
			return
		}
		for i := range len(scenarioOps) {
			script = append(script, scenarioOps[i])
			walk()
			script = script[:len(script)-1]
			if t.Failed() {
				return
			}
		}
	}
	walk()
	t.Logf("%d scenarios of length %d", count, maxLen)
}

// scenario is one run of a script: the Mutex under test, and the
// reference model's view of it.
type scenario struct {
	t      *testing.T
	script string
	queue  []*scenarioWaiter // model: who is waiting, earliest first
	m      Mutex
	step   int
	held   bool // model: is the mutex held?
}

func runScenario(t *testing.T, script string) {
	t.Helper()
	sc := &scenario{t: t, script: script}
	defer sc.drain()

	for sc.step = range len(script) {
		switch script[sc.step] {
		case 'L':
			sc.lock(false)
		case 'C':
			sc.lock(true)
		case 'D':
			sc.lockContextDone()
		case 'T':
			if got := sc.m.TryLock(); got != !sc.held {
				sc.failf("TryLock = %v with held=%v", got, sc.held)
			}
			sc.held = true // either it was already, or we just took it
		case 'U':
			sc.unlock()
		case 'F':
			sc.cancelFirst(slices.All(sc.queue))
		case 'B':
			sc.cancelFirst(slices.Backward(sc.queue))
		}

		sc.compare()
		if t.Failed() {
			return
		}
	}
}

func (sc *scenario) failf(format string, args ...any) {
	sc.t.Helper()
	sc.t.Errorf("scenario %q, step %d (%c): "+format,
		append([]any{sc.script, sc.step, sc.script[sc.step]}, args...)...)
}

// lock is Lock, or LockContext with a live ctx: it acquires an
// unlocked mutex, and otherwise queues a waiter behind the holder.
func (sc *scenario) lock(withCtx bool) {
	if !sc.held {
		if withCtx {
			if err := sc.m.LockContext(context.Background()); err != nil {
				sc.failf("LockContext on an unlocked mutex: %v", err)
			}
		} else {
			sc.m.Lock()
		}
		sc.held = true
		return
	}

	w := &scenarioWaiter{done: make(chan error, 1)}
	before := WaitersLen(&sc.m)
	if withCtx {
		ctx, cancel := context.WithCancel(context.Background())
		w.cancel = cancel
		go func() { w.done <- sc.m.LockContext(ctx) }()
	} else {
		go func() { sc.m.Lock(); w.done <- nil }()
	}
	waitFor(sc.t, "waiter to queue", func() bool { return WaitersLen(&sc.m) == before+1 })
	sc.queue = append(sc.queue, w)
}

// lockContextDone is LockContext with a ctx that is already done.
func (sc *scenario) lockContextDone() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := sc.m.LockContext(ctx)
	switch {
	case sc.held && err == nil:
		sc.failf("LockContext with a done ctx acquired a held mutex")
	case !sc.held && err != nil:
		sc.failf("LockContext on an unlocked mutex: %v", err)
	}
	sc.held = true // either it was already, or we just took it
}

func (sc *scenario) unlock() {
	if !sc.held {
		defer func() {
			if recover() == nil {
				sc.failf("Unlock of an unlocked mutex did not panic")
			}
		}()
		sc.m.Unlock()
		return
	}

	sc.m.Unlock()
	if len(sc.queue) == 0 {
		sc.held = false
		return
	}

	// The mutex passes to the earliest waiter, and to it alone.
	w := sc.queue[0]
	select {
	case err := <-w.done:
		if err != nil {
			sc.failf("earliest waiter failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		sc.failf("earliest waiter was not served")
		return
	}
	if w.cancel != nil {
		w.cancel()
	}
	sc.queue = sc.queue[1:]
}

// cancelFirst cancels the first queued LockContext that order yields.
func (sc *scenario) cancelFirst(order iter.Seq2[int, *scenarioWaiter]) {
	for i, w := range order {
		if w.cancel == nil {
			continue
		}
		sc.queue = slices.Delete(sc.queue, i, i+1)
		w.cancel()
		select {
		case err := <-w.done:
			if err == nil {
				sc.failf("canceled waiter acquired the mutex")
			}
		case <-time.After(5 * time.Second):
			sc.failf("canceled waiter did not return")
		}
		return
	}
}

// compare checks that the Mutex agrees with the model.
func (sc *scenario) compare() {
	if got := WaitersLen(&sc.m); got != len(sc.queue) {
		sc.failf("waiters = %d, model has %d", got, len(sc.queue))
	}
	for i, w := range sc.queue {
		select {
		case <-w.done:
			sc.failf("waiter %d of %d returned while still queued", i, len(sc.queue))
			return
		default:
		}
	}

	sc.m.mu.Lock()
	outstanding := sc.m.next.Load() - sc.m.serving.Load()
	listLen := sc.m.waiters.len
	sc.m.mu.Unlock()
	switch {
	case !sc.held && (outstanding != 0 || listLen != 0):
		sc.failf("unlocked, but %d tickets outstanding and %d list elements", outstanding, listLen)
	case sc.held && outstanding < uint64(1+len(sc.queue)):
		sc.failf("held with %d waiters, but only %d tickets outstanding", len(sc.queue), outstanding)
	case listLen > 2*len(sc.queue):
		// Tombstones only survive between, or ahead of, live
		// waiters, and adjacent ones merge.
		sc.failf("%d list elements for %d waiters", listLen, len(sc.queue))
	}
}

// drain serves whoever is left, so that no goroutine outlives the
// scenario, and checks that the mutex comes to rest unlocked.
func (sc *scenario) drain() {
	for sc.held {
		sc.m.Unlock()
		if len(sc.queue) == 0 {
			break
		}
		w := sc.queue[0]
		if err := <-w.done; err != nil {
			sc.t.Errorf("scenario %q, drain: waiter failed: %v", sc.script, err)
		}
		if w.cancel != nil {
			w.cancel()
		}
		sc.queue = sc.queue[1:]
	}
	requireUnlocked(sc.t, &sc.m)
}
