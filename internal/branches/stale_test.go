package branches

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"workstation/internal/forge"
	"workstation/internal/gitx"
)

// commitAt commits with a chosen date, so a branch can really be "old".
func commitAt(t *testing.T, dir, file string, when time.Time) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", ".")
	cmd := exec.Command("git", "-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false", "commit", "-m", file)
	stamp := when.UTC().Format("2006-01-02T15:04:05Z")
	cmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE="+stamp, "GIT_AUTHOR_DATE="+stamp)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
}

// staleFixture: branches without a (merged) PR, of every kind of risk.
//
//	merged-pr     PR merged 40 days ago, tip == PR head           -> pr-merged
//	stale-merged  old commit, already merged into main            -> stale-merged  (safe)
//	stale-remote  old commit, pushed to origin                    -> stale-on-remote
//	stale-local   old commit, exists only on this machine         -> stale-local-only
//	declined      old commit, PR was declined, only local         -> stale-local-only
//	has-open      old commit but an open PR                       -> ignored
//	has-wt        old commit but checked out in a worktree        -> ignored
//	fresh-local   unmerged, unpushed, but committed 5 days ago    -> ignored
type staleFixture struct {
	repo string
	now  time.Time
	prs  []forge.PR
	in   CleanupInput
}

func newStaleFixture(t *testing.T) staleFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	bare, repo := filepath.Join(root, "origin.git"), filepath.Join(root, "repo")
	for _, d := range []string{bare, repo} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	ago := func(days int) time.Time { return now.Add(-time.Duration(days) * day) }

	mustGit(t, bare, "init", "--bare", "-b", "main")
	mustGit(t, repo, "init", "-b", "main")
	mustGit(t, repo, "remote", "add", "origin", bare)
	commitAt(t, repo, "base.txt", ago(200))

	branch := func(name string, when time.Time) {
		mustGit(t, repo, "checkout", "-b", name, "main")
		commitAt(t, repo, name+".txt", when)
		mustGit(t, repo, "checkout", "main")
	}
	for name, days := range map[string]int{"merged-pr": 60, "stale-remote": 60, "stale-local": 45, "declined": 50, "has-open": 50, "has-wt": 50} {
		branch(name, ago(days))
	}
	branch("fresh-local", ago(5))
	branch("stale-merged", ago(90))
	mustGit(t, repo, "merge", "--no-ff", "-m", "merge stale-merged", "stale-merged")
	mustGit(t, repo, "push", "origin", "main", "stale-remote")
	mustGit(t, repo, "fetch", "origin")

	tip := func(b string) string {
		s, err := gitx.BranchTip(repo, b)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	prs := []forge.PR{
		{Number: 1, Title: "merged one", Source: "merged-pr", Dest: "main", State: forge.StateMerged, HeadSHA: tip("merged-pr"), MergedAt: ago(40), UpdatedAt: ago(40)},
		{Number: 2, Title: "dropped", Source: "declined", Dest: "main", State: forge.StateDeclined, HeadSHA: tip("declined"), UpdatedAt: ago(40)},
		{Number: 3, Title: "still open", Source: "has-open", Dest: "main", State: forge.StateOpen, HeadSHA: tip("has-open"), UpdatedAt: now},
	}
	return staleFixture{
		repo: repo, now: now, prs: prs,
		in: CleanupInput{PRs: prs, PRsKnown: true, Now: now, Worktrees: map[string]Worktree{"has-wt": {Name: "wt", Path: "/wt"}}},
	}
}

func kinds(p CleanupPreview) map[string]string {
	m := map[string]string{}
	for _, c := range p.Candidates {
		m[c.Branch] = c.Kind
	}
	return m
}

