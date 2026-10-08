// Package tickets keeps what only the human can say about a piece of work: is it
// active, parked or blocked, what is the next step, which side discoveries came
// up. Everything else about a ticket (worktrees, branches, sessions, PRs) is
// derived from Git and Claude and never stored here.
//
// Like the notes store, it is small, bounded, private (0600) and written
// atomically; a missing or corrupt file counts as empty.
package tickets

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"workstation/internal/safeio"
)

// State is the human's decision about a ticket. A ticket with no decision yet is
// "inbox" (the empty State): it was spotted from a branch and nobody triaged it.
type State string

const (
	StateInbox   State = ""
	StateBacklog State = "backlog"
	StateReady   State = "ready"
	StateActive  State = "active"
	StateParked  State = "parked"
	StateBlocked State = "blocked"
	StateDone    State = "done"
	StateIgnored State = "ignored"
)

var validStates = map[State]bool{
	StateInbox: true, StateBacklog: true, StateReady: true, StateActive: true,
	StateParked: true, StateBlocked: true, StateDone: true, StateIgnored: true,
}

// Discovery statuses.
const (
	DiscoveryOpen     = "open"
	DiscoveryDropped  = "dropped"
	DiscoveryInScope  = "in-scope"
	DiscoveryPromoted = "promoted"
)

// Discovery sources.
const (
	SourceHuman = "human"
	SourceCLI   = "cli"
)

const (
	MaxTitle         = 200
	MaxNextStep      = 1000
	MaxBlocked       = 200
	MaxDiscovery     = 300
	MaxDiscoveries   = 50
	MaxBlockedBy     = 10
	MaxLink          = 500
	MaxTickets       = 2000
	MaxDismissed     = 1000
	maxFile          = 4 << 20
	storeVersion     = 1
	activeWIPDefault = 3
)

// DefaultWIPLimit is how many active tickets before the page warns.
func DefaultWIPLimit() int { return activeWIPDefault }

// Discovery is a line that came up while working on a ticket and is not part of
// it: it waits for a decision instead of becoming work by itself.
type Discovery struct {
	ID         string    `json:"id"`
	Text       string    `json:"text"`
	Source     string    `json:"source"`
	Status     string    `json:"status"`
	PromotedTo string    `json:"promotedTo,omitempty"` // ticket id, when Status is promoted
	At         time.Time `json:"at"`
}

// Ticket is the stored part of a ticket.
type Ticket struct {
	ID            string      `json:"id"`
	Repo          string      `json:"repo"`
	Ref           string      `json:"ref"`
	Branch        string      `json:"branch,omitempty"` // a branch that belongs to this ticket although its name says otherwise
	Title         string      `json:"title,omitempty"`
	State         State       `json:"state,omitempty"`
	BlockedReason string      `json:"blockedReason,omitempty"`
	BlockedBy     []string    `json:"blockedBy,omitempty"` // ids of the tickets this one waits for
	NextStep      string      `json:"nextStep,omitempty"`
	Link          string      `json:"link,omitempty"` // where this ticket lives in your tracker, whichever it is
	Discoveries   []Discovery `json:"discoveries,omitempty"`
	Created       time.Time   `json:"created"`
	Updated       time.Time   `json:"updated"`
}

// Update is a partial change: nil fields stay as they are.
type Update struct {
	Repo, Ref     string
	Branch        *string
	Title         *string
	State         *State
	BlockedReason *string
	BlockedBy     *[]string // replaces the whole list
	NextStep      *string
	Link          *string
}

type fileData struct {
	Version   int                `json:"version"`
	Migrated  bool               `json:"migrated,omitempty"` // stars and notes were already copied in
	Tickets   map[string]*Ticket `json:"tickets"`
	Dismissed map[string]string  `json:"dismissed,omitempty"` // session id -> when it stopped mattering
}

// Store is safe for concurrent use. An empty path keeps everything in memory.
type Store struct {
	path string
	now  func() time.Time
	mu   sync.Mutex
	data fileData
}

func New(path string) *Store {
	s := &Store{path: path, now: time.Now, data: fileData{Version: storeVersion, Tickets: map[string]*Ticket{}}}
	s.load()
	return s
}

