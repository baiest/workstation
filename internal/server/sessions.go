package server

import (
	"net/http"
	"time"

	"workstation/internal/claude"
	"workstation/internal/workspace"
)

// WithSessionsBuild enables the cheap GET /api/sessions: where each Claude session
// works, without the git state of every worktree (workspace.Builder.BuildSessions).
func WithSessionsBuild(f BuildFunc) Option { return func(o *options) { o.buildSessions = f } }

type sessionsResponse struct {
	Repos []struct {
		Path      string `json:"path"`
		Worktrees []struct {
			Path     string           `json:"path"`
			Sessions []claude.Session `json:"sessions"`
		} `json:"worktrees"`
	} `json:"repos"`
	Unlinked    []workspace.Unlinked `json:"unlinked"`
	GeneratedAt time.Time            `json:"generatedAt"`
}

func (s *Server) handleSessions(w http.ResponseWriter, _ *http.Request) {
	build := s.buildSessions
	if build == nil {
		build = s.refresh // no cheap build: the full one gives the same answer, only slower
	}
	ws, err := build()
	if err != nil {
		internalError(w, "sessions", err)
		return
	}
	if s.buildSessions != nil {
		s.mergeSessions(ws)
	}

	var resp sessionsResponse
	resp.Unlinked, resp.GeneratedAt = ws.Unlinked, ws.GeneratedAt
	for _, r := range ws.Repos {
		rr := struct {
			Path      string `json:"path"`
			Worktrees []struct {
				Path     string           `json:"path"`
				Sessions []claude.Session `json:"sessions"`
			} `json:"worktrees"`
		}{Path: r.Path}
		for _, wt := range r.Worktrees {
			rr.Worktrees = append(rr.Worktrees, struct {
				Path     string           `json:"path"`
				Sessions []claude.Session `json:"sessions"`
			}{wt.Path, wt.Sessions})
		}
		resp.Repos = append(resp.Repos, rr)
	}
	writeJSON(w, resp)
}

// mergeSessions puts fresh sessions into the last full snapshot, so actions (resume,
// plan) know a chat that started since, and discoveries it wrote are picked up. The
// git detail of the snapshot is left as it was.
func (s *Server) mergeSessions(fresh workspace.Workspace) {
	s.mu.Lock()
	last := s.last
	if last == nil {
		s.mu.Unlock()
		return
	}
	merged := *last
	merged.Repos = make([]workspace.Repo, len(last.Repos))
	for i, r := range last.Repos {
		merged.Repos[i] = r
		merged.Repos[i].Worktrees = make([]workspace.Worktree, len(r.Worktrees))
		copy(merged.Repos[i].Worktrees, r.Worktrees)
		for j, wt := range merged.Repos[i].Worktrees {
			if sessions, ok := sessionsAt(fresh, r.Path, wt.Path); ok {
				merged.Repos[i].Worktrees[j].Sessions = sessions
			}
		}
	}
	merged.Unlinked = fresh.Unlinked
	s.last = &merged
	s.mu.Unlock()
	s.harvestDiscoveries(merged)
}

func sessionsAt(ws workspace.Workspace, repo, path string) ([]claude.Session, bool) {
	for _, r := range ws.Repos {
		if r.Path != repo {
			continue
		}
		for _, w := range r.Worktrees {
			if workspace.SamePath(w.Path, path) {
				return w.Sessions, true
			}
		}
	}
	return nil, false
}
