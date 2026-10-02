package branches

import (
	"context"
	"errors"
	"sync"
	"time"

	"workstation/internal/forge"
	"workstation/internal/gitx"
)

const prTimeout = 20 * time.Second

// Response is a graph plus non-fatal problems (e.g. PRs could not be fetched).
type Response struct {
	Graph    Graph    `json:"graph"`
	Warnings []string `json:"warnings"`
	// PRsPending is true when the graph was built without any pull request data
	// because none was cached yet: a full request will bring them.
	PRsPending bool `json:"prsPending"`
}

// Service builds graphs and caches the (network-bound) PR lookups per repo.
type Service struct {
	TTL       time.Duration
	Now       func() time.Time
	RemoteURL func(dir string) (string, error)
	Detect    func(dir, remoteURL string) (forge.Provider, error)

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	prs     []forge.PR
	warning string
	at      time.Time
}

// NewService wires real Git and the given forge dependencies.
func NewService(deps forge.Deps) *Service {
	return &Service{
		TTL:       time.Minute,
		Now:       time.Now,
		RemoteURL: gitx.RemoteURL,
		Detect:    func(dir, url string) (forge.Provider, error) { return forge.Detect(dir, url, deps) },
	}
}

// Graph builds the graph of repo. refresh bypasses the PR cache.
func (s *Service) Graph(repo string, wts map[string]Worktree, includeMerged, refresh bool) (Response, error) {
	prs, warning := s.pullRequests(repo, refresh)

	g, err := Build(repo, Input{Worktrees: wts, PRs: prs, IncludeMerged: includeMerged, Now: s.Now()})
	if err != nil {
		return Response{}, err
	}
	resp := Response{Graph: g, Warnings: []string{}}
	if warning != "" {
		resp.Warnings = append(resp.Warnings, warning)
	}
	return resp, nil
}

// GraphWithoutPRs builds the graph from git alone, so it is as fast as git is.
// It never touches the network: pull requests are used only if an earlier
// request already cached them (however old), and otherwise PRsPending says a
// full Graph call will bring them. The page paints this first and fills in the
// pull requests when the full call returns.
func (s *Service) GraphWithoutPRs(repo string, wts map[string]Worktree, includeMerged bool) (Response, error) {
	s.mu.Lock()
	c, hadCache := s.cache[repo]
	s.mu.Unlock()

	g, err := Build(repo, Input{Worktrees: wts, PRs: c.prs, IncludeMerged: includeMerged, Now: s.Now()})
	if err != nil {
		return Response{}, err
	}
	resp := Response{Graph: g, Warnings: []string{}, PRsPending: !hadCache}
	if hadCache && c.warning != "" {
		resp.Warnings = append(resp.Warnings, c.warning)
	}
	return resp, nil
}

// CleanupResponse is the cleanup preview plus non-fatal problems.
type CleanupResponse struct {
	CleanupPreview
	Warnings []string `json:"warnings"`
}

// CleanupPreview lists the branches that can be cleaned up in repo. PRs may come
// from the cache (refresh bypasses it); if they cannot be read at all, nothing
// is offered, because nothing proves a branch is merged.
func (s *Service) CleanupPreview(repo string, wts map[string]Worktree, days int, refresh bool) (CleanupResponse, error) {
	prs, warning := s.pullRequests(repo, refresh)
	p, err := CleanupCandidates(repo, CleanupInput{Worktrees: wts, PRs: prs, Days: days, Now: s.Now()})
	if err != nil {
		return CleanupResponse{}, err
	}
	resp := CleanupResponse{CleanupPreview: p, Warnings: []string{}}
	if warning != "" {
		resp.Warnings = append(resp.Warnings, warning)
	}
	return resp, nil
}

// CleanupDelete deletes the chosen branches. It always re-fetches the pull
// requests: a destructive action must not rest on a minute-old cache.
func (s *Service) CleanupDelete(repo string, wts map[string]Worktree, days int, req []DeleteRequest) ([]DeleteResult, error) {
	prs, _ := s.pullRequests(repo, true)
	return DeleteBranches(repo, CleanupInput{Worktrees: wts, PRs: prs, Days: days, Now: s.Now()}, req)
}

// pullRequests returns PRs for repo (possibly cached) and a warning if the
// lookup failed. Repos without a known forge are silent: nothing to report.
func (s *Service) pullRequests(repo string, refresh bool) ([]forge.PR, string) {
	s.mu.Lock()
	c, ok := s.cache[repo]
	s.mu.Unlock()
	if ok && !refresh && s.Now().Sub(c.at) < s.TTL {
		return c.prs, c.warning
	}

	prs, warning := s.fetch(repo)

	s.mu.Lock()
	if s.cache == nil {
		s.cache = map[string]cached{}
	}
	s.cache[repo] = cached{prs: prs, warning: warning, at: s.Now()}
	s.mu.Unlock()
	return prs, warning
}

func (s *Service) fetch(repo string) ([]forge.PR, string) {
	url, err := s.RemoteURL(repo)
	if err != nil {
		return nil, "" // no origin: a local-only repo
	}
	provider, err := s.Detect(repo, url)
	if errors.Is(err, forge.ErrNoProvider) {
		return nil, ""
	}
	if err != nil {
		return nil, err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), prTimeout)
	defer cancel()
	prs, err := provider.PullRequests(ctx)
	if err != nil {
		return nil, err.Error()
	}
	return prs, ""
}
