package fifomu

// Test-only accessors into unexported state.

// WaitersLen returns the number of goroutines currently queued as
// waiters on m. It takes the internal lock, so it is safe to call
// concurrently with Lock/Unlock/LockContext.
func WaitersLen(m *Mutex) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.waiters.len
}
