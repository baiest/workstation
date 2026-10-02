package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"workstation/internal/proc"
)

// Timeout bounds every git invocation, so one hung repository (network
// filesystem, huge repo) cannot pin a request or pile up processes.
var Timeout = 30 * time.Second

// hardening is prepended to every call. Repos come from wherever the user ran
// Claude, and a repo's own .git/config can name commands that git would run:
// core.fsmonitor on `status`, hooks, a pager. Command-line -c beats repo config.
// safe.directory is deliberately left alone: it protects against foreign repos.
var hardening = []string{
	"-c", "core.fsmonitor=false",
	"-c", "core.hooksPath=" + os.DevNull,
	"-c", "core.pager=cat",
}

// run executes git in dir. GIT_OPTIONAL_LOCKS=0 keeps `status` from taking
// index.lock, so refreshing never collides with the user's own git commands.
func run(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	full := append([]string{"-C", dir}, hardening...)
	cmd := exec.CommandContext(ctx, "git", append(full, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	proc.KillTreeOnCancel(cmd)
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			// wrapped without the ExitError so callers cannot mistake a kill for "false"
			return "", fmt.Errorf("git %s: timed out after %s: %w", strings.Join(args, " "), Timeout, ctx.Err())
		}
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// heads qualifies a branch name. A bare name resolves to a same-named tag
// first, which a hostile repo could use to distort the branch graph.
func heads(name string) string { return "refs/heads/" + name }

// Worktrees lists every worktree of the repository containing dir.
func Worktrees(dir string) ([]Worktree, error) {
	out, err := run(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return ParseWorktrees(out), nil
}

// RepoRoot returns the main worktree path of the repository containing dir.
func RepoRoot(dir string) (string, error) {
	wts, err := Worktrees(dir)
	if err != nil {
		return "", err
	}
	if len(wts) == 0 {
		return "", errors.New("git reported no worktrees")
	}
	return wts[0].Path, nil
}

// TopLevel returns the root of the worktree containing dir (dir may be a
// subdirectory). Unlike a path-prefix guess it is exact for nested worktrees.
func TopLevel(dir string) (string, error) {
	out, err := run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return filepath.FromSlash(strings.TrimSpace(out)), nil
}

// RemoteURL returns the URL of the "origin" remote.
func RemoteURL(dir string) (string, error) {
	out, err := run(dir, "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// DefaultBranch returns the repo's default branch name: origin/HEAD when set,
// else a local main or master. Empty when none can be determined.
func DefaultBranch(dir string) string {
	if out, err := run(dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		name := strings.TrimPrefix(strings.TrimSpace(out), "origin/")
		if validBranchName(dir, name) {
			return name
		}
	}
	for _, name := range []string{"main", "master"} {
		if _, err := run(dir, "show-ref", "--verify", "--quiet", heads(name)); err == nil {
			return name
		}
	}
	return ""
}

// validBranchName guards names that come from repo-controlled refs before they
// are used as git arguments (a leading "-" would be read as an option).
func validBranchName(dir, name string) bool {
	if name == "" || strings.HasPrefix(name, "-") {
		return false
	}
	_, err := run(dir, "check-ref-format", "--branch", name)
	return err == nil
}

// Branch is a local branch tip.
type Branch struct {
	Name string
	Tip  string
	Date string // committer date of the tip, strict ISO 8601
}

// LocalBranches lists refs/heads.
func LocalBranches(dir string) ([]Branch, error) {
	out, err := run(dir, "for-each-ref", "--format=%(refname:lstrip=2)%00%(objectname)%00%(committerdate:iso-strict)", "refs/heads")
	if err != nil {
		return nil, err
	}
	var list []Branch
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.Split(strings.TrimSpace(line), "\x00")
		if len(parts) == 3 {
			list = append(list, Branch{Name: parts[0], Tip: parts[1], Date: parts[2]})
		}
	}
	return list, nil
}

// BranchTip returns the commit a local branch points at.
func BranchTip(dir, name string) (string, error) {
	if name == "" || strings.HasPrefix(name, "-") {
		return "", fmt.Errorf("invalid branch name %q", name)
	}
	out, err := run(dir, "rev-parse", "--verify", "--quiet", heads(name)+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// DeleteBranch deletes the local branch name, but only if it still points at
// expectedSHA (what the user was shown), so a branch that moved in the
// meantime is never lost. Git itself refuses a branch checked out in any
// worktree. Remote branches are never touched.
func DeleteBranch(dir, name, expectedSHA string) error {
	if !validBranchName(dir, name) {
		return fmt.Errorf("invalid branch name %q", name)
	}
	tip, err := BranchTip(dir, name)
	if err != nil {
		return err
	}
	if tip != expectedSHA {
		return fmt.Errorf("branch %q moved since it was listed (now %s); not deleting", name, tip)
	}
	_, err = run(dir, "branch", "-D", "--", name)
	return err
}

// NoMerged returns the local branches that are not fully merged into base.
func NoMerged(dir, base string) (map[string]bool, error) {
	out, err := run(dir, "branch", "--no-merged", heads(base), "--format=%(refname:lstrip=2)")
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, name := range strings.Fields(out) {
		set[name] = true
	}
	return set, nil
}

// IsAncestor reports whether branch a is an ancestor of (or equal to) branch b.
// An unknown branch or a timeout is an error, not "false".
func IsAncestor(dir, a, b string) (bool, error) {
	_, err := run(dir, "merge-base", "--is-ancestor", heads(a), heads(b))
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// CountBetween counts commits reachable from branch b but not from branch a.
func CountBetween(dir, a, b string) (int, error) {
	out, err := run(dir, "rev-list", "--count", heads(a)+".."+heads(b))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// AheadBehind returns how many commits branch has that base lacks (ahead) and
// how many base has that branch lacks (behind).
func AheadBehind(dir, base, branch string) (ahead, behind int, err error) {
	out, err := run(dir, "rev-list", "--left-right", "--count", heads(base)+"..."+heads(branch))
	if err != nil {
		return 0, 0, err
	}
	l, r, _ := strings.Cut(strings.TrimSpace(out), "\t")
	behind, _ = strconv.Atoi(l)
	ahead, _ = strconv.Atoi(r)
	return ahead, behind, nil
}

// StatusOf returns the git status of the worktree at dir.
func StatusOf(dir string) (Status, error) {
	out, err := run(dir, "status", "--porcelain=v2", "--branch")
	if err != nil {
		return Status{}, err
	}
	return ParseStatus(out), nil
}

// LastCommit returns the HEAD commit of dir. ok is false for a repo with no
// commits yet.
func LastCommit(dir string) (Commit, bool, error) {
	out, err := run(dir, "log", "-1", "--format="+LogFormat)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return Commit{}, false, nil
		}
		return Commit{}, false, err
	}
	c, ok := ParseCommit(out)
	return c, ok, nil
}
