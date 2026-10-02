// Package gitx wraps the git CLI. Git is the source of truth; we only parse
// the stable porcelain output of official commands.
package gitx

import (
	"path/filepath"
	"strconv"
	"strings"
)

// Worktree is one entry of `git worktree list --porcelain`.
type Worktree struct {
	Path     string
	Head     string
	Branch   string // short name, empty when detached or bare
	Detached bool
	Bare     bool
	IsMain   bool // first entry is always the main worktree
}

// ParseWorktrees parses `git worktree list --porcelain`.
func ParseWorktrees(out string) []Worktree {
	var list []Worktree
	var cur *Worktree

	flush := func() {
		if cur != nil {
			cur.IsMain = len(list) == 0
			list = append(list, *cur)
			cur = nil
		}
	}

	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		key, val, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			flush()
			cur = &Worktree{Path: filepath.FromSlash(val)}
		case "HEAD":
			if cur != nil {
				cur.Head = val
			}
		case "branch":
			if cur != nil {
				cur.Branch = strings.TrimPrefix(val, "refs/heads/")
			}
		case "detached":
			if cur != nil {
				cur.Detached = true
			}
		case "bare":
			if cur != nil {
				cur.Bare = true
			}
		}
	}
	flush()
	return list
}

// Status summarises `git status --porcelain=v2 --branch`.
type Status struct {
	Branch      string
	Detached    bool
	HasUpstream bool
	Ahead       int
	Behind      int
	Staged      int // files with an index change
	Modified    int // files with a working-tree change
	Untracked   int
	Conflicts   int
}

// Dirty reports whether there is anything uncommitted.
func (s Status) Dirty() bool {
	return s.Staged+s.Modified+s.Untracked+s.Conflicts > 0
}

// ParseStatus parses `git status --porcelain=v2 --branch`.
func ParseStatus(out string) Status {
	var s Status
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			head := strings.TrimPrefix(line, "# branch.head ")
			if head == "(detached)" {
				s.Detached = true
			} else {
				s.Branch = head
			}
		case strings.HasPrefix(line, "# branch.upstream "):
			s.HasUpstream = true
		case strings.HasPrefix(line, "# branch.ab "):
			s.Ahead, s.Behind = parseAheadBehind(strings.TrimPrefix(line, "# branch.ab "))
		case strings.HasPrefix(line, "1 "), strings.HasPrefix(line, "2 "):
			s.countXY(line)
		case strings.HasPrefix(line, "u "):
			s.Conflicts++
		case strings.HasPrefix(line, "? "):
			s.Untracked++
		}
	}
	return s
}

// countXY reads the two-letter XY field of an ordinary/renamed entry.
func (s *Status) countXY(line string) {
	fields := strings.SplitN(line, " ", 3)
	if len(fields) < 3 || len(fields[1]) != 2 {
		return
	}
	if fields[1][0] != '.' {
		s.Staged++
	}
	if fields[1][1] != '.' {
		s.Modified++
	}
}

func parseAheadBehind(v string) (ahead, behind int) {
	a, b, _ := strings.Cut(v, " ")
	ahead, _ = strconv.Atoi(strings.TrimPrefix(a, "+"))
	behind, _ = strconv.Atoi(strings.TrimPrefix(b, "-"))
	return ahead, behind
}

// Commit is the last commit of a worktree.
type Commit struct {
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
	Date    string `json:"date"` // strict ISO 8601
}

// LogFormat is the --format passed to `git log -1`; ParseCommit reads it back.
const LogFormat = "%h%x00%s%x00%cI"

// ParseCommit parses the output of `git log -1 --format=LogFormat`.
func ParseCommit(out string) (Commit, bool) {
	parts := strings.Split(strings.TrimSpace(out), "\x00")
	if len(parts) != 3 {
		return Commit{}, false
	}
	return Commit{Hash: parts[0], Subject: parts[1], Date: parts[2]}, true
}
