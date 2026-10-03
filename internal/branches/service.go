package branches

import (
	"context"
	"errors"
	"sync"
	"time"

	"workstation/internal/forge"
	"workstation/internal/gitx"
)

const (
	prTimeout         = 20 * time.Second
	defaultTTL        = 5 * time.Minute
	defaultFailureTTL = 30 * time.Second
)

// Response is a graph plus non-fatal problems (e.g. PRs could not be fetched).
type Response struct {
	Graph    Graph    `json:"graph"`
	Warnings []string `json:"warnings"`
	// PRsPending is true when the graph was built without any pull request data
	// because none was cached yet: a full request will bring them.
	PRsPending bool `json:"prsPending"`
	// PRsFetchedAt is when the pull requests shown were read from the forge.
	PRsFetchedAt time.Time `json:"prsFetchedAt,omitzero"`
	// PRsStale means older than the cache lifetime: shown at once while a refresh
	// runs in the background (ask again a few seconds later for the new data).
	PRsStale bool `json:"prsStale"`
}

// Service builds graphs and caches the (network-bound) PR lookups per repo.
//
// The cache is stale-while-revalidate: a lookup within TTL is served from
// memory; an older one is served immediately and refreshed in the background, so
// pages never wait for the forge once a repo has been read once. Concurrent
// lookups of a repo share one fetch, and with a Store the cache also survives
// restarts.
type Service struct {
	TTL        time.Duration // how long PR data counts as fresh (default 5 min)
	FailureTTL time.Duration // how long a failed lookup is remembered (default 30 s)
	Now        func() time.Time
	RemoteURL  func(dir string) (string, error)
	Detect     func(dir, remoteURL string) (forge.Provider, error)
	Store      Store // optional persistence

	mu       sync.Mutex
	cache    map[string]cached
	inflight map[string]*flightCall
	bg       sync.WaitGroup
}

type cached struct {
	prs     []forge.PR
	warning string
	at      time.Time
}

type flightCall struct {
	done chan struct{}
	res  cached
}

// NewService wires real Git and the given forge dependencies.
func NewService(deps forge.Deps) *Service {
	return &Service{
		TTL:        defaultTTL,
		FailureTTL: defaultFailureTTL,
		Now:        time.Now,
		RemoteURL:  gitx.RemoteURL,
		Detect:     func(dir, url string) (forge.Provider, error) { return forge.Detect(dir, url, deps) },
	}
}

// Wait blocks until background refreshes have finished (tests, orderly shutdown).
func (s *Service) Wait() { s.bg.Wait() }

type mode int

const (
	modeCacheOnly  mode = iota // never touch the network
	modeRevalidate             // fresh cache as is; old cache served and refreshed in the background
	modeForce                  // always fetch now
)

// prResult is what a caller gets from the cache.
type prResult struct {
	prs       []forge.PR
	warning   string
	fetchedAt time.Time
	stale     bool // older than the cache lifetime
	known     bool // there is any data at all
}

func resultOf(c cached, known, stale bool) prResult {
	return prResult{prs: c.prs, warning: c.warning, fetchedAt: c.at, stale: stale, known: known}
}

// Graph builds the graph of repo. refresh forces a fresh PR lookup; otherwise
// old data is shown at once and refreshed in the background.
func (s *Service) Graph(repo string, wts map[string]Worktree, includeMerged, refresh bool) (Response, error) {
	m := modeRevalidate
	if refresh {
		m = modeForce
	}
	d := s.prData(repo, m)

	g, err := Build(repo, Input{Worktrees: wts, PRs: d.prs, IncludeMerged: includeMerged, Now: s.Now()})
	if err != nil {
		return Response{}, err
	}
	resp := Response{Graph: g, Warnings: []string{}, PRsFetchedAt: d.fetchedAt, PRsStale: d.stale}
	if d.warning != "" {
		resp.Warnings = append(resp.Warnings, d.warning)
	}
	return resp, nil
}

// GraphWithoutPRs builds the graph from git alone, so it is as fast as git is.
// It never touches the network: pull requests are used only if they were cached
// (in memory or on disk, however old), and otherwise PRsPending says a full
// Graph call will bring them. The page paints this first and fills in the pull
// requests when the full call returns.
func (s *Service) GraphWithoutPRs(repo string, wts map[string]Worktree, includeMerged bool) (Response, error) {
	d := s.prData(repo, modeCacheOnly)

	g, err := Build(repo, Input{Worktrees: wts, PRs: d.prs, IncludeMerged: includeMerged, Now: s.Now()})
	if err != nil {
		return Response{}, err
	}
	resp := Response{Graph: g, Warnings: []string{}, PRsPending: !d.known, PRsFetchedAt: d.fetchedAt, PRsStale: d.stale}
	if d.known && d.warning != "" {
		resp.Warnings = append(resp.Warnings, d.warning)
	}
	return resp, nil
}