// load treats a missing, corrupt or oversized file as empty: tickets are a convenience.
func (s *Store) load() {
	if s.path == "" {
		return
	}
	raw, err := safeio.ReadFile(s.path, maxFile)
	if err != nil {
		return
	}
	var d fileData
	if json.Unmarshal(raw, &d) != nil {
		return
	}
	if d.Tickets == nil {
		d.Tickets = map[string]*Ticket{}
	}
	for id, t := range d.Tickets {
		if t == nil || t.ID != id {
			delete(d.Tickets, id)
		}
	}
	d.Version = storeVersion
	s.data = d
}

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

// Snapshot is a copy of everything stored, safe to hand out.
type Snapshot struct {
	Tickets   map[string]Ticket `json:"tickets"`
	Dismissed []string          `json:"dismissed"`
}

func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := Snapshot{Tickets: make(map[string]Ticket, len(s.data.Tickets)), Dismissed: make([]string, 0, len(s.data.Dismissed))}
	for id, t := range s.data.Tickets {
		cp := *t
		cp.Discoveries = append([]Discovery(nil), t.Discoveries...)
		out.Tickets[id] = cp
	}
	for id := range s.data.Dismissed {
		out.Dismissed = append(out.Dismissed, id)
	}
	sort.Strings(out.Dismissed)
	return out
}

// Get returns a copy of one ticket.
func (s *Store) Get(id string) (Ticket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.data.Tickets[id]
	if !ok {
		return Ticket{}, false
	}
	cp := *t
	cp.Discoveries = append([]Discovery(nil), t.Discoveries...)
	return cp, true
}

// oneLine makes a one-line text: control characters dropped, newlines and tabs become spaces.
func oneLine(text string) string {
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
	return strings.Join(strings.Fields(b.String()), " ")
}

