package wtclean

import (
	"time"

	"workstation/internal/forge"
	"workstation/internal/gitx"
	"workstation/internal/workspace"
)

// Why a worktree is offered. A merged one is finished work; a dormant one has been idle
// for a long time with no PR in flight, and is offered only when its commits cannot be lost.
const (
	KindMerged          = "merged"
	KindDormantMerged   = "dormant-merged"    // idle, and the branch is already inside the default branch
	KindDormantOnRemote = "dormant-on-remote" // idle, and a remote has every commit (as of the last fetch)
)

const defaultDormantDays = 14

func normalizeDormantDays(d int) int {
	switch {
	case d <= 0:
		return defaultDormantDays
	case d > maxDays:
		return maxDays
	}
	return d
}

// lastActivity is the newest sign of life in a worktree: its last commit or its newest Claude session.
func lastActivity(w workspace.Worktree) time.Time {
	var newest time.Time
	if c := w.Git.LastCommit; c != nil {
		if t, err := time.Parse(time.RFC3339, c.Date); err == nil {
			newest = t
		}
	}
	for _, s := range w.Sessions {
		if s.LastActivity.After(newest) {
			newest = s.LastActivity
		}
	}
	return newest
}

// dormantCandidates adds the idle worktrees without a PR in flight whose work is safe elsewhere.
// Worktrees that look idle but would lose work are listed as skipped, with the reason.
func dormantCandidates(in Input, def string, prs map[string]*forge.PR, now time.Time, out *Preview) {
	idleFor := time.Duration(normalizeDormantDays(in.DormantDays)) * 24 * time.Hour
	for _, w := range in.Repo.Worktrees {
		if w.IsMain || w.Detached || w.Branch == "" || w.Branch == def {
			continue
		}
		if pr := prs[w.Branch]; pr != nil && pr.State != forge.StateDeclined {
			continue // an open PR is work in review; a merged one is the merged flow
		}
		last := lastActivity(w)
		if last.IsZero() || now.Sub(last) < idleFor {
			continue
		}
		skip := func(reason string) {
			out.Skipped = append(out.Skipped, Skipped{Path: w.Path, Name: w.Name, Branch: w.Branch, Reason: "idle, but " + reason})
		}
		switch {
		case w.GitError != "":
			skip("git cannot read this worktree")
		case live(w):
			skip("a Claude session is running in it")
		case w.Git.Dirty:
			skip("it has uncommitted changes or untracked files")
		default:
			kind, head, reason := dormantKind(in.Repo.Path, w, def)
			if reason != "" {
				skip(reason)
				continue
			}
			out.Candidates = append(out.Candidates, Candidate{
				Path: w.Path, Name: w.Name, Branch: w.Branch, SHA: head, Kind: kind, LastActivity: last,
			})
		}
	}
}

// dormantKind decides whether removing the folder can lose anything. The branch itself is never touched.
func dormantKind(repo string, w workspace.Worktree, def string) (kind, head, reason string) {
	head, err := gitx.HeadSHA(w.Path)
	if err != nil {
		return "", "", "cannot read its HEAD"
	}
	if ok, err := gitx.IsAncestor(repo, w.Branch, def); err == nil && ok {
		return KindDormantMerged, head, ""
	}
	if ok, err := gitx.RemoteContains(repo, head); err == nil && ok {
		return KindDormantOnRemote, head, ""
	}
	return "", head, "its commits exist only on this machine (not merged, not pushed)"
}
