package claude

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func TestDeriveState(t *testing.T) {
	ago := func(d time.Duration) time.Time { return t0.Add(-d) }
	tests := []struct {
		name      string
		alive     bool
		raw       string
		phase     Phase
		at        time.Time
		want      State
		heuristic bool
	}{
		// a process is alive
		{"busy right after the prompt: thinking", true, "busy", PhasePrompted, ago(2 * time.Second), StateThinking, false},
		{"busy after a tool finished: thinking", true, "busy", PhaseToolResult, ago(3 * time.Second), StateThinking, false},
		{"busy with a fresh tool request: running it", true, "busy", PhaseToolPending, ago(5 * time.Second), StateRunningTool, false},
		{"busy with a tool request that never completes: maybe waiting for approval", true, "busy", PhaseToolPending, ago(2 * time.Minute), StateNeedsApproval, true},
		{"the turn finished: waiting for you", true, "busy", PhaseDone, ago(time.Minute), StateWaiting, false},
		{"idle and the turn finished: waiting for you", true, "idle", PhaseDone, ago(time.Hour), StateWaiting, false},
		{"idle after an interruption: waiting for you", true, "idle", PhaseInterrupted, ago(time.Minute), StateWaiting, false},
		{"alive, last thing was an API error: failed", true, "idle", PhaseError, ago(time.Minute), StateFailed, false},
		{"alive but no transcript info yet", true, "busy", PhaseNone, time.Time{}, StateThinking, false},
		{"alive with a status we do not know and nothing to go on", true, "mystery", PhaseNone, time.Time{}, StateUnknown, false},
		// no process
		{"no process, the turn finished", false, "", PhaseDone, ago(time.Hour), StateFinished, false},
		{"no process, API error", false, "", PhaseError, ago(time.Hour), StateFailed, false},
		{"no process, interrupted", false, "", PhaseInterrupted, ago(time.Hour), StateInterrupted, false},
		{"no process, closed in the middle of a turn", false, "", PhaseToolPending, ago(time.Hour), StateStopped, false},
		{"no process, nothing known", false, "", PhaseNone, time.Time{}, StateStopped, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, heur := deriveState(tt.alive, tt.raw, tt.phase, tt.at, t0)
			if got != tt.want || heur != tt.heuristic {
				t.Fatalf("got %q heuristic=%v, want %q heuristic=%v", got, heur, tt.want, tt.heuristic)
			}
		})
	}
}

// line builders for transcript fixtures
const ts = `"timestamp":"2026-10-02T11:59:00.000Z"`

func rec(kind, rest string) string {
	return `{"type":"` + kind + `",` + ts + `,"cwd":"/w"` + rest + `}` + "\n"
}

var (
	userText      = func(s string) string { return rec("user", `,"message":{"role":"user","content":"`+s+`"}`) }
	userBlocks    = func(blocks string) string { return rec("user", `,"message":{"role":"user","content":[`+blocks+`]}`) }
	assistantEnd  = rec("assistant", `,"message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"Done."}]}`)
	assistantTool = rec("assistant", `,"message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"Let me run it."},{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}`)
	apiError      = rec("assistant", `,"isApiErrorMessage":true,"message":{"role":"assistant","model":"<synthetic>","stop_reason":"stop_sequence","content":[{"type":"text","text":"API Error: 529 overloaded"}]}`)
	turnDone      = rec("system", `,"subtype":"turn_duration","durationMs":1200`)
	noise         = rec("system", `,"subtype":"informational"`) + rec("system", `,"subtype":"bridge_status"`) + rec("attachment", `,"attachment":{}`)
)

func phaseOf(t *testing.T, content string) Phase {
	t.Helper()
	p := filepath.Join(t.TempDir(), "s.jsonl")
	write(t, p, `{"type":"mode","mode":"normal"}`+"\n"+content)
	info, err := readTranscript(p)
	if err != nil {
		t.Fatal(err)
	}
	return info.Phase
}

