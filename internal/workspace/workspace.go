// Package workspace joins Git worktrees with Claude sessions.
//
// Git is the source of truth for repos and worktrees. A session is linked to a
// worktree only when git itself says its cwd lives in that worktree
// (`rev-parse --show-toplevel`); a path-prefix guess would wrongly attach a
// session from a deleted `repo/.claude/worktrees/x` to the main worktree.
// Everything else is reported as unlinked, with a reason.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"workstation/internal/claude"
	"workstation/internal/gitx"
)

const (
	reasonMissing   = "directory no longer exists (worktree deleted?)"
	reasonNotGit    = "not inside a git repository"
	reasonNotListed = "git does not list this directory as a worktree"
	gitConcurrency  = 8
)

type Workspace struct {
	Repos       []Repo     `json:"repos"`
	Unlinked    []Unlinked `json:"unlinked"`
	Warnings    []string   `json:"warnings"`
	GeneratedAt time.Time  `json:"generatedAt"`
}

type Repo struct {
	Name      string     `json:"name"`
	Path      string     `json:"path"`
	Worktrees []Worktree `json:"worktrees"`
}

type Worktree struct {
	Name     string           `json:"name"`
	Path     string           `json:"path"`
	Branch   string           `json:"branch,omitempty"`
	Head     string           `json:"head"`
	Detached bool             `json:"detached"`
	IsMain   bool             `json:"isMain"`
	Git      GitInfo          `json:"git"`
	GitError string           `json:"gitError,omitempty"`
	Sessions []claude.Session `json:"sessions"` // newest first
}

type GitInfo struct {
	Dirty       bool         `json:"dirty"`
	Staged      int          `json:"staged"`
	Modified    int          `json:"modified"`
	Untracked   int          `json:"untracked"`
	Conflicts   int          `json:"conflicts"`
	HasUpstream bool         `json:"hasUpstream"`
	Ahead       int          `json:"ahead"`
	Behind      int          `json:"behind"`
	LastCommit  *gitx.Commit `json:"lastCommit,omitempty"`
}

type Unlinked struct {
	Session claude.Session `json:"session"`
	Reason  string         `json:"reason"`
}

// Builder computes a Workspace from Git plus a Claude session provider.
type Builder struct {
	Provider   claude.ClaudeSessionProvider
	ExtraRepos []string // optional repos to list even without sessions
}

func (b Builder) Build() (Workspace, error) {
	ws := Workspace{GeneratedAt: time.Now(), Repos: []Repo{}, Unlinked: []Unlinked{}, Warnings: []string{}}
	r := newResolver()

	for _, p := range b.ExtraRepos {
		if _, err := r.addRepo(p); err != nil {
			ws.Warnings = append(ws.Warnings, fmt.Sprintf("configured repo %q: %v", p, err))
		}
	}

	sessions, err := b.Provider.Sessions()
	if err != nil {
		ws.Warnings = append(ws.Warnings, "claude sessions: "+err.Error())
	}
	for _, s := range sessions {
		s.Resumable = s.ResumeID() != ""
		if reason := r.place(s); reason != "" {
			if reason == reasonMissing {
				s.Resumable = false // `claude --resume` must run in the original directory
			}
			ws.Unlinked = append(ws.Unlinked, Unlinked{Session: s, Reason: reason})
		}
	}

	ws.Repos = r.finish()
	return ws, nil
}

// --- resolver ---------------------------------------------------------------

type resolver struct {
	repos  map[string]*repoEntry
	byPath map[string]*Worktree // normalized worktree path -> worktree
	cache  map[string]placement // normalized cwd -> outcome, avoids repeated git calls
}

type repoEntry struct {
	repo Repo
	wts  []*Worktree
}

type placement struct {
	wt     *Worktree
	reason string
}

func newResolver() *resolver {
	return &resolver{
		repos:  map[string]*repoEntry{},
		byPath: map[string]*Worktree{},
		cache:  map[string]placement{},
	}
}

