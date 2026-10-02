// Package server exposes the workspace over a localhost-only HTTP API and
// serves the embedded frontend.
package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"

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

	mu   sync.Mutex
	last *workspace.Workspace // snapshot actions are validated against
}

// Option customises New.
type Option func(*options)

type options struct {
	lan      bool
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

// WithLAN also accepts requests addressed to a private-range IP (10/8,
// 172.16/12, 192.168/16), so the page can be opened from another device on the
// local network. There is no authentication: use only on a network you trust.
func WithLAN() Option { return func(o *options) { o.lan = true } }

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
	return guard(mux, o.lan)
}

// guard blocks DNS-rebinding (Host must be loopback, or a private IP in LAN
// mode) and cross-site requests (Origin, when sent, must match Host; mutating
// requests must be JSON, which browsers will not send cross-site without a
// preflight we never grant).
func guard(next http.Handler, lan bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAllowedHost(r.Host, lan) {
			http.Error(w, "forbidden host", http.StatusForbidden)
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

func isAllowedHost(hostport string, lan bool) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	switch host {
	case "127.0.0.1", "localhost", "[::1]", "::1":
		return true
	}
	if !lan {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsPrivate()
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp := workspaceResponse{Workspace: ws}
	resp.Capabilities.Editor = s.launcher.EditorName()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) refresh() (workspace.Workspace, error) {
	ws, err := s.build()
	if err != nil {
		return ws, err
	}
	s.mu.Lock()
	s.last = &ws
	s.mu.Unlock()
	return ws, nil
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, resp)
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := s.run(action, req, ws); err != nil {
		code := http.StatusInternalServerError
		var bad badRequest
		if errors.As(err, &bad) {
			code = http.StatusBadRequest
		}
		http.Error(w, err.Error(), code)
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
