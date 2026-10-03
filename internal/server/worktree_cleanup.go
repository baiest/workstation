package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"workstation/internal/branches"
	"workstation/internal/workspace"
	"workstation/internal/wtclean"
)

const maxWorktreeRemovals = 100

// WorktreeCleanupService lists and removes merged worktrees (implemented by
// wtclean.Service). Removing deletes folders, so the service re-checks every
// worktree itself; this layer validates the request and hands it a freshly
// built repo, never the snapshot the page loaded earlier.
type WorktreeCleanupService interface {
	Preview(repo workspace.Repo, days int) (wtclean.Response, error)
	Remove(repo workspace.Repo, days int, req []wtclean.RemoveRequest) ([]wtclean.RemoveResult, error)
}

// WithWorktreeCleanup enables GET and POST /api/worktree-cleanup.
func WithWorktreeCleanup(c WorktreeCleanupService) Option {
	return func(o *options) { o.worktreeCleanup = c }
}

// freshRepo rebuilds the workspace and returns the repo at path.
func (s *Server) freshRepo(path string) (workspace.Repo, bool, error) {
	ws, err := s.refresh()
	if err != nil {
		return workspace.Repo{}, false, err
	}
	repo, ok := findRepo(ws, path)
	return repo, ok, nil
}

func (s *Server) handleWorktreeCleanupPreview(w http.ResponseWriter, r *http.Request) {
	if s.worktreeCleanup == nil {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	days := 0
	if v := q.Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			http.Error(w, "days must be a number", http.StatusBadRequest)
			return
		}
		days = n
	}
	repo, ok, err := s.freshRepo(q.Get("repo"))
	if err != nil {
		internalError(w, "worktree cleanup", err)
		return
	}
	if !ok {
		http.Error(w, "unknown repo", http.StatusBadRequest)
		return
	}

	resp, err := s.worktreeCleanup.Preview(repo, days)
	if s.cleanupFailed(w, err) {
		return
	}
	writeJSON(w, resp)
}

type worktreeCleanupRequest struct {
	Repo      string                  `json:"repo"`
	Days      int                     `json:"days"`
	Worktrees []wtclean.RemoveRequest `json:"worktrees"`
}

func (s *Server) handleWorktreeCleanupRemove(w http.ResponseWriter, r *http.Request) {
	if s.worktreeCleanup == nil {
		http.NotFound(w, r)
		return
	}
	var req worktreeCleanupRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<17)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if msg := validateWorktreeRequest(req); msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	repo, ok, err := s.freshRepo(req.Repo)
	if err != nil {
		internalError(w, "worktree cleanup", err)
		return
	}
	if !ok {
		http.Error(w, "unknown repo", http.StatusBadRequest)
		return
	}

	results, err := s.worktreeCleanup.Remove(repo, req.Days, req.Worktrees)
	if s.cleanupFailed(w, err) {
		return
	}
	writeJSON(w, map[string]any{"results": results})
}

// cleanupFailed writes the response for err and reports whether there was one.
func (s *Server) cleanupFailed(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, branches.ErrNoDefaultBranch):
		http.Error(w, branches.ErrNoDefaultBranch.Error(), http.StatusUnprocessableEntity)
	default:
		internalError(w, "worktree cleanup", err)
	}
	return true
}

func validateWorktreeRequest(req worktreeCleanupRequest) string {
	if len(req.Worktrees) == 0 {
		return "no worktrees selected"
	}
	if len(req.Worktrees) > maxWorktreeRemovals {
		return "too many worktrees in one request"
	}
	for _, wt := range req.Worktrees {
		if wt.Path == "" || wt.SHA == "" {
			return "each worktree needs its path and the commit shown in the preview"
		}
	}
	return ""
}
