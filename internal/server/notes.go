package server

import "workstation/internal/notes"

// NoteStore holds the old per-worktree stars and notes (implemented by notes.Store).
// They are no longer edited: the first workspace build copies them into tickets
// (see migrateNotes), and removing a worktree still forgets its leftover note.
type NoteStore interface {
	All() map[string]notes.Note
	Delete(path string) error
}

// WithNotes gives the server the old stars and notes, to migrate them into tickets.
func WithNotes(n NoteStore) Option { return func(o *options) { o.notes = n } }
