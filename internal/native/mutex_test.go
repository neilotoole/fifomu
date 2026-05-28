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
