// Package server hosts the local HTTP API and the WebSocket terminal channel.
//
// The app is a desktop shell around a loopback server: the WebView loads
// http://127.0.0.1:<port>, which keeps UI reloads, devtools and the browser
// fallback mode working with one code path.
package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"ohmyjo/internal/config"
	"ohmyjo/internal/history"
	"ohmyjo/internal/profiles"
	"ohmyjo/internal/session"
	"ohmyjo/internal/system"
)

// Version is stamped into the ready frame and the /api/health payload.
const Version = "0.1.0"

// Protocol is the wire protocol revision the frontend must match.
const Protocol = 1

// Assets resolves the built frontend bundle.
type Assets interface {
	// Open returns the body and content type for a frontend path ("" = index).
	Open(path string) ([]byte, string, error)
}

type clientMessage struct {
	Type    string `json:"type"`
	ReqID   string `json:"reqId"`
	ID      string `json:"id"`
	Profile string `json:"profile"`
	Cwd     string `json:"cwd"`
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
	Data    string `json:"data"`
	Query   string `json:"query"`
	Limit   int    `json:"limit"`
	Version int    `json:"version"`
	Client  string `json:"client"`
}

type serverMessage struct {
	Type     string             `json:"type"`
	Version  string             `json:"version,omitempty"`
	Protocol int                `json:"protocol,omitempty"`
	Platform string             `json:"platform,omitempty"`
	ReqID    string             `json:"reqId,omitempty"`
	ID       string             `json:"id,omitempty"`
	Profile  string             `json:"profile,omitempty"`
	Name     string             `json:"name,omitempty"`
	Pid      int                `json:"pid,omitempty"`
	Cwd      string             `json:"cwd,omitempty"`
	Data     string             `json:"data,omitempty"`
	Code     string             `json:"code,omitempty"`
	Message  string             `json:"message,omitempty"`
	ExitCode int                `json:"exitCode,omitempty"`
	Replay   string             `json:"replay,omitempty"`
	Info     *session.Info      `json:"info,omitempty"`
	History  []string           `json:"history,omitempty"`
	Sessions []session.Info     `json:"sessions,omitempty"`
	Config   *config.Config     `json:"config,omitempty"`
	Git      *system.GitInfo    `json:"git,omitempty"`
	Dir      *system.DirListing `json:"dir,omitempty"`
	Fonts    []string           `json:"fonts,omitempty"`
	Error    string             `json:"error,omitempty"`
}

// Server wires config, profiles, sessions and the system service into HTTP
// handlers plus one WebSocket endpoint per UI window.
type Server struct {
	Cfg      *config.Loader
	Sessions *session.Manager
	Sys      *system.Service
	Assets   Assets
	// History records the commands typed in each session; HistFile persists the
	// shared, cross-session history the way a shell's own history file does.
	History  *history.Store
	HistFile *history.File

	mu      sync.RWMutex
	sockets map[*wsClient]struct{}

	httpSrv  *http.Server
	listener net.Listener
	url      string
}

// New builds a server. onExit is invoked when a shell dies on its own.
func New(cfg *config.Loader, mgr *session.Manager, sys *system.Service, assets Assets, hist *history.Store, histFile *history.File) *Server {
	if hist == nil {
		hist = history.New()
	}
	s := &Server{
		Cfg: cfg, Sessions: mgr, Sys: sys, Assets: assets,
		History: hist, HistFile: histFile,
		sockets: map[*wsClient]struct{}{},
	}
	// Sessions exiting spontaneously must reach every open window so panes can
	// be marked dead without the frontend polling.
	mgr.ExitHook = func(id string, code int) {
		if cmds := s.History.List(id); len(cmds) > 0 {
			s.HistFile.Append(cmds...)
		}
		s.History.Forget(id)
		s.broadcast(serverMessage{Type: "exit", ID: id, ExitCode: code})
		s.broadcastSessions()
	}
	return s
}

// Listen binds the loopback port. Port 0 lets the OS choose.
func (s *Server) Listen(port int) (string, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return "", err
	}
	s.listener = ln
	s.url = "http://" + ln.Addr().String()
	return s.url, nil
}

