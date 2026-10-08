package server

import (
	"net/http"
	"testing"
	"testing/fstest"

	"workstation/internal/notes"
	"workstation/internal/workspace"
)

func TestRemovingAWorktreeForgetsItsNote(t *testing.T) {
	ws, _ := fixtureWorkspace(t)
	store := notes.New("")
	_ = store.Set("/p/done", notes.Note{Starred: true})
	_ = store.Set("/p/other", notes.Note{Starred: true})
	h := New(func() (workspace.Workspace, error) { return ws, nil }, &fakeLauncher{}, fstest.MapFS{},
		WithNotes(store), WithWorktreeCleanup(&fakeWT{}))

	body := `{"repo":"/r","days":14,"worktrees":[{"path":"/p/done","sha":"abc"}]}`
	if rec := do(h, http.MethodPost, "/api/worktree-cleanup", body, nil); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	got := store.All()
	if _, kept := got["/p/done"]; kept || !got["/p/other"].Starred {
		t.Fatalf("only the removed worktree's note goes: %v", got)
	}
}

func TestWorktreeCleanupPassesTheDormantAge(t *testing.T) {
	fw := &fakeWT{}
	h, _ := newServerWith(t, WithWorktreeCleanup(fw))
	if rec := do(h, http.MethodGet, "/api/worktree-cleanup?repo=/r&days=7&dormantDays=21", "", nil); rec.Code != 200 || fw.dormant != 21 {
		t.Fatalf("preview: %d dormant=%d", rec.Code, fw.dormant)
	}
	if rec := do(h, http.MethodGet, "/api/worktree-cleanup?repo=/r&dormantDays=x", "", nil); rec.Code != 400 {
		t.Fatalf("a bad age must be 400, got %d", rec.Code)
	}
	body := `{"repo":"/r","days":7,"dormantDays":21,"worktrees":[{"path":"/p/done","sha":"abc"}]}`
	if rec := do(h, http.MethodPost, "/api/worktree-cleanup", body, nil); rec.Code != 200 || fw.dormant != 21 {
		t.Fatalf("remove: %d dormant=%d", rec.Code, fw.dormant)
	}
}
