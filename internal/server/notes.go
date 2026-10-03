package server

import (
	"encoding/json"
	"net/http"

	"workstation/internal/notes"
)

// NoteStore keeps stars and "where I left off" notes (implemented by notes.Store).
type NoteStore interface {
	All() map[string]notes.Note
	Set(path string, n notes.Note) error
	Delete(path string) error
}

// WithNotes enables GET/POST /api/notes.
func WithNotes(n NoteStore) Option { return func(o *options) { o.notes = n } }

func (s *Server) handleNotesGet(w http.ResponseWriter, r *http.Request) {
	if s.notes == nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, s.notes.All())
}

type noteRequest struct {
	Path    string `json:"path"`
	Starred bool   `json:"starred"`
	Text    string `json:"text"`
}

// handleNotesSet only accepts worktrees of the current snapshot: the store never
// grows from arbitrary paths a page could send.
func (s *Server) handleNotesSet(w http.ResponseWriter, r *http.Request) {
	if s.notes == nil {
		http.NotFound(w, r)
		return
	}
	var req noteRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	ws, err := s.snapshot()
	if err != nil {
		internalError(w, "notes", err)
		return
	}
	if !knownWorktree(ws, req.Path) {
		http.Error(w, "unknown worktree path", http.StatusBadRequest)
		return
	}
	if err := s.notes.Set(req.Path, notes.Note{Starred: req.Starred, Text: req.Text}); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest) // limits only: written for the user
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
