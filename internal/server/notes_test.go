package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"workstation/internal/notes"
	"workstation/internal/workspace"
)

func notesServer(t *testing.T) (http.Handler, string, string) {
	t.Helper()
	ws, dir := fixtureWorkspace(t)
	store := notes.New(filepath.Join(t.TempDir(), "notes.json"))
	h := New(func() (workspace.Workspace, error) { return ws, nil }, &fakeLauncher{}, fstest.MapFS{}, WithNotes(store))
	return h, dir, filepath.Join(t.TempDir(), "x")
}

func TestNotesRoundTrip(t *testing.T) {
	h, dir, _ := notesServer(t)
	body, _ := json.Marshal(map[string]any{"path": dir, "starred": true, "text": "next: tests"})
	if rec := do(h, http.MethodPost, "/api/notes", string(body), nil); rec.Code != 204 {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}
	rec := do(h, http.MethodGet, "/api/notes", "", nil)
	var got map[string]notes.Note
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || !got[dir].Starred || got[dir].Text != "next: tests" {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestNotesRejectUnknownWorktreeAndLongText(t *testing.T) {
	h, dir, other := notesServer(t)
	unknown, _ := json.Marshal(map[string]any{"path": other, "starred": true})
	if rec := do(h, http.MethodPost, "/api/notes", string(unknown), nil); rec.Code != 400 {
		t.Errorf("unknown worktree: %d", rec.Code)
	}
	long, _ := json.Marshal(map[string]any{"path": dir, "text": strings.Repeat("x", notes.MaxText+1)})
	if rec := do(h, http.MethodPost, "/api/notes", string(long), nil); rec.Code != 400 {
		t.Errorf("long text: %d", rec.Code)
	}
}

func TestNotesDisabledWithoutStore(t *testing.T) {
	h, _, _, _ := newTestServer(t)
	if rec := do(h, http.MethodGet, "/api/notes", "", nil); rec.Code != 404 {
		t.Fatalf("got %d", rec.Code)
	}
}

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
