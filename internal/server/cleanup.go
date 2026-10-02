package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"workstation/internal/branches"
	"workstation/internal/workspace"
)

const maxCleanupBranches = 200

// CleanupService lists and deletes merged local branches (implemented by
// branches.Service). Deleting is destructive, so the service re-checks every
// branch itself; this layer only validates the request shape and the repo.
type CleanupService interface {
	CleanupPreview(repo string, wts map[string]branches.Worktree, days int, refresh bool) (branches.CleanupResponse, error)
	CleanupDelete(repo string, wts map[string]branches.Worktree, days int, req []branches.DeleteRequest) ([]branches.DeleteResult, error)
}

// WithCleanup enables GET and POST /api/cleanup.
func WithCleanup(c CleanupService) Option { return func(o *options) { o.cleanup = c } }

// worktreesByBranch maps each checked-out branch to its worktree. Those branches
// are never offered for deletion.
func worktreesByBranch(repo workspace.Repo) map[string]branches.Worktree {
	wts := map[string]branches.Worktree{}
	for _, wt := range repo.Worktrees {
		if wt.Branch != "" {
			wts[wt.Branch] = branches.Worktree{Name: wt.Name, Path: wt.Path}
		}
	}
	return wts
}

// handleCleanupPreview lists the branches that could be deleted. Nothing is changed.
func (s *Server) handleCleanupPreview(w http.ResponseWriter, r *http.Request) {
	if s.cleanup == nil {
		http.NotFound(w, r)
		return
	}
	ws, err := s.snapshot()
	if err != nil {
		internalError(w, "cleanup", err)
		return
	}
	q := r.URL.Query()
	repo, ok := findRepo(ws, q.Get("repo"))
	if !ok {
		http.Error(w, "unknown repo", http.StatusBadRequest)
		return
	}
	days := 0
	if v := q.Get("days"); v != "" {
		if days, err = strconv.Atoi(v); err != nil {
			http.Error(w, "days must be a number", http.StatusBadRequest)
			return
		}
	}

	resp, err := s.cleanup.CleanupPreview(repo.Path, worktreesByBranch(repo), days, q.Get("refresh") == "1")
	if errors.Is(err, branches.ErrNoDefaultBranch) {
		http.Error(w, branches.ErrNoDefaultBranch.Error(), http.StatusUnprocessableEntity)
		return
	}
	if err != nil {
		internalError(w, "cleanup", err)
		return
	}
	writeJSON(w, resp)
}

type cleanupRequest struct {
	Repo     string                   `json:"repo"`
	Days     int                      `json:"days"`
	Branches []branches.DeleteRequest `json:"branches"`
}

// handleCleanupDelete deletes the branches the user ticked in the preview.
func (s *Server) handleCleanupDelete(w http.ResponseWriter, r *http.Request) {
	if s.cleanup == nil {
		http.NotFound(w, r)
		return
	}
	var req cleanupRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<18)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if msg := validateCleanupRequest(req); msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	ws, err := s.snapshot()
	if err != nil {
		internalError(w, "cleanup", err)
		return
	}
	repo, ok := findRepo(ws, req.Repo)
	if !ok {
		http.Error(w, "unknown repo", http.StatusBadRequest)
		return
	}

	results, err := s.cleanup.CleanupDelete(repo.Path, worktreesByBranch(repo), req.Days, req.Branches)
	if errors.Is(err, branches.ErrNoDefaultBranch) {
		http.Error(w, branches.ErrNoDefaultBranch.Error(), http.StatusUnprocessableEntity)
		return
	}
	if err != nil {
		internalError(w, "cleanup", err)
		return
	}
	writeJSON(w, map[string]any{"results": results})
}

func validateCleanupRequest(req cleanupRequest) string {
	if len(req.Branches) == 0 {
		return "no branches selected"
	}
	if len(req.Branches) > maxCleanupBranches {
		return "too many branches in one request"
	}
	for _, b := range req.Branches {
		if b.Branch == "" || strings.HasPrefix(b.Branch, "-") || b.SHA == "" {
			return "each branch needs a name and the commit shown in the preview"
		}
	}
	return ""
}
