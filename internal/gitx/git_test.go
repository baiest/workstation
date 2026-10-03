package gitx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// A repo's own .git/config can name a command (core.fsmonitor) that `git status`
// runs. Repos come from wherever the user ran Claude, so we must not run it.
func TestStatusDoesNotRunRepoConfiguredCommands(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	mustGit(t, repo, "init", "-b", "main")
	commitFile(t, repo, "a.txt", "1", "c1")

	marker := filepath.ToSlash(filepath.Join(t.TempDir(), "pwned"))
	mustGit(t, repo, "config", "core.fsmonitor", "echo x > '"+marker+"'; :")

	// precondition: the vector is real here (plain git runs it); otherwise this test proves nothing
	plain := exec.Command("git", "-C", repo, "status", "--porcelain")
	_ = plain.Run()
	if _, err := os.Stat(marker); err != nil {
		t.Skip("this git does not run core.fsmonitor commands; nothing to defend against")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	if _, err := StatusOf(repo); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("StatusOf executed the repository's core.fsmonitor command")
	}
}

func TestRunTimesOutHungGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	mustGit(t, repo, "init", "-b", "main")

	old := Timeout
	Timeout = 300 * time.Millisecond
	defer func() { Timeout = old }()

	start := time.Now()
	_, err := run(repo, "-c", "alias.hang=!sleep 20", "hang")
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected a deadline error, got %v", err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("a hung git must be killed at the timeout, took %s", took)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error should say it timed out: %v", err)
	}
}

func TestTimeoutIsNotReadAsNotAncestor(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	mustGit(t, repo, "init", "-b", "main")
	commitFile(t, repo, "a.txt", "1", "c1")

	old := Timeout
	Timeout = time.Nanosecond
	defer func() { Timeout = old }()
	if ok, err := IsAncestor(repo, "main", "main"); err == nil || ok {
		t.Fatalf("a timeout must surface as an error, got ok=%v err=%v", ok, err)
	}
}

// A tag named like a branch wins plain ref resolution; branch helpers must
// always mean the branch, or a hostile repo could distort the graph.
func TestBranchHelpersIgnoreSameNamedTags(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	mustGit(t, repo, "init", "-b", "main")
	commitFile(t, repo, "a.txt", "1", "c1")
	mustGit(t, repo, "tag", "feat") // tag "feat" -> main's commit
	mustGit(t, repo, "checkout", "-b", "feat")
	commitFile(t, repo, "b.txt", "2", "f1")
	mustGit(t, repo, "checkout", "main")

	if n, err := CountBetween(repo, "main", "feat"); err != nil || n != 1 {
		t.Errorf("CountBetween must use the branch, got %d %v", n, err)
	}
	if a, b, err := AheadBehind(repo, "main", "feat"); err != nil || a != 1 || b != 0 {
		t.Errorf("AheadBehind must use the branch, got %d/%d %v", a, b, err)
	}
	if ok, err := IsAncestor(repo, "main", "feat"); err != nil || !ok {
		t.Errorf("main is an ancestor of the feat branch: %v %v", ok, err)
	}
	if ok, err := IsAncestor(repo, "feat", "main"); err != nil || ok {
		t.Errorf("the feat branch is not an ancestor of main: %v %v", ok, err)
	}
	un, err := NoMerged(repo, "main")
	if err != nil || !un["feat"] {
		t.Errorf("NoMerged = %v %v", un, err)
	}
}

func TestBranchTip(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	mustGit(t, repo, "init", "-b", "main")
	commitFile(t, repo, "a.txt", "1", "c1")
	mustGit(t, repo, "tag", "main") // a tag with the branch's name must not confuse it

	tip, err := BranchTip(repo, "main")
	if err != nil || len(tip) != 40 {
		t.Fatalf("BranchTip = %q, %v", tip, err)
	}
	if _, err := BranchTip(repo, "nope"); err == nil {
		t.Error("an unknown branch must be an error")
	}
	if _, err := BranchTip(repo, "--all"); err == nil {
		t.Error("an option-looking name must be refused")
	}
}

func TestDeleteBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	mustGit(t, repo, "init", "-b", "main")
	commitFile(t, repo, "a.txt", "1", "c1")
	for _, b := range []string{"gone", "moved", "checked-out"} {
		mustGit(t, repo, "branch", b)
	}
	mustGit(t, repo, "checkout", "checked-out")
	main, _ := BranchTip(repo, "main")

	// happy path: expected tip matches
	if err := DeleteBranch(repo, "gone", main); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := BranchTip(repo, "gone"); err == nil {
		t.Fatal("branch should be gone")
	}

	// the branch moved since the preview: refuse, keep it
	if err := DeleteBranch(repo, "moved", "0000000000000000000000000000000000000000"); err == nil {
		t.Fatal("a branch whose tip differs from the previewed one must not be deleted")
	}
	if _, err := BranchTip(repo, "moved"); err != nil {
		t.Fatal("the branch must still exist")
	}

	// git itself refuses a checked-out branch
	if err := DeleteBranch(repo, "checked-out", main); err == nil {
		t.Fatal("a checked-out branch must not be deleted")
	}

	for _, bad := range []string{"", "-D", "--all", "a b", "x..y"} {
		if err := DeleteBranch(repo, bad, main); err == nil {
			t.Errorf("name %q must be refused", bad)
		}
	}
}

