// Package notes keeps what the user adds to a worktree: a star (focus) and a
// one-line "where I left off" note. It is the only data workstation writes, so
// it is small, bounded, private (0600) and written atomically.
package notes

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"

	"workstation/internal/safeio"
)

const (
	MaxText    = 200
	MaxEntries = 500
	maxFile    = 256 << 10
)

// Note is what is kept for one worktree path.
type Note struct {
	Starred bool   `json:"starred,omitempty"`
	Text    string `json:"text,omitempty"`
}

// Store is safe for concurrent use. An empty path keeps everything in memory.
type Store struct {
	path string
	mu   sync.Mutex
	data map[string]Note
}

func New(path string) *Store {
	s := &Store{path: path, data: map[string]Note{}}
	s.load()
	return s
}

// load treats a missing, corrupt or oversized file as empty: notes are a convenience.
func (s *Store) load() {
	if s.path == "" {
		return
	}
	raw, err := safeio.ReadFile(s.path, maxFile)
	if err != nil {
		return
	}
	var m map[string]Note
	if json.Unmarshal(raw, &m) == nil {
		s.data = m
	}
}

func (s *Store) All() map[string]Note {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Note, len(s.data))
	for k, v := range s.data {
		out[k] = v
	}
	return out
}

// clean makes a one-line text: control characters dropped, newlines and tabs become spaces.
func clean(text string) string {
	var b strings.Builder
	for _, r := range text {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteRune(' ')
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// Set stores the note; one with no star and no text is removed.
func (s *Store) Set(path string, n Note) error {
	n.Text = clean(n.Text)
	if len([]rune(n.Text)) > MaxText {
		return fmt.Errorf("note is longer than %d characters", MaxText)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !n.Starred && n.Text == "" {
		delete(s.data, path)
		return s.save()
	}
	if _, exists := s.data[path]; !exists && len(s.data) >= MaxEntries {
		return errors.New("too many notes")
	}
	s.data[path] = n
	return s.save()
}

func (s *Store) Delete(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, path)
	return s.save()
}

// save writes to a temp file and renames it, so a crash never leaves half a file.
func (s *Store) save() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(s.data)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
