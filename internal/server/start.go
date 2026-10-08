package server

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"workstation/internal/gitx"
	"workstation/internal/tickets"
	"workstation/internal/workspace"
)

// GitOps is the little of git that starting work needs (implemented by gitx).
type GitOps interface {
	LocalBranches(repo string) ([]gitx.Branch, error)
	StartPoint(repo string) string
	DefaultBranch(repo string) string
	AddWorktree(repo, path, branch, base string, create bool) error
}

type realGit struct{}

func (realGit) LocalBranches(repo string) ([]gitx.Branch, error) { return gitx.LocalBranches(repo) }
func (realGit) StartPoint(repo string) string                    { return gitx.StartPoint(repo) }
func (realGit) DefaultBranch(repo string) string                 { return gitx.DefaultBranch(repo) }
func (realGit) AddWorktree(repo, path, branch, base string, create bool) error {
	return gitx.AddWorktree(repo, path, branch, base, create)
}

// WithGit replaces the git operations used to create worktrees (tests).
func WithGit(g GitOps) Option { return func(o *options) { o.git = g } }

// worktreesDir is where new worktrees go, below the repo, where Claude Code puts its own.
var worktreesDir = filepath.Join(".claude", "worktrees")

// start opens Claude on a ticket: in its worktree if it has one, else in a new
// one (the existing branch of the ticket, or a new branch from the default
// branch). The first message is built here from what is stored, never taken from
// the page. Starting a ticket makes it active: you chose to carry it.
func (s *Server) start(req actionRequest, ws workspace.Workspace) error {
	if s.tickets == nil {
		return badRequest("tickets are not enabled")
	}
	repo, ok := findRepo(ws, req.Repo)
	if !ok {
		return badRequest("unknown repo")
	}
	ref := req.Ref
	if !tickets.ValidRef(ref) {
		return badRequest("invalid ticket reference")
	}
	stored, _ := s.tickets.Get(tickets.ID(repo.Path, ref))
	switch stored.State {
	case tickets.StateBlocked:
		return badRequest("this ticket is blocked (" + orNone(stored.BlockedReason) + "): unblock it first")
	case tickets.StateDone:
		return badRequest("this ticket is done: reopen it first")
	case tickets.StateIgnored:
		return badRequest("this ticket is ignored: track it again first")
	}

	dir, branch, err := s.worktreeFor(repo, ref, stored)
	if err != nil {
		return err
	}

	update := tickets.Update{Repo: repo.Path, Ref: ref}
	if stored.State != tickets.StateActive {
		active := tickets.StateActive
		update.State = &active
	}
	if !tickets.IsKey(ref) && !strings.HasPrefix(ref, "branch:") && stored.Branch == "" && branch != "" {
		update.Branch = &branch // a ticket born from a note owns the branch made for it
	}
	if update.State != nil || update.Branch != nil {
		if _, err := s.tickets.Apply(update); err != nil {
			return err
		}
		stored, _ = s.tickets.Get(tickets.ID(repo.Path, ref))
	}
	return s.launcher.Prompt(dir, startBrief(ref, titleOf(repo, ref, stored), branch, dir, stored))
}

// worktreeFor finds or creates the folder to work in, and returns it with its branch.
func (s *Server) worktreeFor(repo workspace.Repo, ref string, stored tickets.Ticket) (dir, branch string, err error) {
	for _, w := range repo.Worktrees {
		if !w.IsMain && w.Ticket == ref {
			return w.Path, w.Branch, nil
		}
	}

	git := s.git
	branches, err := git.LocalBranches(repo.Path)
	if err != nil {
		return "", "", err
	}
	if b, ok := s.branchOf(branches, ref, stored); ok {
		for _, w := range repo.Worktrees {
			if w.IsMain && w.Branch == b {
				return w.Path, b, nil // the main folder has it checked out: work there
			}
		}
		return s.addWorktree(repo, b, "", false)
	}

	if !tickets.IsKey(ref) && strings.HasPrefix(ref, "branch:") {
		return "", "", badRequest("the branch of this ticket no longer exists")
	}
	base := git.StartPoint(repo.Path)
	if base == "" {
		return "", "", badRequest("this repo has no default branch to start from")
	}
	name := uniqueBranch(branches, tickets.BranchFor(ref, stored.Title))
	return s.addWorktree(repo, name, base, true)
}

// branchOf picks the local branch that belongs to the ticket: the one it owns, else the newest named after it.
func (s *Server) branchOf(branches []gitx.Branch, ref string, stored tickets.Ticket) (string, bool) {
	if stored.Branch != "" {
		for _, b := range branches {
			if b.Name == stored.Branch {
				return b.Name, true
			}
		}
	}
	var mine []gitx.Branch
	for _, b := range branches {
		if s.keyer.Ref(b.Name, "") == ref {
			mine = append(mine, b)
		}
	}
	if len(mine) == 0 {
		return "", false
	}
	sort.SliceStable(mine, func(i, j int) bool { return mine[i].Date > mine[j].Date }) // ISO dates sort as text
	return mine[0].Name, true
}

