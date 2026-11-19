package fifomu

// Test-only accessors into unexported state.

// WaitersLen returns the number of goroutines currently queued as
// waiters on m. Tombstones left by canceled LockContext calls are not
// counted. It takes the internal lock, so it is safe to call
// concurrently with Lock/Unlock/LockContext.
func WaitersLen(m *Mutex) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for e := m.waiters.front(); e != nil; e = e.next {
		if e == &m.waiters.root {
			break
		}
		if !e.abandoned() {
			n++
		}
	}
	return n
}
