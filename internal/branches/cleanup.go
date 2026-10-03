package branches

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"workstation/internal/forge"
	"workstation/internal/gitx"
)

const (
	defaultCleanupDays = 30
	maxCleanupDays     = 3650
	maxDeleteRequest   = 200
)

// What makes a branch deletable, from safest to riskiest.
const (
	KindPRMerged       = "pr-merged"        // its PR merged long ago and nothing was added after
	KindStaleMerged    = "stale-merged"     // no PR, old, already inside the default branch: loses nothing
	KindStaleOnRemote  = "stale-on-remote"  // no PR, old, but a remote has the commits (as of the last fetch)
	KindStaleLocalOnly = "stale-local-only" // no PR, old, commits exist ONLY on this machine
)

var kindRank = map[string]int{KindPRMerged: 0, KindStaleMerged: 1, KindStaleOnRemote: 2, KindStaleLocalOnly: 3}

// CleanupInput describes what the cleanup may look at.
type CleanupInput struct {
	Worktrees map[string]Worktree // branches checked out somewhere; never deleted
	PRs       []forge.PR
	// PRsKnown says the PR list is trustworthy. Without it a branch that merely
	// looks PR-less (the lookup failed) must not be offered for being "stale".
	PRsKnown bool
	// AllowLocalOnly is the user's explicit yes to deleting branches whose commits
	// exist nowhere else.
	AllowLocalOnly bool
	Days           int       // age threshold: merged this long ago, or last commit this long ago (default 30)
	Now            time.Time // zero = time.Now()
}

// Candidate is a local branch that can be deleted.
type Candidate struct {
	Branch     string    `json:"branch"`
	SHA        string    `json:"sha"` // the tip we saw; deletion only proceeds if it is unchanged
	Kind       string    `json:"kind"`
	PRNumber   int       `json:"prNumber,omitempty"`
	PRTitle    string    `json:"prTitle,omitempty"`
	PRURL      string    `json:"prUrl,omitempty"`
	MergedAt   time.Time `json:"mergedAt,omitzero"`
	LastCommit time.Time `json:"lastCommit,omitzero"`
	Ahead      int       `json:"ahead,omitempty"` // commits not in the default branch (what is at stake)
}

// Skipped is a branch with a merged PR that was deliberately kept, and why.
type Skipped struct {
	Branch   string `json:"branch"`
	PRNumber int    `json:"prNumber"`
	Reason   string `json:"reason"`
}

type CleanupPreview struct {
	Days       int         `json:"days"`
	Candidates []Candidate `json:"candidates"`
	Skipped    []Skipped   `json:"skipped"`
}

// CleanupCandidates lists the local branches that can be cleaned up: those whose
// PR merged long ago and, when the PR data is trustworthy, old branches that
// never had a merged PR (graded by how much work would be lost). Remote
// branches are never considered, nor branches with an open PR or a worktree.
func CleanupCandidates(dir string, in CleanupInput) (CleanupPreview, error) {
	def := gitx.DefaultBranch(dir)
	if def == "" {
		return CleanupPreview{}, ErrNoDefaultBranch
	}
	all, err := gitx.LocalBranches(dir)
	if err != nil {
		return CleanupPreview{}, err
	}

	days := normalizeDays(in.Days)
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
	prs := bestPRs(in.PRs) // an open PR on the same branch name wins, so a reused name is never deleted

	out := CleanupPreview{Days: days, Candidates: []Candidate{}, Skipped: []Skipped{}}
	for _, b := range all {
		if b.Name == def {
			continue
		}
		pr := prs[b.Name]
		switch {
		case pr != nil && pr.State == forge.StateMerged:
			candidate, skipped := mergedPRBranch(dir, def, b, pr, in, cutoff, now)
			if candidate != nil {
				out.Candidates = append(out.Candidates, *candidate)
			}
			if skipped != nil {
				out.Skipped = append(out.Skipped, *skipped)
			}
		case pr != nil && pr.State == forge.StateOpen:
			// still in review: never a cleanup target
		default:
			if c, ok := staleBranch(dir, def, b, pr, in, cutoff); ok {
				out.Candidates = append(out.Candidates, c)
			}
		}
	}
	sort.Slice(out.Candidates, func(i, j int) bool {
		a, b := out.Candidates[i], out.Candidates[j]
		if kindRank[a.Kind] != kindRank[b.Kind] {
			return kindRank[a.Kind] < kindRank[b.Kind]
		}
		return a.Branch < b.Branch
	})
	sort.Slice(out.Skipped, func(i, j int) bool { return out.Skipped[i].Branch < out.Skipped[j].Branch })
	return out, nil
}

// mergedPRBranch handles a branch whose PR is merged.
func mergedPRBranch(dir, def string, b gitx.Branch, pr *forge.PR, in CleanupInput, cutoff, now time.Time) (*Candidate, *Skipped) {
	mergedAt := pr.MergedAt
	if mergedAt.IsZero() {
		mergedAt = pr.UpdatedAt
	}
	skip := func(reason string) (*Candidate, *Skipped) {
		return nil, &Skipped{Branch: b.Name, PRNumber: pr.Number, Reason: reason}
	}

	if wt, checkedOut := in.Worktrees[b.Name]; checkedOut {
		return skip("checked out in worktree " + wt.Name)
	}
	if mergedAt.After(cutoff) {
		return skip(fmt.Sprintf("merged %d days ago (needs %d or more)", int(now.Sub(mergedAt)/(24*time.Hour)), normalizeDays(in.Days)))
	}
	if !onlyMergedWork(dir, b, pr, def) {
		return skip("has commits that were not part of the merged PR")
	}
	return &Candidate{
		Branch: b.Name, SHA: b.Tip, Kind: KindPRMerged,
		PRNumber: pr.Number, PRTitle: pr.Title, PRURL: pr.URL, MergedAt: mergedAt,
	}, nil
}

