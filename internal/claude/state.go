package claude

import (
	"encoding/json"
	"strings"
	"time"
)

// State is what a session is doing right now, as shown to the user. It combines
// the live process file (is it running? busy?) with the last meaningful record
// of its transcript. Only needs-approval is a guess, and says so.
type State string

const (
	StateThinking      State = "thinking"       // running: the model is working on its answer
	StateRunningTool   State = "running-tool"   // running: a tool was requested a moment ago
	StateNeedsApproval State = "needs-approval" // HEURISTIC: a tool request got no result for a while
	StateWaiting       State = "waiting"        // running and the turn finished: it is your move
	StateFailed        State = "failed"         // the last thing recorded was an API error
	StateInterrupted   State = "interrupted"    // you stopped it, and it is not running
	StateFinished      State = "finished"       // not running; the last turn completed
	StateStopped       State = "stopped"        // not running, closed mid-turn or nothing known
	StateUnknown       State = "unknown"
)

// Phase is where the transcript ends, from its last meaningful record.
type Phase string

const (
	PhaseNone        Phase = ""
	PhasePrompted    Phase = "prompted"     // the user message (or model reasoning) is last: a reply is due
	PhaseToolPending Phase = "tool-pending" // the model asked for a tool; no result recorded yet
	PhaseToolResult  Phase = "tool-result"  // a tool answered; the model continues
	PhaseDone        Phase = "done"         // the turn finished
	PhaseError       Phase = "error"        // an API error message
	PhaseInterrupted Phase = "interrupted"  // "[Request interrupted by user...]"
)

// approvalAfter: a tool request with no result for this long is probably waiting
// for the user's approval (or is a slow tool: we cannot tell, hence the guess).
const approvalAfter = 30 * time.Second

// deriveState maps the live process + transcript phase to the state shown. The
// bool says the state is a heuristic rather than something recorded.
func deriveState(alive bool, raw string, phase Phase, at, now time.Time) (State, bool) {
	if !alive {
		switch phase {
		case PhaseError:
			return StateFailed, false
		case PhaseInterrupted:
			return StateInterrupted, false
		case PhaseDone:
			return StateFinished, false
		}
		return StateStopped, false
	}

	switch phase {
	case PhaseError:
		return StateFailed, false
	case PhaseDone, PhaseInterrupted:
		return StateWaiting, false
	case PhaseToolPending:
		if !at.IsZero() && now.Sub(at) > approvalAfter {
			return StateNeedsApproval, true
		}
		return StateRunningTool, false
	case PhasePrompted, PhaseToolResult:
		return StateThinking, false
	}

	// no usable transcript record: fall back to what the process file says
	switch raw {
	case "busy":
		return StateThinking, false
	case "idle":
		return StateWaiting, false
	}
	return StateUnknown, false
}

type block struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// blocksOf returns the content blocks of a message; a plain string becomes one text block.
func blocksOf(raw json.RawMessage) []block {
	var blocks []block
	if json.Unmarshal(raw, &blocks) == nil {
		return blocks
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []block{{Type: "text", Text: s}}
	}
	return nil
}

// recordPhase classifies one transcript record. ok is false for records that say
// nothing about the conversation state (noise, subagents, injected context).
func recordPhase(r record) (Phase, bool) {
	if r.IsSidechain || r.IsMeta {
		return PhaseNone, false
	}
	switch r.Type {
	case "system":
		if r.Subtype == "turn_duration" {
			return PhaseDone, true
		}
	case "assistant":
		return assistantPhase(r), true
	case "user":
		return userPhase(r), true
	}
	return PhaseNone, false
}

func assistantPhase(r record) Phase {
	if r.IsApiError {
		return PhaseError
	}
	if r.Message == nil {
		return PhaseDone
	}
	onlyThinking := true
	for _, b := range blocksOf(r.Message.Content) {
		if b.Type == "tool_use" {
			return PhaseToolPending
		}
		if b.Type != "thinking" && b.Type != "redacted_thinking" {
			onlyThinking = false
		}
	}
	if onlyThinking && r.Message.StopReason != "end_turn" {
		return PhasePrompted // still reasoning
	}
	return PhaseDone
}

func userPhase(r record) Phase {
	if r.Message == nil {
		return PhasePrompted
	}
	for _, b := range blocksOf(r.Message.Content) {
		switch {
		case b.Type == "tool_result":
			return PhaseToolResult
		case b.Type == "text" && strings.HasPrefix(strings.TrimSpace(b.Text), "[Request interrupted by user"):
			return PhaseInterrupted
		}
	}
	return PhasePrompted
}
