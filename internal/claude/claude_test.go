package claude

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

const transcript = `{"type":"last-prompt","leafUuid":"x","sessionId":"s1"}
{"type":"mode","mode":"normal","sessionId":"s1"}
{"type":"attachment","timestamp":"2026-10-02T00:43:52.368Z","cwd":"C:\\repo\\wt","gitBranch":"LOY-1-feat","slug":"cool-plan-name","sessionId":"s1"}
{"type":"user","timestamp":"2026-10-02T01:00:00.000Z","cwd":"C:\\repo\\wt","message":{"role":"user","content":"do it"}}
{"type":"assistant","timestamp":"2026-10-02T01:00:05.000Z","cwd":"C:\\repo\\wt","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash"},{"type":"text","text":"First answer"}]}}
{"type":"user","timestamp":"2026-10-02T01:00:06.000Z","cwd":"C:\\repo\\wt","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}
{"type":"assistant","timestamp":"2026-10-02T01:00:09.000Z","cwd":"C:\\repo\\wt","message":{"role":"assistant","content":[{"type":"text","text":"Done.\nAll tests pass."}]}}
{"type":"system","subtype":"turn_duration","timestamp":"2026-10-02T01:00:10.000Z"}
`

func TestReadTranscript(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s1.jsonl")
	write(t, p, transcript)

	got, err := readTranscript(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cwd != `C:\repo\wt` || got.Branch != "LOY-1-feat" {
		t.Fatalf("cwd/branch wrong: %+v", got)
	}
	if !got.LastActivity.Equal(mustTime(t, "2026-10-02T01:00:10Z")) {
		t.Fatalf("last activity = %v", got.LastActivity)
	}
	if got.LastMessage != "Done. All tests pass." {
		t.Fatalf("last message = %q", got.LastMessage)
	}
	if got.Slug != "cool-plan-name" {
		t.Fatalf("slug = %q", got.Slug)
	}
}

func TestReadTranscriptSlugOnlyInTail(t *testing.T) {
	filler := `{"type":"user","timestamp":"2026-10-02T00:50:00.000Z","cwd":"/w","message":{"role":"user","content":"` +
		strings.Repeat("x", 4000) + `"}}` + "\n"
	content := strings.Repeat(filler, 60) +
		`{"type":"assistant","timestamp":"2026-10-02T03:00:00.000Z","cwd":"/w","slug":"late-slug","message":{"role":"assistant","content":[]}}` + "\n"
	p := filepath.Join(t.TempDir(), "s.jsonl")
	write(t, p, content)

	got, err := readTranscript(p)
	if err != nil || got.Slug != "late-slug" {
		t.Fatalf("slug = %q err=%v", got.Slug, err)
	}
}

func TestReadPlan(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "plans", "cool-plan-name.md"), "# Plan\n\n- step")
	c := NewCLI(root, func(int) bool { return false })

	p, err := c.ReadPlan("cool-plan-name")
	if err != nil || p.Markdown != "# Plan\n\n- step" || p.Slug != "cool-plan-name" ||
		p.ModifiedAt.IsZero() || p.Path != filepath.Join(root, "plans", "cool-plan-name.md") {
		t.Fatalf("got %+v err=%v", p, err)
	}

	for _, bad := range []string{"", "../x", `..\x`, "a/b", "UPPER", "a.b"} {
		if _, err := c.ReadPlan(bad); err == nil {
			t.Errorf("slug %q must be rejected", bad)
		}
	}
	if _, err := c.ReadPlan("missing-plan"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing plan should wrap ErrNotExist, got %v", err)
	}

	write(t, filepath.Join(root, "plans", "huge.md"), strings.Repeat("x", maxPlanBytes+1))
	if _, err := c.ReadPlan("huge"); err == nil {
		t.Error("oversized plan must be rejected")
	}
}

