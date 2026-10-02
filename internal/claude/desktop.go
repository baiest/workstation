package claude

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Desktop reads Claude Desktop's Code-tab sessions:
//
//	<root>/claude-code-sessions/<account>/<org>/local_<id>.json
//
// Each file has the CLI session id (cliSessionId), which links it to the CLI
// transcript. Desktop has no liveness info we can verify, so status is unknown
// unless Merge joins it with a CLI session.
type Desktop struct{ roots []string }

func NewDesktop(roots []string) *Desktop { return &Desktop{roots: roots} }

type desktopFile struct {
	SessionID      string `json:"sessionId"`
	CLISessionID   string `json:"cliSessionId"`
	Cwd            string `json:"cwd"`
	OriginCwd      string `json:"originCwd"`
	SourceBranch   string `json:"sourceBranch"`
	CreatedAt      int64  `json:"createdAt"`
	LastActivityAt int64  `json:"lastActivityAt"`
	IsArchived     bool   `json:"isArchived"`
	Title          string `json:"title"`
}

func (d *Desktop) Sessions() ([]Session, error) {
	var out []Session
	for _, root := range d.roots {
		_ = filepath.WalkDir(filepath.Join(root, "claude-code-sessions"), func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() || !strings.HasPrefix(e.Name(), "local_") || !strings.HasSuffix(e.Name(), ".json") {
				return nil
			}
			if s, ok := readDesktopFile(p); ok {
				out = append(out, s)
			}
			return nil
		})
	}
	return out, nil
}

func readDesktopFile(path string) (Session, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Session{}, false
	}
	var f desktopFile
	if json.Unmarshal(data, &f) != nil || f.SessionID == "" || f.IsArchived {
		return Session{}, false
	}
	id := f.CLISessionID
	if id == "" {
		id = f.SessionID
	}
	ms := f.LastActivityAt
	if ms == 0 {
		ms = f.CreatedAt
	}
	return Session{
		ID: id, DesktopID: f.SessionID, Source: SourceDesktop, Title: f.Title,
		Cwd: f.Cwd, OriginCwd: f.OriginCwd, Branch: f.SourceBranch,
		Status: StatusUnknown, LastActivity: time.UnixMilli(ms),
	}, true
}
