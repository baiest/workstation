package server

import (
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"workstation/internal/claude"
	"workstation/internal/workspace"
)

func TestStaleSessionsOnlyListsStoppedOldOrOrphaned(t *testing.T) {
	now := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	live := t.TempDir()
	old := now.AddDate(0, 0, -30)
	recent := now.AddDate(0, 0, -1)
	repo := workspace.Repo{Name: "r", Path: "/r", Worktrees: []workspace.Worktree{{Path: live, Sessions: []claude.Session{
		{ID: "old-stopped", Cwd: live, Status: claude.StatusStopped, LastActivity: old},
		{ID: "recent-stopped", Cwd: live, Status: claude.StatusStopped, LastActivity: recent},
		{ID: "old-working", Cwd: live, Status: claude.StatusWorking, LastActivity: old},
		{ID: "old-idle", Cwd: live, Status: claude.StatusIdle, LastActivity: old},
		{ID: "old-unknown", Cwd: live, Status: claude.StatusUnknown, LastActivity: old},
		{ID: "gone-dir", Cwd: "/does/not/exist", Status: claude.StatusStopped, LastActivity: recent},
	}}}}
	ws := workspace.Workspace{Unlinked: []workspace.Unlinked{
		{Session: claude.Session{ID: "unlinked-in", Cwd: "/r/.wt/x", Status: claude.StatusStopped, LastActivity: recent}},
		{Session: claude.Session{ID: "unlinked-out", Cwd: "/other/x", Status: claude.StatusStopped, LastActivity: old}},
	}}

	var got []string
	for _, s := range staleSessions(repo, ws, 14, now) {
		got = append(got, s.ID)
	}
	if strings.Join(got, ",") != "old-stopped,gone-dir,unlinked-in" {
		t.Fatalf("got %v", got)
	}
}

func TestCleanupActionLaunchesClaudeInTheRepo(t *testing.T) {
	ws, dir := fixtureWorkspace(t)
	ws.Repos[0].Worktrees[0].Sessions = append(ws.Repos[0].Worktrees[0].Sessions,
		claude.Session{ID: "stale-one", Cwd: dir, Status: claude.StatusStopped, LastActivity: time.Now().AddDate(0, 0, -60)},
		claude.Session{ID: "busy-one", Cwd: dir, Status: claude.StatusWorking, LastActivity: time.Now().AddDate(0, 0, -60)})
	l := &fakeLauncher{}
	h := New(func() (workspace.Workspace, error) { return ws, nil }, l, fstest.MapFS{}, WithWorktreeCleanup(&fakeWT{}))

	if rec := do(h, http.MethodPost, "/api/actions/cleanup", `{"path":"/r","days":30}`, nil); rec.Code != 204 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(l.prompts) != 1 || l.calls[0] != "prompt:/r" {
		t.Fatalf("calls %v", l.calls)
	}
	p := l.prompts[0]
	for _, want := range []string{"/p/done", "stale-one", "Ask for my confirmation"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "busy-one") {
		t.Errorf("a running session must not be offered:\n%s", p)
	}

	if rec := do(h, http.MethodPost, "/api/actions/cleanup", `{"path":"/not/a/repo"}`, nil); rec.Code != 400 {
		t.Errorf("unknown repo: %d", rec.Code)
	}
	if len(l.prompts) != 1 {
		t.Error("an unknown repo must not launch anything")
	}
}
