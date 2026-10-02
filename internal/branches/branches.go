// Package branches builds the dependency graph of a repository's active
// branches: which branch is based on which, with PR and worktree info attached.
//
// A branch's parent comes from its PR's base branch when that base is itself
// in the graph; otherwise from Git ancestry (the closest other branch whose tip
// is an ancestor). Each edge says which of the two produced it.
package branches

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"workstation/internal/forge"
	"workstation/internal/gitx"
)

const (
	ViaPR  = "pr"
	ViaGit = "git"

	defaultMaxNodes = 40
	mergedWindow    = 30 * 24 * time.Hour
	gitConcurrency  = 8
)

type Worktree struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type Input struct {
	Worktrees     map[string]Worktree // by branch name
	PRs           []forge.PR
	IncludeMerged bool      // also show branches whose PR merged within 30 days
	Now           time.Time // zero = time.Now()
	MaxNodes      int       // zero = 40
}

type Node struct {
	Branch    string    `json:"branch"`
	Parent    string    `json:"parent,omitempty"`
	Via       string    `json:"via,omitempty"` // "pr" | "git"
	IsDefault bool      `json:"isDefault"`
	Merged    bool      `json:"merged"` // PR merged, or no commits of its own
	Ahead     int       `json:"ahead"`
	Behind    int       `json:"behind"`
	Tip       string    `json:"tip"`
	Date      string    `json:"date"` // committer date of the tip, ISO 8601
	Worktree  *Worktree `json:"worktree,omitempty"`
	PR        *forge.PR `json:"pr,omitempty"`
}

type Graph struct {
	Default string `json:"default"`
	Nodes   []Node `json:"nodes"` // default branch first
	Hidden  int    `json:"hidden"`
}

type candidate struct {
	branch gitx.Branch
	pr     *forge.PR
	wt     *Worktree
	rank   int // lower = kept first when over the cap
}

func Build(dir string, in Input) (Graph, error) {
	def := gitx.DefaultBranch(dir)
	if def == "" {
		return Graph{}, fmt.Errorf("cannot determine the default branch (no origin/HEAD, main or master)")
	}
	all, err := gitx.LocalBranches(dir)
	if err != nil {
		return Graph{}, err
	}
	byName := map[string]gitx.Branch{}
	for _, b := range all {
		byName[b.Name] = b
	}
	defBranch, ok := byName[def]
	if !ok {
		return Graph{}, fmt.Errorf("default branch %q does not exist locally", def)
	}
	unmerged, err := gitx.NoMerged(dir, def)
	if err != nil {
		return Graph{}, err
	}

	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	prs := bestPRs(in.PRs)

	var cands []candidate
	for _, b := range all {
		if b.Name == def {
			continue
		}
		if c, active := classify(b, prs[b.Name], in, unmerged[b.Name], now); active {
			cands = append(cands, c)
		}
	}

	max := in.MaxNodes
	if max <= 0 {
		max = defaultMaxNodes
	}
	kept, hidden := applyCap(cands, max-1)

	nodes := []Node{{Branch: def, IsDefault: true, Tip: defBranch.Tip, Date: defBranch.Date}}
	for _, c := range kept {
		n := Node{Branch: c.branch.Name, Tip: c.branch.Tip, Date: c.branch.Date, Worktree: c.wt, PR: c.pr}
		n.Merged = !unmerged[n.Branch] || (c.pr != nil && c.pr.State == forge.StateMerged)
		nodes = append(nodes, n)
	}

	resolveParents(dir, def, nodes)
	fillAheadBehind(dir, def, nodes)
	return Graph{Default: def, Nodes: nodes, Hidden: hidden}, nil
}

// bestPRs keeps one PR per source branch: an open one, else the most recent.
func bestPRs(prs []forge.PR) map[string]*forge.PR {
	best := map[string]*forge.PR{}
	for i := range prs {
		p := &prs[i]
		cur, ok := best[p.Source]
		if !ok || (p.State == forge.StateOpen && cur.State != forge.StateOpen) ||
			(p.State == cur.State && p.UpdatedAt.After(cur.UpdatedAt)) {
			best[p.Source] = p
		}
	}
	return best
}

