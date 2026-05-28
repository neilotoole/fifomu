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
