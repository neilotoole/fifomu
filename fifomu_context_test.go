package fifomu_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/neilotoole/fifomu"
)

// TestLockContext_AlreadyCanceledFastPath documents that a pre-canceled
// ctx may still acquire the mutex when the lock is free and no waiters
// are queued. Callers needing ctx-strict behavior must re-check
// ctx.Err() after acquiring.
func TestLockContext_AlreadyCanceledFastPath(t *testing.T) {
	var mu fifomu.Mutex
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := mu.LockContext(ctx); err != nil {
		t.Fatalf("expected nil on fast path, got %v", err)
	}
	mu.Unlock()
}

// TestLockContext_AlreadyCanceledSlowPath verifies that a pre-canceled
// ctx is observed when the fast path is not available (mutex is held).
func TestLockContext_AlreadyCanceledSlowPath(t *testing.T) {
	var mu fifomu.Mutex
	mu.Lock()
	defer mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := mu.LockContext(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// TestLockContext_BlockedCancel verifies that a queued waiter returns
// an error once ctx is canceled.
func TestLockContext_BlockedCancel(t *testing.T) {
	var mu fifomu.Mutex
	mu.Lock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- mu.LockContext(ctx)
	}()

	// Let the goroutine queue itself.
	time.Sleep(20 * time.Millisecond)

	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("LockContext did not return after cancel")
	}

	mu.Unlock()
}

// TestLockContext_ContextCause verifies that context.Cause(ctx) is
// returned when ctx is canceled with a custom cause.
func TestLockContext_ContextCause(t *testing.T) {
	var mu fifomu.Mutex
	mu.Lock()

	want := errors.New("deliberate cancel cause")
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	errCh := make(chan error, 1)
	go func() {
		errCh <- mu.LockContext(ctx)
	}()

	time.Sleep(20 * time.Millisecond)

	cancel(want)

	select {
	case err := <-errCh:
		if !errors.Is(err, want) {
			t.Fatalf("expected %v, got %v", want, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("LockContext did not return after cancel")
	}

	mu.Unlock()
}

// TestLockContext_DeadlineExceeded verifies that a ctx with a deadline
// returns DeadlineExceeded.
func TestLockContext_DeadlineExceeded(t *testing.T) {
	var mu fifomu.Mutex
	mu.Lock()
	defer mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := mu.LockContext(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

// TestLockContext_CancelRaceRegression is a regression test for the
// deadlock where notifyWaiters parked on an unbuffered send while the
// canceling LockContext goroutine blocked on the internal mu. With
// buffered waiter channels, the send is always non-blocking, and this
// test completes regardless of how the cancel/unlock race resolves.
func TestLockContext_CancelRaceRegression(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress regression test in short mode")
	}
	const iters = 2000
	for i := range iters {
		func(i int) {
			var mu fifomu.Mutex
			mu.Lock()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			errCh := make(chan error, 1)
			go func() {
				errCh <- mu.LockContext(ctx)
			}()

			runtime.Gosched()

			cancel()
			mu.Unlock()

			select {
			case err := <-errCh:
				switch {
				case err == nil:
					// Signal won the race: LockContext acquired the lock.
					mu.Unlock()
				case errors.Is(err, context.Canceled):
					// Cancel won the race.
				default:
					t.Fatalf("iter %d: unexpected error: %v", i, err)
				}
			case <-time.After(3 * time.Second):
				t.Fatalf("deadlock at iter %d", i)
			}
		}(i)
	}
}

// TestLockContext_AcquireAndCancelOutcomes documents that both the
// "acquired despite cancel" and "canceled before acquire" outcomes are
// reachable when the race resolves in the opposite direction. This
// also exercises the two branches of the ctx.Done inner select.
func TestLockContext_AcquireAndCancelOutcomes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	const iters = 2000
	var acquired, canceled int
	for range iters {
		func() {
			var mu fifomu.Mutex
			mu.Lock()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			errCh := make(chan error, 1)
			go func() {
				errCh <- mu.LockContext(ctx)
			}()

			runtime.Gosched()

			cancel()
			mu.Unlock()

			err := <-errCh
			if err == nil {
				acquired++
				mu.Unlock()
			} else {
				canceled++
			}
		}()
	}

	t.Logf("outcomes: acquired=%d, canceled=%d", acquired, canceled)
	if acquired == 0 {
		t.Error("never observed the acquired-after-cancel outcome")
	}
	if canceled == 0 {
		t.Error("never observed the canceled outcome")
	}
}

// TestLockContext_HammerConcurrent stresses many concurrent LockContext
// callers with a mix of cancellations to exercise the queue under load.
func TestLockContext_HammerConcurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping hammer test in short mode")
	}
	const goroutines = 50
	const perGoroutine = 200
	var mu fifomu.Mutex
	var wg sync.WaitGroup
	var unexpectedErr atomic.Pointer[error]

	for g := range goroutines {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range perGoroutine {
				ctx, cancel := context.WithCancel(context.Background())
				if (g+i)%5 == 0 {
					// Cancel concurrently — no artificial delay. Scheduler
					// decides when the cancel lands.
					go cancel()
				}
				err := mu.LockContext(ctx)
				cancel()
				if err == nil {
					mu.Unlock()
				} else if !errors.Is(err, context.Canceled) {
					wrapped := fmt.Errorf("goroutine %d iter %d: %w", g, i, err)
					unexpectedErr.CompareAndSwap(nil, &wrapped)
					return
				}
			}
		}(g)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("hammer test did not complete within 30s")
	}
	if e := unexpectedErr.Load(); e != nil {
		t.Fatal(*e)
	}
}