// classify decides whether a branch belongs in the graph and how important it is.
func classify(b gitx.Branch, pr *forge.PR, in Input, unmerged bool, now time.Time) (candidate, bool) {
	c := candidate{branch: b, pr: pr}
	if w, ok := in.Worktrees[b.Name]; ok {
		c.wt = &w
	}
	mergedPR := pr != nil && pr.State == forge.StateMerged

	switch {
	case c.wt != nil:
		c.rank = 0
	case pr != nil && pr.State == forge.StateOpen:
		c.rank = 1
	case unmerged && !mergedPR: // squash-merged branches stay "unmerged" in git; the PR knows better
		c.rank = 2
	case in.IncludeMerged && mergedPR && now.Sub(pr.UpdatedAt) <= mergedWindow:
		c.rank = 3
	default:
		return c, false
	}
	return c, true
}

// applyCap keeps the most important candidates, then orders them by name.
func applyCap(cands []candidate, limit int) (kept []candidate, hidden int) {
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].rank != cands[j].rank {
			return cands[i].rank < cands[j].rank
		}
		return cands[i].branch.Date > cands[j].branch.Date
	})
	if limit < 0 {
		limit = 0
	}
	if len(cands) > limit {
		hidden = len(cands) - limit
		cands = cands[:limit]
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].branch.Name < cands[j].branch.Name })
	return cands, hidden
}

// resolveParents sets Parent/Via on every non-default node.
func resolveParents(dir, def string, nodes []Node) {
	inGraph := map[string]bool{}
	for _, n := range nodes {
		inGraph[n.Branch] = true
	}

	parallel(len(nodes), func(i int) {
		n := &nodes[i]
		if n.IsDefault {
			return
		}
		if n.PR != nil && n.PR.Dest != n.Branch && inGraph[n.PR.Dest] {
			n.Parent, n.Via = n.PR.Dest, ViaPR
			return
		}
		n.Parent, n.Via = ancestryParent(dir, def, *n, nodes), ViaGit
	})
	breakCycles(def, nodes)
}

// ancestryParent picks the closest other node whose tip is an ancestor of n.
// Falls back to the default branch (e.g. when it has moved on since n was cut).
func ancestryParent(dir, def string, n Node, nodes []Node) string {
	best, bestN := def, -1
	for _, c := range nodes {
		if c.Branch == n.Branch {
			continue
		}
		if c.Tip == n.Tip && c.Branch != def {
			continue // identical tips are ambiguous; only the default may claim them
		}
		if ok, err := gitx.IsAncestor(dir, c.Branch, n.Branch); err != nil || !ok {
			continue
		}
		dist, err := gitx.CountBetween(dir, c.Branch, n.Branch)
		if err != nil {
			continue
		}
		if bestN < 0 || dist < bestN || (dist == bestN && c.Branch == def) {
			best, bestN = c.Branch, dist
		}
	}
	return best
}

// breakCycles guards against PRs that point at each other.
func breakCycles(def string, nodes []Node) {
	parent := map[string]int{}
	for i, n := range nodes {
		parent[n.Branch] = i
	}
	for i := range nodes {
		cur, steps := nodes[i], 0
		for !cur.IsDefault && steps <= len(nodes) {
			cur = nodes[parent[cur.Parent]]
			steps++
		}
		if steps > len(nodes) {
			nodes[i].Parent, nodes[i].Via = def, ViaGit
		}
	}
}

func fillAheadBehind(dir, def string, nodes []Node) {
	parallel(len(nodes), func(i int) {
		if nodes[i].IsDefault {
			return
		}
		if a, b, err := gitx.AheadBehind(dir, def, nodes[i].Branch); err == nil {
			nodes[i].Ahead, nodes[i].Behind = a, b
		}
	})
}

func parallel(n int, fn func(i int)) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, gitConcurrency)
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			fn(i)
		}()
	}
	wg.Wait()
}
