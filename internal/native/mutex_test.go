package native_test

import (
	"testing"

	"github.com/neilotoole/fifomu/internal/native"
)

func TestMutex_LockUnlock(t *testing.T) {
	var mu native.Mutex
	mu.Lock()
	mu.Unlock()
	mu.Lock()
	mu.Unlock()
}