func TestRemoteContains(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	bare := filepath.Join(root, "origin.git")
	repo := filepath.Join(root, "repo")
	for _, d := range []string{bare, repo} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustGit(t, bare, "init", "--bare", "-b", "main")
	mustGit(t, repo, "init", "-b", "main")
	commitFile(t, repo, "a.txt", "1", "c1")
	mustGit(t, repo, "remote", "add", "origin", bare)
	mustGit(t, repo, "branch", "pushed")
	mustGit(t, repo, "checkout", "-b", "local-only")
	commitFile(t, repo, "b.txt", "2", "only here")
	mustGit(t, repo, "checkout", "main")
	mustGit(t, repo, "push", "origin", "main", "pushed")
	mustGit(t, repo, "fetch", "origin")

	pushed, _ := BranchTip(repo, "pushed")
	local, _ := BranchTip(repo, "local-only")

	if ok, err := RemoteContains(repo, pushed); err != nil || !ok {
		t.Errorf("a pushed commit is on a remote: %v %v", ok, err)
	}
	if ok, err := RemoteContains(repo, local); err != nil || ok {
		t.Errorf("a commit that was never pushed is not: %v %v", ok, err)
	}
	if _, err := RemoteContains(repo, "--all"); err == nil {
		t.Error("an option-looking value must be refused")
	}
	if ok, err := RemoteContains(repo, "0000000000000000000000000000000000000000"); err == nil && ok {
		t.Error("an unknown commit cannot be on a remote")
	}

	noRemote := t.TempDir()
	mustGit(t, noRemote, "init", "-b", "main")
	commitFile(t, noRemote, "a.txt", "1", "c1")
	tip, _ := BranchTip(noRemote, "main")
	if ok, err := RemoteContains(noRemote, tip); err != nil || ok {
		t.Errorf("a repo without remotes has nothing on a remote: %v %v", ok, err)
	}
}

func TestHeadSHA(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	mustGit(t, repo, "init", "-b", "main")
	commitFile(t, repo, "a.txt", "1", "c1")
	want, _ := BranchTip(repo, "main")

	if got, err := HeadSHA(repo); err != nil || got != want {
		t.Fatalf("HeadSHA = %q, %v; want %q", got, err, want)
	}
	if _, err := HeadSHA(filepath.Join(repo, "missing")); err == nil {
		t.Error("a missing directory must be an error")
	}
}

func TestRemoveWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "init", "-b", "main")
	commitFile(t, repo, "a.txt", "1", "c1")
	addWT := func(name string) string {
		p := filepath.Join(root, name)
		mustGit(t, repo, "worktree", "add", "-b", name, p)
		return p
	}
	clean, dirty, moved := addWT("clean"), addWT("dirty"), addWT("moved")
	sha := func(p string) string {
		s, err := HeadSHA(p)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	// happy path
	if err := RemoveWorktree(repo, clean, sha(clean)); err != nil {
		t.Fatalf("remove clean worktree: %v", err)
	}
	if _, err := os.Stat(clean); !os.IsNotExist(err) {
		t.Fatal("the folder must be gone")
	}
	if _, err := BranchTip(repo, "clean"); err != nil {
		t.Fatal("removing a worktree must keep its branch")
	}

	// an untracked file: git refuses and nothing is deleted (we never pass --force)
	if err := os.WriteFile(filepath.Join(dirty, "notes.txt"), []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveWorktree(repo, dirty, sha(dirty)); err == nil {
		t.Fatal("a worktree with untracked files must not be removed")
	}
	if _, err := os.Stat(filepath.Join(dirty, "notes.txt")); err != nil {
		t.Fatal("the user's file must still be there")
	}

	// HEAD moved since the preview
	if err := RemoveWorktree(repo, moved, "0000000000000000000000000000000000000000"); err == nil {
		t.Fatal("a worktree whose HEAD differs from the previewed commit must not be removed")
	}
	if _, err := os.Stat(moved); err != nil {
		t.Fatal("the folder must still exist")
	}

	// never the main worktree
	if err := RemoveWorktree(repo, repo, sha(repo)); err == nil {
		t.Fatal("the main worktree must not be removable")
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		t.Fatal("the repository must be intact")
	}
}

func TestDefaultBranchRejectsOddNames(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	mustGit(t, repo, "init", "-b", "main")
	commitFile(t, repo, "a.txt", "1", "c1")
	mustGit(t, repo, "remote", "add", "origin", "git@github.com:o/r.git")
	// a symbolic ref target that is not a valid branch name
	mustGit(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/--output=x")

	if d := DefaultBranch(repo); d != "main" {
		t.Errorf("an invalid origin/HEAD must fall back to local main, got %q", d)
	}
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