// URL is the address the WebView should load.
func (s *Server) URL() string { return s.url }

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/profiles", s.handleProfiles)
	mux.HandleFunc("/api/fonts", s.handleFonts)
	mux.HandleFunc("/api/git", s.handleGit)
	mux.HandleFunc("/api/dir", s.handleDir)
	mux.HandleFunc("/api/reveal", s.handleReveal)
	mux.HandleFunc("/api/open", s.handleOpen)
	mux.HandleFunc("/api/pick", s.handlePick)
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/closeall", s.handleCloseAll)
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/", s.handleStatic)
	return withNoStore(mux)
}

// Serve blocks until the server stops.
func (s *Server) Serve() error {
	if s.listener == nil {
		if _, err := s.Listen(0); err != nil {
			return err
		}
	}
	s.httpSrv = &http.Server{
		Handler:           s.handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	err := s.httpSrv.Serve(s.listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Close stops the HTTP server and every live shell.
func (s *Server) Close() {
	if s.httpSrv != nil {
		_ = s.httpSrv.Close()
	} else if s.listener != nil {
		_ = s.listener.Close()
	}
	s.Sessions.Shutdown()
}

// apiJSON writes a JSON response; failures are logged, not surfaced, because
// the frontend already has the HTTP status.
func apiJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("write response: %v", err)
	}
}

func withNoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The bundle is served from disk during development; caching it would
		// hide edits on reload.
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	apiJSON(w, http.StatusOK, map[string]any{
		"version":  Version,
		"protocol": Protocol,
		"uptime":   time.Since(started).Seconds(),
		// The settings panel shows where config.json lives; the path is a
		// runtime concern (a --config flag can move it), so the UI asks.
		"configPath": s.Cfg.Path(),
	})
}

