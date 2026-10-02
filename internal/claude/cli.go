package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"workstation/internal/safeio"
)

// CLI reads sessions from the Claude Code config dir (~/.claude):
//
//	projects/<encoded-cwd>/<sessionId>.jsonl   transcripts (history)
//	sessions/<pid>.json                        one file per running process
type CLI struct {
	root  string
	alive func(pid int) bool
}

func NewCLI(root string, alive func(pid int) bool) *CLI {
	return &CLI{root: root, alive: alive}
}

type liveSession struct {
	Pid       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
	Name      string `json:"name"`
}

func (c *CLI) Sessions() ([]Session, error) {
	live := c.liveSessions()

	projects, err := os.ReadDir(filepath.Join(c.root, "projects"))
	if err != nil {
		return nil, nil // Claude never ran here: not an error
	}

	var out []Session
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		dir := filepath.Join(c.root, "projects", p.Name())
		files, _ := os.ReadDir(dir)
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			id := strings.TrimSuffix(f.Name(), ".jsonl")
			info, err := readTranscript(filepath.Join(dir, f.Name()))
			if err != nil || info.Cwd == "" {
				continue // unreadable or no cwd: cannot be placed anywhere
			}
			s := Session{
				ID: id, Source: SourceCLI, Cwd: info.Cwd, Branch: info.Branch,
				LastActivity: info.LastActivity, LastMessage: info.LastMessage,
			}
			if validSlug(info.Slug) {
				s.Slug = info.Slug
				s.HasPlan = c.planExists(info.Slug)
			}
			s.Status, s.RawStatus = statusFor(live[id], c.alive)
			if l, ok := live[id]; ok {
				s.Title = l.Name
			}
			out = append(out, s)
		}
	}
	return out, nil
}

const (
	maxPlanBytes = 1 << 20
	maxJSON      = 1 << 20 // small metadata files (live sessions, Desktop sessions)
)

var slugRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// validSlug guards the file name built from a transcript field.
func validSlug(s string) bool { return slugRe.MatchString(s) }

func (c *CLI) planPath(slug string) string {
	return filepath.Join(c.root, "plans", slug+".md")
}

// planExists uses Lstat: a symlink in plans/ is not a plan (it could point at
// any file the user can read).
func (c *CLI) planExists(slug string) bool {
	st, err := os.Lstat(c.planPath(slug))
	return err == nil && st.Mode().IsRegular()
}

// Plan is a session's plan document.
type Plan struct {
	Slug       string    `json:"slug"`
	Path       string    `json:"path"`
	Markdown   string    `json:"markdown"`
	ModifiedAt time.Time `json:"modifiedAt"`
}

// ReadPlan reads <root>/plans/<slug>.md. The slug is validated, so a crafted
// transcript cannot make us read outside the plans directory.
func (c *CLI) ReadPlan(slug string) (Plan, error) {
	if !validSlug(slug) {
		return Plan{}, fmt.Errorf("invalid plan slug %q", slug)
	}
	path := c.planPath(slug)
	st, err := os.Lstat(path)
	if err != nil {
		return Plan{}, err
	}
	if !st.Mode().IsRegular() || st.Size() > maxPlanBytes {
		return Plan{}, fmt.Errorf("plan %s is not a regular file under %d bytes", slug, maxPlanBytes)
	}
	data, err := safeio.ReadFile(path, maxPlanBytes)
	if err != nil {
		return Plan{}, err
	}
	return Plan{Slug: slug, Path: path, Markdown: string(data), ModifiedAt: st.ModTime()}, nil
}

// statusFor maps a live process entry to a Status. Only values we have
// observed are translated; anything else stays unknown.
func statusFor(l liveSession, alive func(int) bool) (Status, string) {
	if l.SessionID == "" || !alive(l.Pid) {
		return StatusStopped, ""
	}
	switch l.Status {
	case "busy":
		return StatusWorking, l.Status
	case "idle":
		return StatusIdle, l.Status
	}
	return StatusUnknown, l.Status
}

func (c *CLI) liveSessions() map[string]liveSession {
	live := map[string]liveSession{}
	files, _ := os.ReadDir(filepath.Join(c.root, "sessions"))
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		data, err := safeio.ReadFile(filepath.Join(c.root, "sessions", f.Name()), maxJSON)
		if err != nil {
			continue // unreadable or oversized: treated as no live info
		}
		var l liveSession
		if json.Unmarshal(data, &l) == nil && l.SessionID != "" {
			live[l.SessionID] = l
		}
	}
	return live
}
