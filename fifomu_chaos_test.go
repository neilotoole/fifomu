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
// The mutual-exclusion invariant is checked at the boundary of each
// successful acquire: an atomic counter is incremented and expected
// to read 1; any value > 1 means two goroutines believed they held
// the mutex at the same time, which would be a correctness
// disaster.
//
// This is a probabilistic test — it cannot prove correctness, but
// under -race it catches classes of bugs (missing synchronization,
// double-signal, wrong branch in notifyWaiters) that targeted
// tests might miss because they exercise a specific code path in
// isolation.
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

	// acquireSection asserts mutual exclusion around a held section.
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

	t.Logf("chaos: Lock=%d  TryLock=%d/%d  LockContext=%d/%d",
		lockOps.Load(),
		tryLockSuccess.Load(), tryLockAttempts.Load(),
		ctxSuccess.Load(), ctxAttempts.Load(),
	)

	// Sanity: in a 2s run with ~20 goroutines, we should see some
	// activity on each code path. A zero on any count means this
	// configuration isn't actually exercising that branch.
	if lockOps.Load() == 0 || tryLockAttempts.Load() == 0 || ctxAttempts.Load() == 0 {
		t.Errorf("one or more code paths not exercised: %+v",
			map[string]int64{
				"lock":          lockOps.Load(),
				"tryLock":       tryLockAttempts.Load(),
				"lockContext":   ctxAttempts.Load(),
				"tryLockWin":    tryLockSuccess.Load(),
				"lockContextWin": ctxSuccess.Load(),
			})
	}
}