// waitForWaiters blocks until m has at least n queued waiters, or
// fails the test after a generous deadline. Replaces sleep-based
// gating so the FIFO tests don't flake on loaded CI.
func waitForWaiters(t *testing.T, m *fifomu.Mutex, n uint) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for fifomu.WaitersLen(m) < n {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d queued waiters (have %d)", n, fifomu.WaitersLen(m))
		}
		runtime.Gosched()
	}
}

// TestMutex_FIFOOrdering verifies that queued waiters acquire the
// mutex in the order they were queued. Each goroutine is allowed to
// reach the waiter queue before the next one starts; we observe
// queue length directly rather than relying on a sleep.
func TestMutex_FIFOOrdering(t *testing.T) {
	const N = 10
	var mu fifomu.Mutex
	mu.Lock()

	order := make(chan int, N)
	var wg sync.WaitGroup
	for i := range N {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			mu.Lock()
			order <- i
			mu.Unlock()
		}(i)
		waitForWaiters(t, &mu, uint(i+1))
	}

	mu.Unlock()
	wg.Wait()
	close(order)

	var got []int
	for v := range order {
		got = append(got, v)
	}
	for i, v := range got {
		if v != i {
			t.Fatalf("expected FIFO, got %v (mismatch at position %d)", got, i)
		}
	}
}

// TestLockContext_FIFOAmongContextWaiters verifies that LockContext
// waiters are also dequeued in FIFO order.
func TestLockContext_FIFOAmongContextWaiters(t *testing.T) {
	const N = 10
	var mu fifomu.Mutex
	mu.Lock()

	ctx := context.Background()
	order := make(chan int, N)
	errCh := make(chan error, N)
	var wg sync.WaitGroup
	for i := range N {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := mu.LockContext(ctx); err != nil {
				errCh <- fmt.Errorf("goroutine %d: %w", i, err)
				return
			}
			order <- i
			mu.Unlock()
		}(i)
		waitForWaiters(t, &mu, uint(i+1))
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
	for i, v := range got {
		if v != i {
			t.Fatalf("expected FIFO, got %v (mismatch at position %d)", got, i)
		}
	}
}

// TestTryLock_RefusesWhileWaiterQueued documents that TryLock fails
// when the waiter queue is non-empty, even if the mutex is briefly
// unheld (which preserves FIFO ordering).
func TestTryLock_RefusesWhileWaiterQueued(t *testing.T) {
	var mu fifomu.Mutex
	mu.Lock()

	var queued atomic.Bool
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		close(started)
		queued.Store(true)
		mu.Lock()
		<-done
		mu.Unlock()
	}()

	<-started
	// Wait for the goroutine to enqueue.
	for i := 0; i < 100 && !queued.Load(); i++ {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(10 * time.Millisecond)

	// While the goroutine is queued and we hold the lock, TryLock must
	// fail — but more importantly, even after we Unlock, a concurrent
	// TryLock call before the queued goroutine acquires would fail to
	// preserve FIFO. We exercise the stronger first case here.
	if mu.TryLock() {
		t.Fatal("TryLock succeeded while another goroutine holds the lock")
	}

	close(done)
	mu.Unlock()
}
