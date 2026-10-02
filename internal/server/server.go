// Package server exposes the workspace over a localhost-only HTTP API and
// serves the embedded frontend.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"workstation/internal/branches"
	"workstation/internal/claude"
	"workstation/internal/workspace"
)

type BuildFunc func() (workspace.Workspace, error)

type Server struct {
	build    BuildFunc
	launcher Launcher
	web      fs.FS
	plans    PlanReader
	branches BranchService

	mu     sync.Mutex
	last   *workspace.Workspace // snapshot actions are validated against
	flight *flight              // workspace build in progress, if any
}

// Option customises New.
type Option func(*options)

type options struct {
	plans    PlanReader
	branches BranchService
}

// PlanReader loads a session's plan by slug (implemented by claude.CLI).
type PlanReader interface {
	ReadPlan(slug string) (claude.Plan, error)
}

// BranchService builds a repo's branch graph (implemented by branches.Service).
type BranchService interface {
	Graph(repo string, wts map[string]branches.Worktree, includeMerged, refresh bool) (branches.Response, error)
}

// WithPlans enables GET /api/plan.
func WithPlans(p PlanReader) Option { return func(o *options) { o.plans = p } }

// WithBranches enables GET /api/branches.
func WithBranches(b BranchService) Option { return func(o *options) { o.branches = b } }

// NewHTTPServer wraps h in an http.Server with explicit timeouts, so slow or
// stalled connections cannot pile up. WriteTimeout leaves room for a cold
// workspace build (many git processes).
func NewHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
}

// RequireLoopback refuses any listen address that is not explicitly a loopback
// host with a port. There is no authentication, so the server must never be
// reachable from the network (an empty host or 0.0.0.0 would listen on every
// interface).
func RequireLoopback(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return fmt.Errorf("listen address %q must be host:port", addr)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("refusing to listen on %q: only loopback addresses are allowed (the server has no authentication)", addr)
}

// New returns the HTTP handler. web is the built frontend (may be empty).
func New(build BuildFunc, launcher Launcher, web fs.FS, opts ...Option) http.Handler {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	s := &Server{build: build, launcher: launcher, web: web, plans: o.plans, branches: o.branches}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspace", s.handleWorkspace)
	mux.HandleFunc("GET /api/plan", s.handlePlan)
	mux.HandleFunc("GET /api/branches", s.handleBranches)
	mux.HandleFunc("/api/actions/{action}", s.handleAction)
	mux.HandleFunc("/", s.handleStatic)
	return guard(mux)
}

// csp: scripts only from the app itself; no framing, no forms, no base tag.
// Styles allow inline because Vue sets element styles; that does not execute code.
const csp = "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

func setSecurityHeaders(h http.Header) {
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
}

