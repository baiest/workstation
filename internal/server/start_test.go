package server

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"workstation/internal/gitx"
	"workstation/internal/tickets"
	"workstation/internal/workspace"
)

type fakeGit struct {
	branches []gitx.Branch
	start    string
	def      string
	err      error
	added    []added
}

type added struct {
	repo, path, branch, base string
	create                   bool
}

func (f *fakeGit) LocalBranches(string) ([]gitx.Branch, error) { return f.branches, nil }
func (f *fakeGit) StartPoint(string) string                    { return f.start }
func (f *fakeGit) DefaultBranch(string) string                 { return f.def }
func (f *fakeGit) AddWorktree(repo, path, branch, base string, create bool) error {
	f.added = append(f.added, added{repo, path, branch, base, create})
	return f.err
}

func startServer(t *testing.T, g *fakeGit, mutate func(*workspace.Workspace)) (http.Handler, *tickets.Store, *fakeLauncher, string) {
	t.Helper()
	ws, dir := fixtureWorkspace(t)
	if mutate != nil {
		mutate(&ws)
	}
	store := tickets.New("")
	l := &fakeLauncher{}
	h := New(func() (workspace.Workspace, error) { return ws, nil }, l, fstest.MapFS{},
		WithTickets(store, tickets.Keyer{}), WithGit(g))
	return h, store, l, dir
}

func startReq(ref string) map[string]any { return map[string]any{"repo": "/r", "ref": ref} }

func TestStartUsesTheTicketWorktreeAndMakesItActive(t *testing.T) {
	g := &fakeGit{}
	h, store, l, dir := startServer(t, g, nil)
	next := "write the migration"
	if _, err := store.Apply(tickets.Update{Repo: "/r", Ref: "branch:feat", NextStep: &next}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddDiscovery("/r", "branch:feat", "refresh tokens never expire", "cli"); err != nil {
		t.Fatal(err)
	}

	if code, body := post(h, "/api/actions/start", startReq("branch:feat")); code != 204 {
		t.Fatalf("%d %s", code, body)
	}
	if len(g.added) != 0 {
		t.Fatalf("an existing worktree is reused, not created again: %+v", g.added)
	}
	if len(l.prompts) != 1 || l.calls[0] != "prompt:"+dir {
		t.Fatalf("Claude opens in the worktree: %v %v", l.calls, l.prompts)
	}
	brief := l.prompts[0]
	for _, want := range []string{"write the migration", "refresh tokens never expire", "NOT part of this ticket", "workstation ticket", "workstation next", "Branch: feat"} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief lacks %q:\n%s", want, brief)
		}
	}
	if tk, _ := store.Get("/r|branch:feat"); tk.State != tickets.StateActive {
		t.Fatalf("starting a ticket makes it active: %+v", tk)
	}
}

func TestStartChecksOutTheExistingBranchOfTheTicketInANewWorktree(t *testing.T) {
	g := &fakeGit{start: "refs/heads/main", branches: []gitx.Branch{
		{Name: "feature/REG-9-old", Date: "2026-09-01T00:00:00+00:00"},
		{Name: "feature/REG-9-new", Date: "2026-10-01T00:00:00+00:00"},
		{Name: "other", Date: "2026-10-02T00:00:00+00:00"},
	}}
	h, _, l, _ := startServer(t, g, nil)
	if code, body := post(h, "/api/actions/start", startReq("REG-9")); code != 204 {
		t.Fatalf("%d %s", code, body)
	}
	if len(g.added) != 1 || g.added[0].create || g.added[0].branch != "feature/REG-9-new" {
		t.Fatalf("the newest branch of the ticket is checked out: %+v", g.added)
	}
	if want := filepath.Join("/r", ".claude", "worktrees", "feature-REG-9-new"); g.added[0].path != want {
		t.Fatalf("the folder is below the repo, without slashes from the branch: %q, want %q", g.added[0].path, want)
	}
	if len(l.prompts) != 1 || !strings.Contains(l.prompts[0], "REG-9") {
		t.Fatalf("%v", l.prompts)
	}
}

func TestStartWorksInTheMainFolderWhenItHasTheBranch(t *testing.T) {
	g := &fakeGit{branches: []gitx.Branch{{Name: "feature/REG-9-x", Date: "2026-10-01T00:00:00+00:00"}}}
	h, _, l, _ := startServer(t, g, func(ws *workspace.Workspace) {
		ws.Repos[0].Worktrees = append(ws.Repos[0].Worktrees, workspace.Worktree{Name: "r", Path: "/r", Branch: "feature/REG-9-x", IsMain: true, Ticket: "REG-9"})
	})
	if code, _ := post(h, "/api/actions/start", startReq("REG-9")); code != 204 {
		t.Fatal(code)
	}
	if len(g.added) != 0 || l.calls[0] != "prompt:/r" {
		t.Fatalf("nothing to create; work where the branch already is: %+v %v", g.added, l.calls)
	}
}