// PullRequests returns the pull requests of repo and a warning if they could not
// be read. Other features use it too; refresh forces a lookup.
func (s *Service) PullRequests(repo string, refresh bool) ([]forge.PR, string) {
	return s.pullRequests(repo, refresh)
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
	p, err := CleanupCandidates(repo, CleanupInput{Worktrees: wts, PRs: prs, PRsKnown: warning == "", Days: days, Now: s.Now()})
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
// requests: a destructive action must not rest on a cache.
func (s *Service) CleanupDelete(repo string, wts map[string]Worktree, days int, allowLocalOnly bool, req []DeleteRequest) ([]DeleteResult, error) {
	prs, warning := s.pullRequests(repo, true)
	in := CleanupInput{Worktrees: wts, PRs: prs, PRsKnown: warning == "", AllowLocalOnly: allowLocalOnly, Days: days, Now: s.Now()}
	return DeleteBranches(repo, in, req)
}

func (s *Service) pullRequests(repo string, refresh bool) ([]forge.PR, string) {
	m := modeRevalidate
	if refresh {
		m = modeForce
	}
	d := s.prData(repo, m)
	return d.prs, d.warning
}

// prData is the one door to the PR cache.
func (s *Service) prData(repo string, m mode) prResult {
	c, ok := s.entry(repo)

	switch {
	case m == modeCacheOnly:
		return resultOf(c, ok, ok && !s.fresh(c))
	case m == modeForce || !ok:
		return resultOf(s.fetchShared(repo), true, false)
	case s.fresh(c):
		return resultOf(c, true, false)
	case c.warning != "":
		// the last lookup failed: there is nothing worth showing, so try again now
		return resultOf(s.fetchShared(repo), true, false)
	}

	// good data, but old: show it now, refresh behind the scenes
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		s.fetchShared(repo)
	}()
	return resultOf(c, true, true)
}

// entry returns the cached data for repo, loading it from the Store the first time.
func (s *Service) entry(repo string) (cached, bool) {
	s.mu.Lock()
	c, ok := s.cache[repo]
	s.mu.Unlock()
	if ok || s.Store == nil {
		return c, ok
	}

	snap, found := s.Store.Load(repo)
	if !found {
		return cached{}, false
	}
	c = cached{prs: snap.PRs, at: snap.FetchedAt}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache == nil {
		s.cache = map[string]cached{}
	}
	if existing, raced := s.cache[repo]; raced { // a fetch finished meanwhile: that is newer
		return existing, true
	}
	s.cache[repo] = c
	return c, true
}

func (s *Service) fresh(c cached) bool {
	ttl := s.TTL
	if ttl <= 0 {
		ttl = defaultTTL
	}
	if c.warning != "" {
		failure := s.FailureTTL
		if failure <= 0 {
			failure = defaultFailureTTL
		}
		ttl = min(ttl, failure)
	}
	return s.Now().Sub(c.at) < ttl
}

// fetchShared reads the PRs from the forge, once per repo at a time: callers
// that arrive while a fetch is running wait for it instead of starting another.
func (s *Service) fetchShared(repo string) cached {
	s.mu.Lock()
	if call, running := s.inflight[repo]; running {
		s.mu.Unlock()
		<-call.done
		return call.res
	}
	call := &flightCall{done: make(chan struct{})}
	if s.inflight == nil {
		s.inflight = map[string]*flightCall{}
	}
	s.inflight[repo] = call
	prev, hadPrev := s.cache[repo]
	s.mu.Unlock()

	prs, warning := s.fetch(repo)
	res := cached{prs: prs, warning: warning, at: s.Now()}
	if warning != "" && hadPrev && prev.warning == "" {
		res.prs = prev.prs // a failed refresh must not blank what we already know
	}

	s.mu.Lock()
	if s.cache == nil {
		s.cache = map[string]cached{}
	}
	s.cache[repo] = res
	delete(s.inflight, repo)
	s.mu.Unlock()

	if warning == "" && s.Store != nil {
		_ = s.Store.Save(repo, Snapshot{FetchedAt: res.at, PRs: res.prs}) // best effort: the cache is only a speed-up
	}
	call.res = res
	close(call.done)
	return res
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