func TestStaleBranchesAreOfferedByRisk(t *testing.T) {
	t.Parallel()
	f := newStaleFixture(t)

	got, err := CleanupCandidates(f.repo, f.in)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"merged-pr":    KindPRMerged,
		"stale-merged": KindStaleMerged,
		"stale-remote": KindStaleOnRemote,
		"stale-local":  KindStaleLocalOnly,
		"declined":     KindStaleLocalOnly,
	}
	if k := kinds(got); len(k) != len(want) {
		t.Fatalf("candidates = %v, want %v", k, want)
	}
	for b, k := range kinds(got) {
		if want[b] != k {
			t.Errorf("%s: kind %q, want %q", b, k, want[b])
		}
	}

	by := map[string]Candidate{}
	for _, c := range got.Candidates {
		by[c.Branch] = c
	}
	if by["stale-local"].Ahead != 1 {
		t.Errorf("a local-only branch says how many commits are at stake: %+v", by["stale-local"])
	}
	if by["stale-local"].LastCommit.IsZero() || by["stale-local"].SHA == "" {
		t.Errorf("stale candidates carry the last commit date and sha: %+v", by["stale-local"])
	}
	if by["declined"].PRNumber != 2 {
		t.Errorf("a declined PR is still shown: %+v", by["declined"])
	}
	for _, never := range []string{"has-open", "has-wt", "fresh-local", "main"} {
		if _, ok := by[never]; ok {
			t.Errorf("%s must not be offered", never)
		}
	}
}

func TestSafeCandidatesComeFirst(t *testing.T) {
	t.Parallel()
	f := newStaleFixture(t)
	got, _ := CleanupCandidates(f.repo, f.in)
	var order []string
	for _, c := range got.Candidates {
		order = append(order, c.Kind)
	}
	want := []string{KindPRMerged, KindStaleMerged, KindStaleOnRemote, KindStaleLocalOnly, KindStaleLocalOnly}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

// Without PR data every branch looks "PR-less": nothing may be offered on that basis.
func TestNoStaleBranchesWithoutPullRequestData(t *testing.T) {
	t.Parallel()
	f := newStaleFixture(t)
	f.in.PRsKnown = false
	got, err := CleanupCandidates(f.repo, f.in)
	if err != nil {
		t.Fatal(err)
	}
	if k := kinds(got); len(k) != 1 || k["merged-pr"] != KindPRMerged {
		t.Fatalf("only the branch proven merged by its PR may remain: %v", k)
	}
}

func TestStaleAgeUsesTheLastCommit(t *testing.T) {
	t.Parallel()
	f := newStaleFixture(t)

	f.in.Days = 55 // stale-remote (60 days) and stale-merged (90) qualify; stale-local (45) and declined (50) do not
	got, _ := CleanupCandidates(f.repo, f.in)
	k := kinds(got)
	if _, ok := k["stale-local"]; ok {
		t.Errorf("45 days is not older than 55: %v", k)
	}
	if k["stale-remote"] != KindStaleOnRemote || k["stale-merged"] != KindStaleMerged {
		t.Errorf("older branches must qualify: %v", k)
	}

	f.in.Days = 1 // everything unmerged and PR-less qualifies, including the 5-day-old one
	got, _ = CleanupCandidates(f.repo, f.in)
	if kinds(got)["fresh-local"] != KindStaleLocalOnly {
		t.Errorf("1 day: %v", kinds(got))
	}
}

func TestLocalOnlyBranchesNeedAnExplicitYes(t *testing.T) {
	t.Parallel()
	f := newStaleFixture(t)
	preview, _ := CleanupCandidates(f.repo, f.in)
	sha := map[string]string{}
	for _, c := range preview.Candidates {
		sha[c.Branch] = c.SHA
	}
	reqs := []DeleteRequest{
		{Branch: "stale-merged", SHA: sha["stale-merged"]},
		{Branch: "stale-remote", SHA: sha["stale-remote"]},
		{Branch: "stale-local", SHA: sha["stale-local"]},
	}

	results, err := DeleteBranches(f.repo, f.in, reqs)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]DeleteResult{}
	for _, r := range results {
		by[r.Branch] = r
	}
	if !by["stale-merged"].Deleted || !by["stale-remote"].Deleted {
		t.Errorf("merged and remote-backed branches delete normally: %+v", results)
	}
	if by["stale-local"].Deleted || !strings.Contains(by["stale-local"].Error, "only") {
		t.Errorf("a local-only branch must not be deleted without the explicit yes: %+v", by["stale-local"])
	}
	if _, err := gitx.BranchTip(f.repo, "stale-local"); err != nil {
		t.Fatal("the local-only branch must still exist")
	}

	f.in.AllowLocalOnly = true
	results, err = DeleteBranches(f.repo, f.in, []DeleteRequest{{Branch: "stale-local", SHA: sha["stale-local"]}})
	if err != nil || !results[0].Deleted {
		t.Fatalf("with the explicit yes it is deleted: %+v %v", results, err)
	}
	if _, err := gitx.BranchTip(f.repo, "stale-local"); err == nil {
		t.Fatal("the branch should be gone")
	}
}