func TestStartCreatesABranchForATicketWithNone(t *testing.T) {
	g := &fakeGit{start: "refs/remotes/origin/main"}
	h, store, l, _ := startServer(t, g, nil)
	title := "Fix token refresh"
	_, _ = store.Apply(tickets.Update{Repo: "/r", Ref: "REG-7", Title: &title})

	if code, body := post(h, "/api/actions/start", startReq("REG-7")); code != 204 {
		t.Fatalf("%d %s", code, body)
	}
	if len(g.added) != 1 || !g.added[0].create || g.added[0].branch != "REG-7-fix-token-refresh" || g.added[0].base != "refs/remotes/origin/main" {
		t.Fatalf("a new branch from the start point: %+v", g.added)
	}

	// the name is taken: a suffix, never a clash
	g.added = nil
	_, _ = store.Apply(tickets.Update{Repo: "/r", Ref: "note:ab", Title: &title})
	g.branches = []gitx.Branch{{Name: "fix-token-refresh"}}
	if code, _ := post(h, "/api/actions/start", startReq("note:ab")); code != 204 {
		t.Fatal(code)
	}
	if g.added[0].branch != "fix-token-refresh-2" {
		t.Fatalf("a taken name gets a suffix: %+v", g.added)
	}
	if tk, _ := store.Get("/r|note:ab"); tk.Branch != "fix-token-refresh-2" || tk.State != tickets.StateActive {
		t.Fatalf("a ticket born from a note owns the branch made for it: %+v", tk)
	}
	if len(l.prompts) != 2 {
		t.Fatalf("%d sessions", len(l.prompts))
	}
}

func TestStartRefusesWhatShouldNotBeStarted(t *testing.T) {
	for name, c := range map[string]struct {
		state tickets.State
		want  string
	}{
		"blocked": {tickets.StateBlocked, "blocked"},
		"done":    {tickets.StateDone, "done"},
		"ignored": {tickets.StateIgnored, "ignored"},
	} {
		g := &fakeGit{start: "refs/heads/main"}
		h, store, l, _ := startServer(t, g, nil)
		st, reason := c.state, "waits for Ana"
		_, _ = store.Apply(tickets.Update{Repo: "/r", Ref: "REG-1", State: &st, BlockedReason: &reason})
		code, body := post(h, "/api/actions/start", startReq("REG-1"))
		if code != 400 || !strings.Contains(body, c.want) {
			t.Errorf("%s: %d %q", name, code, body)
		}
		if len(l.calls) != 0 || len(g.added) != 0 {
			t.Errorf("%s: nothing may be created or launched: %v %v", name, l.calls, g.added)
		}
	}
}

func TestStartRejectsBadRequests(t *testing.T) {
	g := &fakeGit{} // no start point: a new branch is impossible
	h, store, l, _ := startServer(t, g, nil)
	for name, body := range map[string]map[string]any{
		"unknown repo": {"repo": "/elsewhere", "ref": "REG-1"},
		"bad ref":      {"repo": "/r", "ref": "a|b"},
		"no ref":       {"repo": "/r"},
		"no base":      {"repo": "/r", "ref": "REG-1"},
		"gone branch":  {"repo": "/r", "ref": "branch:gone"},
	} {
		if code, _ := post(h, "/api/actions/start", body); code != 400 {
			t.Errorf("%s: status %d, want 400", name, code)
		}
	}
	if len(l.calls) != 0 || len(g.added) != 0 || len(store.Snapshot().Tickets) != 0 {
		t.Fatalf("a refused start changes nothing: %v %v %+v", l.calls, g.added, store.Snapshot().Tickets)
	}
}

func TestStartReportsAFailingWorktreeCreation(t *testing.T) {
	g := &fakeGit{start: "refs/heads/main", err: errors.New("fatal: boom")}
	h, store, l, _ := startServer(t, g, nil)
	code, body := post(h, "/api/actions/start", startReq("REG-3"))
	if code != 400 || !strings.Contains(body, "boom") {
		t.Fatalf("%d %q", code, body)
	}
	if len(l.calls) != 0 {
		t.Fatal("no session without a worktree")
	}
	if len(store.Snapshot().Tickets) != 0 {
		t.Fatal("the ticket is not made active when nothing started")
	}
}

func TestReviewOpensAReadOnlySessionInAKnownWorktree(t *testing.T) {
	g := &fakeGit{def: "main"}
	h, _, l, dir := startServer(t, g, nil)
	if code, body := post(h, "/api/actions/review", map[string]any{"path": dir}); code != 204 {
		t.Fatalf("%d %s", code, body)
	}
	if len(l.prompts) != 1 || l.calls[0] != "prompt:"+dir || !strings.Contains(l.prompts[0], "READ-ONLY") || !strings.Contains(l.prompts[0], "main...HEAD") {
		t.Fatalf("%v %v", l.calls, l.prompts)
	}
	if code, _ := post(h, "/api/actions/review", map[string]any{"path": "/not/a/worktree"}); code != 400 {
		t.Fatalf("unknown path: %d", code)
	}
	g.def = ""
	if code, _ := post(h, "/api/actions/review", map[string]any{"path": dir}); code != 400 {
		t.Fatalf("no default branch: %d", code)
	}
}

func TestStartBriefCarriesTheTrackerLink(t *testing.T) {
	g := &fakeGit{}
	ws, _ := fixtureWorkspace(t)
	l := &fakeLauncher{}
	store := tickets.New("")
	h := New(func() (workspace.Workspace, error) { return ws, nil }, l, fstest.MapFS{},
		WithTickets(store, tickets.Keyer{}), WithGit(g), WithTicketLink("https://tracker.example.com/browse/{key}"))

	ws.Repos[0].Worktrees[0].Ticket = "REG-5"
	if code, body := post(h, "/api/actions/start", startReq("REG-5")); code != 204 {
		t.Fatalf("%d %s", code, body)
	}
	if !strings.Contains(l.prompts[0], "Tracker: https://tracker.example.com/browse/REG-5") {
		t.Fatalf("%s", l.prompts[0])
	}

	link := "https://trello.com/c/abc"
	_, _ = store.Apply(tickets.Update{Repo: "/r", Ref: "REG-5", Link: &link})
	if code, _ := post(h, "/api/actions/start", startReq("REG-5")); code != 204 || !strings.Contains(l.prompts[1], "Tracker: "+link) {
		t.Fatalf("a link written by hand wins: %v", l.prompts)
	}
}
