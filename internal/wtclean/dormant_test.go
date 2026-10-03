package wtclean

import (
	"path/filepath"
	"testing"
	"time"

	"workstation/internal/forge"
)

// dormantInput: every worktree of the fixture has been idle for 30 days (commits are dated "now" in
// the fixture, so the clock moves forward instead), and no PR data is passed unless a test adds some.
func (f fixture) dormantInput(t *testing.T, prs []forge.PR, live ...string) Input {
	t.Helper()
	return Input{Repo: f.build(t, live...), PRs: prs, Now: time.Now().Add(30 * day), IncludeDormant: true}
}

func (f fixture) pushToRemote(t *testing.T, branches ...string) {
	t.Helper()
	remote := filepath.Join(filepath.Dir(f.repo), "remote.git")
	mustGit(t, filepath.Dir(f.repo), "init", "--bare", "-b", "main", remote)
	mustGit(t, f.repo, "remote", "add", "origin", remote)
	for _, b := range branches {
		mustGit(t, f.repo, "push", "origin", b)
	}
}

func kinds(p Preview) map[string]string {
	out := map[string]string{}
	for _, c := range p.Candidates {
		out[c.Name] = c.Kind
	}
	return out
}

func TestDormantIsOffUnlessAsked(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	in := f.dormantInput(t, nil)
	in.IncludeDormant = false
	got, err := Candidates(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Candidates) != 0 {
		t.Fatalf("merged-only mode must not offer idle worktrees: %v", kinds(got))
	}
}

func TestDormantOffersWorkThatCannotBeLost(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.pushToRemote(t, "done") // backed up on a remote

	got, err := Candidates(f.dormantInput(t, nil, "live"))
	if err != nil {
		t.Fatal(err)
	}
	k := kinds(got)
	if k["ancestor"] != KindDormantMerged {
		t.Errorf("a branch already inside main is safe: %v", k)
	}
	if k["done"] != KindDormantOnRemote {
		t.Errorf("work that a remote has is safe: %v", k)
	}
	for _, name := range []string{"nopr", "diverged", "recent", "dirty", "live"} {
		if _, ok := k[name]; ok {
			t.Errorf("%s must not be offered: %v", name, k)
		}
	}
	reasons := map[string]string{}
	for _, s := range got.Skipped {
		reasons[s.Name] = s.Reason
	}
	if reasons["nopr"] == "" || reasons["dirty"] == "" || reasons["live"] == "" {
		t.Errorf("idle worktrees that stay must say why: %v", reasons)
	}
}

func TestDormantIgnoresOpenPRsAndRecentWork(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.pushToRemote(t, "open", "nopr")
	prs := []forge.PR{{Number: 7, Source: "open", Dest: "main", State: forge.StateOpen, UpdatedAt: time.Now()}}

	got, _ := Candidates(f.dormantInput(t, prs))
	if _, ok := kinds(got)["open"]; ok {
		t.Error("a worktree with an open PR is in review, not dormant")
	}
	if kinds(got)["nopr"] != KindDormantOnRemote {
		t.Errorf("nopr is pushed and idle: %v", kinds(got))
	}

	fresh := f.dormantInput(t, nil)
	fresh.Now = time.Now().Add(2 * day)
	got, _ = Candidates(fresh)
	if len(got.Candidates) != 0 {
		t.Errorf("worked on 2 days ago is not dormant: %v", kinds(got))
	}
}

func TestRemoveDormantRechecks(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.pushToRemote(t, "done")
	in := f.dormantInput(t, nil)
	got, _ := Candidates(in)
	var req []RemoveRequest
	for _, c := range got.Candidates {
		req = append(req, RemoveRequest{Path: c.Path, SHA: c.SHA})
	}
	if len(req) != 2 {
		t.Fatalf("setup: %v", kinds(got))
	}

	// not asking for dormant removal refuses them: they are no longer "eligible"
	merged := in
	merged.IncludeDormant = false
	res, err := Remove(merged, req)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Removed {
			t.Errorf("%s removed without includeDormant", r.Name)
		}
	}

	res, err = Remove(in, req)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if !r.Removed {
			t.Errorf("%s: %s", r.Name, r.Error)
		}
	}
}
