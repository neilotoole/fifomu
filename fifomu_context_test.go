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

	// Deterministically wait for the goroutine to enqueue.
	waitForWaiters(t, &mu, 1)

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

	// Deterministically wait for the goroutine to enqueue.
	waitForWaiters(t, &mu, 1)

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

// TestLockContext_AcquireAndCancelOutcomes runs the cancel/unlock race
// and reports the split between the two reachable outcomes: "acquired
// despite cancel" (the Unlock signal landed in the waiter's buffer
// before the cancel handler could dequeue it) and "canceled before
// acquire". Which side wins is timing- and hardware-dependent — on fast
// machines the signal almost always wins, so neither count is guaranteed
// nonzero. The test therefore asserts only that every outcome is
// well-formed (nil or context.Canceled) and that no iteration deadlocks,
// and logs the split rather than requiring both. The deterministic
// canceled path is covered by TestLockContext_BlockedCancel.
func TestLockContext_AcquireAndCancelOutcomes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	const iters = 2000
	var acquired, canceled int
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
					acquired++
					mu.Unlock()
				case errors.Is(err, context.Canceled):
					canceled++
				default:
					t.Fatalf("iter %d: unexpected error: %v", i, err)
				}
			case <-time.After(3 * time.Second):
				t.Fatalf("deadlock at iter %d", i)
			}
		}(i)
	}

	t.Logf("outcomes: acquired=%d, canceled=%d", acquired, canceled)
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
func waitForWaiters(t *testing.T, m *fifomu.Mutex, n int) {
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
		waitForWaiters(t, &mu, i+1)
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

	var wg sync.WaitGroup
	wg.Go(func() {
		mu.Lock()
		mu.Unlock() //nolint:staticcheck // acquire-then-release is the assertion
	})

	// Deterministically wait for the goroutine to enqueue.
	waitForWaiters(t, &mu, 1)

	// While the goroutine is queued and we hold the lock, TryLock must fail.
	if mu.TryLock() {
		t.Fatal("TryLock succeeded while another goroutine holds the lock")
	}

	mu.Unlock()
	wg.Wait()
}

// BenchmarkLockContext_FastPath measures the fast path of
// LockContext with each parallel worker holding its own mutex
// (no cross-goroutine contention). Direct comparison with
// BenchmarkMutexUncontended/fifomu shows LockContext's per-call
// overhead vs plain Lock.
func BenchmarkLockContext_FastPath(b *testing.B) {
	b.ReportAllocs()
	ctx := context.Background()
	b.RunParallel(func(pb *testing.PB) {
		var mu fifomu.Mutex
		for pb.Next() {
			_ = mu.LockContext(ctx)
			mu.Unlock()
		}
	})
}

// BenchmarkLockContext_Contended runs parallel LockContext
// callers contending on the same mutex, with no cancellation.
// This measures the queue+signal round-trip through the
// buffered waiter channel. Compare directly to
// BenchmarkMutex/fifomu — any large gap would indicate that
// LockContext's slow path carries overhead beyond Lock's.
func BenchmarkLockContext_Contended(b *testing.B) {
	b.ReportAllocs()
	var mu fifomu.Mutex
	ctx := context.Background()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = mu.LockContext(ctx)
			mu.Unlock()
		}
	})
}

// BenchmarkLockContext_Cancel measures the slow-path cancel
// handler. The mutex is held for the entire benchmark, forcing
// every LockContext call through queue-then-cancel-then-dequeue.
// A shared pre-canceled context is reused across iterations to
// isolate fifomu's allocations from context.WithCancel's.
// Serial (not RunParallel) because only one goroutine can be
// in the cancel path at a time under this construction.
func BenchmarkLockContext_Cancel(b *testing.B) {
	b.ReportAllocs()
	var mu fifomu.Mutex
	mu.Lock()
	defer mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for b.Loop() {
		_ = mu.LockContext(ctx)
	}
}
