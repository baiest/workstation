// Package wtclean finds linked worktrees that are safe to remove (their PR is
// merged and nothing is left in them) and removes the ones the user picks.
//
// Removing a worktree deletes a folder, so every step is conservative:
// eligibility is recomputed when removing, the commit the user saw must still be
// HEAD, a fresh `git status` must be clean, and git is never asked to --force.
package wtclean

import (
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"workstation/internal/branches"
	"workstation/internal/claude"
	"workstation/internal/forge"
	"workstation/internal/gitx"
	"workstation/internal/workspace"
)

const (
	defaultDays = 7
	maxDays     = 3650
	maxRemove   = 100
)

// Input describes one repo. Repo should come from a fresh workspace build.
type Input struct {
	Repo workspace.Repo
	PRs  []forge.PR
	Days int       // the PR must have been merged at least this long ago (default 7)
	Now  time.Time // zero = time.Now()
}

type Candidate struct {
	Path     string    `json:"path"`
	Name     string    `json:"name"` // the folder
	Branch   string    `json:"branch"`
	SHA      string    `json:"sha"` // HEAD of the worktree when listed; removal only proceeds if unchanged
	PRNumber int       `json:"prNumber"`
	PRTitle  string    `json:"prTitle"`
	PRURL    string    `json:"prUrl,omitempty"`
	MergedAt time.Time `json:"mergedAt"`
}

// Skipped is a worktree with a merged PR that was kept, and why.
type Skipped struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Branch   string `json:"branch"`
	PRNumber int    `json:"prNumber"`
	Reason   string `json:"reason"`
}

type Preview struct {
	Days       int         `json:"days"`
	Candidates []Candidate `json:"candidates"`
	Skipped    []Skipped   `json:"skipped"`
}

func normalizeDays(d int) int {
	switch {
	case d <= 0:
		return defaultDays
	case d > maxDays:
		return maxDays
	}
	return d
}

func live(w workspace.Worktree) bool {
	for _, s := range w.Sessions {
		if s.Status == claude.StatusWorking || s.Status == claude.StatusIdle {
			return true
		}
	}
	return false
}

// Candidates lists the linked worktrees that can be removed. Only worktrees
// whose branch has a merged PR are considered; the main worktree, detached
// ones and the default branch never are.
func Candidates(in Input) (Preview, error) {
	def := gitx.DefaultBranch(in.Repo.Path)
	if def == "" {
		return Preview{}, branches.ErrNoDefaultBranch
	}
	days := normalizeDays(in.Days)
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
	prs := branches.BestPRs(in.PRs)

	out := Preview{Days: days, Candidates: []Candidate{}, Skipped: []Skipped{}}
	for _, w := range in.Repo.Worktrees {
		if w.IsMain || w.Detached || w.Branch == "" || w.Branch == def {
			continue
		}
		pr := prs[w.Branch]
		if pr == nil || pr.State != forge.StateMerged {
			continue
		}
		mergedAt := pr.MergedAt
		if mergedAt.IsZero() {
			mergedAt = pr.UpdatedAt
		}
		skip := func(reason string) {
			out.Skipped = append(out.Skipped, Skipped{Path: w.Path, Name: w.Name, Branch: w.Branch, PRNumber: pr.Number, Reason: reason})
		}

		switch {
		case w.GitError != "":
			skip("git cannot read this worktree")
		case live(w):
			skip("a Claude session is running in it")
		case w.Git.Dirty:
			skip("uncommitted changes or untracked files")
		case mergedAt.After(cutoff):
			skip(fmt.Sprintf("merged %d days ago (needs %d or more)", int(now.Sub(mergedAt)/(24*time.Hour)), days))
		default:
			head, err := gitx.HeadSHA(w.Path)
			switch {
			case err != nil:
				skip("cannot read its HEAD")
			case !onlyMergedWork(in.Repo.Path, w.Branch, head, pr, def):
				skip("has commits that were not part of the merged PR")
			default:
				out.Candidates = append(out.Candidates, Candidate{
					Path: w.Path, Name: w.Name, Branch: w.Branch, SHA: head,
					PRNumber: pr.Number, PRTitle: pr.Title, PRURL: pr.URL, MergedAt: mergedAt,
				})
			}
		}
	}
	sort.Slice(out.Candidates, func(i, j int) bool { return out.Candidates[i].Name < out.Candidates[j].Name })
	sort.Slice(out.Skipped, func(i, j int) bool { return out.Skipped[i].Name < out.Skipped[j].Name })
	return out, nil
}

// onlyMergedWork: HEAD is the commit the PR merged, or the branch is already inside the default branch.
func onlyMergedWork(repo, branch, head string, pr *forge.PR, def string) bool {
	if branches.SameCommit(head, pr.HeadSHA) {
		return true
	}
	ok, err := gitx.IsAncestor(repo, branch, def)
	return err == nil && ok
}

type RemoveRequest struct {
	Path string `json:"path"`
	SHA  string `json:"sha"`
}

type RemoveResult struct {
	Path    string `json:"path"`
	Name    string `json:"name,omitempty"`
	Branch  string `json:"branch,omitempty"`
	SHA     string `json:"sha"`
	Removed bool   `json:"removed"`
	Error   string `json:"error,omitempty"`
}

// Remove removes the worktrees the user chose. The request is never trusted:
// each path must still be a candidate, still at the commit shown, and have a
// clean `git status` right now.
func Remove(in Input, req []RemoveRequest) ([]RemoveResult, error) {
	if len(req) > maxRemove {
		return nil, fmt.Errorf("refusing to remove more than %d worktrees at once", maxRemove)
	}
	preview, err := Candidates(in)
	if err != nil {
		return nil, err
	}
	eligible := make(map[string]Candidate, len(preview.Candidates))
	for _, c := range preview.Candidates {
		eligible[filepath.Clean(c.Path)] = c
	}

	results := make([]RemoveResult, 0, len(req))
	done := map[string]bool{}
	for _, r := range req {
		key := filepath.Clean(r.Path)
		c, ok := eligible[key]
		res := RemoveResult{Path: r.Path, SHA: r.SHA, Name: c.Name, Branch: c.Branch}

		switch {
		case done[key]:
			res.Error = "listed more than once"
		case !ok:
			res.Error = "not eligible for removal (any more)"
		case c.SHA != r.SHA:
			res.Error = fmt.Sprintf("the preview is out of date: HEAD is now %s", short(c.SHA))
		default:
			res.Error = removeOne(in.Repo.Path, c, r.SHA)
			res.Removed = res.Error == ""
		}
		done[key] = true
		results = append(results, res)
	}
	return results, nil
}

// removeOne re-checks the worktree against git right now, then removes it. It
// returns "" on success.
func removeOne(repo string, c Candidate, sha string) string {
	st, err := gitx.StatusOf(c.Path)
	if err != nil {
		return "cannot read git status: " + firstLine(err.Error())
	}
	if st.Dirty() {
		return "uncommitted changes or untracked files appeared since the preview"
	}
	if err := gitx.RemoveWorktree(repo, c.Path, sha); err != nil {
		return firstLine(err.Error())
	}
	return ""
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}
