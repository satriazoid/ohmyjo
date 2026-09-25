// Package session owns live shell processes: one ConPTY per session, a read
// pump that fans raw bytes to subscribers, and a registry that guarantees no
// process outlives the app.
package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ohmyjo/internal/config"
)

// ErrNotFound is returned when a session id is unknown.
var ErrNotFound = errors.New("session not found")

// ErrNoConPTY is returned on Windows builds older than 1809.
var ErrNoConPTY = errors.New("ConPTY is unavailable: Windows 10 1809+ required")

// process is the platform pseudo-terminal backing a session. It is satisfied by
// *conpty.ConPty on Windows and by a reporting stub elsewhere.
type process interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Resize(cols, rows int) error
	Pid() int
	Wait(ctx context.Context) (uint32, error)
	Close() error
}

// replayLimit caps the byte budget replayed to a reconnecting client, so a
// flood of output cannot grow the buffer without bound.
const replayLimit = 512 * 1024

// maxDimension clamps resize requests; ConPTY takes int16 coordinates and the
// frontend sends values derived from pixel sizes, which can be absurd.
const maxDimension = 1000

// Info is a snapshot of a session's public state.
type Info struct {
	ID       string `json:"id"`
	Profile  string `json:"profile"`
	Name     string `json:"name"`
	Pid      int    `json:"pid"`
	Cwd      string `json:"cwd"`
	Status   string `json:"status"`
	ExitCode *int   `json:"exitCode,omitempty"`
}

// ExitHook is called once when a shell exits on its own, and also when the
// manager closes it, so that the per-session resources hanging off the id (its
// command history, for one) are released either way.
type ExitHook func(id string, code int)

// Session is a single shell process attached to a ConPTY.
type Session struct {
	ID      string
	Profile string
	Name    string
	Cwd     string

	pty    process
	done   chan struct{}
	onExit ExitHook

	mu       sync.Mutex
	subs     map[int]func([]byte)
	nextSub  int
	closed   bool
	exitCode *int
	replay   []byte
	colsRows [2]int
	writeMu  sync.Mutex
}

// Write forwards raw input bytes to the shell.
func (s *Session) Write(data []byte) error {
	if !s.alive() {
		return os.ErrClosed
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.pty.Write(data)
	return err
}

func (s *Session) alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed
}

// Resize changes the ConPTY dimensions. Repeated identical sizes are dropped so
// a splitter drag does not spam the console host.
func (s *Session) Resize(cols, rows int) error {
	if cols < 1 || rows < 1 {
		return fmt.Errorf("invalid terminal size %dx%d", cols, rows)
	}
	if cols > maxDimension {
		cols = maxDimension
	}
	if rows > maxDimension {
		rows = maxDimension
	}
	s.mu.Lock()
	if s.colsRows[0] == cols && s.colsRows[1] == rows {
		s.mu.Unlock()
		return nil
	}
	s.colsRows = [2]int{cols, rows}
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return os.ErrClosed
	}
	return s.pty.Resize(cols, rows)
}

// Subscribe registers a sink for output bytes. It returns the buffered replay
// for a reconnecting client and a cancel function.
func (s *Session) Subscribe(fn func([]byte)) (replay string, cancel func()) {
	s.mu.Lock()
	if s.subs == nil {
		s.subs = map[int]func([]byte){}
	}
	id := s.nextSub
	s.nextSub++
	s.subs[id] = fn
	replay = string(s.replay)
	s.mu.Unlock()
	return replay, func() {
		s.mu.Lock()
		delete(s.subs, id)
		s.mu.Unlock()
	}
}

// fanout delivers a chunk to every subscriber. Sinks are invoked without the
// lock held: a slow WebSocket writer must not stall the PTY read pump.
func (s *Session) fanout(chunk []byte) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.replay = append(s.replay, chunk...)
	if len(s.replay) > replayLimit {
		s.replay = append(s.replay[:0], s.replay[len(s.replay)-replayLimit:]...)
	}
	subs := make([]func([]byte), 0, len(s.subs))
	for _, fn := range s.subs {
		subs = append(subs, fn)
	}
	s.mu.Unlock()
	for _, fn := range subs {
		fn(chunk)
	}
}

// pump reads the ConPTY until the pipe closes, which is what the console host
// does when the shell process exits. One goroutine per session, 32 KiB stack.
func (s *Session) pump() {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.fanout(chunk)
		}
		if err != nil {
			return
		}
	}
}

// watchExit reaps the process so no zombie is left, records the exit code and
// notifies the manager.
func (s *Session) watchExit() {
	code, err := s.pty.Wait(context.Background())
	s.mu.Lock()
	if err != nil {
		code = ^uint32(0)
	}
	ic := int(int32(code))
	s.exitCode = &ic
	s.mu.Unlock()

	close(s.done)
	if s.onExit != nil {
		s.onExit(s.ID, ic)
	}
}

// Info returns the current public snapshot.
func (s *Session) Info() Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := "running"
	if s.closed {
		status = "exited"
	}
	return Info{
		ID: s.ID, Profile: s.Profile, Name: s.Name, Pid: s.pty.Pid(),
		Cwd: s.Cwd, Status: status, ExitCode: s.exitCode,
	}
}

// Done is closed once the shell process has exited.
func (s *Session) Done() <-chan struct{} { return s.done }

// Close terminates the shell and releases the ConPTY handles. Closing the
// pseudo-console makes the attached process receive CTRL_CLOSE_EVENT, which is
// how Windows Terminal ends a pane. Idempotent, so a tab close racing with a
// spontaneous exit cannot double-free.
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.subs = nil
	s.replay = nil
	s.mu.Unlock()
	return s.pty.Close()
}

