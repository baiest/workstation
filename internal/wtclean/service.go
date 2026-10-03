package wtclean

import (
	"time"

	"workstation/internal/forge"
	"workstation/internal/workspace"
)

// Response is a preview plus non-fatal problems (e.g. PRs could not be read).
type Response struct {
	Preview
	Warnings []string `json:"warnings"`
}

// Service wires the cleanup to a source of pull requests.
type Service struct {
	// PRs returns the pull requests of a repo path and a warning if they could
	// not be read; refresh bypasses any cache (branches.Service.PullRequests).
	PRs func(repo string, refresh bool) ([]forge.PR, string)
	Now func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Preview lists what could be removed. Without PR data nothing is offered:
// nothing would prove a worktree's work is merged.
func (s *Service) Preview(repo workspace.Repo, days int) (Response, error) {
	prs, warning := s.PRs(repo.Path, false)
	p, err := Candidates(Input{Repo: repo, PRs: prs, Days: days, Now: s.now()})
	if err != nil {
		return Response{}, err
	}
	resp := Response{Preview: p, Warnings: []string{}}
	if warning != "" {
		resp.Warnings = append(resp.Warnings, warning)
	}
	return resp, nil
}

// Remove removes the chosen worktrees. It always re-fetches the pull requests: a
// destructive action must not rest on a cache.
func (s *Service) Remove(repo workspace.Repo, days int, req []RemoveRequest) ([]RemoveResult, error) {
	prs, _ := s.PRs(repo.Path, true)
	return Remove(Input{Repo: repo, PRs: prs, Days: days, Now: s.now()}, req)
}
