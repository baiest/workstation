package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"workstation/internal/tickets"
	"workstation/internal/workspace"
)

// TicketStore keeps the human side of tickets (implemented by tickets.Store).
type TicketStore interface {
	Snapshot() tickets.Snapshot
	Get(id string) (tickets.Ticket, bool)
	Apply(u tickets.Update) (tickets.Ticket, error)
	AddDiscovery(repo, ref, text, source string) (tickets.Discovery, error)
	Triage(ticketID, discoveryID, action string) (tickets.Ticket, *tickets.Ticket, error)
	Dismiss(sessionID string, dismissed bool) error
	Dismissed(sessionID string) bool
	Migrated() bool
	MarkMigrated() error
}

// WithTicketLink sets the address template that turns a ticket key into a link to the user's tracker.
func WithTicketLink(template string) Option { return func(o *options) { o.linkTemplate = template } }

// WithTickets enables /api/tickets. The keyer names the ticket of branches the
// workspace does not carry (the branch graph).
func WithTickets(t TicketStore, k tickets.Keyer) Option {
	return func(o *options) { o.tickets, o.keyer = t, k }
}

type ticketsResponse struct {
	tickets.Snapshot
	Config struct {
		WIPLimit int `json:"wipLimit"`
		// LinkTemplate turns a ticket key into a link to the user's tracker; "" when none is configured.
		LinkTemplate string `json:"linkTemplate"`
	} `json:"config"`
}

func (s *Server) handleTicketsGet(w http.ResponseWriter, r *http.Request) {
	if s.tickets == nil {
		http.NotFound(w, r)
		return
	}
	resp := ticketsResponse{Snapshot: s.tickets.Snapshot()}
	resp.Config.WIPLimit = tickets.DefaultWIPLimit()
	resp.Config.LinkTemplate = s.linkTemplate
	writeJSON(w, resp)
}

// ticketTarget says which ticket a request is about: a worktree of the current
// snapshot (what the command line sends), or a repo plus a ref (what the page
// sends). A repo must be one the workspace knows; the store never grows from
// arbitrary paths.
type ticketTarget struct {
	Worktree string `json:"worktree"`
	Repo     string `json:"repo"`
	Ref      string `json:"ref"`
}

func resolveTarget(ws workspace.Workspace, t ticketTarget) (repo, ref string, err error) {
	if t.Worktree != "" {
		for _, r := range ws.Repos {
			for _, w := range r.Worktrees {
				if workspace.SamePath(w.Path, t.Worktree) {
					if w.Ticket == "" {
						return "", "", badRequest("this worktree has no branch to name a ticket after")
					}
					return r.Path, w.Ticket, nil
				}
			}
		}
		return "", "", badRequest("unknown worktree path")
	}
	if _, ok := findRepo(ws, t.Repo); !ok {
		return "", "", badRequest("unknown repo")
	}
	if !tickets.ValidRef(t.Ref) {
		return "", "", badRequest("invalid ticket reference")
	}
	return t.Repo, t.Ref, nil
}

// decode reads a small JSON body and loads the snapshot; ok is false after replying.
func (s *Server) decode(w http.ResponseWriter, r *http.Request, route string, v any) (workspace.Workspace, bool) {
	if s.tickets == nil {
		http.NotFound(w, r)
		return workspace.Workspace{}, false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(v); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return workspace.Workspace{}, false
	}
	ws, err := s.snapshot()
	if err != nil {
		internalError(w, route, err)
		return workspace.Workspace{}, false
	}
	return ws, true
}

// replyTicket answers a store call: limits and validation are written for the user.
func replyTicket(w http.ResponseWriter, t tickets.Ticket, err error) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, t)
}

// currentTicket is what a session needs to know when it starts: which ticket the
// worktree it works in belongs to, and what was decided about it.
type currentTicket struct {
	Ref           string              `json:"ref"`
	Repo          string              `json:"repo"`
	Branch        string              `json:"branch"`
	State         string              `json:"state"`
	Title         string              `json:"title,omitempty"`
	NextStep      string              `json:"nextStep,omitempty"`
	BlockedReason string              `json:"blockedReason,omitempty"`
	Discoveries   []tickets.Discovery `json:"discoveries"` // the open ones
}

func (s *Server) handleTicketCurrent(w http.ResponseWriter, r *http.Request) {
	if s.tickets == nil {
		http.NotFound(w, r)
		return
	}
	ws, err := s.snapshot()
	if err != nil {
		internalError(w, "ticket", err)
		return
	}
	repo, ref, err := resolveTarget(ws, ticketTarget{Worktree: r.URL.Query().Get("worktree")})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	out := currentTicket{Ref: ref, Repo: repo, State: "inbox", Discoveries: []tickets.Discovery{}}
	for _, rp := range ws.Repos {
		for _, wt := range rp.Worktrees {
			if rp.Path == repo && wt.Ticket == ref && out.Branch == "" {
				out.Branch = wt.Branch
			}
		}
	}
	if t, ok := s.tickets.Get(tickets.ID(repo, ref)); ok {
		out.Title, out.NextStep, out.BlockedReason = t.Title, t.NextStep, t.BlockedReason
		if t.State != tickets.StateInbox {
			out.State = string(t.State)
		}
		for _, d := range t.Discoveries {
			if d.Status == tickets.DiscoveryOpen {
				out.Discoveries = append(out.Discoveries, d)
			}
		}
	}
	writeJSON(w, out)
}