// guard sets browser security headers and blocks DNS-rebinding (Host must be
// loopback) and cross-site requests (Sec-Fetch-Site must be same-origin or
// none; Origin, when sent, must match Host; mutating requests must be JSON,
// which browsers will not send cross-site without a preflight we never grant).
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w.Header())
		if !isLoopbackHost(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			http.Error(w, "forbidden cross-site request", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			if u, err := url.Parse(origin); err != nil || u.Host != r.Host {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}
		if r.Method == http.MethodPost && r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackHost(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	switch host {
	case "127.0.0.1", "localhost", "[::1]", "::1":
		return true
	}
	return false
}

type workspaceResponse struct {
	workspace.Workspace
	Capabilities struct {
		Editor string `json:"editor"`
	} `json:"capabilities"`
}

func (s *Server) handleWorkspace(w http.ResponseWriter, _ *http.Request) {
	ws, err := s.refresh()
	if err != nil {
		internalError(w, "workspace", err)
		return
	}
	resp := workspaceResponse{Workspace: ws}
	resp.Capabilities.Editor = s.launcher.EditorName()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

// flight is a workspace build in progress that concurrent callers wait for.
type flight struct {
	done chan struct{}
	ws   workspace.Workspace
	err  error
}

// refresh rebuilds the workspace. A build spawns many git processes, so
// concurrent requests share the one already running instead of starting more.
// Once it finishes, the next request builds again (Refresh stays fresh).
func (s *Server) refresh() (workspace.Workspace, error) {
	s.mu.Lock()
	if f := s.flight; f != nil {
		s.mu.Unlock()
		<-f.done
		return f.ws, f.err
	}
	f := &flight{done: make(chan struct{})}
	s.flight = f
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.flight = nil
		if f.err == nil {
			ws := f.ws
			s.last = &ws
		}
		s.mu.Unlock()
		close(f.done)
	}()
	f.err = errors.New("workspace build did not complete") // replaced unless build panics
	f.ws, f.err = s.build()
	return f.ws, f.err
}

func (s *Server) snapshot() (workspace.Workspace, error) {
	s.mu.Lock()
	last := s.last
	s.mu.Unlock()
	if last != nil {
		return *last, nil
	}
	return s.refresh()
}

// handlePlan returns the plan of a session known to the last snapshot. The
// client sends a session id, never a path or slug.
func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request) {
	if s.plans == nil {
		http.NotFound(w, r)
		return
	}
	ws, err := s.snapshot()
	if err != nil {
		internalError(w, "plan", err)
		return
	}
	sess, ok := findSession(ws, r.URL.Query().Get("session"))
	if !ok || !sess.HasPlan || sess.Slug == "" {
		http.NotFound(w, r)
		return
	}
	plan, err := s.plans.ReadPlan(sess.Slug)
	if errors.Is(err, os.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		internalError(w, "plan", err)
		return
	}
	writeJSON(w, plan)
}

// handleBranches builds the branch graph of a repo known to the last snapshot.
// merged=1 includes recently merged branches; refresh=1 bypasses the PR cache.
func (s *Server) handleBranches(w http.ResponseWriter, r *http.Request) {
	if s.branches == nil {
		http.NotFound(w, r)
		return
	}
	ws, err := s.snapshot()
	if err != nil {
		internalError(w, "branches", err)
		return
	}
	repo, ok := findRepo(ws, r.URL.Query().Get("repo"))
	if !ok {
		http.Error(w, "unknown repo", http.StatusBadRequest)
		return
	}

	wts := map[string]branches.Worktree{}
	for _, wt := range repo.Worktrees {
		if wt.Branch != "" {
			wts[wt.Branch] = branches.Worktree{Name: wt.Name, Path: wt.Path}
		}
	}
	q := r.URL.Query()
	resp, err := s.branches.Graph(repo.Path, wts, q.Get("merged") == "1", q.Get("refresh") == "1")
	if errors.Is(err, branches.ErrNoDefaultBranch) {
		http.Error(w, branches.ErrNoDefaultBranch.Error(), http.StatusUnprocessableEntity)
		return
	}
	if err != nil {
		internalError(w, "branches", err)
		return
	}
	writeJSON(w, resp)
}

// internalError logs the real cause and tells the client only that something
// failed: errors carry paths, git stderr and file names that are none of a
// browser page's business.
func internalError(w http.ResponseWriter, route string, err error) {
	log.Printf("workstation: %s: %v", route, err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func findRepo(ws workspace.Workspace, path string) (workspace.Repo, bool) {
	for _, r := range ws.Repos {
		if r.Path == path {
			return r, true
		}
	}
	return workspace.Repo{}, false
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

type actionRequest struct {
	Path      string `json:"path"`
	SessionID string `json:"sessionId"`
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	action := r.PathValue("action")
	if action != "terminal" && action != "editor" && action != "resume" {
		http.NotFound(w, r)
		return
	}
	var req actionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	ws, err := s.snapshot()
	if err != nil {
		internalError(w, "action", err)
		return
	}

	if err := s.run(action, req, ws); err != nil {
		var bad badRequest
		if errors.As(err, &bad) {
			http.Error(w, bad.Error(), http.StatusBadRequest) // written for the user
			return
		}
		internalError(w, "action "+action, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type badRequest string

func (b badRequest) Error() string { return string(b) }

// run executes an action only against paths/sessions present in the last
// computed workspace, never against arbitrary client-supplied values.
func (s *Server) run(action string, req actionRequest, ws workspace.Workspace) error {
	if action == "resume" {
		sess, ok := findSession(ws, req.SessionID)
		if !ok || !sess.Resumable {
			return badRequest("unknown or non-resumable session")
		}
		if _, err := os.Stat(sess.Cwd); err != nil {
			return badRequest("session directory no longer exists")
		}
		return s.launcher.Resume(sess.Cwd, sess.ResumeID())
	}

	if !knownWorktree(ws, req.Path) {
		return badRequest("unknown worktree path")
	}
	if action == "editor" {
		return s.launcher.Editor(req.Path)
	}
	return s.launcher.Terminal(req.Path)
}

func knownWorktree(ws workspace.Workspace, path string) bool {
	for _, r := range ws.Repos {
		for _, w := range r.Worktrees {
			if w.Path == path {
				return true
			}
		}
	}
	return false
}

func findSession(ws workspace.Workspace, id string) (claude.Session, bool) {
	if id == "" {
		return claude.Session{}, false
	}
	for _, r := range ws.Repos {
		for _, w := range r.Worktrees {
			for _, s := range w.Sessions {
				if s.ID == id {
					return s, true
				}
			}
		}
	}
	for _, u := range ws.Unlinked {
		if u.Session.ID == id {
			return u.Session, true
		}
	}
	return claude.Session{}, false
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if _, err := fs.Stat(s.web, "index.html"); err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("Frontend not built. Run `pnpm install && pnpm build` in web/, or `pnpm dev` for the dev server.\n"))
		return
	}
	http.FileServerFS(s.web).ServeHTTP(w, r)
}
