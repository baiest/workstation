package branches

import (
	"errors"
	"strings"
	"testing"
	"time"

	"workstation/internal/forge"
	"workstation/internal/gitx"
)

const day = 24 * time.Hour

// cleanupFixture builds a repo with one branch per situation the cleanup must tell apart:
//
//	old-squash    unmerged in git (squash-merged PR), tip == PR head, merged 45 days ago  -> candidate
//	old-ancestor  already inside main, PR merged 60 days ago, PR head differs             -> candidate
//	recent        PR merged 5 days ago                                                    -> too recent
//	diverged      PR merged 90 days ago but the branch has commits the PR did not merge   -> kept
//	has-wt        PR merged 90 days ago but it has a worktree                             -> kept
//	open          PR still open                                                           -> ignored
//	no-pr         no PR at all                                                            -> ignored
type cleanupFixture struct {
	repo string
	now  time.Time
	prs  []forge.PR
	in   CleanupInput
}

func newCleanupFixture(t *testing.T) cleanupFixture {
	t.Helper()
	repo := newRepo(t) // main with c1, merged-old, ...; we add our own branches on top
	mustGit(t, repo, "checkout", "main")

	branchWithCommit := func(name string) {
		mustGit(t, repo, "checkout", "-b", name, "main")
		commit(t, repo, name+".txt", name)
		mustGit(t, repo, "checkout", "main")
	}
	for _, b := range []string{"old-squash", "recent", "diverged", "has-wt", "open", "no-pr"} {
		branchWithCommit(b)
	}
	mustGit(t, repo, "branch", "old-ancestor") // at main's tip: already merged in git

	tip := func(b string) string {
		s, err := gitx.BranchTip(repo, b)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	merged := func(n int, branch string, ago time.Duration, head string) forge.PR {
		return forge.PR{Number: n, Title: "PR " + branch, URL: "https://x/pull/" + branch, Source: branch, Dest: "main",
			State: forge.StateMerged, HeadSHA: head, MergedAt: now.Add(-ago), UpdatedAt: now.Add(-ago)}
	}
	prs := []forge.PR{
		merged(1, "old-squash", 45*day, tip("old-squash")),
		merged(2, "old-ancestor", 60*day, "1111111111111111111111111111111111111111"),
		merged(3, "recent", 5*day, tip("recent")),
		merged(4, "diverged", 90*day, tip("main")), // PR merged main's commit, not this branch's later one
		merged(5, "has-wt", 90*day, tip("has-wt")),
		{Number: 6, Source: "open", Dest: "main", State: forge.StateOpen, HeadSHA: tip("open"), UpdatedAt: now},
		merged(7, "main", 400*day, tip("main")), // a PR whose source is the default branch must never be a candidate
	}
	return cleanupFixture{
		repo: repo, now: now, prs: prs,
		in: CleanupInput{PRs: prs, Now: now, Worktrees: map[string]Worktree{"has-wt": {Name: "wt", Path: "/wt"}}},
	}
}

func candidateNames(p CleanupPreview) []string {
	var names []string
	for _, c := range p.Candidates {
		names = append(names, c.Branch)
	}
	return names
}

func TestCleanupCandidates(t *testing.T) {
	t.Parallel()
	f := newCleanupFixture(t)

	got, err := CleanupCandidates(f.repo, f.in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Days != 30 {
		t.Errorf("default age is 30 days, got %d", got.Days)
	}
	if names := candidateNames(got); strings.Join(names, ",") != "old-ancestor,old-squash" {
		t.Fatalf("candidates = %v, want old-ancestor and old-squash (sorted)", names)
	}

	byName := map[string]Candidate{}
	for _, c := range got.Candidates {
		byName[c.Branch] = c
	}
	sq := byName["old-squash"]
	wantSHA, _ := gitx.BranchTip(f.repo, "old-squash")
	if sq.SHA != wantSHA || sq.PRNumber != 1 || sq.PRTitle != "PR old-squash" || sq.PRURL == "" || !sq.MergedAt.Equal(f.now.Add(-45*day)) {
		t.Errorf("old-squash candidate: %+v", sq)
	}

	reasons := map[string]string{}
	for _, s := range got.Skipped {
		reasons[s.Branch] = s.Reason
	}
	if len(reasons) != 3 {
		t.Fatalf("skipped (merged PR but not eligible) = %v", reasons)
	}
	if !strings.Contains(reasons["recent"], "5 days") {
		t.Errorf("recent: %q", reasons["recent"])
	}
	if !strings.Contains(reasons["diverged"], "commits") {
		t.Errorf("diverged: %q", reasons["diverged"])
	}
	if !strings.Contains(reasons["has-wt"], "worktree") {
		t.Errorf("has-wt: %q", reasons["has-wt"])
	}
	for _, never := range []string{"main", "open", "no-pr", "merged-old", "feat-a"} {
		if _, in := byName[never]; in {
			t.Errorf("%s must never be a candidate", never)
		}
	}
}

func TestCleanupAgeIsConfigurable(t *testing.T) {
	t.Parallel()
	f := newCleanupFixture(t)

	f.in.Days = 3
	got, err := CleanupCandidates(f.repo, f.in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Days != 3 || strings.Join(candidateNames(got), ",") != "old-ancestor,old-squash,recent" {
		t.Fatalf("3 days: %v", candidateNames(got))
	}

	f.in.Days = 50
	got, _ = CleanupCandidates(f.repo, f.in)
	if strings.Join(candidateNames(got), ",") != "old-ancestor" { // 60 days qualifies, 45 no longer does
		t.Fatalf("50 days: %v", candidateNames(got))
	}

	for _, bad := range []int{-5, 0} {
		f.in.Days = bad
		if got, _ := CleanupCandidates(f.repo, f.in); got.Days != 30 {
			t.Errorf("days=%d must fall back to 30, got %d", bad, got.Days)
		}
	}
	f.in.Days = 100000
	if got, _ := CleanupCandidates(f.repo, f.in); got.Days != 3650 {
		t.Errorf("days must be capped, got %d", got.Days)
	}
}

func TestCleanupNeedsADefaultBranch(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	mustGit(t, repo, "init", "-b", "trunk")
	commit(t, repo, "a", "c1")
	if _, err := CleanupCandidates(repo, CleanupInput{}); !errors.Is(err, ErrNoDefaultBranch) {
		t.Fatalf("got %v", err)
	}
}

func TestDeleteBranchesOnlyDeletesWhatWasPreviewed(t *testing.T) {
	t.Parallel()
	f := newCleanupFixture(t)
	preview, err := CleanupCandidates(f.repo, f.in)
	if err != nil {
		t.Fatal(err)
	}
	var squashSHA string
	for _, c := range preview.Candidates {
		if c.Branch == "old-squash" {
			squashSHA = c.SHA
		}
	}
	recentSHA, _ := gitx.BranchTip(f.repo, "recent")

	results, err := DeleteBranches(f.repo, f.in, []DeleteRequest{
		{Branch: "old-squash", SHA: squashSHA},                                    // fine
		{Branch: "old-ancestor", SHA: "2222222222222222222222222222222222222222"}, // preview is stale
		{Branch: "recent", SHA: recentSHA},                                        // not eligible
		{Branch: "has-wt", SHA: "x"},                                              // not eligible
		{Branch: "ghost", SHA: "x"},                                               // does not exist
		{Branch: "main", SHA: "x"},                                                // default branch
	})
	if err != nil {
		t.Fatal(err)
	}

	by := map[string]DeleteResult{}
	for _, r := range results {
		by[r.Branch] = r
	}
	if !by["old-squash"].Deleted || by["old-squash"].SHA != squashSHA || by["old-squash"].Error != "" {
		t.Errorf("old-squash: %+v", by["old-squash"])
	}
	if by["old-ancestor"].Deleted || !strings.Contains(by["old-ancestor"].Error, "preview") {
		t.Errorf("a stale SHA must not delete: %+v", by["old-ancestor"])
	}
	for _, b := range []string{"recent", "has-wt", "ghost", "main"} {
		if by[b].Deleted || !strings.Contains(by[b].Error, "eligible") {
			t.Errorf("%s must be reported as not eligible: %+v", b, by[b])
		}
	}
	if len(results) != 6 {
		t.Errorf("every request gets a result: %d", len(results))
	}

	// what is really left in the repo
	for branch, shouldExist := range map[string]bool{"old-squash": false, "old-ancestor": true, "recent": true, "has-wt": true, "main": true, "diverged": true} {
		_, err := gitx.BranchTip(f.repo, branch)
		if (err == nil) != shouldExist {
			t.Errorf("%s exists=%v, want %v", branch, err == nil, shouldExist)
		}
	}
}

func TestDeleteBranchesRefusesHugeRequests(t *testing.T) {
	t.Parallel()
	f := newCleanupFixture(t)
	req := make([]DeleteRequest, 201)
	if _, err := DeleteBranches(f.repo, f.in, req); err == nil {
		t.Fatal("more than 200 branches in one request must be refused")
	}
}
