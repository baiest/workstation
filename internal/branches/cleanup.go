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

// CleanupInput describes what the cleanup may look at.
type CleanupInput struct {
	Worktrees map[string]Worktree // branches checked out somewhere; never deleted
	PRs       []forge.PR
	Days      int       // a PR must have been merged at least this long ago (default 30)
	Now       time.Time // zero = time.Now()
}

// Candidate is a local branch that is safe to delete: its PR merged long ago
// and nothing was added to the branch after that.
type Candidate struct {
	Branch   string    `json:"branch"`
	SHA      string    `json:"sha"` // the tip we saw; deletion only proceeds if it is unchanged
	PRNumber int       `json:"prNumber"`
	PRTitle  string    `json:"prTitle"`
	PRURL    string    `json:"prUrl,omitempty"`
	MergedAt time.Time `json:"mergedAt"`
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

// CleanupCandidates lists the local branches that can be cleaned up. Only
// branches whose pull request is merged qualify; remote branches are never
// considered.
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
		pr := prs[b.Name]
		if b.Name == def || pr == nil || pr.State != forge.StateMerged {
			continue
		}
		mergedAt := pr.MergedAt
		if mergedAt.IsZero() {
			mergedAt = pr.UpdatedAt
		}
		skip := func(reason string) {
			out.Skipped = append(out.Skipped, Skipped{Branch: b.Name, PRNumber: pr.Number, Reason: reason})
		}

		switch wt, checkedOut := in.Worktrees[b.Name]; {
		case checkedOut:
			skip("checked out in worktree " + wt.Name)
		case mergedAt.After(cutoff):
			skip(fmt.Sprintf("merged %d days ago (needs %d or more)", int(now.Sub(mergedAt)/(24*time.Hour)), days))
		case !onlyMergedWork(dir, b, pr, def):
			skip("has commits that were not part of the merged PR")
		default:
			out.Candidates = append(out.Candidates, Candidate{
				Branch: b.Name, SHA: b.Tip, PRNumber: pr.Number, PRTitle: pr.Title, PRURL: pr.URL, MergedAt: mergedAt,
			})
		}
	}
	sort.Slice(out.Candidates, func(i, j int) bool { return out.Candidates[i].Branch < out.Candidates[j].Branch })
	sort.Slice(out.Skipped, func(i, j int) bool { return out.Skipped[i].Branch < out.Skipped[j].Branch })
	return out, nil
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
// still a candidate AND still at the tip the user was shown.
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
