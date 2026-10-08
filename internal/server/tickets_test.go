package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"workstation/internal/branches"
	"workstation/internal/claude"
	"workstation/internal/notes"
	"workstation/internal/tickets"
	"workstation/internal/workspace"
)

func ticketServer(t *testing.T, extra ...Option) (http.Handler, *tickets.Store, string) {
	t.Helper()
	ws, dir := fixtureWorkspace(t)
	store := tickets.New(filepath.Join(t.TempDir(), "tickets.json"))
	opts := append([]Option{WithTickets(store, tickets.Keyer{})}, extra...)
	h := New(func() (workspace.Workspace, error) { return ws, nil }, &fakeLauncher{}, fstest.MapFS{}, opts...)
	return h, store, dir
}

func post(h http.Handler, path string, v any) (int, string) {
	b, _ := json.Marshal(v)
	rec := do(h, http.MethodPost, path, string(b), nil)
	return rec.Code, rec.Body.String()
}

func TestTicketsDisabledWithoutStore(t *testing.T) {
	h, _, _, _ := newTestServer(t)
	if rec := do(h, http.MethodGet, "/api/tickets", "", nil); rec.Code != 404 {
		t.Fatalf("got %d", rec.Code)
	}
	if code, _ := post(h, "/api/tickets", map[string]any{"repo": "/r", "ref": "REG-1"}); code != 404 {
		t.Fatalf("got %d", code)
	}
}

