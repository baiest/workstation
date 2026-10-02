package branches

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"workstation/internal/forge"
)

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir,
		"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func commit(t *testing.T, dir, file, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(msg), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", ".")
	mustGit(t, dir, "commit", "-m", msg)
}

// newRepo: main(c1) -> feat-a(+1) -> feat-b(+1, stacked on feat-a);
// merged-old merged into main; same-as-main points at main's tip.
func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	mustGit(t, repo, "init", "-b", "main")
	commit(t, repo, "a", "c1")
	mustGit(t, repo, "checkout", "-b", "feat-a")
	commit(t, repo, "b", "a1")
	mustGit(t, repo, "checkout", "-b", "feat-b")
	commit(t, repo, "c", "b1")
	mustGit(t, repo, "checkout", "main")
	mustGit(t, repo, "checkout", "-b", "merged-old")
	commit(t, repo, "d", "m1")
	mustGit(t, repo, "checkout", "main")
	mustGit(t, repo, "merge", "--no-ff", "-m", "merge old", "merged-old")
	mustGit(t, repo, "branch", "same-as-main")
	return repo
}

func nodeMap(g Graph) map[string]Node {
	m := map[string]Node{}
	for _, n := range g.Nodes {
		m[n.Branch] = n
	}
	return m
}

func TestBuildAncestryGraph(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)

	g, err := Build(repo, Input{})
	if err != nil {
		t.Fatal(err)
	}
	if g.Default != "main" {
		t.Fatalf("default = %q", g.Default)
	}
	nodes := nodeMap(g)
	if len(nodes) != 3 {
		t.Fatalf("expected main, feat-a, feat-b (merged/equal branches hidden), got %+v", g.Nodes)
	}

	if !nodes["main"].IsDefault || nodes["main"].Parent != "" {
		t.Errorf("main: %+v", nodes["main"])
	}
	a, b := nodes["feat-a"], nodes["feat-b"]
	if a.Parent != "main" || a.Via != ViaGit || a.Ahead != 1 || a.Behind != 2 {
		t.Errorf("feat-a: %+v", a)
	}
	if b.Parent != "feat-a" || b.Via != ViaGit || b.Ahead != 2 {
		t.Errorf("feat-b should depend on feat-a (ancestry): %+v", b)
	}
	if g.Nodes[0].Branch != "main" {
		t.Errorf("default branch must come first: %+v", g.Nodes)
	}
	if _, err := time.Parse(time.RFC3339, a.Date); err != nil {
		t.Errorf("node must carry the tip date (to flag stale branches): %q", a.Date)
	}
}

func TestBuildPRBaseOverridesAncestry(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	prs := []forge.PR{{Number: 7, Source: "feat-b", Dest: "main", State: forge.StateOpen, Title: "b"}}

	g, err := Build(repo, Input{PRs: prs})
	if err != nil {
		t.Fatal(err)
	}
	b := nodeMap(g)["feat-b"]
	if b.Parent != "main" || b.Via != ViaPR || b.PR == nil || b.PR.Number != 7 {
		t.Fatalf("PR base must win: %+v", b)
	}
}

func TestBuildPRWithUnknownDestFallsBackToGit(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	prs := []forge.PR{{Number: 7, Source: "feat-b", Dest: "release/1.0", State: forge.StateOpen}}

	g, err := Build(repo, Input{PRs: prs})
	if err != nil {
		t.Fatal(err)
	}
	if b := nodeMap(g)["feat-b"]; b.Parent != "feat-a" || b.Via != ViaGit || b.PR == nil {
		t.Fatalf("unknown PR dest should fall back to ancestry but keep the PR: %+v", b)
	}
}

