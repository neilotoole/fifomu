package native_test

import (
	"sync"
	"testing"

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
