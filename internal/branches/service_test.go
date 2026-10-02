package branches

import (
	"context"
	"errors"
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