func TestBuildWorktreeBranchesAreNodes(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	in := Input{Worktrees: map[string]Worktree{"same-as-main": {Name: "wt-same", Path: "/wt/same"}}}

	g, err := Build(repo, in)
	if err != nil {
		t.Fatal(err)
	}
	n, ok := nodeMap(g)["same-as-main"]
	if !ok || n.Worktree == nil || n.Worktree.Name != "wt-same" || n.Parent != "main" || !n.Merged {
		t.Fatalf("worktree branch should appear under main, flagged merged: %+v", n)
	}
}

func TestBuildMergedPRsToggle(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	now := time.Now()
	prs := []forge.PR{
		{Number: 1, Source: "merged-old", Dest: "main", State: forge.StateMerged, UpdatedAt: now.Add(-24 * time.Hour)},
		{Number: 2, Source: "same-as-main", Dest: "main", State: forge.StateMerged, UpdatedAt: now.Add(-90 * 24 * time.Hour)},
	}

	hidden, err := Build(repo, Input{PRs: prs, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := nodeMap(hidden)["merged-old"]; ok {
		t.Error("merged branch must be hidden by default")
	}

	shown, err := Build(repo, Input{PRs: prs, Now: now, IncludeMerged: true})
	if err != nil {
		t.Fatal(err)
	}
	m := nodeMap(shown)
	if n, ok := m["merged-old"]; !ok || !n.Merged || n.Parent != "main" || n.PR == nil {
		t.Errorf("recent merged PR branch should show: %+v", n)
	}
	if _, ok := m["same-as-main"]; ok {
		t.Error("PR merged 90 days ago is outside the 30 day window")
	}
}

func TestBuildMergedPRBranchWithCommitsAheadIsNotActive(t *testing.T) {
	t.Parallel()
	// squash-merged branches stay "unmerged" for git; the merged PR must hide them
	repo := newRepo(t)
	prs := []forge.PR{{Number: 3, Source: "feat-a", Dest: "main", State: forge.StateMerged, UpdatedAt: time.Now()}}
	g, err := Build(repo, Input{PRs: prs})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := nodeMap(g)["feat-a"]; ok {
		t.Fatalf("feat-a (PR merged) should be hidden: %+v", g.Nodes)
	}
}

func TestBuildNodeCap(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	for _, name := range []string{"x1", "x2", "x3"} {
		mustGit(t, repo, "checkout", "-b", name, "main")
		commit(t, repo, name, name)
	}
	mustGit(t, repo, "checkout", "main")

	g, err := Build(repo, Input{MaxNodes: 4})
	if err != nil {
		t.Fatal(err)
	}
	// candidates: main + feat-a, feat-b, x1, x2, x3 = 6, cap 4 -> 2 hidden
	if len(g.Nodes) != 4 || g.Hidden != 2 {
		t.Fatalf("nodes=%d hidden=%d", len(g.Nodes), g.Hidden)
	}
	if g.Nodes[0].Branch != "main" {
		t.Errorf("default must survive the cap")
	}
}

func TestBuildPrefersWorktreeAndOpenPRUnderCap(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	for _, name := range []string{"x1", "x2"} {
		mustGit(t, repo, "checkout", "-b", name, "main")
		commit(t, repo, name, name)
	}
	mustGit(t, repo, "checkout", "main")
	in := Input{
		MaxNodes:  3,
		Worktrees: map[string]Worktree{"x1": {Name: "x1", Path: "/x1"}},
		PRs:       []forge.PR{{Number: 9, Source: "x2", Dest: "main", State: forge.StateOpen}},
	}

	g, err := Build(repo, in)
	if err != nil {
		t.Fatal(err)
	}
	m := nodeMap(g)
	if _, ok := m["x1"]; !ok {
		t.Error("worktree branch dropped by cap")
	}
	if _, ok := m["x2"]; !ok {
		t.Error("open-PR branch dropped by cap")
	}
}

func TestBuildNoDefaultBranch(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	mustGit(t, repo, "init", "-b", "trunk")
	commit(t, repo, "a", "c1")
	if _, err := Build(repo, Input{}); err == nil {
		t.Fatal("expected an error when no default branch can be determined")
	}
}
