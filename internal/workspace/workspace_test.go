package workspace

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"workstation/internal/claude"
)

type fakeProvider struct {
	sessions []claude.Session
	err      error
}

func (f fakeProvider) Sessions() ([]claude.Session, error) { return f.sessions, f.err }

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir,
		"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func mkdir(t *testing.T, p string) string {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// fixture: repo with main + linked worktree "feat" (dirty), plus a stale dir
// under repo/.claude/worktrees that git does not know about.
type fixture struct {
	root, repo, wt, stale, outside string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := fixture{
		root:    root,
		repo:    mkdir(t, filepath.Join(root, "repo")),
		wt:      filepath.Join(root, "wt-feat"),
		stale:   filepath.Join(root, "repo", ".claude", "worktrees", "gone"), // never created
		outside: mkdir(t, filepath.Join(root, "plain-dir")),
	}
	mustGit(t, f.repo, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(f.repo, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, f.repo, "add", ".")
	mustGit(t, f.repo, "commit", "-m", "first")
	mustGit(t, f.repo, "worktree", "add", "-b", "feat", f.wt)
	if err := os.WriteFile(filepath.Join(f.wt, "new.txt"), []byte("n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestBuild(t *testing.T) {
	f := newFixture(t)
	sub := mkdir(t, filepath.Join(f.repo, "pkg"))
	now := time.Now()

	sessions := []claude.Session{
		{ID: "in-wt", Cwd: f.wt, Status: claude.StatusWorking, LastActivity: now},
		{ID: "older-in-wt", Cwd: f.wt, Status: claude.StatusStopped, LastActivity: now.Add(-time.Hour)},
		{ID: "in-subdir", Cwd: sub, Status: claude.StatusStopped, LastActivity: now.Add(-2 * time.Hour)},
		{ID: "stale", Cwd: f.stale, OriginCwd: f.repo, Status: claude.StatusUnknown},
		{ID: "plain", Cwd: f.outside, Status: claude.StatusStopped},
		{ID: "missing", Cwd: filepath.Join(f.root, "nope")},
	}

	ws, err := Builder{Provider: fakeProvider{sessions: sessions}}.Build()
	if err != nil {
		t.Fatal(err)
	}

	if len(ws.Repos) != 1 {
		t.Fatalf("expected 1 repo, got %+v", ws.Repos)
	}
	repo := ws.Repos[0]
	if repo.Name != "repo" || len(repo.Worktrees) != 2 {
		t.Fatalf("unexpected repo: %+v", repo)
	}

	main, feat := repo.Worktrees[0], repo.Worktrees[1]
	if !main.IsMain || main.Name != "repo" || main.Branch != "main" || main.Git.Dirty {
		t.Fatalf("bad main worktree: %+v", main)
	}
	if feat.Name != "wt-feat" || feat.Branch != "feat" || !feat.Git.Dirty || feat.Git.Untracked != 1 ||
		feat.Git.LastCommit == nil || feat.Git.LastCommit.Subject != "first" {
		t.Fatalf("bad linked worktree: %+v", feat)
	}

	if ids := sessionIDs(feat.Sessions); len(ids) != 2 || ids[0] != "in-wt" || ids[1] != "older-in-wt" {
		t.Fatalf("feat sessions (newest first) = %v", ids)
	}
	if ids := sessionIDs(main.Sessions); len(ids) != 1 || ids[0] != "in-subdir" {
		t.Fatalf("main sessions = %v (subdir session must link to its worktree)", ids)
	}

	reasons := map[string]string{}
	for _, u := range ws.Unlinked {
		reasons[u.Session.ID] = u.Reason
		wantResumable := u.Session.ID == "plain" // only sessions whose directory still exists can resume
		if u.Session.Resumable != wantResumable {
			t.Errorf("%s: Resumable = %v, want %v", u.Session.ID, u.Session.Resumable, wantResumable)
		}
	}
	if len(reasons) != 3 || reasons["stale"] == "" || reasons["plain"] == "" || reasons["missing"] == "" {
		t.Fatalf("unlinked = %+v", ws.Unlinked)
	}
	if reasons["stale"] == reasons["plain"] {
		t.Fatalf("stale worktree dir and non-git dir should have different reasons: %v", reasons)
	}
}

func TestBuildExtraRepoWithoutSessions(t *testing.T) {
	f := newFixture(t)

	ws, err := Builder{Provider: fakeProvider{}, ExtraRepos: []string{f.repo, filepath.Join(f.root, "nope")}}.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.Repos) != 1 || len(ws.Repos[0].Worktrees) != 2 {
		t.Fatalf("configured repo not listed: %+v", ws.Repos)
	}
	if len(ws.Warnings) != 1 {
		t.Fatalf("invalid configured repo should warn, got %v", ws.Warnings)
	}
}

func TestBuildProviderErrorIsWarning(t *testing.T) {
	f := newFixture(t)
	ws, err := Builder{Provider: fakeProvider{err: errors.New("boom")}, ExtraRepos: []string{f.repo}}.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.Warnings) != 1 || len(ws.Repos) != 1 {
		t.Fatalf("expected a warning and git data anyway: %+v", ws)
	}
}

func TestBuildWorktreeWithMissingDirReportsGitError(t *testing.T) {
	f := newFixture(t)
	if err := os.RemoveAll(f.wt); err != nil {
		t.Fatal(err)
	}
	ws, err := Builder{Provider: fakeProvider{}, ExtraRepos: []string{f.repo}}.Build()
	if err != nil {
		t.Fatal(err)
	}
	if got := ws.Repos[0].Worktrees[1]; got.GitError == "" {
		t.Fatalf("expected GitError for missing worktree dir: %+v", got)
	}
}

func sessionIDs(ss []claude.Session) []string {
	var ids []string
	for _, s := range ss {
		ids = append(ids, s.ID)
	}
	return ids
}

func TestNormalize(t *testing.T) {
	a := normalize(`C:\Users\Me\Repo\`)
	b := normalize(`c:\users\me\repo`)
	if a != b && isCaseInsensitiveOS() {
		t.Fatalf("expected case-insensitive match: %q vs %q", a, b)
	}
}