func (s *Server) addWorktree(repo workspace.Repo, branch, base string, create bool) (string, string, error) {
	dir := filepath.Join(repo.Path, worktreesDir, strings.ReplaceAll(branch, "/", "-"))
	if err := s.git.AddWorktree(repo.Path, dir, branch, base, create); err != nil {
		return "", "", badRequest("could not create the worktree: " + err.Error())
	}
	return dir, branch, nil
}

// uniqueBranch adds -2, -3... until no local branch has the name.
func uniqueBranch(branches []gitx.Branch, name string) string {
	taken := map[string]bool{}
	for _, b := range branches {
		taken[b.Name] = true
	}
	if !taken[name] {
		return name
	}
	for i := 2; ; i++ {
		if n := fmt.Sprintf("%s-%d", name, i); !taken[n] {
			return n
		}
	}
}

func orNone(s string) string {
	if s == "" {
		return "no reason given"
	}
	return s
}

// titleOf is what the ticket is called: what was stored, else the PR title of its worktree, else its branch.
func titleOf(repo workspace.Repo, ref string, stored tickets.Ticket) string {
	if stored.Title != "" {
		return stored.Title
	}
	for _, w := range repo.Worktrees {
		if w.Ticket != ref || w.IsMain {
			continue
		}
		if w.PR != nil && w.PR.Title != "" {
			return w.PR.Title
		}
		if w.Branch != "" {
			return w.Branch
		}
	}
	return strings.TrimPrefix(strings.TrimPrefix(ref, "branch:"), "folder:")
}

// startBrief is the first message of a session started from a ticket.
func startBrief(ref, title, branch, dir string, t tickets.Ticket) string {
	var b strings.Builder
	label := title
	if tickets.IsKey(ref) {
		label = ref + ": " + title
	}
	fmt.Fprintf(&b, "You are picking up a ticket that workstation tracks for me.\n\nTicket: %s\n", label)
	if branch != "" {
		fmt.Fprintf(&b, "Branch: %s\n", branch)
	}
	fmt.Fprintf(&b, "Folder: %s\n", dir)

	if t.NextStep != "" {
		fmt.Fprintf(&b, "\nNext step, written by whoever worked on this last:\n%s\n", indent(t.NextStep))
	} else {
		b.WriteString("\nNo next step has been written yet: if the ticket is not clear to you, ask me what to do first.\n")
	}

	var inScope, open []string
	for _, d := range t.Discoveries {
		switch d.Status {
		case tickets.DiscoveryInScope:
			inScope = append(inScope, d.Text)
		case tickets.DiscoveryOpen:
			open = append(open, d.Text)
		}
	}
	if len(inScope) > 0 {
		b.WriteString("\nDecided to be part of this ticket:\n" + bullets(inScope))
	}
	if len(open) > 0 {
		b.WriteString("\nNoted earlier and NOT part of this ticket (do not work on these):\n" + bullets(open))
	}

	b.WriteString(`
How to work:
1. If the workstation command exists, run "workstation ticket" first: it shows the current state of this ticket.
2. Stay inside this ticket. When you notice something else (another bug, a refactor, a doubt), do not fix it: run workstation discover "one line saying what and where" and carry on.
3. Before you stop, run workstation next "what to do first next time" so the next session can start from it.
If the workstation command is not available, skip those steps and tell me once.
`)
	return b.String()
}

func indent(s string) string { return "  " + strings.ReplaceAll(s, "\n", "\n  ") }

func bullets(list []string) string {
	var b strings.Builder
	for _, l := range list {
		b.WriteString("  - " + l + "\n")
	}
	return b.String()
}

// review opens a session whose only job is to read the work of a worktree and
// report. It cannot be forced to be read-only from here, so the prompt says so
// plainly.
func (s *Server) review(req actionRequest, ws workspace.Workspace) error {
	if !knownWorktree(ws, req.Path) {
		return badRequest("unknown worktree path")
	}
	base := s.git.DefaultBranch(req.Path)
	if base == "" {
		return badRequest("this repo has no default branch to compare with")
	}
	return s.launcher.Prompt(req.Path, reviewBrief(base))
}

func reviewBrief(base string) string {
	return fmt.Sprintf(`Review the work on this branch, as a reviewer who did not write it.

Compare it with %[1]s (git diff %[1]s...HEAD, git log %[1]s..HEAD). This is a READ-ONLY session: do not edit files, do not commit, do not run anything that changes the repo or its state.

Report, in this order:
1. Bugs and risks, most serious first, each with file:line and what goes wrong.
2. Missing or weak tests.
3. What looks right (briefly).

If you find something that belongs to another ticket, run workstation discover "one line" instead of describing it at length. If the workstation command is not available, just mention it at the end.
`, base)
}
