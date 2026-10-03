package wtclean

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"workstation/internal/branches"
	"workstation/internal/claude"
	"workstation/internal/forge"
	"workstation/internal/gitx"
	"workstation/internal/workspace"
)

const day = 24 * time.Hour

type sessions []claude.Session

func (s sessions) Sessions() ([]claude.Session, error) { return s, nil }

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir,
		"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func commitIn(t *testing.T, dir, file string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", ".")
	mustGit(t, dir, "commit", "-m", file)
}

// fixture: a repo whose worktrees cover every situation the cleanup must tell apart.
//
//	done       clean, PR merged 20d ago, nothing beyond the PR           -> candidate
//	ancestor   no commits of its own (already in main), PR merged 20d    -> candidate
//	recent     PR merged 2 days ago                                      -> too recent
//	dirty      PR merged, but an untracked file in the folder            -> kept
//	live       PR merged, but a Claude session is working in it          -> kept
//	diverged   PR merged, but the branch has later commits               -> kept
//	open       PR still open                                             -> ignored
//	nopr       no PR                                                     -> ignored
type fixture struct {
	repo string
	now  time.Time
	prs  []forge.PR
	path map[string]string // worktree name -> folder
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
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "init", "-b", "main")
	commitIn(t, repo, "base.txt")
	mainTip, _ := gitx.BranchTip(repo, "main")

	f := fixture{repo: repo, now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC), path: map[string]string{}}
	add := func(name string, withCommit bool) {
		p := filepath.Join(root, name)
		mustGit(t, repo, "worktree", "add", "-b", name, p)
		if withCommit {
			commitIn(t, p, name+".txt")
		}
		f.path[name] = p
	}
	for _, n := range []string{"done", "recent", "dirty", "live", "diverged", "open", "nopr"} {
		add(n, true)
	}
	add("ancestor", false)

	head := func(name string) string {
		h, err := gitx.HeadSHA(f.path[name])
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	merged := func(n int, branch string, ago time.Duration, headSHA string) forge.PR {
		return forge.PR{Number: n, Title: "PR " + branch, URL: "https://x/" + branch, Source: branch, Dest: "main",
			State: forge.StateMerged, HeadSHA: headSHA, MergedAt: f.now.Add(-ago), UpdatedAt: f.now.Add(-ago)}
	}
	f.prs = []forge.PR{
		merged(1, "done", 20*day, head("done")),
		merged(2, "ancestor", 20*day, "1111111111111111111111111111111111111111"),
		merged(3, "recent", 2*day, head("recent")),
		merged(4, "dirty", 20*day, head("dirty")),
		merged(5, "live", 20*day, head("live")),
		merged(6, "diverged", 20*day, mainTip), // the PR merged an older commit than the branch now has
		{Number: 7, Source: "open", Dest: "main", State: forge.StateOpen, HeadSHA: head("open"), UpdatedAt: f.now},
	}

	// state that must make a worktree "kept"
	if err := os.WriteFile(filepath.Join(f.path["dirty"], "notes.txt"), []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

// build asks the real workspace builder for the repo, so Git state is real.
func (f fixture) build(t *testing.T, live ...string) workspace.Repo {
	t.Helper()
	var ss sessions
	for _, name := range live {
		ss = append(ss, claude.Session{ID: "s-" + name, Cwd: f.path[name], Status: claude.StatusWorking, LastActivity: f.now})
	}
	ws, err := workspace.Builder{Provider: ss, ExtraRepos: []string{f.repo}}.Build()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range ws.Repos {
		if filepath.Clean(r.Path) == filepath.Clean(f.repo) {
			return r
		}
	}
	t.Fatalf("repo not found in %+v", ws.Repos)
	return workspace.Repo{}
}

func (f fixture) input(t *testing.T, days int, live ...string) Input {
	return Input{Repo: f.build(t, live...), PRs: f.prs, Days: days, Now: f.now}
}

func names(p Preview) string {
	var n []string
	for _, c := range p.Candidates {
		n = append(n, c.Name)
	}
	return strings.Join(n, ",")
}

func TestCandidates(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	got, err := Candidates(f.input(t, 0, "live"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Days != 7 {
		t.Errorf("the default age is 7 days, got %d", got.Days)
	}
	if names(got) != "ancestor,done" {
		t.Fatalf("candidates = %q, want ancestor,done", names(got))
	}

	done := got.Candidates[1]
	wantSHA, _ := gitx.HeadSHA(f.path["done"])
	if done.Path != f.path["done"] || done.Branch != "done" || done.SHA != wantSHA || done.PRNumber != 1 || done.PRTitle != "PR done" || !done.MergedAt.Equal(f.now.Add(-20*day)) {
		t.Errorf("done: %+v", done)
	}

	reasons := map[string]string{}
	for _, s := range got.Skipped {
		reasons[s.Name] = s.Reason
	}
	for name, word := range map[string]string{"recent": "days", "dirty": "uncommitted", "live": "Claude", "diverged": "commits"} {
		if !strings.Contains(reasons[name], word) {
			t.Errorf("%s: reason %q should mention %q", name, reasons[name], word)
		}
	}
	for _, ignored := range []string{"open", "nopr", "repo"} {
		if _, ok := reasons[ignored]; ok {
			t.Errorf("%s has no merged PR (or is the main worktree): it must not be listed at all", ignored)
		}
	}
}

func TestCandidatesAgeIsConfigurable(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	got, err := Candidates(f.input(t, 1, "live"))
	if err != nil {
		t.Fatal(err)
	}
	if names(got) != "ancestor,done,recent" {
		t.Fatalf("1 day: %q", names(got))
	}
	got, _ = Candidates(f.input(t, 25, "live"))
	if names(got) != "" {
		t.Fatalf("25 days: nothing is old enough, got %q", names(got))
	}
	if got, _ := Candidates(f.input(t, 100000, "live")); got.Days != 3650 {
		t.Errorf("days must be capped, got %d", got.Days)
	}
}

func TestCandidatesNeedsADefaultBranch(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	mustGit(t, repo, "init", "-b", "trunk")
	commitIn(t, repo, "a")
	if _, err := Candidates(Input{Repo: workspace.Repo{Path: repo}}); !errors.Is(err, branches.ErrNoDefaultBranch) {
		t.Fatalf("got %v", err)
	}
}

func TestRemoveOnlyWhatWasPreviewed(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	in := f.input(t, 0, "live")
	preview, err := Candidates(in)
	if err != nil {
		t.Fatal(err)
	}
	shaOf := map[string]string{}
	for _, c := range preview.Candidates {
		shaOf[c.Name] = c.SHA
	}

	results, err := Remove(in, []RemoveRequest{
		{Path: f.path["done"], SHA: shaOf["done"]},                                  // fine
		{Path: f.path["ancestor"], SHA: "2222222222222222222222222222222222222222"}, // stale preview
		{Path: f.path["recent"], SHA: "x"},                                          // not eligible
		{Path: f.path["dirty"], SHA: "x"},                                           // not eligible
		{Path: f.path["live"], SHA: "x"},                                            // not eligible
		{Path: f.repo, SHA: "x"},                                                    // the main worktree
		{Path: filepath.Join(filepath.Dir(f.repo), "nowhere"), SHA: "x"},            // not a worktree of this repo
	})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]RemoveResult{}
	for _, r := range results {
		by[r.Path] = r
	}
	if !by[f.path["done"]].Removed || by[f.path["done"]].Error != "" {
		t.Errorf("done: %+v", by[f.path["done"]])
	}
	if by[f.path["ancestor"]].Removed || !strings.Contains(by[f.path["ancestor"]].Error, "preview") {
		t.Errorf("a stale preview must not remove: %+v", by[f.path["ancestor"]])
	}
	for _, p := range []string{f.path["recent"], f.path["dirty"], f.path["live"], f.repo, filepath.Join(filepath.Dir(f.repo), "nowhere")} {
		if by[p].Removed || !strings.Contains(by[p].Error, "eligible") {
			t.Errorf("%s must be reported as not eligible: %+v", p, by[p])
		}
	}
	if len(results) != 7 {
		t.Errorf("every request gets a result: %d", len(results))
	}

	for name, shouldExist := range map[string]bool{"done": false, "ancestor": true, "recent": true, "dirty": true, "live": true} {
		_, err := os.Stat(f.path[name])
		if (err == nil) != shouldExist {
			t.Errorf("%s folder exists=%v, want %v", name, err == nil, shouldExist)
		}
	}
	if _, err := os.Stat(filepath.Join(f.path["dirty"], "notes.txt")); err != nil {
		t.Error("the user's untracked file must survive")
	}
	if _, err := gitx.BranchTip(f.repo, "done"); err != nil {
		t.Error("removing a worktree keeps its branch")
	}
}

// Between the preview and the confirmation a file may appear; the fresh check at
// removal time must catch it.
func TestRemoveRechecksForNewWork(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	in := f.input(t, 0, "live")
	preview, err := Candidates(in)
	if err != nil {
		t.Fatal(err)
	}
	var sha string
	for _, c := range preview.Candidates {
		if c.Name == "done" {
			sha = c.SHA
		}
	}

	// the snapshot (in) still says "clean", but now there is unsaved work
	if err := os.WriteFile(filepath.Join(f.path["done"], "later.txt"), []byte("new work"), 0o644); err != nil {
		t.Fatal(err)
	}
	results, err := Remove(in, []RemoveRequest{{Path: f.path["done"], SHA: sha}})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Removed || !strings.Contains(results[0].Error, "uncommitted") {
		t.Fatalf("new work must stop the removal: %+v", results[0])
	}
	if _, err := os.Stat(filepath.Join(f.path["done"], "later.txt")); err != nil {
		t.Fatal("the new file must survive")
	}
}

func TestRemoveRefusesHugeAndDuplicateRequests(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	in := f.input(t, 0, "live")
	if _, err := Remove(in, make([]RemoveRequest, 101)); err == nil {
		t.Fatal("more than 100 worktrees at once must be refused")
	}
	preview, _ := Candidates(in)
	c := preview.Candidates[0]
	results, err := Remove(in, []RemoveRequest{{Path: c.Path, SHA: c.SHA}, {Path: c.Path, SHA: c.SHA}})
	if err != nil || !results[0].Removed || results[1].Removed || !strings.Contains(results[1].Error, "more than once") {
		t.Fatalf("a repeated path is removed once: %+v %v", results, err)
	}
}