type ticketUpdateRequest struct {
	ticketTarget
	Title         *string        `json:"title"`
	State         *tickets.State `json:"state"`
	BlockedReason *string        `json:"blockedReason"`
	NextStep      *string        `json:"nextStep"`
	BlockedBy     *[]string      `json:"blockedBy"`
	Link          *string        `json:"link"`
	Branch        *string        `json:"branch"`
}

func (s *Server) handleTicketsSet(w http.ResponseWriter, r *http.Request) {
	var req ticketUpdateRequest
	ws, ok := s.decode(w, r, "tickets", &req)
	if !ok {
		return
	}
	repo, ref, err := resolveTarget(ws, req.ticketTarget)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.BlockedBy != nil {
		for _, id := range *req.BlockedBy {
			if r, _, ok := tickets.SplitID(id); !ok {
				http.Error(w, "invalid ticket id", http.StatusBadRequest)
				return
			} else if _, known := findRepo(ws, r); !known {
				http.Error(w, "unknown repo in a dependency", http.StatusBadRequest)
				return
			}
		}
	}
	t, err := s.tickets.Apply(tickets.Update{
		Repo: repo, Ref: ref, Title: req.Title, State: req.State,
		BlockedReason: req.BlockedReason, BlockedBy: req.BlockedBy, NextStep: req.NextStep, Link: req.Link, Branch: req.Branch,
	})
	replyTicket(w, t, err)
}

type discoveryRequest struct {
	ticketTarget
	Text   string `json:"text"`
	Source string `json:"source"`
}

func (s *Server) handleDiscoveryAdd(w http.ResponseWriter, r *http.Request) {
	var req discoveryRequest
	ws, ok := s.decode(w, r, "discoveries", &req)
	if !ok {
		return
	}
	repo, ref, err := resolveTarget(ws, req.ticketTarget)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := s.tickets.AddDiscovery(repo, ref, req.Text, req.Source); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	t, _ := s.tickets.Get(tickets.ID(repo, ref))
	writeJSON(w, t)
}

type triageRequest struct {
	ID          string `json:"id"`
	DiscoveryID string `json:"discoveryId"`
	Action      string `json:"action"`
}

func (s *Server) handleDiscoveryTriage(w http.ResponseWriter, r *http.Request) {
	var req triageRequest
	if _, ok := s.decode(w, r, "triage", &req); !ok {
		return
	}
	t, _, err := s.tickets.Triage(req.ID, req.DiscoveryID, req.Action)
	if errors.Is(err, tickets.ErrNotFound) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	replyTicket(w, t, err)
}

type dismissRequest struct {
	SessionID string `json:"sessionId"`
	Dismissed bool   `json:"dismissed"`
}

func (s *Server) handleDismiss(w http.ResponseWriter, r *http.Request) {
	var req dismissRequest
	ws, ok := s.decode(w, r, "dismiss", &req)
	if !ok {
		return
	}
	if req.Dismissed {
		if _, known := findSession(ws, req.SessionID); !known {
			http.Error(w, "unknown session", http.StatusBadRequest)
			return
		}
	}
	if err := s.tickets.Dismiss(req.SessionID, req.Dismissed); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// harvestDiscoveries turns the DISCOVERY: lines that chats wrote (when they could
// not run `workstation discover`) into discoveries of the ticket of the worktree
// they work in. They only ever land as open: a human triages them like any other.
// Lines already known, decided or not, are skipped, so a dropped one stays dropped.
func (s *Server) harvestDiscoveries(ws workspace.Workspace) {
	if s.tickets == nil {
		return
	}
	for _, r := range ws.Repos {
		for _, w := range r.Worktrees {
			if w.IsMain || w.Ticket == "" {
				continue
			}
			for _, sess := range w.Sessions {
				if len(sess.Discoveries) == 0 || s.tickets.Dismissed(sess.ID) {
					continue
				}
				for _, text := range sess.Discoveries {
					if _, err := s.tickets.AddDiscovery(r.Path, w.Ticket, text, tickets.SourceTranscript); err != nil {
						break // a full ticket: the human has to triage before more come in
					}
				}
			}
		}
	}
}

// migrateNotes copies the old per-worktree stars and notes into tickets, once: a
// star becomes "active", a note becomes the next step. It never overwrites what
// a ticket already says. Notes of worktrees that no longer exist are dropped.
func (s *Server) migrateNotes(ws workspace.Workspace) {
	if s.tickets == nil || s.notes == nil || s.tickets.Migrated() || len(ws.Repos) == 0 {
		return
	}
	old := s.notes.All()
	for _, r := range ws.Repos {
		for _, w := range r.Worktrees {
			n, ok := old[w.Path]
			if !ok || w.IsMain || w.Ticket == "" || (!n.Starred && n.Text == "") {
				continue
			}
			cur, _ := s.tickets.Get(tickets.ID(r.Path, w.Ticket))
			u := tickets.Update{Repo: r.Path, Ref: w.Ticket}
			if n.Starred && cur.State == tickets.StateInbox {
				active := tickets.StateActive
				u.State = &active
			}
			if n.Text != "" && cur.NextStep == "" {
				text := n.Text
				u.NextStep = &text
			}
			if u.State == nil && u.NextStep == nil {
				continue
			}
			if _, err := s.tickets.Apply(u); err != nil {
				return // try again on the next refresh rather than lose a note
			}
		}
	}
	_ = s.tickets.MarkMigrated()
}
