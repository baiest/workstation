package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir,
		"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func samePath(t *testing.T, a, b string) bool {
	t.Helper()
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

func commitFile(t *testing.T, dir, name, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", ".")
	mustGit(t, dir, "commit", "-m", msg)
}

func TestBranchHelpers(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	mustGit(t, repo, "init", "-b", "main")
	commitFile(t, repo, "a.txt", "1", "c1")
	mustGit(t, repo, "checkout", "-b", "feat-a")
	commitFile(t, repo, "b.txt", "2", "a1")
	mustGit(t, repo, "checkout", "-b", "feat-b")
	commitFile(t, repo, "c.txt", "3", "b1")
	commitFile(t, repo, "d.txt", "4", "b2")
	mustGit(t, repo, "checkout", "main")
	mustGit(t, repo, "branch", "same-as-main")
	commitFile(t, repo, "e.txt", "5", "main2")

	if _, err := RemoteURL(repo); err == nil {
		t.Error("RemoteURL without origin must error")
	}
	mustGit(t, repo, "remote", "add", "origin", "git@github.com:o/r.git")
	if u, err := RemoteURL(repo); err != nil || u != "git@github.com:o/r.git" {
		t.Errorf("RemoteURL = %q, %v", u, err)
	}

	if d := DefaultBranch(repo); d != "main" {
		t.Errorf("DefaultBranch = %q", d)
	}

	bs, err := LocalBranches(repo)
	if err != nil || len(bs) != 4 {
		t.Fatalf("LocalBranches = %+v, %v", bs, err)
	}
	tips := map[string]string{}
	for _, b := range bs {
		if b.Tip == "" || b.Date == "" {
			t.Errorf("incomplete branch %+v", b)
		}
		tips[b.Name] = b.Tip
	}

	un, err := NoMerged(repo, "main")
	if err != nil || !un["feat-a"] || !un["feat-b"] || un["same-as-main"] || un["main"] {
		t.Errorf("NoMerged = %v, %v", un, err)
	}

	if ok, err := IsAncestor(repo, "feat-a", "feat-b"); err != nil || !ok {
		t.Errorf("feat-a should be ancestor of feat-b: %v %v", ok, err)
	}
	if ok, err := IsAncestor(repo, "main", "feat-b"); err != nil || ok {
		t.Errorf("moved main is not an ancestor of feat-b: %v %v", ok, err)
	}
	if _, err := IsAncestor(repo, "nope", "feat-b"); err == nil {
		t.Error("unknown ref must be an error, not 'false'")
	}

	if n, err := CountBetween(repo, "feat-a", "feat-b"); err != nil || n != 2 {
		t.Errorf("CountBetween = %d, %v", n, err)
	}
	if ahead, behind, err := AheadBehind(repo, "main", "feat-b"); err != nil || ahead != 3 || behind != 1 {
		t.Errorf("AheadBehind = %d/%d, %v", ahead, behind, err)
	}
}

func TestGitIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	wt := filepath.Join(root, "wt")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "add", ".")
	mustGit(t, repo, "commit", "-m", "first")
	mustGit(t, repo, "worktree", "add", "-b", "feat", wt)

	// make the linked worktree dirty: 1 modified, 1 untracked
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "new.txt"), []byte("n"), 0o644); err != nil {
		t.Fatal(err)
	}

	wts, err := Worktrees(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(wts) != 2 || !wts[0].IsMain || wts[1].Branch != "feat" || !samePath(t, wts[1].Path, wt) {
		t.Fatalf("unexpected worktrees: %+v", wts)
	}

	st, err := StatusOf(wt)
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch != "feat" || st.Modified != 1 || st.Untracked != 1 || st.Staged != 0 || !st.Dirty() {
		t.Fatalf("unexpected status: %+v", st)
	}

	c, ok, err := LastCommit(wt)
	if err != nil || !ok || c.Subject != "first" {
		t.Fatalf("unexpected commit: %+v ok=%v err=%v", c, ok, err)
	}

	// from a linked worktree, the repo root resolves to the main worktree
	root2, err := RepoRoot(wt)
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(t, root2, repo) {
		t.Fatalf("RepoRoot(wt) = %q, want %q", root2, repo)
	}

	sub := filepath.Join(wt, "pkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	top, err := TopLevel(sub)
	if err != nil || !samePath(t, top, wt) {
		t.Fatalf("TopLevel(sub) = %q err=%v, want %q", top, err, wt)
	}

	if _, err := RepoRoot(root); err == nil {
		t.Fatal("expected error outside a repo")
	}
}