func TestReadTranscriptLargeFileUsesTail(t *testing.T) {
	filler := `{"type":"user","timestamp":"2026-10-02T00:50:00.000Z","message":{"role":"user","content":"` +
		strings.Repeat("x", 4000) + `"}}` + "\n"
	content := transcript[:strings.Index(transcript, `{"type":"user"`)] + // head with cwd
		strings.Repeat(filler, 40) + // > 64KB of middle
		`{"type":"assistant","timestamp":"2026-10-02T03:00:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":"late"}]}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-10-02T03:00:01.000Z","mess` // truncated, still being written

	p := filepath.Join(t.TempDir(), "s1.jsonl")
	write(t, p, content)

	got, err := readTranscript(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cwd != `C:\repo\wt` {
		t.Fatalf("cwd = %q", got.Cwd)
	}
	if !got.LastActivity.Equal(mustTime(t, "2026-10-02T03:00:00Z")) || got.LastMessage != "late" {
		t.Fatalf("tail not used: %+v", got)
	}
}

func TestReadTranscriptCwdOnlyInTail(t *testing.T) {
	filler := `{"type":"user","timestamp":"2026-10-02T00:50:00.000Z","message":{"role":"user","content":"` +
		strings.Repeat("x", 4000) + `"}}` + "\n"
	// 60 head lines without cwd, so only the tail can provide it
	content := strings.Repeat(filler, 60) +
		`{"type":"assistant","timestamp":"2026-10-02T03:00:00.000Z","cwd":"/tail/cwd","message":{"role":"assistant","content":[{"type":"text","text":"x"}]}}` + "\n"
	p := filepath.Join(t.TempDir(), "s.jsonl")
	write(t, p, content)

	got, err := readTranscript(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cwd != "/tail/cwd" {
		t.Fatalf("cwd = %q", got.Cwd)
	}
}

func setupCLI(t *testing.T) (root string) {
	t.Helper()
	root = t.TempDir()
	write(t, filepath.Join(root, "projects", "enc-a", "s-busy.jsonl"), strings.ReplaceAll(transcript, "s1", "s-busy"))
	write(t, filepath.Join(root, "projects", "enc-a", "s-idle.jsonl"), transcript)
	write(t, filepath.Join(root, "projects", "enc-a", "s-weird.jsonl"), transcript)
	write(t, filepath.Join(root, "projects", "enc-a", "s-dead.jsonl"), strings.ReplaceAll(transcript, "cool-plan-name", "ghost-plan"))
	write(t, filepath.Join(root, "projects", "enc-a", "s-none.jsonl"), strings.ReplaceAll(transcript, `"slug":"cool-plan-name",`, ""))
	write(t, filepath.Join(root, "projects", "enc-a", "s-evil.jsonl"), strings.ReplaceAll(transcript, "cool-plan-name", "../evil"))
	write(t, filepath.Join(root, "plans", "cool-plan-name.md"), "# plan")
	write(t, filepath.Join(root, "evil.md"), "# not a plan")
	write(t, filepath.Join(root, "projects", "enc-a", "no-cwd.jsonl"), `{"type":"mode"}`+"\n")
	write(t, filepath.Join(root, "projects", "enc-a", "notes.txt"), "ignored")

	write(t, filepath.Join(root, "sessions", "100.json"), `{"pid":100,"sessionId":"s-busy","cwd":"C:\\repo\\wt","status":"busy","name":"my-task"}`)
	write(t, filepath.Join(root, "sessions", "101.json"), `{"pid":101,"sessionId":"s-idle","status":"idle"}`)
	write(t, filepath.Join(root, "sessions", "102.json"), `{"pid":102,"sessionId":"s-weird","status":"compacting"}`)
	write(t, filepath.Join(root, "sessions", "103.json"), `{"pid":103,"sessionId":"s-dead","status":"busy"}`)
	write(t, filepath.Join(root, "sessions", "104.key"), "not json")
	write(t, filepath.Join(root, "sessions", "bad.json"), "{broken")
	return root
}

func TestCLISessions(t *testing.T) {
	root := setupCLI(t)
	alive := func(pid int) bool { return pid != 103 }

	got, err := NewCLI(root, alive).Sessions()
	if err != nil {
		t.Fatal(err)
	}

	byID := map[string]Session{}
	for _, s := range got {
		byID[s.ID] = s
	}
	if len(byID) != 6 {
		t.Fatalf("expected 6 sessions (no-cwd and non-jsonl skipped), got %d: %+v", len(byID), byID)
	}
	for id, want := range map[string]bool{"s-busy": true, "s-idle": true, "s-dead": false, "s-none": false, "s-evil": false} {
		if byID[id].HasPlan != want {
			t.Errorf("%s: HasPlan = %v, want %v (slug %q)", id, byID[id].HasPlan, want, byID[id].Slug)
		}
	}
	if byID["s-busy"].Slug != "cool-plan-name" {
		t.Errorf("slug not exposed: %+v", byID["s-busy"])
	}

	cases := []struct {
		id     string
		status Status
		raw    string
	}{
		{"s-busy", StatusWorking, "busy"},
		{"s-idle", StatusIdle, "idle"},
		{"s-weird", StatusUnknown, "compacting"},
		{"s-dead", StatusStopped, ""},
		{"s-none", StatusStopped, ""},
	}
	for _, c := range cases {
		s := byID[c.id]
		if s.Status != c.status || s.RawStatus != c.raw {
			t.Errorf("%s: status=%q raw=%q, want %q %q", c.id, s.Status, s.RawStatus, c.status, c.raw)
		}
		if s.Source != SourceCLI || s.Cwd != `C:\repo\wt` || s.Branch != "LOY-1-feat" {
			t.Errorf("%s: bad base fields %+v", c.id, s)
		}
	}
	if byID["s-busy"].Title != "my-task" {
		t.Errorf("title should come from live session name, got %q", byID["s-busy"].Title)
	}
}

func TestCLISessionsMissingRoot(t *testing.T) {
	got, err := NewCLI(filepath.Join(t.TempDir(), "nope"), func(int) bool { return true }).Sessions()
	if err != nil || len(got) != 0 {
		t.Fatalf("missing root must be empty, got %v %v", got, err)
	}
}

func TestDesktopSessions(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "claude-code-sessions", "acct", "org")
	write(t, filepath.Join(dir, "local_a.json"), `{"sessionId":"local_a","cliSessionId":"cli-1","cwd":"C:\\repo\\.claude\\worktrees\\x","originCwd":"C:\\repo","sourceBranch":"main","createdAt":1774216429521,"lastActivityAt":1774216904688,"isArchived":false,"title":"Add checks"}`)
	write(t, filepath.Join(dir, "local_b.json"), `{"sessionId":"local_b","cwd":"C:\\x","isArchived":true,"title":"old"}`)
	write(t, filepath.Join(dir, "scheduled-tasks.json"), `{"tasks":[]}`)
	write(t, filepath.Join(dir, "local_bad.json"), `{nope`)

	got, err := NewDesktop([]string{root, filepath.Join(root, "missing")}).Sessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 (archived/non-session/bad skipped), got %+v", got)
	}
	s := got[0]
	if s.ID != "cli-1" || s.DesktopID != "local_a" || s.Title != "Add checks" || s.Source != SourceDesktop ||
		s.OriginCwd != `C:\repo` || s.Cwd != `C:\repo\.claude\worktrees\x` || s.Branch != "main" || s.Status != StatusUnknown {
		t.Fatalf("bad desktop session: %+v", s)
	}
	if !s.LastActivity.Equal(time.UnixMilli(1774216904688)) {
		t.Fatalf("last activity = %v", s.LastActivity)
	}
}