// addRepo registers the repository containing dir along with all its worktrees.
func (r *resolver) addRepo(dir string) (*repoEntry, error) {
	list, err := gitx.Worktrees(dir)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("no worktrees reported for %s", dir)
	}
	key := normalize(list[0].Path)
	if e, ok := r.repos[key]; ok {
		return e, nil
	}

	e := &repoEntry{repo: Repo{Name: filepath.Base(list[0].Path), Path: list[0].Path}}
	for _, g := range list {
		if g.Bare {
			continue
		}
		w := &Worktree{
			Name: filepath.Base(g.Path), Path: g.Path, Branch: g.Branch, Head: g.Head,
			Detached: g.Detached, IsMain: g.IsMain, Sessions: []claude.Session{},
		}
		e.wts = append(e.wts, w)
		r.byPath[normalize(g.Path)] = w
	}
	r.repos[key] = e
	return e, nil
}

// place links s to a worktree and returns "" or the reason it stays unlinked.
func (r *resolver) place(s claude.Session) string {
	key := normalize(s.Cwd)
	p, ok := r.cache[key]
	if !ok {
		p = r.resolve(s)
		r.cache[key] = p
	}
	if p.wt != nil {
		p.wt.Sessions = append(p.wt.Sessions, s)
		return ""
	}
	return p.reason
}

func (r *resolver) resolve(s claude.Session) placement {
	if w, ok := r.byPath[normalize(s.Cwd)]; ok {
		return placement{wt: w}
	}
	if _, err := os.Stat(s.Cwd); err != nil {
		if s.OriginCwd != "" {
			_, _ = r.addRepo(s.OriginCwd) // still show the repo this orphan came from
		}
		return placement{reason: reasonMissing}
	}
	top, err := gitx.TopLevel(s.Cwd)
	if err != nil {
		return placement{reason: reasonNotGit}
	}
	if _, err := r.addRepo(top); err != nil {
		return placement{reason: reasonNotGit}
	}
	if w, ok := r.byPath[normalize(top)]; ok {
		return placement{wt: w}
	}
	return placement{reason: reasonNotListed}
}

// finish fills git info (bounded concurrency) and returns sorted repos.
func (r *resolver) finish() []Repo {
	var wg sync.WaitGroup
	sem := make(chan struct{}, gitConcurrency)
	for _, e := range r.repos {
		for _, w := range e.wts {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer func() { <-sem; wg.Done() }()
				fillGit(w)
			}()
		}
	}
	wg.Wait()

	repos := make([]Repo, 0, len(r.repos))
	for _, e := range r.repos {
		sortWorktrees(e.wts)
		for _, w := range e.wts {
			sort.SliceStable(w.Sessions, func(i, j int) bool { return w.Sessions[i].LastActivity.After(w.Sessions[j].LastActivity) })
			e.repo.Worktrees = append(e.repo.Worktrees, *w)
		}
		repos = append(repos, e.repo)
	}
	sort.Slice(repos, func(i, j int) bool {
		return strings.ToLower(repos[i].Name) < strings.ToLower(repos[j].Name)
	})
	return repos
}

func fillGit(w *Worktree) {
	st, err := gitx.StatusOf(w.Path)
	if err != nil {
		w.GitError = err.Error()
		return
	}
	w.Git = GitInfo{
		Dirty: st.Dirty(), Staged: st.Staged, Modified: st.Modified, Untracked: st.Untracked,
		Conflicts: st.Conflicts, HasUpstream: st.HasUpstream, Ahead: st.Ahead, Behind: st.Behind,
	}
	if c, ok, err := gitx.LastCommit(w.Path); err == nil && ok {
		w.Git.LastCommit = &c
	}
}

// sortWorktrees puts the main worktree first, then most recently active.
func sortWorktrees(wts []*Worktree) {
	latest := func(w *Worktree) time.Time {
		var t time.Time
		for _, s := range w.Sessions {
			if s.LastActivity.After(t) {
				t = s.LastActivity
			}
		}
		return t
	}
	sort.SliceStable(wts, func(i, j int) bool {
		a, b := wts[i], wts[j]
		if a.IsMain != b.IsMain {
			return a.IsMain
		}
		if ta, tb := latest(a), latest(b); !ta.Equal(tb) {
			return ta.After(tb)
		}
		return a.Name < b.Name
	})
}

func isCaseInsensitiveOS() bool {
	return runtime.GOOS == "windows" || runtime.GOOS == "darwin"
}

// normalize makes paths comparable across separators, trailing slashes and
// (on Windows/macOS) letter case.
func normalize(p string) string {
	p = filepath.ToSlash(filepath.Clean(p))
	if isCaseInsensitiveOS() {
		p = strings.ToLower(p)
	}
	return p
}
