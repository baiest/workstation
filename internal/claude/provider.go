// Package claude discovers local Claude Code sessions.
//
// Everything here reads undocumented, internal files written by Claude Code
// (CLI) and Claude Desktop. It is isolated behind ClaudeSessionProvider so a
// format change only breaks this package. See README "Claude data sources".
package claude

import (
	"errors"
	"sort"
	"time"
)

type Status string

const (
	StatusWorking Status = "working"
	StatusIdle    Status = "idle"
	StatusStopped Status = "stopped"
	StatusUnknown Status = "unknown"
)

type Source string

const (
	SourceCLI     Source = "cli"
	SourceDesktop Source = "desktop"
)

// Session is one Claude Code conversation. Fields that cannot be determined
// reliably are left empty / StatusUnknown rather than guessed.
type Session struct {
	ID             string    `json:"id"` // CLI session id (transcript file name); Desktop-only sessions fall back to the Desktop id
	DesktopID      string    `json:"desktopId,omitempty"`
	Source         Source    `json:"source"`
	Title          string    `json:"title,omitempty"`
	Prompt         string    `json:"prompt,omitempty"`    // the first thing the user asked: tells chats apart when there is no title
	Cwd            string    `json:"cwd"`                 // where the session works NOW: the worktree it belongs to
	StartCwd       string    `json:"startCwd,omitempty"`  // where it began, if that differs: holds the transcript, so resume runs there
	OriginCwd      string    `json:"originCwd,omitempty"` // Desktop only: the repo the worktree was created from
	Branch         string    `json:"branch,omitempty"`
	Status         Status    `json:"status"`
	RawStatus      string    `json:"rawStatus,omitempty"` // untranslated status of a live process, for tooltips
	LastActivity   time.Time `json:"lastActivity"`
	LastMessage    string    `json:"lastMessage,omitempty"`
	Resumable      bool      `json:"resumable"`
	State          State     `json:"state"`                    // what it is doing now (thinking, waiting for you, failed...)
	StateHeuristic bool      `json:"stateHeuristic,omitempty"` // the state is a guess (needs-approval)
	Slug           string    `json:"slug,omitempty"`           // names the plan file, CLI sessions only
	HasPlan        bool      `json:"hasPlan"`                  // a plan file for Slug exists on disk
}

// ResumeID returns the id usable with `claude --resume`, or "" when only a
// Desktop id is known.
func (s Session) ResumeID() string {
	if s.DesktopID != "" && s.ID == s.DesktopID {
		return ""
	}
	return s.ID
}

// ClaudeSessionProvider is the seam that isolates Claude's internal storage.
type ClaudeSessionProvider interface {
	Sessions() ([]Session, error)
}

// Combined is the provider the app uses: CLI transcripts enriched by Desktop
// metadata. If one side fails, the other side's sessions are still returned
// alongside the error.
type Combined struct {
	CLI     ClaudeSessionProvider
	Desktop ClaudeSessionProvider
}

func (c Combined) Sessions() ([]Session, error) {
	cli, cliErr := c.CLI.Sessions()
	desk, deskErr := c.Desktop.Sessions()
	return Merge(cli, desk), errors.Join(cliErr, deskErr)
}

// Merge combines CLI and Desktop sessions. A Desktop session whose
// cliSessionId matches a CLI session enriches it (title, origin repo);
// unmatched Desktop sessions are kept as-is. Result is newest first.
func Merge(cli, desktop []Session) []Session {
	byID := make(map[string]Session, len(desktop))
	for _, d := range desktop {
		byID[d.ID] = d
	}

	out := make([]Session, 0, len(cli)+len(desktop))
	for _, c := range cli {
		if d, ok := byID[c.ID]; ok {
			delete(byID, c.ID)
			c.DesktopID = d.DesktopID
			c.OriginCwd = d.OriginCwd
			c.Source = SourceDesktop
			if d.Title != "" {
				c.Title = d.Title
			}
			if d.LastActivity.After(c.LastActivity) {
				c.LastActivity = d.LastActivity
			}
		}
		out = append(out, c)
	}
	for _, d := range desktop {
		if _, left := byID[d.ID]; left {
			out = append(out, d)
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].LastActivity.After(out[j].LastActivity) })
	return out
}