func TestDesktopSessionWithoutCLIID(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "claude-code-sessions", "a", "b", "local_c.json"), `{"sessionId":"local_c","cwd":"/w","lastActivityAt":1}`)
	got, err := NewDesktop([]string{root}).Sessions()
	if err != nil || len(got) != 1 || got[0].ID != "local_c" || got[0].ResumeID() != "" {
		t.Fatalf("got %+v err=%v", got, err)
	}
}

type stubProvider struct {
	s   []Session
	err error
}

func (p stubProvider) Sessions() ([]Session, error) { return p.s, p.err }

func TestCombined(t *testing.T) {
	cli := stubProvider{s: []Session{{ID: "a", Source: SourceCLI}}}
	desk := stubProvider{s: []Session{{ID: "a", DesktopID: "local_a", Title: "T", Source: SourceDesktop}}}

	got, err := Combined{CLI: cli, Desktop: desk}.Sessions()
	if err != nil || len(got) != 1 || got[0].Title != "T" {
		t.Fatalf("got %+v err=%v", got, err)
	}

	// a failing provider must not hide the other one's data
	got, err = Combined{CLI: cli, Desktop: stubProvider{err: os.ErrPermission}}.Sessions()
	if err == nil || len(got) != 1 {
		t.Fatalf("expected data plus error, got %+v err=%v", got, err)
	}
}

func TestMerge(t *testing.T) {
	cli := []Session{
		{ID: "cli-1", Source: SourceCLI, Cwd: "/w", Status: StatusWorking, LastActivity: time.UnixMilli(2000), LastMessage: "hi"},
		{ID: "cli-2", Source: SourceCLI, Cwd: "/w2", Status: StatusStopped},
	}
	desk := []Session{
		{ID: "cli-1", DesktopID: "local_a", Source: SourceDesktop, Title: "Titled", OriginCwd: "/origin", Cwd: "/w", LastActivity: time.UnixMilli(1000)},
		{ID: "cli-9", DesktopID: "local_z", Source: SourceDesktop, Title: "Only desktop", Status: StatusUnknown},
	}

	got := Merge(cli, desk)

	byID := map[string]Session{}
	for _, s := range got {
		byID[s.ID] = s
	}
	if len(got) != 3 {
		t.Fatalf("expected 3, got %+v", got)
	}
	m := byID["cli-1"]
	if m.Title != "Titled" || m.Source != SourceDesktop || m.DesktopID != "local_a" || m.OriginCwd != "/origin" ||
		m.Status != StatusWorking || m.LastMessage != "hi" || !m.LastActivity.Equal(time.UnixMilli(2000)) {
		t.Fatalf("merge lost data: %+v", m)
	}
	if byID["cli-2"].Source != SourceCLI || byID["cli-9"].Title != "Only desktop" {
		t.Fatalf("unmerged sessions altered: %+v", got)
	}
}
