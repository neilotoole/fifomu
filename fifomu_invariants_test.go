package fifomu_test

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/neilotoole/fifomu"
)

// TestMutex_MixedQueueLockAndLockContext verifies that the waiter
// queue treats Lock and LockContext entries identically: entries
// wake in FIFO order regardless of which entry point queued them.
// A future refactor that branched on the entry point would
// silently break this.
func TestMutex_MixedQueueLockAndLockContext(t *testing.T) {
	const N = 6
	var mu fifomu.Mutex
	mu.Lock()

	order := make(chan int, N)
	errCh := make(chan error, N)
	var wg sync.WaitGroup
	ctx := context.Background()

	for i := range N {
		wg.Go(func() {
			// Even i uses Lock, odd i uses LockContext, to
			// interleave entry points in the queue.
			if i%2 == 0 {
				mu.Lock()
			} else if err := mu.LockContext(ctx); err != nil {
				errCh <- err
				return
			}
			order <- i
			mu.Unlock()
		})
		waitForWaiters(t, &mu, i+1)
	}

	mu.Unlock()
	wg.Wait()
	close(order)
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}

	var got []int
	for v := range order {
		got = append(got, v)
	}
	want := []int{0, 1, 2, 3, 4, 5}
	if !slices.Equal(got, want) {
		t.Fatalf("mixed queue FIFO violation: want %v, got %v", want, got)
	}
}

// TestMutex_MiddleOfQueueCancel verifies that canceling a waiter in
// the middle of the queue leaves the queue in a consistent state:
// the front waiter is still served next, and subsequent waiters
// follow in original order (with the canceled one excluded).
func TestMutex_MiddleOfQueueCancel(t *testing.T) {
	var mu fifomu.Mutex
	mu.Lock()

	order := make(chan int, 3)
	var wg sync.WaitGroup

	// Waiter A (Lock), queued first.
	wg.Go(func() {
		mu.Lock()
		order <- 0 // A
		mu.Unlock()
	})
	waitForWaiters(t, &mu, 1)

	// Waiter B (LockContext with cancelable ctx), in the middle.
	ctxB, cancelB := context.WithCancel(context.Background())
	bDone := make(chan error, 1)
	go func() {
		bDone <- mu.LockContext(ctxB)
	}()
	waitForWaiters(t, &mu, 2)

	// Waiters C and D (Lock), behind B.
	wg.Go(func() {
		mu.Lock()
		order <- 2 // C (index skips 1 which is B)
		mu.Unlock()
	})
	waitForWaiters(t, &mu, 3)

	wg.Go(func() {
		mu.Lock()
		order <- 3 // D
		mu.Unlock()
	})
	waitForWaiters(t, &mu, 4)

	// Cancel B. It should return Canceled and leave the queue [A, C, D].
	cancelB()
	select {
	case err := <-bDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiter B expected Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter B did not return after cancel")
	}

	// Queue length should now be 3.
	if got, want := fifomu.WaitersLen(&mu), 3; got != want {
		t.Fatalf("waiters.len after middle cancel = %d, want %d", got, want)
	}

	// Release the lock. A, C, D should acquire in order.
	mu.Unlock()
	wg.Wait()
	close(order)

	var got []int
	for v := range order {
		got = append(got, v)
	}
	want := []int{0, 2, 3}
	if !slices.Equal(got, want) {
		t.Fatalf("middle-cancel FIFO violation: want %v, got %v", want, got)
	}
}

// TestMutex_UnlockFromDifferentGoroutine verifies the documented
// semantic that a mutex locked by one goroutine may be unlocked by
// another. This is rarely exercised but explicitly allowed per the
// Unlock doc comment.
func TestMutex_UnlockFromDifferentGoroutine(t *testing.T) {
	var mu fifomu.Mutex

	locked := make(chan struct{})
	go func() {
		mu.Lock()
		close(locked)
	}()
	<-locked

	// Main goroutine (different from the one that locked) unlocks.
	mu.Unlock()

	// A third goroutine should now be able to acquire.
	acquired := make(chan struct{})
	go func() {
		mu.Lock()
		mu.Unlock() //nolint:staticcheck // acquire-then-release is the assertion
		close(acquired)
	}()

	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("cross-goroutine unlock left mutex unacquirable")
	}
}

// TestMutex_UnlockPanicMessage locks in the exact panic message
// used for double-unlock, matching sync.Mutex's message. A silent
// change would regress drop-in compatibility with sync.Mutex
// callers that recover and inspect the message.
func TestMutex_UnlockPanicMessage(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on unlock-of-unlocked")
		}
		const want = "sync: unlock of unlocked mutex"
		got, ok := r.(string)
		if !ok {
			t.Fatalf("panic value not a string: %T %v", r, r)
		}
		if got != want {
			t.Fatalf("panic message = %q, want %q", got, want)
		}
	}()

	var mu fifomu.Mutex
	mu.Unlock()
}

// TestMutex_NoGoroutineLeakAfterCancel runs many
// LockContext-then-cancel cycles and verifies the goroutine count
// returns to the baseline. Catches any future regression that
// strands waiter goroutines (e.g., a lost wakeup on a rare cancel
// interleaving).
func TestMutex_NoGoroutineLeakAfterCancel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping goroutine-leak test in short mode")
	}

	// Warm up; sync.Pool and scheduler state settle.
	runtime.GC()
	runtime.GC()
	baseline := runtime.NumGoroutine()

	const N = 1000
	for range N {
		var mu fifomu.Mutex
		mu.Lock()

		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		wg.Go(func() {
			// If LockContext succeeds despite cancel (signal won the
			// race), we must Unlock to keep the mutex protocol balanced
			// with our test's Unlock below.
			if err := mu.LockContext(ctx); err == nil {
				mu.Unlock()
				return
			}
			// Cancel won the race: the test's Unlock will run and
			// find no waiters, which is fine.
		})

		// Ensure the goroutine is queued before we race cancel vs. unlock.
		waitForWaiters(t, &mu, 1)

		cancel()
		mu.Unlock()
		wg.Wait()
	}

	// Give the scheduler time to retire goroutines that have finished
	// but not yet been cleaned up.
	runtime.GC()
	runtime.GC()
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > baseline+5 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		runtime.GC()
	}

	final := runtime.NumGoroutine()
	if final > baseline+5 {
		t.Fatalf("goroutine leak after %d cancel cycles: baseline=%d, final=%d",
			N, baseline, final)
	}
}
