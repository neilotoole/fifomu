package fifomu_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs goleak across the whole test binary: any goroutine
// left behind after tests complete is a leak and fails the run.
// This catches any future regression that strands waiter goroutines
// or forgets to clean up a cancel goroutine.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