func TestTranscriptPhase(t *testing.T) {
	toolResult := `{"type":"tool_result","tool_use_id":"t1","content":"ok"}`
	tests := []struct {
		name    string
		content string
		want    Phase
	}{
		{"a finished turn", userText("hi") + assistantEnd + turnDone, PhaseDone},
		{"an assistant end_turn without the duration record yet", userText("hi") + assistantEnd, PhaseDone},
		{"the user just prompted", assistantEnd + turnDone + userText("do more"), PhasePrompted},
		{"a tool was requested", userText("run it") + assistantTool, PhaseToolPending},
		{"a tool answered", userText("run it") + assistantTool + userBlocks(toolResult), PhaseToolResult},
		{"an API error ended it", userText("hi") + apiError, PhaseError},
		{"the user interrupted (string)", userText("run it") + assistantTool + userText("[Request interrupted by user for tool use]"), PhaseInterrupted},
		{"the user interrupted (blocks)", userText("go") + userBlocks(`{"type":"text","text":"[Request interrupted by user]"}`), PhaseInterrupted},
		{"trailing noise is ignored", userText("hi") + assistantEnd + turnDone + noise, PhaseDone},
		{"noise after a pending tool is ignored too", userText("x") + assistantTool + noise, PhaseToolPending},
		{"nothing meaningful", noise, PhaseNone},
		{"subagent (sidechain) records do not count", userText("hi") + assistantEnd + turnDone +
			rec("assistant", `,"isSidechain":true,"message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","id":"s","name":"X","input":{}}]}`), PhaseDone},
		{"injected meta messages do not count", userText("hi") + assistantEnd + turnDone + rec("user", `,"isMeta":true,"message":{"role":"user","content":"caveat"}`), PhaseDone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := phaseOf(t, tt.content); got != tt.want {
				t.Fatalf("phase = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTranscriptPhaseTimestamp(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	write(t, p, userText("hi")+
		`{"type":"assistant","timestamp":"2026-10-02T11:30:00.000Z","cwd":"/w","message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","id":"t","name":"B","input":{}}]}}`+"\n"+
		noise)
	info, err := readTranscript(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Phase != PhaseToolPending || !info.PhaseAt.Equal(time.Date(2026, 10, 2, 11, 30, 0, 0, time.UTC)) {
		t.Fatalf("the phase time must be that of the record that decided it, not of the noise after: %+v", info)
	}
}

// The whole chain: live process file + transcript -> the state shown to the user.
func TestCLISessionsCarryTheirState(t *testing.T) {
	root := t.TempDir()
	add := func(id, content string) {
		write(t, filepath.Join(root, "projects", "enc", id+".jsonl"), strings.ReplaceAll(content, "/w", `/work`))
	}
	add("thinking", userText("go"))
	add("approval", userText("go")+strings.Replace(assistantTool, "11:59:00", "11:50:00", 1))
	add("waiting", userText("go")+assistantEnd+turnDone)
	add("failed", userText("go")+apiError)
	add("finished", userText("go")+assistantEnd+turnDone)
	write(t, filepath.Join(root, "sessions", "1.json"), `{"pid":1,"sessionId":"thinking","status":"busy"}`)
	write(t, filepath.Join(root, "sessions", "2.json"), `{"pid":2,"sessionId":"approval","status":"busy"}`)
	write(t, filepath.Join(root, "sessions", "3.json"), `{"pid":3,"sessionId":"waiting","status":"idle"}`)
	write(t, filepath.Join(root, "sessions", "4.json"), `{"pid":4,"sessionId":"failed","status":"idle"}`)

	c := NewCLI(root, func(pid int) bool { return pid <= 4 })
	c.Now = func() time.Time { return t0 }
	got, err := c.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Session{}
	for _, s := range got {
		by[s.ID] = s
	}
	want := map[string]State{
		"thinking": StateThinking, "approval": StateNeedsApproval, "waiting": StateWaiting,
		"failed": StateFailed, "finished": StateFinished,
	}
	for id, st := range want {
		if by[id].State != st {
			t.Errorf("%s: state %q, want %q", id, by[id].State, st)
		}
	}
	if !by["approval"].StateHeuristic || by["waiting"].StateHeuristic {
		t.Errorf("only the approval guess is a heuristic: %+v / %+v", by["approval"], by["waiting"])
	}
}

func TestDesktopOnlySessionsHaveAnUnknownState(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "claude-code-sessions", "a", "b", "local_x.json"), `{"sessionId":"local_x","cwd":"/w"}`)
	got, _ := NewDesktop([]string{root}).Sessions()
	if len(got) != 1 || got[0].State != StateUnknown {
		t.Fatalf("a session we cannot read must not pretend: %+v", got)
	}
}
