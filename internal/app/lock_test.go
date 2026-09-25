package app

import (
	"sync"
	"testing"
	"time"
)

// The view lock is taken on the message thread, but Windows delivers several
// messages synchronously from inside another: ShowWindow(SW_MAXIMIZE) sends
// WM_SIZE before it returns, and the host answers WM_SIZE by calling Resize.
// That nested call runs on the same thread and needs the lock its caller still
// holds. A plain sync.Mutex froze the whole window there. The message pump
// stopped, so the app went unresponsive and even the system close button did
// nothing.
func TestLockIsReentrant(t *testing.T) {
	var v View
	done := make(chan struct{})
	go func() {
		defer close(done)
		v.lock()
		v.lock()
		v.unlock()
		v.lock()
		v.unlock()
		v.unlock()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("nested lock() deadlocked: a synchronous message callback cannot take the view lock")
	}
}

// After a balanced lock/unlock the mutex must be genuinely free, or the next
// message on the same thread would still be inside the critical section and a
// background goroutine handed work through Post would block forever.
func TestLockReleasesMutually(t *testing.T) {
	var v View
	v.lock()

	acquired := make(chan struct{})
	go func() {
		v.mu.Lock()
		v.mu.Unlock()
		close(acquired)
	}()

	select {
	case <-acquired:
		t.Fatal("the mutex was free while lock() was held")
	case <-time.After(100 * time.Millisecond):
		// Still held, as it should be.
	}

	v.unlock()
	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("unlock() did not release the mutex")
	}
}

// A burst of the same-thread lock/unlock cycle a real session performs: every
// keystroke, mouse event and timer tick takes it. Depth must return to zero
// each time, never drift.
func TestLockDepthReturnsToZero(t *testing.T) {
	var v View
	for i := 0; i < 100; i++ {
		v.lock()
		v.lock()
		v.unlock()
		v.unlock()
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		v.mu.Lock()
		v.mu.Unlock()
	}()

	exited := make(chan struct{})
	go func() { wg.Wait(); close(exited) }()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("lock depth drifted above zero, leaving the mutex permanently held")
	}
}