var started = time.Now()

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		apiJSON(w, http.StatusOK, s.wireConfig())
	case http.MethodPut, http.MethodPost:
		var incoming config.Config
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&incoming); err != nil {
			apiJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.Cfg.Mutate(func(c *config.Config) error {
			// Profiles are owned by the config file (the UI only reads them), so
			// they are carried over from the live config rather than overwritten
			// by a snapshot that may predate them — and which holds the
			// *resolved* list rather than the user's overrides.
			stored := c.Profiles
			*c = incoming
			c.Profiles = stored
			return nil
		}); err != nil {
			apiJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if err := s.Cfg.Save(); err != nil {
			apiJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		apiJSON(w, http.StatusOK, s.wireConfig())
	default:
		apiJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// wireConfig is the config as the frontend needs it: the same shape as the file
// plus the shell profiles that actually exist on this machine. The UI resolves
// profile ids (default profile, restored panes) the moment the config arrives,
// so shipping the raw file's override list there would silently downgrade every
// pane to the last built-in fallback.
func (s *Server) wireConfig() *config.Config {
	cfg := s.Cfg.Get()
	out := *cfg
	home, _ := userHome()
	out.Profiles = profiles.Resolve(cfg.Profiles, home)
	return &out
}

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	cfg := s.Cfg.Get()
	home, _ := userHome()
	apiJSON(w, http.StatusOK, profiles.Resolve(cfg.Profiles, home))
}

func (s *Server) handleFonts(w http.ResponseWriter, r *http.Request) {
	apiJSON(w, http.StatusOK, s.Sys.FontFamilies())
}

func (s *Server) handleGit(w http.ResponseWriter, r *http.Request) {
	apiJSON(w, http.StatusOK, s.Sys.Git(r.URL.Query().Get("path")))
}

func (s *Server) handleDir(w http.ResponseWriter, r *http.Request) {
	apiJSON(w, http.StatusOK, s.Sys.ListDir(r.URL.Query().Get("path")))
}

func (s *Server) handleReveal(w http.ResponseWriter, r *http.Request) {
	if err := s.Sys.Reveal(r.URL.Query().Get("path")); err != nil {
		apiJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	apiJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	if err := s.Sys.Open(r.URL.Query().Get("path")); err != nil {
		apiJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	apiJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handlePick runs the native folder/file picker. The response is a path, or an
// empty path when the user cancelled.
func (s *Server) handlePick(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	title := r.URL.Query().Get("title")
	var (
		path string
		err  error
	)
	if kind == "file" {
		path, err = s.Sys.PickFile(title)
	} else {
		path, err = s.Sys.PickFolder(title)
	}
	if err != nil {
		apiJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	apiJSON(w, http.StatusOK, map[string]string{"path": path})
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	apiJSON(w, http.StatusOK, s.Sessions.List())
}

func (s *Server) handleCloseAll(w http.ResponseWriter, r *http.Request) {
	s.Sessions.Shutdown()
	// Every window has to learn that the sessions are gone; otherwise its panes
	// keep claiming to be running. Reached over HTTP rather than the websocket,
	// so nothing else would notify the existing sockets.
	s.broadcastSessions()
	apiJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleStatic serves the embedded (or on-disk) frontend bundle, falling back
// to index.html so client-side routing works.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if s.Assets == nil {
		http.Error(w, "frontend bundle not embedded; run `npm install && npm run build` in web/", http.StatusNotFound)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	body, ctype, err := s.Assets.Open(path)
	isFallback := false
	if err != nil {
		body, ctype, err = s.Assets.Open("")
		if err != nil {
			http.Error(w, "frontend bundle missing index.html", http.StatusInternalServerError)
			return
		}
		// An unknown path with an extension is a genuine 404, not a route.
		if ext := pathExt(path); ext != "" && ext != ".html" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		isFallback = true
	}
	// The WebView2 profile is persistent, so an unchanged response would be
	// served from cache — including index.html still naming the previous
	// bundle. The shell must always be revalidated (so a new build is picked
	// up), while hashed asset filenames can be cached hard because a content
	// change always produces a new name.
	if isFallback || path == "" || path == "index.html" {
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	w.Header().Set("Content-Type", ctype)
	_, _ = w.Write(body)
}

func pathExt(p string) string {
	i := strings.LastIndexByte(p, '.')
	if i < 0 {
		return ""
	}
	return p[i:]
}

func userHome() (string, error) { return os.UserHomeDir() }

// ---------------------------------------------------------------- websocket

type wsClient struct {
	conn *websocket.Conn

	mu     sync.Mutex
	subs   map[string]func()
	closed bool
}

func (c *wsClient) send(msg serverMessage) {
	payload, err := json.Marshal(msg)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		c.closed = true
	}
}

func (c *wsClient) detachAll() {
	c.mu.Lock()
	subs := c.subs
	c.subs = map[string]func(){}
	c.closed = true
	c.mu.Unlock()
	for _, cancel := range subs {
		cancel()
	}
}

func (c *wsClient) put(id string, cancel func()) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		cancel()
		return
	}
	if old, ok := c.subs[id]; ok {
		old()
	}
	c.subs[id] = cancel
	c.mu.Unlock()
}

func (c *wsClient) remove(id string) {
	c.mu.Lock()
	cancel, ok := c.subs[id]
	delete(c.subs, id)
	c.mu.Unlock()
	if ok {
		cancel()
	}
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  64 * 1024,
	WriteBufferSize: 64 * 1024,
	// The server only ever listens on loopback, and the desktop shell loads the
	// page from a file://-like origin during devtools use.
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &wsClient{conn: conn, subs: map[string]func(){}}
	s.mu.Lock()
	s.sockets[client] = struct{}{}
	s.mu.Unlock()

	defer func() {
		client.detachAll()
		s.mu.Lock()
		delete(s.sockets, client)
		s.mu.Unlock()
		_ = conn.Close()
	}()

	client.send(serverMessage{
		Type: "ready", Version: Version, Protocol: Protocol, Platform: "windows",
	})
	s.sendConfig(client)

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var msg clientMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			client.send(serverMessage{Type: "error", Code: "bad_message", Message: err.Error()})
			continue
		}
		if !s.dispatch(client, msg) {
			return
		}
	}
}

func (s *Server) sendConfig(c *wsClient) {
	c.send(serverMessage{Type: "config", Config: s.wireConfig()})
}

// BroadcastConfig pushes the current config to every open window. It is wired
// to the config loader so an edit to config.json hot-applies in the UI.
func (s *Server) BroadcastConfig() {
	s.broadcast(serverMessage{Type: "config", Config: s.wireConfig()})
}

// broadcast delivers a message to every connected window.
func (s *Server) broadcast(msg serverMessage) {
	s.mu.RLock()
	clients := make([]*wsClient, 0, len(s.sockets))
	for c := range s.sockets {
		clients = append(clients, c)
	}
	s.mu.RUnlock()
	for _, c := range clients {
		c.send(msg)
	}
}

func (s *Server) broadcastSessions() {
	s.broadcast(serverMessage{Type: "sessions", Sessions: s.Sessions.List()})
}

// dispatch handles one client frame; it returns false to close the socket.
func (s *Server) dispatch(c *wsClient, msg clientMessage) bool {
	switch msg.Type {
	case "hello":
		if msg.Version != Protocol {
			c.send(serverMessage{
				Type: "error", Code: "protocol_mismatch",
				Message: fmt.Sprintf("frontend protocol %d, backend %d", msg.Version, Protocol),
			})
		}
		s.sendConfig(c)
		c.send(serverMessage{Type: "sessions", Sessions: s.Sessions.List()})

	case "create":
		s.handleCreate(c, msg)

	case "attach":
		s.handleAttach(c, msg)

	case "detach":
		c.remove(msg.ID)

	case "input":
		sess, err := s.Sessions.Get(msg.ID)
		if err != nil {
			c.send(serverMessage{Type: "error", ID: msg.ID, Code: "not_found", Message: "session gone"})
			return true
		}
		data, err := base64.StdEncoding.DecodeString(msg.Data)
		if err != nil {
			c.send(serverMessage{Type: "error", ID: msg.ID, Code: "bad_payload", Message: err.Error()})
			return true
		}
		if err := sess.Write(data); err != nil {
			c.send(serverMessage{Type: "error", ID: msg.ID, Code: "write_failed", Message: err.Error()})
		} else {
			// Capturing here is what makes a command land in the history the
			// moment Enter arrives. The keystrokes are the only source: a console
			// shell echoes them back wrapped in cursor moves and colour, so trying
			// to read commands out of the echo would mean parsing a repaint
			// stream, and it would also throw away a line typed before the socket
			// attached.
			if cmd := s.History.Type(msg.ID, string(data)); cmd != "" {
				// The history panel is opened on demand and would otherwise only
				// learn about this command from its own request, which leaves it
				// blank until that round trip lands. Pushing the one new command
				// keeps it correct the moment a pane is switched to.
				s.broadcast(serverMessage{Type: "historyEntry", ID: msg.ID, History: []string{cmd}})
			}
		}

	case "resize":
		sess, err := s.Sessions.Get(msg.ID)
		if err != nil {
			return true
		}
		_ = sess.Resize(msg.Cols, msg.Rows)

	case "close":
		if err := s.Sessions.Close(msg.ID); err != nil && !errors.Is(err, session.ErrNotFound) {
			c.send(serverMessage{Type: "error", ID: msg.ID, Code: "close_failed", Message: err.Error()})
		}
		c.remove(msg.ID)
		c.send(serverMessage{Type: "closed", ID: msg.ID})
		s.broadcastSessions()

	case "restart":
		profileID := ""
		if sess, err := s.Sessions.Get(msg.ID); err == nil {
			profileID = sess.Profile
		}
		if err := s.Sessions.Close(msg.ID); err != nil && !errors.Is(err, session.ErrNotFound) {
			c.send(serverMessage{Type: "error", ReqID: msg.ReqID, Code: "close_failed", Message: err.Error()})
			return true
		}
		c.remove(msg.ID)
		s.handleCreate(c, clientMessage{
			Type: "create", ReqID: msg.ReqID, Profile: profileID, Cols: msg.Cols, Rows: msg.Rows,
		})

	case "history":
		// An empty query means "give me everything"; otherwise it filters
		// case-insensitively, newest first.
		c.send(serverMessage{
			Type: "history", ID: msg.ID,
			History: s.History.Search(msg.ID, msg.Query, msg.Limit),
		})

	case "historyClear":
		s.History.Clear(msg.ID)
		c.send(serverMessage{Type: "history", ID: msg.ID, History: []string{}})

	case "list":
		c.send(serverMessage{Type: "sessions", Sessions: s.Sessions.List()})

	case "ping":
		c.send(serverMessage{Type: "pong"})

	default:
		c.send(serverMessage{Type: "error", Code: "unknown_type", Message: msg.Type})
	}
	return true
}

func (s *Server) handleCreate(c *wsClient, msg clientMessage) {
	cfg := s.Cfg.Get()
	profileID := msg.Profile
	if profileID == "" {
		profileID = cfg.Behavior.DefaultProfile
	}
	home, _ := userHome()
	list := profiles.Resolve(cfg.Profiles, home)
	profile, ok := profiles.Find(list, profileID)
	if !ok {
		for _, p := range list {
			if p.Available {
				profile, ok = p, true
				break
			}
		}
	}
	if !ok {
		c.send(serverMessage{
			Type: "error", ReqID: msg.ReqID, Code: "no_profile",
			Message: "no shell profile is available on this machine",
		})
		return
	}
	if !profile.Available {
		c.send(serverMessage{
			Type: "error", ReqID: msg.ReqID, Code: "unavailable",
			Message: fmt.Sprintf("%s not found at %s", profile.Name, profile.Shell),
		})
		return
	}
	cwd := profiles.DefaultCwd(msg.Cwd, home)
	if profile.Cwd != "" && msg.Cwd == "" {
		cwd = profiles.DefaultCwd(profile.Cwd, home)
	}

	id, err := s.Sessions.Start(session.Options{
		Profile: profile, Cols: msg.Cols, Rows: msg.Rows, Cwd: cwd,
	})
	if err != nil {
		c.send(serverMessage{Type: "error", ReqID: msg.ReqID, Code: "spawn_failed", Message: err.Error()})
		return
	}

	sess, err := s.Sessions.Get(id)
	if err != nil {
		c.send(serverMessage{Type: "error", ReqID: msg.ReqID, Code: "spawn_failed", Message: err.Error()})
		return
	}
	info := sess.Info()
	c.send(serverMessage{
		Type: "created", ReqID: msg.ReqID, ID: id, Profile: profile.ID,
		Name: info.Name, Pid: info.Pid, Cwd: info.Cwd,
	})
	s.attach(c, sess, msg.Cols, msg.Rows)
	// Ship the pane's seeded history with the id that owns it. A fresh tab then
	// knows its recall list immediately, instead of showing an empty panel until
	// the user opens it and waits for the round trip. Reverse it so the order
	// matches what the panel's own fetch would return.
	if seeded := s.History.List(id); len(seeded) > 0 {
		reversed := make([]string, len(seeded))
		for i, cmd := range seeded {
			reversed[len(seeded)-1-i] = cmd
		}
		c.send(serverMessage{Type: "history", ID: id, History: reversed})
	}
	s.broadcastSessions()
}

func (s *Server) handleAttach(c *wsClient, msg clientMessage) {
	sess, err := s.Sessions.Get(msg.ID)
	if err != nil {
		c.send(serverMessage{Type: "error", ID: msg.ID, Code: "not_found", Message: "session gone"})
		return
	}
	if msg.Cols > 0 && msg.Rows > 0 {
		_ = sess.Resize(msg.Cols, msg.Rows)
	}
	s.attach(c, sess, msg.Cols, msg.Rows)
}

// attach subscribes one socket to a session and replays its recent output.
func (s *Server) attach(c *wsClient, sess *session.Session, cols, rows int) {
	id := sess.ID
	replay, cancel := sess.Subscribe(func(chunk []byte) {
		c.send(serverMessage{
			Type: "output", ID: id,
			Data: base64.StdEncoding.EncodeToString(chunk),
		})
	})
	c.put(id, cancel)
	info := sess.Info()
	c.send(serverMessage{
		Type: "attached", ID: id, Replay: base64.StdEncoding.EncodeToString([]byte(replay)), Info: &info,
	})
}
