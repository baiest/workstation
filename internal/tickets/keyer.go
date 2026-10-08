package tickets

import (
	"fmt"
	"regexp"
	"strings"
)

// DefaultPattern finds a Jira-style key (REG-5393) at the start of a branch name
// or after a slash (feature/LOY-96-x). The key is the first capture group.
const DefaultPattern = `(?:^|/)([A-Za-z][A-Za-z0-9]{1,9}-\d{1,7})(?:[^A-Za-z0-9]|$)`

// Refs that are not a tracker key start with one of these.
const (
	branchPrefix   = "branch:"
	folderPrefix   = "folder:"
	notePrefix     = "note:" // a ticket born from a discovery: it has no branch yet
	maxRefLen      = 200
	maxPatternSize = 200
)

// Keyer decides which ticket a branch belongs to. One implementation serves the
// whole app, so a worktree, a branch and a command-line call always agree.
type Keyer struct {
	re *regexp.Regexp
}

// NewKeyer compiles a user pattern. An empty one means DefaultPattern. The
// pattern must have exactly one capture group: the key.
func NewKeyer(pattern string) (Keyer, error) {
	if strings.TrimSpace(pattern) == "" {
		pattern = DefaultPattern
	}
	if len(pattern) > maxPatternSize {
		return Keyer{}, fmt.Errorf("ticket pattern is longer than %d characters", maxPatternSize)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Keyer{}, fmt.Errorf("ticket pattern: %w", err)
	}
	if re.NumSubexp() != 1 {
		return Keyer{}, fmt.Errorf("ticket pattern must have exactly one capture group (the key), has %d", re.NumSubexp())
	}
	return Keyer{re: re}, nil
}

var defaultRe = regexp.MustCompile(DefaultPattern)

func (k Keyer) regexp() *regexp.Regexp {
	if k.re == nil {
		return defaultRe
	}
	return k.re
}

// KeyOf is the tracker key found in text, upper-cased, or "".
func (k Keyer) KeyOf(text string) string {
	if text == "" {
		return ""
	}
	m := k.regexp().FindStringSubmatch(text)
	if len(m) < 2 {
		return ""
	}
	return strings.ToUpper(m[1])
}

// Ref names the ticket of a piece of work: its tracker key when the branch (or,
// failing that, the folder) has one; otherwise the branch itself, because a ticket
// can simply be "the work on that branch". "" for something that is no work at
// all (a nameless detached head with no folder).
func (k Keyer) Ref(branch, folder string) string {
	if key := k.KeyOf(branch); key != "" {
		return key
	}
	if key := k.KeyOf(folder); key != "" {
		return key
	}
	if branch != "" {
		return branchPrefix + refSafe(branch)
	}
	if folder != "" {
		return folderPrefix + refSafe(folder)
	}
	return ""
}

// refSafe keeps the separator of ID out of a ref (git allows | in a branch name).
func refSafe(s string) string { return strings.ReplaceAll(s, "|", "_") }

// IsKey reports whether ref is a tracker key rather than a branch or note ref.
func IsKey(ref string) bool {
	return ref != "" && !strings.HasPrefix(ref, branchPrefix) && !strings.HasPrefix(ref, folderPrefix) && !strings.HasPrefix(ref, notePrefix)
}

// ID joins a repo path and a ref into the one identity of a ticket.
func ID(repo, ref string) string { return repo + "|" + ref }

// SplitID is the inverse of ID. ok is false for anything not built by ID.
func SplitID(id string) (repo, ref string, ok bool) {
	i := strings.LastIndex(id, "|")
	if i <= 0 || i == len(id)-1 {
		return "", "", false
	}
	return id[:i], id[i+1:], true
}
