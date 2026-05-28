package native_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/neilotoole/fifomu/internal/native"
)

func TestMutex_LockUnlock(t *testing.T) {
	var mu native.Mutex
	mu.Lock()
	mu.Unlock() //nolint:staticcheck // acquire-then-release is the assertion
	mu.Lock()
	mu.Unlock() //nolint:staticcheck // acquire-then-release is the assertion
}

func TestMutex_ParallelContention(t *testing.T) {
	const goroutines = 20
	const itersPer = 5_000

	var mu native.Mutex
	var counter int

	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range itersPer {
				mu.Lock()
				counter++
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	want := goroutines * itersPer
	if counter != want {
		t.Fatalf("counter = %d, want %d (mutex did not serialize)", counter, want)
	}
}

func TestMutex_TryLock(t *testing.T) {
	var mu native.Mutex

	if !mu.TryLock() {
		t.Fatal("TryLock on unlocked mutex returned false")
	}
	if mu.TryLock() {
		t.Fatal("TryLock on locked mutex returned true")
	}
	mu.Unlock()

	// FIFO TryLock: must fail when a waiter is queued, even if
	// the lock has been momentarily released. We can't deterministically
	// observe that state without instrumentation, but we can at
	// least verify the basic locked/unlocked cases.
	if !mu.TryLock() {
		t.Fatal("TryLock on re-released mutex returned false")
	}
	mu.Unlock()
}

func TestMutex_UnlockOfUnlockedPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Unlock of unlocked mutex did not panic")
		}
		got, _ := r.(string)
		if got != "sync: unlock of unlocked mutex" {
			t.Fatalf("panic = %v, want \"sync: unlock of unlocked mutex\"", r)
		}
	}()
	var mu native.Mutex
	mu.Unlock()
}

// TestMutex_FIFO_Smoke is a probabilistic FIFO check. It cannot
// guarantee strict FIFO (the runtime sema queue ordering across
// the gap between "atomic state CAS" and "actually parked" is not
// strictly bounded), but a high-N test with deliberate stagger
// between Lock calls reliably catches gross reorderings.
func TestMutex_FIFO_Smoke(t *testing.T) {
	const N = 32
	var mu native.Mutex
	mu.Lock() // hold the lock so everyone queues

	order := make(chan int, N)
	var wg sync.WaitGroup
	for i := range N {
		wg.Go(func() {
			mu.Lock()
			order <- i
			mu.Unlock()
		})
		// Stagger to ensure goroutines reach the parking point in
		// arrival order. Without this, the test sometimes fires
		// even for a correct FIFO impl.
		time.Sleep(200 * time.Microsecond)
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
			t.Fatalf("acquisition order = %v, want %v (mismatch at position %d)", got, want(N), i)
		}
	}
}

func want(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

func TestLockContext_FastPath(t *testing.T) {
	var mu native.Mutex
	if err := mu.LockContext(context.Background()); err != nil {
		t.Fatalf("LockContext on uncontended mutex returned %v", err)
	}
	mu.Unlock()
}

func TestLockContext_AlreadyCancelled(t *testing.T) {
	var mu native.Mutex
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// With no waiters and an unlocked mutex, LockContext may
	// succeed via the fast path even though ctx is already done —
	// this matches the documented behavior.
	if err := mu.LockContext(ctx); err != nil {
		t.Fatalf("LockContext on uncontended mutex with cancelled ctx returned %v (expected nil per docs)", err)
	}
	mu.Unlock()
}

func TestLockContext_CancelWhileBlocked(t *testing.T) {
	var mu native.Mutex
	mu.Lock() // hold the lock so LockContext must block

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- mu.LockContext(ctx)
	}()

	// Give the LockContext goroutine time to enqueue.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("LockContext returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("LockContext did not return within 2s after ctx cancel")
	}

	mu.Unlock() // we still hold the lock; nobody got it
}

// TestMutex_MixedFIFO verifies that the waiter queue treats Lock and
// LockContext entries identically: entries wake in FIFO order
// regardless of which entry point queued them.
//
// This invariant exists because users mix the two methods in real
// code; a refactor that branched on the entry point (e.g., parking
// LockContext on a separate sema) would silently break it.
func TestMutex_MixedFIFO(t *testing.T) {
	const N = 8
	var mu native.Mutex
	mu.Lock()

	order := make(chan int, N)
	errCh := make(chan error, N)
	var wg sync.WaitGroup
	ctx := context.Background()

	for i := range N {
		wg.Go(func() {
			// Even i uses Lock, odd i uses LockContext.
			if i%2 == 0 {
				mu.Lock()
			} else if err := mu.LockContext(ctx); err != nil {
				errCh <- err
				return
			}
			order <- i
			mu.Unlock()
		})
		// Stagger so each goroutine reaches the enqueue point in
		// arrival order. The reordering window in lockSlow before
		// the goroutine takes listMu is bounded but real.
		time.Sleep(300 * time.Microsecond)
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
			t.Fatalf("acquisition order = %v, want sequential (mismatch at position %d)", got, i)
		}
	}
}

// TestMutex_CancellationPreservesFIFO verifies that a cancelled
// LockContext entry in the middle of the queue is tombstoned and
// skipped by Unlock — the remaining waiters wake in arrival order
// as if the cancelled entry had never been there.
func TestMutex_CancellationPreservesFIFO(t *testing.T) {
	const N = 6
	var mu native.Mutex
	mu.Lock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	order := make(chan int, N)
	var wg sync.WaitGroup

	// Waiters 0..N-1. Waiter 2 uses LockContext with cancellable
	// ctx; we'll cancel it after everyone has enqueued.
	for i := range N {
		wg.Go(func() {
			if i == 2 {
				if err := mu.LockContext(ctx); err != nil {
					return
				}
			} else {
				mu.Lock()
			}
			order <- i
			mu.Unlock()
		})
		time.Sleep(300 * time.Microsecond)
	}

	// All N goroutines are enqueued. Cancel waiter 2.
	cancel()
	time.Sleep(20 * time.Millisecond) // let the watcher tombstone

	// Now release the lock — Unlock chain should yield 0, 1, 3, 4, 5.
	mu.Unlock()
	wg.Wait()
	close(order)

	var got []int
	for v := range order {
		got = append(got, v)
	}

	want := []int{0, 1, 3, 4, 5}
	if len(got) != len(want) {
		t.Fatalf("got %d acquisitions, want %d (got=%v)", len(got), len(want), got)
	}
	for i, v := range got {
		if v != want[i] {
			t.Fatalf("acquisition order = %v, want %v", got, want)
		}
	}
}
