package fifomu_test

import (
	"context"
	"math/rand/v2"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/neilotoole/fifomu"
)

// TestMutex_Chaos runs a bounded randomized workload: many
// goroutines, each picking a random operation (Lock, TryLock,
// LockContext with a short deadline) over a fixed duration.
//
// Mutual exclusion is checked at the boundary of every successful
// acquire via an atomic counter: on entry to the critical section
// the counter must go 0 → 1, and on exit it must go 1 → 0. Any
// other transition means two goroutines believed they held the
// mutex simultaneously, which is a direct violation of the
// mutex's defining invariant.
//
// This is a probabilistic test — it cannot prove correctness, but
// under -race it catches classes of bugs (missing synchronization,
// double-signal, wrong branch in notifyWaiters) that targeted
// tests might miss because they exercise a specific code path in
// isolation.
//
// Note on the operation mix: with 20 goroutines all picking
// uniformly, the waiter queue stays saturated, so
// Mutex.TryLock — which deliberately refuses while waiters are
// queued — almost never succeeds. That's expected behavior; the
// value of running TryLock here is exercising the refusal path
// under concurrent queue mutations, not measuring successful
// TryLocks.
func TestMutex_Chaos(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping chaos test in short mode")
	}

	const goroutines = 20
	duration := 2 * time.Second

	var mu fifomu.Mutex
	var held atomic.Int32
	var (
		lockOps         atomic.Int64
		tryLockAttempts atomic.Int64
		tryLockSuccess  atomic.Int64
		ctxAttempts     atomic.Int64
		ctxSuccess      atomic.Int64
	)
	var violation atomic.Bool

	done := make(chan struct{})
	time.AfterFunc(duration, func() { close(done) })

	// acquireSection asserts mutual exclusion around a held
	// section. Under the mutex contract, the counter must go
	// 0 → 1 on entry and 1 → 0 on exit; any other observed
	// transition implies a concurrent holder.
	acquireSection := func() {
		if h := held.Add(1); h != 1 {
			violation.Store(true)
		}
		// Brief time in the critical section to increase contention.
		runtime.Gosched()
		if h := held.Add(-1); h != 0 {
			violation.Store(true)
		}
	}

	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(seed, seed^0xdeadbeef))
			for {
				select {
				case <-done:
					return
				default:
				}

				switch rng.IntN(3) {
				case 0:
					mu.Lock()
					acquireSection()
					mu.Unlock()
					lockOps.Add(1)

				case 1:
					tryLockAttempts.Add(1)
					if mu.TryLock() {
						acquireSection()
						mu.Unlock()
						tryLockSuccess.Add(1)
					}

				case 2:
					ctxAttempts.Add(1)
					ctx, cancel := context.WithTimeout(context.Background(),
						time.Duration(rng.IntN(500))*time.Microsecond)
					if err := mu.LockContext(ctx); err == nil {
						acquireSection()
						mu.Unlock()
						ctxSuccess.Add(1)
					}
					cancel()
				}
			}
		}(uint64(g) + 1)
	}

	wg.Wait()

	if violation.Load() {
		t.Fatal("mutual exclusion violated: two goroutines held the mutex concurrently")
	}

	t.Logf("chaos: Lock=%d  TryLock=%d/%d (refusals expected under saturation)  LockContext=%d/%d",
		lockOps.Load(),
		tryLockSuccess.Load(), tryLockAttempts.Load(),
		ctxSuccess.Load(), ctxAttempts.Load(),
	)

	// Sanity: in a 2s run with 20 goroutines, each entry-point should
	// have been attempted many times. A zero on any attempt counter
	// would mean this configuration isn't actually exercising that
	// branch at all. We deliberately don't check tryLockSuccess > 0
	// (the saturated queue makes TryLock success effectively
	// impossible — exercising the refusal path is the goal). We do
	// check that LockContext sees at least some successes to
	// distinguish "exercised and sometimes acquired" from "deadline
	// so short it never wins" — a regression that broke the fast
	// path would drop ctxSuccess to 0.
	if lockOps.Load() == 0 || tryLockAttempts.Load() == 0 || ctxAttempts.Load() == 0 {
		t.Errorf("attempt counter zero — an entry point wasn't exercised: Lock=%d TryLock=%d LockContext=%d",
			lockOps.Load(), tryLockAttempts.Load(), ctxAttempts.Load())
	}
	if ctxSuccess.Load() == 0 {
		t.Errorf("LockContext never acquired in %d attempts; fast-path regression?",
			ctxAttempts.Load())
	}
}
