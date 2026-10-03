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
	// dormantDays > 0 also offers worktrees idle for that many days (see wtclean.Input.IncludeDormant).
	Preview(repo workspace.Repo, days, dormantDays int) (wtclean.Response, error)
	Remove(repo workspace.Repo, days, dormantDays int, req []wtclean.RemoveRequest) ([]wtclean.RemoveResult, error)
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
	days, ok := intParam(w, q.Get("days"), "days")
	if !ok {
		return
	}
	dormantDays, ok := intParam(w, q.Get("dormantDays"), "dormantDays")
	if !ok {
		return
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

	resp, err := s.worktreeCleanup.Preview(repo, days, dormantDays)
	if s.cleanupFailed(w, err) {
		return
	}
	writeJSON(w, resp)
}

type worktreeCleanupRequest struct {
	Repo      string                  `json:"repo"`
	Days      int                     `json:"days"`
	Dormant   int                     `json:"dormantDays"` // 0 = only merged worktrees
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

	results, err := s.worktreeCleanup.Remove(repo, req.Days, req.Dormant, req.Worktrees)
	if s.cleanupFailed(w, err) {
		return
	}
	s.forgetNotes(results)
	writeJSON(w, map[string]any{"results": results})
}

// forgetNotes drops the note and star of every worktree that was removed.
func (s *Server) forgetNotes(results []wtclean.RemoveResult) {
	if s.notes == nil {
		return
	}
	for _, r := range results {
		if r.Removed {
			_ = s.notes.Delete(r.Path) // a leftover note is harmless: it is only ever shown for existing worktrees
		}
	}
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

// intParam reads an optional whole number from the query; it answers 400 itself when it is not one.
func intParam(w http.ResponseWriter, raw, name string) (int, bool) {
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		http.Error(w, name+" must be a number", http.StatusBadRequest)
		return 0, false
	}
	return n, true
}
