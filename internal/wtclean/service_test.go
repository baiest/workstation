package wtclean

import (
	"strings"
	"testing"
	"time"

	"workstation/internal/forge"
)

type prSource struct {
	prs     []forge.PR
	warning string
	calls   []bool // the refresh argument of each call
}

func (p *prSource) get(_ string, refresh bool) ([]forge.PR, string) {
	p.calls = append(p.calls, refresh)
	return p.prs, p.warning
}

func TestServicePreviewMayUseCacheButRemoveNeverDoes(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	src := &prSource{prs: f.prs}
	s := &Service{PRs: src.get, Now: func() time.Time { return f.now }}
	repo := f.build(t, "live")

	p, err := s.Preview(repo, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if names(p.Preview) != "ancestor,done" || len(p.Warnings) != 0 {
		t.Fatalf("preview: %+v", p)
	}
	var sha string
	for _, c := range p.Candidates {
		if c.Name == "done" {
			sha = c.SHA
		}
	}

	results, err := s.Remove(repo, 0, 0, []RemoveRequest{{Path: f.path["done"], SHA: sha}})
	if err != nil || len(results) != 1 || !results[0].Removed {
		t.Fatalf("remove: %+v %v", results, err)
	}
	if len(src.calls) != 2 || src.calls[0] != false || src.calls[1] != true {
		t.Fatalf("removing must re-fetch the pull requests instead of trusting a cache: %v", src.calls)
	}
}

func TestServiceWithoutPullRequestsRemovesNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	src := &prSource{warning: "github: gh not logged in"}
	s := &Service{PRs: src.get, Now: func() time.Time { return f.now }}
	repo := f.build(t, "live")

	p, err := s.Preview(repo, 0, 0)
	if err != nil || len(p.Candidates) != 0 || len(p.Warnings) != 1 {
		t.Fatalf("no PR data means nothing is provably merged: %+v %v", p, err)
	}
	results, err := s.Remove(repo, 0, 0, []RemoveRequest{{Path: f.path["done"], SHA: "x"}})
	if err != nil || results[0].Removed || !strings.Contains(results[0].Error, "eligible") {
		t.Fatalf("%+v %v", results, err)
	}
}