// Manager owns every live session.
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	order    []string
	nextID   int
	home     string
	ExitHook ExitHook
	// Seed is called with a session id right after it spawns, so the owner can
	// prime that session's command history (a shell loads its history file at
	// startup; this is the equivalent for a pane).
	Seed func(id string)
}

func NewManager(onExit ExitHook) *Manager {
	home, _ := os.UserHomeDir()
	return &Manager{
		sessions: map[string]*Session{},
		home:     home,
		ExitHook: onExit,
	}
}

// Options describes a session to spawn.
type Options struct {
	Profile config.Profile
	Cols    int
	Rows    int
	Cwd     string
	Env     map[string]string
}

// spawn is the process factory. It is a variable so tests can substitute a
// fake ConPTY without a real console host; production always uses spawnProcess.
var spawn = spawnProcess

// Start spawns a shell and returns its session id.
func (m *Manager) Start(opts Options) (string, error) {
	cols, rows := clampSize(opts.Cols, opts.Rows)
	cwd := opts.Cwd
	if cwd == "" {
		cwd = m.home
	}
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		cwd = m.home
	}

	cmdline := CommandLine(opts.Profile.Shell, opts.Profile.Args)
	env := buildEnv(opts.Profile, opts.Env)

	p, err := spawn(cmdline, cwd, env, cols, rows)
	if err != nil {
		return "", fmt.Errorf("spawn %s: %w", opts.Profile.Shell, err)
	}

	name := opts.Profile.Name
	if name == "" {
		name = opts.Profile.ID
	}
	s := &Session{
		ID: "", Profile: opts.Profile.ID, Name: name, Cwd: cwd,
		pty: p, done: make(chan struct{}),
		colsRows: [2]int{cols, rows}, onExit: m.ExitHook,
	}

	m.mu.Lock()
	m.nextID++
	s.ID = fmt.Sprintf("s%d", m.nextID)
	m.sessions[s.ID] = s
	m.order = append(m.order, s.ID)
	m.mu.Unlock()

	go s.pump()
	go s.watchExit()
	if m.Seed != nil {
		m.Seed(s.ID)
	}
	return s.ID, nil
}

func clampSize(cols, rows int) (int, int) {
	if cols < 1 {
		cols = 120
	}
	if rows < 1 {
		rows = 30
	}
	if cols > maxDimension {
		cols = maxDimension
	}
	if rows > maxDimension {
		rows = maxDimension
	}
	return cols, rows
}

// Get returns a session by id.
func (m *Manager) Get(id string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, ErrNotFound
	}
	return s, nil
}

// List returns every session in creation order.
func (m *Manager) List() []Info {
	m.mu.RLock()
	ids := append([]string{}, m.order...)
	m.mu.RUnlock()
	out := make([]Info, 0, len(ids))
	for _, id := range ids {
		m.mu.RLock()
		s, ok := m.sessions[id]
		m.mu.RUnlock()
		if !ok {
			continue
		}
		out = append(out, s.Info())
	}
	return out
}

// Close kills one session and drops it from the registry. A closed session is
// gone from the user's point of view (the pane has no shell), so keeping it in
// List would leave a dead entry in the sidebar for the rest of the run.
func (m *Manager) Close(id string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	// Drop it from the registry before closing, so the exit notification that
	// Close triggers already describes a list without it.
	m.mu.Lock()
	delete(m.sessions, id)
	for i, existing := range m.order {
		if existing == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	m.mu.Unlock()

	return s.Close()
}

// Shutdown kills every session so no shell outlives the app, and empties the
// registry so nothing keeps describing the shells it just reaped.
func (m *Manager) Shutdown() {
	m.mu.RLock()
	ids := append([]string{}, m.order...)
	m.mu.RUnlock()

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_ = m.Close(id)
		}(id)
	}
	wg.Wait()
}

// CommandLine builds a quoted Windows command line for shell plus args.
func CommandLine(shell string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, QuoteArg(shell))
	for _, a := range args {
		parts = append(parts, QuoteArg(a))
	}
	return strings.Join(parts, " ")
}

// QuoteArg quotes one argument following the CommandLineToArgvW rules, which is
// what the C runtime in every child process applies when it parses argv.
func QuoteArg(a string) string {
	if a == "" {
		return `""`
	}
	if !strings.ContainsAny(a, " \t\n\v\"") {
		return a
	}
	var b strings.Builder
	b.WriteByte('"')
	backslashes := 0
	for _, r := range a {
		switch r {
		case '\\':
			backslashes++
			continue
		case '"':
			b.WriteString(strings.Repeat(`\`, backslashes*2+1))
			b.WriteRune('"')
			backslashes = 0
			continue
		}
		if backslashes > 0 {
			b.WriteString(strings.Repeat(`\`, backslashes))
			backslashes = 0
		}
		b.WriteRune(r)
	}
	if backslashes > 0 {
		b.WriteString(strings.Repeat(`\`, backslashes*2))
	}
	b.WriteByte('"')
	return b.String()
}

// buildEnv layers the profile env and per-session overrides over the parent
// environment, replacing (not duplicating) overridden keys. Windows environment
// names are case-insensitive.
func buildEnv(p config.Profile, extra map[string]string) []string {
	overrides := map[string]string{}
	for k, v := range p.Env {
		overrides[strings.ToUpper(k)] = k + "=" + v
	}
	for k, v := range extra {
		overrides[strings.ToUpper(k)] = k + "=" + v
	}
	base := os.Environ()
	env := make([]string, 0, len(base)+len(overrides))
	for _, kv := range base {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			env = append(env, kv)
			continue
		}
		if _, ok := overrides[strings.ToUpper(kv[:eq])]; ok {
			continue
		}
		env = append(env, kv)
	}
	for _, kv := range overrides {
		env = append(env, kv)
	}
	return env
}
