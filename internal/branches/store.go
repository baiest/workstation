package branches

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"workstation/internal/forge"
	"workstation/internal/safeio"
)

const maxCacheFile = 8 << 20

// Snapshot is the pull requests of one repo as of FetchedAt.
type Snapshot struct {
	FetchedAt time.Time  `json:"fetchedAt"`
	PRs       []forge.PR `json:"prs"`
}

// Store keeps snapshots across restarts, so the first paint after starting the
// app already has pull requests instead of waiting for the forge.
type Store interface {
	Load(repo string) (Snapshot, bool)
	Save(repo string, snap Snapshot) error
}

// DiskStore keeps one JSON file per repo in Dir. The files hold PR titles and
// branch names, so the directory is private to the user (0700, files 0600).
// Anything unreadable, corrupt or oversized is treated as "not cached".
type DiskStore struct{ Dir string }

func NewDiskStore(dir string) *DiskStore { return &DiskStore{Dir: dir} }

func (d *DiskStore) path(repo string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(repo)))
	return filepath.Join(d.Dir, hex.EncodeToString(sum[:16])+".json")
}

func (d *DiskStore) Load(repo string) (Snapshot, bool) {
	data, err := safeio.ReadFile(d.path(repo), maxCacheFile)
	if err != nil {
		return Snapshot{}, false
	}
	var snap Snapshot
	if json.Unmarshal(data, &snap) != nil || snap.FetchedAt.IsZero() {
		return Snapshot{}, false
	}
	return snap, true
}

// Save writes atomically (temp file, then rename), so a crash never leaves half a file.
func (d *DiskStore) Save(repo string, snap Snapshot) error {
	if err := os.MkdirAll(d.Dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(d.Dir, "pr-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op after a successful rename

	if err := tmp.Chmod(0o600); err != nil && !os.IsPermission(err) {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), d.path(repo))
}