// multiLine keeps newlines (a next step may be a short list) but drops other control characters.
func multiLine(text string) string {
	var b strings.Builder
	for _, r := range strings.ReplaceAll(text, "\r\n", "\n") {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

func tooLong(what, text string, max int) error {
	if len([]rune(text)) > max {
		return fmt.Errorf("%s is longer than %d characters", what, max)
	}
	return nil
}

// ValidRef accepts a ref built by Keyer.Ref or a note ref: short, one line, no id separator.
func ValidRef(ref string) bool {
	if ref == "" || len(ref) > maxRefLen || strings.Contains(ref, "|") {
		return false
	}
	for _, r := range ref {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// ErrNotFound is returned for an id that is not stored.
var ErrNotFound = errors.New("no such ticket")

func (s *Store) entry(repo, ref string) (*Ticket, error) {
	if repo == "" || !ValidRef(ref) {
		return nil, errors.New("invalid ticket reference")
	}
	id := ID(repo, ref)
	if t, ok := s.data.Tickets[id]; ok {
		return t, nil
	}
	if len(s.data.Tickets) >= MaxTickets {
		return nil, errors.New("too many tickets")
	}
	now := s.now()
	t := &Ticket{ID: id, Repo: repo, Ref: ref, Created: now, Updated: now}
	s.data.Tickets[id] = t
	return t, nil
}

// prune drops a ticket that carries no decision and no text, so the store only
// holds what a human wrote.
func (s *Store) prune(t *Ticket) {
	if t.State == StateInbox && t.Title == "" && t.NextStep == "" && t.BlockedReason == "" && t.Branch == "" && t.Link == "" && len(t.Discoveries) == 0 && len(t.BlockedBy) == 0 {
		delete(s.data.Tickets, t.ID)
	}
}

// Apply validates u and stores it, creating the ticket on first use.
func (s *Store) Apply(u Update) (Ticket, error) {
	var title, next, blocked, branch string
	if u.Title != nil {
		title = oneLine(*u.Title)
		if err := tooLong("title", title, MaxTitle); err != nil {
			return Ticket{}, err
		}
	}
	if u.NextStep != nil {
		next = multiLine(*u.NextStep)
		if err := tooLong("next step", next, MaxNextStep); err != nil {
			return Ticket{}, err
		}
	}
	if u.BlockedReason != nil {
		blocked = oneLine(*u.BlockedReason)
		if err := tooLong("blocked reason", blocked, MaxBlocked); err != nil {
			return Ticket{}, err
		}
	}
	if u.Branch != nil {
		branch = oneLine(*u.Branch)
		if err := tooLong("branch", branch, maxRefLen); err != nil {
			return Ticket{}, err
		}
	}
	if u.State != nil && !validStates[*u.State] {
		return Ticket{}, fmt.Errorf("unknown state %q", *u.State)
	}
	var link string
	if u.Link != nil {
		var err error
		if link, err = cleanLink(*u.Link); err != nil {
			return Ticket{}, err
		}
	}
	var deps []string
	if u.BlockedBy != nil {
		var err error
		if deps, err = cleanDeps(*u.BlockedBy); err != nil {
			return Ticket{}, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.entry(u.Repo, u.Ref)
	if err != nil {
		return Ticket{}, err
	}
	if u.BlockedBy != nil { // checked before anything changes: a refused update changes nothing
		if err := s.checkNoCycle(t.ID, deps); err != nil {
			s.prune(t)
			return Ticket{}, err
		}
	}
	if u.Title != nil {
		t.Title = title
	}
	if u.NextStep != nil {
		t.NextStep = next
	}
	if u.BlockedReason != nil {
		t.BlockedReason = blocked
	}
	if u.Branch != nil {
		t.Branch = branch
	}
	if u.BlockedBy != nil {
		t.BlockedBy = deps
	}
	if u.Link != nil {
		t.Link = link
	}
	if u.State != nil {
		t.State = *u.State
		if t.State != StateBlocked && u.BlockedReason == nil {
			t.BlockedReason = "" // an old reason would mislead once it is no longer blocked
		}
	}
	t.Updated = s.now()
	cp := *t
	cp.Discoveries = append([]Discovery(nil), t.Discoveries...)
	s.prune(t)
	return cp, s.save()
}

// cleanLink accepts an empty link or an absolute http(s) address: it ends up in an href.
func cleanLink(in string) (string, error) {
	in = strings.TrimSpace(in)
	if in == "" {
		return "", nil
	}
	if err := tooLong("link", in, MaxLink); err != nil {
		return "", err
	}
	u, err := url.Parse(in)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("a link must be an http(s) address")
	}
	for _, r := range in {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", errors.New("a link cannot contain spaces or control characters")
		}
	}
	return u.String(), nil
}

// cleanDeps validates a dependency list: ids built by ID, no repeats, no more than MaxBlockedBy.
func cleanDeps(in []string) ([]string, error) {
	if len(in) > MaxBlockedBy {
		return nil, fmt.Errorf("a ticket can wait for at most %d others", MaxBlockedBy)
	}
	var out []string
	seen := map[string]bool{}
	for _, id := range in {
		repo, ref, ok := SplitID(id)
		if !ok || repo == "" || !ValidRef(ref) {
			return nil, fmt.Errorf("invalid ticket id %q", id)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// checkNoCycle refuses dependencies that would make a ticket wait, directly or
// through others, for itself. Only stored tickets can have dependencies.
func (s *Store) checkNoCycle(id string, deps []string) error {
	for _, d := range deps {
		if d == id {
			return errors.New("a ticket cannot wait for itself")
		}
	}
	seen := map[string]bool{}
	var reaches func(from string) bool
	reaches = func(from string) bool {
		if from == id {
			return true
		}
		if seen[from] {
			return false
		}
		seen[from] = true
		if t, ok := s.data.Tickets[from]; ok {
			for _, next := range t.BlockedBy {
				if reaches(next) {
					return true
				}
			}
		}
		return false
	}
	for _, d := range deps {
		if reaches(d) {
			return errors.New("that would be a loop: the other ticket already waits for this one")
		}
	}
	return nil
}

func newID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// AddDiscovery records a line that is not part of the ticket, waiting for triage.
func (s *Store) AddDiscovery(repo, ref, text, source string) (Discovery, error) {
	text = oneLine(text)
	if text == "" {
		return Discovery{}, errors.New("a discovery needs some text")
	}
	if err := tooLong("discovery", text, MaxDiscovery); err != nil {
		return Discovery{}, err
	}
	if source != SourceCLI {
		source = SourceHuman
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.entry(repo, ref)
	if err != nil {
		return Discovery{}, err
	}
	for _, d := range t.Discoveries {
		if d.Status == DiscoveryOpen && strings.EqualFold(d.Text, text) {
			return d, nil // the same line twice is one discovery
		}
	}
	open := 0
	for _, d := range t.Discoveries {
		if d.Status == DiscoveryOpen {
			open++
		}
	}
	if open >= MaxDiscoveries {
		return Discovery{}, fmt.Errorf("this ticket already has %d open discoveries: triage some first", MaxDiscoveries)
	}
	d := Discovery{ID: newID(), Text: text, Source: source, Status: DiscoveryOpen, At: s.now()}
	t.Discoveries = append(t.Discoveries, d)
	if len(t.Discoveries) > MaxDiscoveries*2 { // keep the history bounded: forget the oldest decided ones
		t.Discoveries = trimDecided(t.Discoveries)
	}
	t.Updated = s.now()
	return d, s.save()
}

func trimDecided(ds []Discovery) []Discovery {
	out := ds[:0:0]
	drop := len(ds) - MaxDiscoveries*2 + 1
	for _, d := range ds {
		if drop > 0 && d.Status != DiscoveryOpen {
			drop--
			continue
		}
		out = append(out, d)
	}
	return out
}

// Triage decisions on a discovery.
const (
	TriageDrop    = "drop"
	TriageInScope = "in-scope"
	TriageReopen  = "reopen"
	TriagePromote = "promote"
)

// Triage decides what happens to a discovery. Promoting creates a new ticket in
// the same repo, always parked: a discovery never becomes active work by itself.
// It returns the ticket the discovery lives in and, for a promotion, the new one.
func (s *Store) Triage(ticketID, discoveryID, action string) (Ticket, *Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.data.Tickets[ticketID]
	if !ok {
		return Ticket{}, nil, ErrNotFound
	}
	idx := -1
	for i := range t.Discoveries {
		if t.Discoveries[i].ID == discoveryID {
			idx = i
		}
	}
	if idx < 0 {
		return Ticket{}, nil, errors.New("no such discovery")
	}
	d := &t.Discoveries[idx]
	var born *Ticket
	switch action {
	case TriageDrop:
		d.Status = DiscoveryDropped
	case TriageInScope:
		d.Status = DiscoveryInScope
	case TriageReopen:
		d.Status, d.PromotedTo = DiscoveryOpen, ""
	case TriagePromote:
		if d.Status == DiscoveryPromoted {
			return Ticket{}, nil, errors.New("this discovery was already promoted")
		}
		if len(s.data.Tickets) >= MaxTickets {
			return Ticket{}, nil, errors.New("too many tickets")
		}
		now := s.now()
		ref := notePrefix + d.ID
		nt := &Ticket{ID: ID(t.Repo, ref), Repo: t.Repo, Ref: ref, Title: d.Text, State: StateParked, Created: now, Updated: now}
		s.data.Tickets[nt.ID] = nt
		d.Status, d.PromotedTo = DiscoveryPromoted, nt.ID
		cp := *nt
		born = &cp
	default:
		return Ticket{}, nil, fmt.Errorf("unknown triage action %q", action)
	}
	t.Updated = s.now()
	cp := *t
	cp.Discoveries = append([]Discovery(nil), t.Discoveries...)
	return cp, born, s.save()
}

// Dismiss makes a session stop appearing in the attention lists (or brings it back).
func (s *Store) Dismiss(sessionID string, dismissed bool) error {
	if sessionID == "" || len(sessionID) > 128 {
		return errors.New("invalid session id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !dismissed {
		delete(s.data.Dismissed, sessionID)
		return s.save()
	}
	if s.data.Dismissed == nil {
		s.data.Dismissed = map[string]string{}
	}
	if _, already := s.data.Dismissed[sessionID]; !already && len(s.data.Dismissed) >= MaxDismissed {
		oldest, oldestAt := "", ""
		for id, at := range s.data.Dismissed {
			if oldest == "" || at < oldestAt {
				oldest, oldestAt = id, at
			}
		}
		delete(s.data.Dismissed, oldest)
	}
	s.data.Dismissed[sessionID] = s.now().UTC().Format(time.RFC3339)
	return s.save()
}

// Migrated reports whether the old per-worktree stars and notes were already copied in.
func (s *Store) Migrated() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Migrated
}

// MarkMigrated records that the migration ran, so it never runs twice.
func (s *Store) MarkMigrated() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Migrated = true
	return s.save()
}
