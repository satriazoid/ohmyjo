package session

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"ohmyjo/internal/config"
)

// fakePTY stands in for a ConPTY so the registry can be tested without a real
// console host. Read blocks until the process is said to have exited.
type fakePTY struct {
	done    chan struct{}
	once    sync.Once
	code    uint32
	waitErr error
}

func newFakePTY(code uint32) *fakePTY {
	return &fakePTY{done: make(chan struct{}), code: code}
}

func (f *fakePTY) Read(p []byte) (int, error) {
	<-f.done
	return 0, io.EOF
}

func (f *fakePTY) Write(p []byte) (int, error) { return len(p), nil }
func (f *fakePTY) Resize(cols, rows int) error { return nil }
func (f *fakePTY) Pid() int                    { return 4242 }

func (f *fakePTY) Wait(ctx context.Context) (uint32, error) {
	<-f.done
	return f.code, f.waitErr
}

// Close ends the fake shell, exactly like closing a ConPTY.
func (f *fakePTY) Close() error {
	f.once.Do(func() { close(f.done) })
	return nil
}

// withFakePTY swaps the spawn seam for the duration of one test. It records the
// command line it was asked to launch.
var lastSpawn struct {
	mu      sync.Mutex
	cmdline string
	cwd     string
}

func withFakePTY(t *testing.T, code uint32) {
	t.Helper()
	old := spawn
	spawn = func(cmdline, cwd string, env []string, cols, rows int) (process, error) {
		lastSpawn.mu.Lock()
		lastSpawn.cmdline, lastSpawn.cwd = cmdline, cwd
		lastSpawn.mu.Unlock()
		return newFakePTY(code), nil
	}
	t.Cleanup(func() { spawn = old })
}

func startOne(t *testing.T, m *Manager) string {
	t.Helper()
	id, err := m.Start(Options{
		Profile: config.Profile{ID: "test", Name: "Test", Shell: "shell.exe"},
		Cols:    80, Rows: 24,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return id
}

// A closed session must leave the registry, or the sidebar keeps showing a dead
// entry for the rest of the run.
func TestCloseRemovesSessionFromRegistry(t *testing.T) {
	withFakePTY(t, 0)
	m := NewManager(nil)

	id := startOne(t, m)
	if got := len(m.List()); got != 1 {
		t.Fatalf("after start: List() len = %d, want 1", got)
	}
	if err := m.Close(id); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := len(m.List()); got != 0 {
		t.Fatalf("after close: List() len = %d, want 0", got)
	}
	if _, err := m.Get(id); err != ErrNotFound {
		t.Fatalf("after close: Get(%s) err = %v, want ErrNotFound", id, err)
	}
	// Closing twice reports not-found rather than panicking.
	if err := m.Close(id); err != ErrNotFound {
		t.Fatalf("second Close err = %v, want ErrNotFound", err)
	}
}

// Closing deliberately must still run the exit hook: that is what flushes a
// session's command history to disk before the id is forgotten.
func TestCloseFiresExitHook(t *testing.T) {
	withFakePTY(t, 0)
	got := make(chan string, 4)
	m := NewManager(func(id string, code int) { got <- id })

	id := startOne(t, m)
	if err := m.Close(id); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case seen := <-got:
		if seen != id {
			t.Fatalf("hook id = %q, want %q", seen, id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("exit hook was not called on deliberate close")
	}
}

// A shell that dies on its own keeps its slot, so the pane can show the code;
// only an explicit Close drops it.
func TestSpontaneousExitKeepsSession(t *testing.T) {
	withFakePTY(t, 7)
	exited := make(chan int, 1)
	m := NewManager(func(id string, code int) { exited <- code })

	id := startOne(t, m)
	sess, err := m.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	_ = sess.pty.Close() // the shell terminates by itself

	select {
	case code := <-exited:
		if code != 7 {
			t.Fatalf("hook code = %d, want 7", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("exit hook was not called on spontaneous exit")
	}

	list := m.List()
	if len(list) != 1 {
		t.Fatalf("List() len = %d, want the dead session kept", len(list))
	}
	if list[0].ExitCode == nil || *list[0].ExitCode != 7 {
		t.Fatalf("ExitCode = %v, want 7", list[0].ExitCode)
	}
}