func TestTicketUpdateByRepoAndRefAndByWorktree(t *testing.T) {
	h, store, dir := ticketServer(t)

	if code, body := post(h, "/api/tickets", map[string]any{"repo": "/r", "ref": "REG-1", "state": "active", "nextStep": "run the tests"}); code != 200 {
		t.Fatalf("by ref: %d %s", code, body)
	}
	if code, body := post(h, "/api/tickets", map[string]any{"worktree": dir, "state": "parked"}); code != 200 {
		t.Fatalf("by worktree: %d %s", code, body)
	}
	snap := store.Snapshot().Tickets
	if snap["/r|REG-1"].State != tickets.StateActive || snap["/r|REG-1"].NextStep != "run the tests" {
		t.Fatalf("REG-1: %+v", snap["/r|REG-1"])
	}
	if snap["/r|branch:feat"].State != tickets.StateParked {
		t.Fatalf("a worktree names its ticket by its branch: %+v", snap)
	}

	rec := do(h, http.MethodGet, "/api/tickets", "", nil)
	var got struct {
		Tickets map[string]tickets.Ticket `json:"tickets"`
		Config  struct{ WIPLimit int }    `json:"config"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Tickets) != 2 || got.Config.WIPLimit != 3 {
		t.Fatalf("GET: %v %s", err, rec.Body)
	}
}

func TestTicketUpdateRejectsWhatItDoesNotKnow(t *testing.T) {
	h, store, _ := ticketServer(t)
	for name, body := range map[string]map[string]any{
		"unknown repo":     {"repo": "/elsewhere", "ref": "REG-1", "state": "active"},
		"unknown worktree": {"worktree": "/nope", "state": "active"},
		"bad ref":          {"repo": "/r", "ref": "a|b", "state": "active"},
		"bad state":        {"repo": "/r", "ref": "REG-1", "state": "later"},
		"long next step":   {"repo": "/r", "ref": "REG-1", "nextStep": strings.Repeat("x", tickets.MaxNextStep+1)},
	} {
		if code, _ := post(h, "/api/tickets", body); code != 400 {
			t.Errorf("%s: status %d, want 400", name, code)
		}
	}
	if rec := do(h, http.MethodPost, "/api/tickets", "{", nil); rec.Code != 400 {
		t.Errorf("bad json: %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/api/tickets", `{}`, map[string]string{"Content-Type": "text/plain"}); rec.Code != 415 {
		t.Errorf("content type: %d", rec.Code)
	}
	if n := len(store.Snapshot().Tickets); n != 0 {
		t.Fatalf("refused requests stored %d tickets", n)
	}
}

func TestDiscoveryFlow(t *testing.T) {
	h, store, dir := ticketServer(t)

	code, body := post(h, "/api/tickets/discoveries", map[string]any{"worktree": dir, "text": "refresh tokens never expire", "source": "cli"})
	if code != 200 {
		t.Fatalf("add: %d %s", code, body)
	}
	var tk tickets.Ticket
	if err := json.Unmarshal([]byte(body), &tk); err != nil || len(tk.Discoveries) != 1 || tk.Discoveries[0].Source != "cli" {
		t.Fatalf("add reply: %v %s", err, body)
	}
	if code, _ := post(h, "/api/tickets/discoveries", map[string]any{"worktree": dir, "text": "  "}); code != 400 {
		t.Fatalf("empty text: %d", code)
	}

	code, body = post(h, "/api/tickets/triage", map[string]any{"id": tk.ID, "discoveryId": tk.Discoveries[0].ID, "action": "promote"})
	if code != 200 {
		t.Fatalf("promote: %d %s", code, body)
	}
	var parked int
	for _, t2 := range store.Snapshot().Tickets {
		if t2.State == tickets.StateParked && t2.Title == "refresh tokens never expire" {
			parked++
		}
	}
	if parked != 1 {
		t.Fatalf("a promoted discovery becomes exactly one parked ticket: %+v", store.Snapshot().Tickets)
	}

	if code, _ := post(h, "/api/tickets/triage", map[string]any{"id": "/r|nope", "discoveryId": "x", "action": "drop"}); code != 404 {
		t.Fatalf("unknown ticket: %d", code)
	}
	if code, _ := post(h, "/api/tickets/triage", map[string]any{"id": tk.ID, "discoveryId": "x", "action": "drop"}); code != 400 {
		t.Fatalf("unknown discovery: %d", code)
	}
}

func TestDismissOnlyKnownSessions(t *testing.T) {
	h, store, _ := ticketServer(t)
	if code, _ := post(h, "/api/tickets/dismiss", map[string]any{"sessionId": "sess-1", "dismissed": true}); code != 204 {
		t.Fatalf("known session: %d", code)
	}
	if got := store.Snapshot().Dismissed; len(got) != 1 || got[0] != "sess-1" {
		t.Fatalf("dismissed = %v", got)
	}
	if code, _ := post(h, "/api/tickets/dismiss", map[string]any{"sessionId": "made-up", "dismissed": true}); code != 400 {
		t.Fatalf("unknown session: %d", code)
	}
	if code, _ := post(h, "/api/tickets/dismiss", map[string]any{"sessionId": "sess-1", "dismissed": false}); code != 204 || len(store.Snapshot().Dismissed) != 0 {
		t.Fatalf("restore: %d", code)
	}
}

func TestStarsAndNotesMigrateOnceIntoTickets(t *testing.T) {
	ws, dir := fixtureWorkspace(t)
	old := notes.New("")
	_ = old.Set(dir, notes.Note{Starred: true, Text: "next: write tests"})
	store := tickets.New("")
	h := New(func() (workspace.Workspace, error) { return ws, nil }, &fakeLauncher{}, fstest.MapFS{},
		WithNotes(old), WithTickets(store, tickets.Keyer{}))

	do(h, http.MethodGet, "/api/workspace", "", nil)
	tk, ok := store.Get("/r|branch:feat")
	if !ok || tk.State != tickets.StateActive || tk.NextStep != "next: write tests" || !store.Migrated() {
		t.Fatalf("migrated ticket: %+v ok=%v", tk, ok)
	}

	// later edits are not overwritten by the old note
	next := "changed"
	_, _ = store.Apply(tickets.Update{Repo: "/r", Ref: "branch:feat", NextStep: &next})
	do(h, http.MethodGet, "/api/workspace", "", nil)
	if tk, _ := store.Get("/r|branch:feat"); tk.NextStep != "changed" {
		t.Fatalf("the migration must run once: %+v", tk)
	}
}

// nodeBranches answers with a small graph so the server can name each node's ticket.
type nodeBranches struct{ fakeBranches }

func (nodeBranches) Graph(string, map[string]branches.Worktree, bool, bool) (branches.Response, error) {
	return branches.Response{Graph: branches.Graph{Default: "main", Nodes: []branches.Node{
		{Branch: "main", IsDefault: true}, {Branch: "feature/REG-77-x"}, {Branch: "legacy"},
	}}, Warnings: []string{}}, nil
}

func TestBranchNodesCarryTheirTicket(t *testing.T) {
	ws, _ := fixtureWorkspace(t)
	h := New(func() (workspace.Workspace, error) { return ws, nil }, &fakeLauncher{}, fstest.MapFS{},
		WithBranches(&nodeBranches{}), WithTickets(tickets.New(""), tickets.Keyer{}))
	rec := do(h, http.MethodGet, "/api/branches?repo=/r", "", nil)
	var got branches.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
		t.Fatalf("%d %v %s", rec.Code, err, rec.Body)
	}
	want := map[string]string{"main": "", "feature/REG-77-x": "REG-77", "legacy": "branch:legacy"}
	for _, n := range got.Graph.Nodes {
		if n.Ticket != want[n.Branch] {
			t.Errorf("%s: ticket %q, want %q", n.Branch, n.Ticket, want[n.Branch])
		}
	}
}

func TestDependenciesThroughTheAPI(t *testing.T) {
	h, store, _ := ticketServer(t)
	if code, body := post(h, "/api/tickets", map[string]any{"repo": "/r", "ref": "REG-2", "state": "blocked", "blockedReason": "waits", "blockedBy": []string{"/r|REG-1"}}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if tk, _ := store.Get("/r|REG-2"); len(tk.BlockedBy) != 1 || tk.BlockedBy[0] != "/r|REG-1" {
		t.Fatalf("%+v", tk)
	}
	for name, deps := range map[string][]string{
		"a loop":           {"/r|REG-2"},
		"itself":           {"/r|REG-1"},
		"not an id":        {"REG-3"},
		"a repo not known": {"/elsewhere|REG-3"},
	} {
		body := map[string]any{"repo": "/r", "ref": "REG-1", "blockedBy": deps}
		if code, _ := post(h, "/api/tickets", body); code != 400 {
			t.Errorf("%s: status %d, want 400", name, code)
		}
	}
	if tk, _ := store.Get("/r|REG-1"); len(tk.BlockedBy) != 0 {
		t.Fatalf("a refused dependency is not stored: %+v", tk)
	}
}

func TestTicketLinkTemplateIsExposedAndLinksAreSaved(t *testing.T) {
	h, store, _ := ticketServer(t, WithTicketLink("https://tracker.example.com/browse/{key}"))
	rec := do(h, http.MethodGet, "/api/tickets", "", nil)
	if !strings.Contains(rec.Body.String(), `"linkTemplate":"https://tracker.example.com/browse/{key}"`) {
		t.Fatalf("%s", rec.Body)
	}
	if code, body := post(h, "/api/tickets", map[string]any{"repo": "/r", "ref": "REG-1", "link": "https://github.com/o/r/issues/4"}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if tk, _ := store.Get("/r|REG-1"); tk.Link != "https://github.com/o/r/issues/4" {
		t.Fatalf("%+v", tk)
	}
	if code, _ := post(h, "/api/tickets", map[string]any{"repo": "/r", "ref": "REG-1", "link": "javascript:alert(1)"}); code != 400 {
		t.Fatalf("a script link must be refused: %d", code)
	}
}

func TestTranscriptDiscoveriesLandOpenOnceAndStayDecided(t *testing.T) {
	ws, dir := fixtureWorkspace(t)
	ws.Repos[0].Worktrees[0].Sessions[0].Discoveries = []string{"refresh tokens never expire", "retry helper swallows errors"}
	ws.Repos[0].Worktrees = append(ws.Repos[0].Worktrees, workspace.Worktree{
		Name: "main", Path: "/r", Branch: "main", IsMain: true, Ticket: "branch:main",
		Sessions: []claude.Session{{ID: "m1", Discoveries: []string{"never from the main folder"}}},
	})
	store := tickets.New("")
	h := New(func() (workspace.Workspace, error) { return ws, nil }, &fakeLauncher{}, fstest.MapFS{}, WithTickets(store, tickets.Keyer{}))

	do(h, http.MethodGet, "/api/workspace", "", nil)
	tk, ok := store.Get("/r|branch:feat")
	if !ok || len(tk.Discoveries) != 2 || tk.Discoveries[0].Source != tickets.SourceTranscript || tk.Discoveries[0].Status != tickets.DiscoveryOpen {
		t.Fatalf("%+v ok=%v (dir %s)", tk, ok, dir)
	}
	if _, ok := store.Get("/r|branch:main"); ok {
		t.Fatal("the main folder is not a piece of work")
	}

	// the same chat is read again on every refresh: nothing is added twice, and a decision sticks
	if _, _, err := store.Triage(tk.ID, tk.Discoveries[0].ID, tickets.TriageDrop); err != nil {
		t.Fatal(err)
	}
	do(h, http.MethodGet, "/api/workspace", "", nil)
	again, _ := store.Get("/r|branch:feat")
	if len(again.Discoveries) != 2 || again.Discoveries[0].Status != tickets.DiscoveryDropped {
		t.Fatalf("a dropped discovery must not come back: %+v", again.Discoveries)
	}

	// a dismissed session no longer matters: its lines are ignored
	ws.Repos[0].Worktrees[0].Sessions[0].Discoveries = []string{"from a dismissed chat"}
	if err := store.Dismiss("sess-1", true); err != nil {
		t.Fatal(err)
	}
	do(h, http.MethodGet, "/api/workspace", "", nil)
	if final, _ := store.Get("/r|branch:feat"); len(final.Discoveries) != 2 {
		t.Fatalf("%+v", final.Discoveries)
	}
}

func TestSessionsEndpointRefreshesOnlyTheSessionsOfTheSnapshot(t *testing.T) {
	full, dir := fixtureWorkspace(t)
	full.Repos[0].Worktrees[0].Git.Modified = 4
	quick := workspace.Workspace{Repos: []workspace.Repo{{Name: "r", Path: "/r", Worktrees: []workspace.Worktree{{
		Name: "wt", Path: dir, Branch: "feat",
		Sessions: []claude.Session{{ID: "brand-new", Cwd: dir, Resumable: true, Discoveries: []string{"found while watching"}}},
	}}}}}
	store := tickets.New("")
	h := New(func() (workspace.Workspace, error) { return full, nil }, &fakeLauncher{}, fstest.MapFS{},
		WithTickets(store, tickets.Keyer{}), WithSessionsBuild(func() (workspace.Workspace, error) { return quick, nil }))

	do(h, http.MethodGet, "/api/workspace", "", nil) // the full snapshot first
	rec := do(h, http.MethodGet, "/api/sessions", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"brand-new"`) || strings.Contains(rec.Body.String(), `"modified"`) {
		t.Fatalf("only sessions travel: %d %s", rec.Code, rec.Body)
	}

	// an action may now name the new chat, and the git detail of the snapshot survived
	if code, _ := post(h, "/api/actions/resume", map[string]any{"sessionId": "brand-new"}); code != 204 {
		t.Fatalf("resume of a chat seen only by the cheap refresh: %d", code)
	}
	if tk, ok := store.Get("/r|branch:feat"); !ok || len(tk.Discoveries) != 1 {
		t.Fatalf("its DISCOVERY lines are picked up too: %+v", tk)
	}
}

func TestSessionsEndpointFallsBackToTheFullBuild(t *testing.T) {
	h, _, _, builds := newTestServer(t)
	rec := do(h, http.MethodGet, "/api/sessions", "", nil)
	if rec.Code != 200 || *builds != 1 || !strings.Contains(rec.Body.String(), `"sess-1"`) {
		t.Fatalf("%d builds=%d %s", rec.Code, *builds, rec.Body)
	}
}