// staleBranch handles a branch with no PR (or a closed, unmerged one) whose last
// commit is old. It is graded by what deleting it would lose.
func staleBranch(dir, def string, b gitx.Branch, pr *forge.PR, in CleanupInput, cutoff time.Time) (Candidate, bool) {
	if !in.PRsKnown {
		return Candidate{}, false
	}
	if _, checkedOut := in.Worktrees[b.Name]; checkedOut {
		return Candidate{}, false
	}
	last, err := time.Parse(time.RFC3339, b.Date)
	if err != nil || last.After(cutoff) {
		return Candidate{}, false
	}

	c := Candidate{Branch: b.Name, SHA: b.Tip, LastCommit: last}
	if pr != nil { // a declined PR: show it, it is useful context
		c.PRNumber, c.PRTitle, c.PRURL = pr.Number, pr.Title, pr.URL
	}

	if merged, err := gitx.IsAncestor(dir, b.Name, def); err == nil && merged {
		c.Kind = KindStaleMerged
		return c, true
	}
	c.Ahead, _ = gitx.CountBetween(dir, def, b.Name)
	if onRemote, err := gitx.RemoteContains(dir, b.Tip); err == nil && onRemote {
		c.Kind = KindStaleOnRemote
		return c, true
	}
	c.Kind = KindStaleLocalOnly
	return c, true
}

func normalizeDays(d int) int {
	switch {
	case d <= 0:
		return defaultCleanupDays
	case d > maxCleanupDays:
		return maxCleanupDays
	}
	return d
}

// onlyMergedWork is true when deleting the branch loses nothing: its tip is the
// commit the PR merged, or the branch is already contained in the default branch.
func onlyMergedWork(dir string, b gitx.Branch, pr *forge.PR, def string) bool {
	if sameCommit(b.Tip, pr.HeadSHA) {
		return true
	}
	ok, err := gitx.IsAncestor(dir, b.Name, def)
	return err == nil && ok
}

// BestPRs keeps one PR per source branch (an open one wins, else the newest).
// Exported for the worktree cleanup, which uses the same rule.
func BestPRs(prs []forge.PR) map[string]*forge.PR { return bestPRs(prs) }

// SameCommit compares full or abbreviated (at least 7 characters) commit ids.
func SameCommit(a, b string) bool { return sameCommit(a, b) }

// sameCommit compares full or abbreviated (at least 7 characters) commit ids.
func sameCommit(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	if len(a) < 7 || len(b) < 7 {
		return false
	}
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

// DeleteRequest is one branch the user chose in the preview, with the tip they saw.
type DeleteRequest struct {
	Branch string `json:"branch"`
	SHA    string `json:"sha"`
}

type DeleteResult struct {
	Branch  string `json:"branch"`
	SHA     string `json:"sha"` // for `git branch <name> <sha>` to restore it
	Deleted bool   `json:"deleted"`
	Error   string `json:"error,omitempty"`
}

// DeleteBranches deletes the requested local branches. The request is never
// trusted: eligibility is recomputed now, and a branch is deleted only if it is
// still a candidate AND still at the tip the user was shown. A branch whose
// commits exist nowhere else needs in.AllowLocalOnly.
func DeleteBranches(dir string, in CleanupInput, req []DeleteRequest) ([]DeleteResult, error) {
	if len(req) > maxDeleteRequest {
		return nil, fmt.Errorf("refusing to delete more than %d branches at once", maxDeleteRequest)
	}
	preview, err := CleanupCandidates(dir, in)
	if err != nil {
		return nil, err
	}
	eligible := make(map[string]Candidate, len(preview.Candidates))
	for _, c := range preview.Candidates {
		eligible[c.Branch] = c
	}

	results := make([]DeleteResult, 0, len(req))
	done := map[string]bool{}
	for _, r := range req {
		res := DeleteResult{Branch: r.Branch, SHA: r.SHA}
		c, ok := eligible[r.Branch]
		switch {
		case done[r.Branch]:
			res.Error = "listed more than once"
		case !ok:
			res.Error = "not eligible for cleanup (any more)"
		case c.SHA != r.SHA:
			res.Error = fmt.Sprintf("the preview is out of date: the branch is now at %s", short(c.SHA))
		case c.Kind == KindStaleLocalOnly && !in.AllowLocalOnly:
			res.Error = fmt.Sprintf("its %d commit(s) exist only on this machine: confirm explicitly to delete it", c.Ahead)
		default:
			if err := gitx.DeleteBranch(dir, r.Branch, r.SHA); err != nil {
				res.Error = firstLine(err.Error())
			} else {
				res.Deleted = true
			}
		}
		done[r.Branch] = true
		results = append(results, res)
	}
	return results, nil
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
