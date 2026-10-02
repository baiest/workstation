package branches

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"workstation/internal/forge"
)

type fakeProvider struct {
	calls int
	prs   []forge.PR
	err   error
}

func (f *fakeProvider) PullRequests(context.Context) ([]forge.PR, error) {
	f.calls++
	return f.prs, f.err
}

func newService(p forge.Provider, detectErr error, now *time.Time) *Service {
	return &Service{
		TTL:       time.Minute,
		Now:       func() time.Time { return *now },
		RemoteURL: func(string) (string, error) { return "git@github.com:o/r.git", nil },
		Detect: func(string, string) (forge.Provider, error) {
			return p, detectErr
		},
	}
}

func TestServiceAttachesPRsAndCaches(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	now := time.Now()
	fp := &fakeProvider{prs: []forge.PR{{Number: 5, Source: "feat-a", Dest: "main", State: forge.StateOpen, Approvals: 1}}}
	s := newService(fp, nil, &now)

	r, err := s.Graph(repo, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if a := nodeMap(r.Graph)["feat-a"]; a.PR == nil || a.PR.Number != 5 || a.Via != ViaPR {
		t.Fatalf("PR not attached: %+v", a)
	}
	if len(r.Warnings) != 0 {
		t.Fatalf("unexpected warnings %v", r.Warnings)
	}

	now = now.Add(30 * time.Second)
	if _, err := s.Graph(repo, nil, false, false); err != nil || fp.calls != 1 {
		t.Fatalf("within TTL must use cache, calls=%d err=%v", fp.calls, err)
	}
	if _, err := s.Graph(repo, nil, false, true); err != nil || fp.calls != 2 {
		t.Fatalf("refresh must bypass cache, calls=%d err=%v", fp.calls, err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := s.Graph(repo, nil, false, false); err != nil || fp.calls != 3 {
		t.Fatalf("expired cache must refetch, calls=%d err=%v", fp.calls, err)
	}
}

// The graph from git alone must not wait for the network: it is what the page
// shows first while the pull requests load.
func TestServiceGraphWithoutPRsNeverCallsTheForge(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	now := time.Now()
	fp := &fakeProvider{prs: []forge.PR{{Number: 5, Source: "feat-a", Dest: "main", State: forge.StateOpen}}}
	s := newService(fp, nil, &now)

	r, err := s.GraphWithoutPRs(repo, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if fp.calls != 0 {
		t.Fatalf("the forge was called %d times", fp.calls)
	}
	if !r.PRsPending || len(r.Graph.Nodes) < 3 {
		t.Fatalf("expected the git graph with PRs pending: %+v", r)
	}
	if a := nodeMap(r.Graph)["feat-a"]; a.PR != nil || a.Parent != "feat-a" && a.Parent != "main" {
		t.Fatalf("feat-a has no PR yet: %+v", a)
	}
}

func TestServiceGraphWithoutPRsUsesWhatIsAlreadyCached(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	now := time.Now()
	fp := &fakeProvider{prs: []forge.PR{{Number: 5, Source: "feat-a", Dest: "main", State: forge.StateOpen}}}
	s := newService(fp, nil, &now)

	if _, err := s.Graph(repo, nil, false, false); err != nil || fp.calls != 1 {
		t.Fatalf("warm the cache: calls=%d err=%v", fp.calls, err)
	}
	now = now.Add(10 * time.Minute) // long expired; still better than nothing for a first paint
	r, err := s.GraphWithoutPRs(repo, nil, false)
	if err != nil || fp.calls != 1 {
		t.Fatalf("must not refetch: calls=%d err=%v", fp.calls, err)
	}
	if a := nodeMap(r.Graph)["feat-a"]; a.PR == nil || a.PR.Number != 5 || r.PRsPending {
		t.Fatalf("cached PRs should be used and not pending: %+v pending=%v", a, r.PRsPending)
	}
}

func TestServiceProviderErrorIsWarningNotFailure(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	now := time.Now()
	s := newService(&fakeProvider{err: errors.New("gh: not logged in")}, nil, &now)

	r, err := s.Graph(repo, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Warnings) != 1 || len(r.Graph.Nodes) < 2 {
		t.Fatalf("expected graph from git plus one warning, got %+v", r)
	}
}

func TestServiceDetectErrors(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	now := time.Now()

	silent := newService(nil, forge.ErrNoProvider, &now)
	if r, err := silent.Graph(repo, nil, false, false); err != nil || len(r.Warnings) != 0 {
		t.Fatalf("unknown host should be silent: %+v %v", r, err)
	}

	loud := newService(nil, errors.New("bitbucket cloud: set BITBUCKET_TOKEN"), &now)
	if r, err := loud.Graph(repo, nil, false, false); err != nil || len(r.Warnings) != 1 {
		t.Fatalf("config problem should warn: %+v %v", r, err)
	}

	noRemote := newService(nil, nil, &now)
	noRemote.RemoteURL = func(string) (string, error) { return "", errors.New("no origin") }
	if r, err := noRemote.Graph(repo, nil, false, false); err != nil || len(r.Warnings) != 0 {
		t.Fatalf("repo without origin should be silent: %+v %v", r, err)
	}
}

func TestServiceCleanupPreviewUsesCacheButDeleteIsFresh(t *testing.T) {
	t.Parallel()
	f := newCleanupFixture(t)
	fp := &fakeProvider{prs: f.prs}
	now := f.now
	s := newService(fp, nil, &now)

	p, err := s.CleanupPreview(f.repo, f.in.Worktrees, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(candidateNames(p.CleanupPreview), ",") != "old-ancestor,old-squash" || len(p.Warnings) != 0 {
		t.Fatalf("preview: %+v", p)
	}
	if _, err := s.CleanupPreview(f.repo, f.in.Worktrees, 0, false); err != nil || fp.calls != 1 {
		t.Fatalf("a second preview within the TTL may use the cache, calls=%d err=%v", fp.calls, err)
	}

	var sha string
	for _, c := range p.Candidates {
		if c.Branch == "old-squash" {
			sha = c.SHA
		}
	}
	results, err := s.CleanupDelete(f.repo, f.in.Worktrees, 0, []DeleteRequest{{Branch: "old-squash", SHA: sha}})
	if err != nil || len(results) != 1 || !results[0].Deleted {
		t.Fatalf("delete: %+v %v", results, err)
	}
	if fp.calls != 2 {
		t.Fatalf("deleting must re-fetch the pull requests instead of trusting the cache, calls=%d", fp.calls)
	}
}

// If the PRs cannot be read there is nothing to prove a branch is merged, so
// nothing may be offered or deleted.
func TestServiceCleanupWithoutPullRequestsDeletesNothing(t *testing.T) {
	t.Parallel()
	f := newCleanupFixture(t)
	now := f.now
	s := newService(&fakeProvider{err: errors.New("gh: not logged in")}, nil, &now)

	p, err := s.CleanupPreview(f.repo, f.in.Worktrees, 0, false)
	if err != nil || len(p.Candidates) != 0 || len(p.Warnings) != 1 {
		t.Fatalf("preview must be empty with a warning: %+v %v", p, err)
	}
	results, err := s.CleanupDelete(f.repo, f.in.Worktrees, 0, []DeleteRequest{{Branch: "old-squash", SHA: "x"}})
	if err != nil || results[0].Deleted {
		t.Fatalf("nothing may be deleted without PR data: %+v %v", results, err)
	}
}

func TestServicePassesWorktreesAndMergedFlag(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	now := time.Now()
	fp := &fakeProvider{prs: []forge.PR{{Number: 1, Source: "merged-old", Dest: "main", State: forge.StateMerged, UpdatedAt: now}}}
	s := newService(fp, nil, &now)

	r, err := s.Graph(repo, map[string]Worktree{"same-as-main": {Name: "w", Path: "/w"}}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	m := nodeMap(r.Graph)
	if m["same-as-main"].Worktree == nil || m["merged-old"].PR == nil {
		t.Fatalf("worktree / merged flag not applied: %+v", r.Graph.Nodes)
	}
}
