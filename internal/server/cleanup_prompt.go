package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"workstation/internal/claude"
	"workstation/internal/workspace"
)

const (
	defaultCleanupDays = 14
	maxCleanupDays     = 365
	maxPromptItems     = 40 // per list; the rest is summarised
)

func normalizeCleanupDays(d int) int {
	switch {
	case d <= 0:
		return defaultCleanupDays
	case d > maxCleanupDays:
		return maxCleanupDays
	}
	return d
}

// cleanupPrompt builds the first message for a Claude session that tidies one
// repo. It is built here, from what the app already knows, so the browser never
// supplies the text. Claude is told to verify and to ask before deleting.
func (s *Server) cleanupPrompt(repo workspace.Repo, ws workspace.Workspace, days int, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Help me clean up the git repo %q (%s). Today is %s; \"old\" means no activity for %d days.\n",
		repo.Name, repo.Path, now.Format("2006-01-02"), days)
	b.WriteString("Below is what my workspace tool found. Treat it as a starting point, not as proof: check each item yourself before acting.\n")

	var warnings []string

	b.WriteString("\n## Worktrees (merged PR, or idle)\n")
	if s.worktreeCleanup == nil {
		b.WriteString("(not available)\n")
	} else if resp, err := s.worktreeCleanup.Preview(repo, days, days); err != nil {
		fmt.Fprintf(&b, "(could not be listed: %v)\n", err)
	} else {
		warnings = append(warnings, resp.Warnings...)
		for i, c := range resp.Candidates {
			if i == maxPromptItems {
				fmt.Fprintf(&b, "- ...and %d more\n", len(resp.Candidates)-i)
				break
			}
			fmt.Fprintf(&b, "- %s  branch %s  [%s]", c.Path, orDash(c.Branch), c.Kind)
			if c.PRNumber > 0 {
				fmt.Fprintf(&b, "  PR #%d", c.PRNumber)
			}
			b.WriteString("\n")
		}
		if len(resp.Candidates) == 0 {
			b.WriteString("(none)\n")
		}
		for i, k := range resp.Skipped {
			if i == maxPromptItems {
				break
			}
			fmt.Fprintf(&b, "- kept: %s (%s)\n", k.Path, k.Reason)
		}
	}

	b.WriteString("\n## Local branches\n")
	if s.cleanup == nil {
		b.WriteString("(not available)\n")
	} else if resp, err := s.cleanup.CleanupPreview(repo.Path, worktreesByBranch(repo), days, false); err != nil {
		fmt.Fprintf(&b, "(could not be listed: %v)\n", err)
	} else {
		warnings = append(warnings, resp.Warnings...)
		for i, c := range resp.Candidates {
			if i == maxPromptItems {
				fmt.Fprintf(&b, "- ...and %d more\n", len(resp.Candidates)-i)
				break
			}
			fmt.Fprintf(&b, "- %s  [%s]", c.Branch, c.Kind)
			if c.Ahead > 0 {
				fmt.Fprintf(&b, "  %d commits not in the default branch", c.Ahead)
			}
			if c.Kind == "stale-local-only" {
				b.WriteString("  RISKY: commits exist nowhere else")
			}
			b.WriteString("\n")
		}
		if len(resp.Candidates) == 0 {
			b.WriteString("(none)\n")
		}
	}

	b.WriteString("\n## Claude sessions (stopped, never running ones)\n")
	stale := staleSessions(repo, ws, days, now)
	for i, sess := range stale {
		if i == maxPromptItems {
			fmt.Fprintf(&b, "- ...and %d more\n", len(stale)-i)
			break
		}
		fmt.Fprintf(&b, "- %s  last active %s  cwd %s  %q\n", sess.ID, sess.LastActivity.Format("2006-01-02"), sess.Cwd, shorten(firstNonEmpty(sess.Title, sess.Prompt), 80))
	}
	if len(stale) == 0 {
		b.WriteString("(none)\n")
	}

	for _, w := range warnings {
		fmt.Fprintf(&b, "\nWarning from the tool: %s\n", w)
	}

	b.WriteString(`
## How to proceed
1. Verify each item with git (merged? uncommitted changes? unpushed commits? is the PR really merged?) and show me a plan grouped as: safe to remove, needs my decision, keep.
2. Ask for my confirmation before deleting anything. Never use --force, never delete a branch with unmerged or unpushed work, and never touch the main worktree.
3. Worktrees: git worktree remove <path>, then git worktree prune. Branches: git branch -d (not -D) unless I say otherwise.
4. Sessions: a session is the file ~/.claude/projects/<encoded-cwd>/<session id>.jsonl. Find it by id, delete only the ones I confirm, and never touch ~/.claude/sessions/*.json (those are running processes) or the session you are running in.
5. Finish with a short summary of what was removed and what was kept.
`)
	return b.String()
}

// staleSessions lists the stopped sessions of repo that are old or whose
// directory is gone. Running sessions (working, idle, unknown) are never listed.
func staleSessions(repo workspace.Repo, ws workspace.Workspace, days int, now time.Time) []claude.Session {
	cutoff := now.AddDate(0, 0, -days)
	var out []claude.Session
	seen := map[string]bool{}
	consider := func(sess claude.Session) {
		if seen[sess.ID] || sess.Status != claude.StatusStopped {
			return
		}
		if sess.LastActivity.Before(cutoff) || !dirExists(sess.Cwd) {
			seen[sess.ID] = true
			out = append(out, sess)
		}
	}
	for _, wt := range repo.Worktrees {
		for _, sess := range wt.Sessions {
			consider(sess)
		}
	}
	for _, u := range ws.Unlinked {
		if within(repo.Path, u.Session.Cwd) || within(repo.Path, u.Session.StartCwd) {
			consider(u.Session)
		}
	}
	return out
}

func dirExists(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// within reports whether p is root or lies below it.
func within(root, p string) bool {
	if root == "" || p == "" {
		return false
	}
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func orDash(s string) string { return firstNonEmpty(s, "-") }

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func shorten(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}
