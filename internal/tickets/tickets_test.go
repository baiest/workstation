package tickets

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func sp(s string) *string { return &s }
func st(s State) *State   { return &s }

func TestKeyOfMatchesTheBranchConventionsWeSupport(t *testing.T) {
	var k Keyer
	for branch, want := range map[string]string{
		"REG-5393-ace-sync-customer-rules": "REG-5393",
		"feature/REG-5413-cycle-warning":   "REG-5413",
		"LOY-96-block-scanner-probes":      "LOY-96",
		"reg-12-lowercase":                 "REG-12",
		"hotfix/AR-6591-poc":               "AR-6591",
		"REG-7":                            "REG-7",
		"main":                             "",
		"feat/max-bid-increment":           "",
		"scrap-services-legacy-end":        "",
		"v2":                               "",
		"":                                 "",
	} {
		if got := k.KeyOf(branch); got != want {
			t.Errorf("KeyOf(%q) = %q, want %q", branch, got, want)
		}
	}
}

func TestRefFallsBackFromKeyToFolderToBranch(t *testing.T) {
	var k Keyer
	cases := []struct{ branch, folder, want string }{
		{"REG-1-a", "x", "REG-1"},
		{"fix/typo", "REG-9-z", "REG-9"}, // the folder carries the key
		{"fix/typo", "typo-wt", "branch:fix/typo"},
		{"", "scratch", "folder:scratch"},
		{"a|b", "", "branch:a_b"}, // the id separator never appears inside a ref
		{"", "", ""},
	}
	for _, c := range cases {
		if got := k.Ref(c.branch, c.folder); got != c.want {
			t.Errorf("Ref(%q,%q) = %q, want %q", c.branch, c.folder, got, c.want)
		}
	}
}

func TestCustomPattern(t *testing.T) {
	k, err := NewKeyer(`(?:^|/)gh-(\d+)`)
	if err != nil {
		t.Fatal(err)
	}
	if got := k.KeyOf("feat/gh-123-fix"); got != "123" {
		t.Errorf("custom key = %q", got)
	}
	for _, bad := range []string{`(`, `no-group`, `(a)(b)`, strings.Repeat("a", 300)} {
		if _, err := NewKeyer(bad); err == nil {
			t.Errorf("pattern %q should be refused", bad)
		}
	}
	if k, err := NewKeyer("  "); err != nil || k.KeyOf("REG-1-x") != "REG-1" {
		t.Errorf("blank means the default: %v", err)
	}
}

func TestIDRoundTripAndIsKey(t *testing.T) {
	id := ID(`C:\r\repo`, "REG-1")
	repo, ref, ok := SplitID(id)
	if !ok || repo != `C:\r\repo` || ref != "REG-1" {
		t.Fatalf("SplitID(%q) = %q %q %v", id, repo, ref, ok)
	}
	for _, bad := range []string{"", "nobar", "|ref", "repo|"} {
		if _, _, ok := SplitID(bad); ok {
			t.Errorf("SplitID(%q) should fail", bad)
		}
	}
	if !IsKey("REG-1") || IsKey("branch:x") || IsKey("note:ab") || IsKey("folder:x") || IsKey("") {
		t.Error("IsKey is wrong")
	}
}

func fixed() func() time.Time {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	return func() time.Time { now = now.Add(time.Minute); return now }
}

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sub", "tickets.json")
	s := New(path)
	s.now = fixed()
	return s, path
}

func TestApplyCreatesUpdatesAndPrunes(t *testing.T) {
	s, path := newStore(t)
	got, err := s.Apply(Update{Repo: "/r", Ref: "REG-1", State: st(StateActive), NextStep: sp("write the\ntests\x00")})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "/r|REG-1" || got.State != StateActive || got.NextStep != "write the\ntests" {
		t.Fatalf("unexpected ticket: %+v", got)
	}

	if _, err := s.Apply(Update{Repo: "/r", Ref: "REG-1", State: st(StateBlocked), BlockedReason: sp("waits for\nAna")}); err != nil {
		t.Fatal(err)
	}
	b, _ := s.Get("/r|REG-1")
	if b.State != StateBlocked || b.BlockedReason != "waits for Ana" || b.NextStep != "write the\ntests" {
		t.Fatalf("partial update must keep the other fields: %+v", b)
	}

	if a, _ := s.Apply(Update{Repo: "/r", Ref: "REG-1", State: st(StateActive)}); a.BlockedReason != "" {
		t.Fatalf("leaving blocked clears the reason: %+v", a)
	}

	if _, err := s.Apply(Update{Repo: "/r", Ref: "REG-1", State: st(StateInbox), NextStep: sp("")}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("/r|REG-1"); ok {
		t.Fatal("a ticket with no decision and no text is not kept")
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("saved: %v", err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Errorf("file mode = %v, want 0600", fi.Mode().Perm())
		}
	}
}

func TestApplyValidation(t *testing.T) {
	s, _ := newStore(t)
	long := strings.Repeat("é", MaxNextStep+1)
	for name, u := range map[string]Update{
		"no repo":       {Ref: "REG-1"},
		"bad ref":       {Repo: "/r", Ref: "a|b"},
		"control ref":   {Repo: "/r", Ref: "a\nb"},
		"bad state":     {Repo: "/r", Ref: "REG-1", State: st("later")},
		"long next":     {Repo: "/r", Ref: "REG-1", NextStep: &long},
		"long title":    {Repo: "/r", Ref: "REG-1", Title: sp(strings.Repeat("t", MaxTitle+1))},
		"long reason":   {Repo: "/r", Ref: "REG-1", BlockedReason: sp(strings.Repeat("r", MaxBlocked+1))},
		"empty (no id)": {},
	} {
		if _, err := s.Apply(u); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if len(s.Snapshot().Tickets) != 0 {
		t.Fatal("a refused update must not leave a ticket behind")
	}
}

func TestStoreIsBounded(t *testing.T) {
	s, _ := newStore(t)
	for i := 0; i < MaxTickets; i++ {
		if _, err := s.Apply(Update{Repo: "/r", Ref: "branch:b" + string(rune('a'+i%26)) + strings.Repeat("x", i/26), State: st(StateParked)}); err != nil {
			t.Fatalf("ticket %d: %v", i, err)
		}
	}
	if _, err := s.Apply(Update{Repo: "/r", Ref: "REG-1", State: st(StateParked)}); err == nil {
		t.Fatal("too many tickets must be refused")
	}
}

func TestReloadKeepsEverythingAndIgnoresGarbage(t *testing.T) {
	s, path := newStore(t)
	if _, err := s.Apply(Update{Repo: "/r", Ref: "REG-1", State: st(StateParked), Title: sp("T")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddDiscovery("/r", "REG-1", "refresh tokens", SourceCLI); err != nil {
		t.Fatal(err)
	}
	if err := s.Dismiss("sess-1", true); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkMigrated(); err != nil {
		t.Fatal(err)
	}

	again := New(path)
	tk, ok := again.Get("/r|REG-1")
	if !ok || tk.State != StateParked || len(tk.Discoveries) != 1 || !again.Migrated() {
		t.Fatalf("reload lost data: %+v migrated=%v", tk, again.Migrated())
	}
	if snap := again.Snapshot(); len(snap.Dismissed) != 1 || snap.Dismissed[0] != "sess-1" {
		t.Fatalf("dismissed = %v", snap.Dismissed)
	}

	for name, body := range map[string]string{"corrupt": "{nope", "null tickets": `{"tickets":null}`, "wrong id": `{"tickets":{"x":{"id":"y"}}}`} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if n := len(New(path).Snapshot().Tickets); n != 0 {
			t.Errorf("%s: expected an empty store, got %d tickets", name, n)
		}
	}
}

func TestDiscoveries(t *testing.T) {
	s, _ := newStore(t)
	d, err := s.AddDiscovery("/r", "REG-1", "  refresh\ntokens expire  ", "weird")
	if err != nil {
		t.Fatal(err)
	}
	if d.Text != "refresh tokens expire" || d.Status != DiscoveryOpen || d.Source != SourceHuman || d.ID == "" {
		t.Fatalf("unexpected discovery: %+v", d)
	}
	if again, _ := s.AddDiscovery("/r", "REG-1", "REFRESH tokens expire", SourceCLI); again.ID != d.ID {
		t.Fatal("the same open line twice is one discovery")
	}
	if _, err := s.AddDiscovery("/r", "REG-1", "   ", SourceCLI); err == nil {
		t.Fatal("empty text must be refused")
	}
	if _, err := s.AddDiscovery("/r", "REG-1", strings.Repeat("x", MaxDiscovery+1), SourceCLI); err == nil {
		t.Fatal("long text must be refused")
	}
	// the ticket exists only because of its discovery, and is still "inbox"
	if tk, ok := s.Get("/r|REG-1"); !ok || tk.State != StateInbox {
		t.Fatalf("%+v %v", tk, ok)
	}
}

func TestTriage(t *testing.T) {
	s, _ := newStore(t)
	a, _ := s.AddDiscovery("/r", "REG-1", "drop me", SourceCLI)
	b, _ := s.AddDiscovery("/r", "REG-1", "keep me", SourceCLI)
	c, _ := s.AddDiscovery("/r", "REG-1", "do me later", SourceCLI)
	id := "/r|REG-1"

	if tk, born, err := s.Triage(id, a.ID, TriageDrop); err != nil || born != nil || tk.Discoveries[0].Status != DiscoveryDropped {
		t.Fatalf("drop: %+v %v %v", tk, born, err)
	}
	if tk, _, err := s.Triage(id, b.ID, TriageInScope); err != nil || tk.Discoveries[1].Status != DiscoveryInScope {
		t.Fatalf("in-scope: %+v %v", tk, err)
	}

	tk, born, err := s.Triage(id, c.ID, TriagePromote)
	if err != nil || born == nil {
		t.Fatalf("promote: %v %v", born, err)
	}
	if born.State != StateParked || born.Title != "do me later" || born.Repo != "/r" || !strings.HasPrefix(born.Ref, "note:") {
		t.Fatalf("a promoted discovery is born parked, never active: %+v", born)
	}
	if tk.Discoveries[2].Status != DiscoveryPromoted || tk.Discoveries[2].PromotedTo != born.ID {
		t.Fatalf("the discovery must point at the new ticket: %+v", tk.Discoveries[2])
	}
	if _, _, err := s.Triage(id, c.ID, TriagePromote); err == nil {
		t.Fatal("promoting twice must be refused")
	}
	if stored, ok := s.Get(born.ID); !ok || stored.State != StateParked {
		t.Fatal("the new ticket is stored")
	}

	if tk, _, err := s.Triage(id, a.ID, TriageReopen); err != nil || tk.Discoveries[0].Status != DiscoveryOpen {
		t.Fatalf("reopen: %v", err)
	}
	if _, _, err := s.Triage(id, "nope", TriageDrop); err == nil {
		t.Fatal("unknown discovery")
	}
	if _, _, err := s.Triage("/r|missing", a.ID, TriageDrop); err != ErrNotFound {
		t.Fatalf("unknown ticket: %v", err)
	}
	if _, _, err := s.Triage(id, a.ID, "explode"); err == nil {
		t.Fatal("unknown action")
	}
}

func TestDismissIsBounded(t *testing.T) {
	s, _ := newStore(t)
	for i := 0; i < MaxDismissed+5; i++ {
		if err := s.Dismiss(strings.Repeat("s", 1+i%50)+string(rune('a'+i%26))+string(rune('a'+i/26%26))+string(rune('a'+i/676)), true); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(s.Snapshot().Dismissed); n > MaxDismissed {
		t.Fatalf("dismissed grew to %d", n)
	}
	if err := s.Dismiss("", true); err == nil {
		t.Fatal("empty id")
	}
	if err := s.Dismiss("only", true); err != nil {
		t.Fatal(err)
	}
	if err := s.Dismiss("only", false); err != nil {
		t.Fatal(err)
	}
	for _, id := range s.Snapshot().Dismissed {
		if id == "only" {
			t.Fatal("undismiss removes it")
		}
	}
}

func TestInMemoryStoreWritesNothing(t *testing.T) {
	s := New("")
	if _, err := s.Apply(Update{Repo: "/r", Ref: "REG-1", State: st(StateActive)}); err != nil {
		t.Fatal(err)
	}
	if len(s.Snapshot().Tickets) != 1 {
		t.Fatal("kept in memory")
	}
}

func ids(refs ...string) *[]string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = ID("/r", r)
	}
	return &out
}

func TestBlockedByIsStoredCleanedAndLimited(t *testing.T) {
	s, _ := newStore(t)
	got, err := s.Apply(Update{Repo: "/r", Ref: "REG-2", State: st(StateBlocked), BlockedBy: ids("REG-1", "REG-1", "REG-3")})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.BlockedBy) != 2 || got.BlockedBy[0] != "/r|REG-1" || got.BlockedBy[1] != "/r|REG-3" {
		t.Fatalf("repeats are one dependency: %v", got.BlockedBy)
	}
	if again, _ := s.Apply(Update{Repo: "/r", Ref: "REG-2", Title: sp("x")}); len(again.BlockedBy) != 2 {
		t.Fatal("an update that does not mention dependencies keeps them")
	}
	if cleared, _ := s.Apply(Update{Repo: "/r", Ref: "REG-2", BlockedBy: &[]string{}}); len(cleared.BlockedBy) != 0 {
		t.Fatal("an empty list clears them")
	}

	many := make([]string, MaxBlockedBy+1)
	for i := range many {
		many[i] = ID("/r", "REG-"+string(rune('a'+i)))
	}
	for name, bad := range map[string][]string{"too many": many, "not an id": {"nope"}, "empty ref": {"/r|"}} {
		if _, err := s.Apply(Update{Repo: "/r", Ref: "REG-2", BlockedBy: &bad}); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestBlockedByRefusesLoops(t *testing.T) {
	s, _ := newStore(t)
	apply := func(ref string, on ...string) error {
		_, err := s.Apply(Update{Repo: "/r", Ref: ref, BlockedBy: ids(on...)})
		return err
	}
	if err := apply("B", "A"); err != nil {
		t.Fatal(err)
	}
	if err := apply("C", "B"); err != nil {
		t.Fatal(err)
	}
	if err := apply("A", "A"); err == nil {
		t.Fatal("waiting for itself")
	}
	if err := apply("A", "C"); err == nil {
		t.Fatal("A waits for C waits for B waits for A: a loop")
	}
	if err := apply("D", "B", "C"); err != nil {
		t.Fatalf("two paths to the same ticket are not a loop: %v", err)
	}
	if _, ok := s.Get("/r|A"); ok {
		t.Fatal("a refused update must not leave a ticket behind")
	}
}

func TestARefusedUpdateChangesNothing(t *testing.T) {
	s, _ := newStore(t)
	_, _ = s.Apply(Update{Repo: "/r", Ref: "B", BlockedBy: ids("A")})
	_, _ = s.Apply(Update{Repo: "/r", Ref: "A", NextStep: sp("keep me")})
	if _, err := s.Apply(Update{Repo: "/r", Ref: "A", NextStep: sp("lost"), BlockedBy: ids("B")}); err == nil {
		t.Fatal("expected a loop")
	}
	if a, _ := s.Get("/r|A"); a.NextStep != "keep me" {
		t.Fatalf("the rest of a refused update must not apply: %+v", a)
	}
}

func TestLinkMustBeAnHTTPAddress(t *testing.T) {
	s, _ := newStore(t)
	got, err := s.Apply(Update{Repo: "/r", Ref: "REG-1", Link: sp("  https://tracker.example.com/browse/REG-1  ")})
	if err != nil || got.Link != "https://tracker.example.com/browse/REG-1" {
		t.Fatalf("%+v %v", got, err)
	}
	for name, bad := range map[string]string{
		"javascript": "javascript:alert(1)",
		"data":       "data:text/html,x",
		"relative":   "/browse/REG-1",
		"no host":    "https:///x",
		"spaces":     "https://x.example.com/a b",
		"too long":   "https://x.example.com/" + strings.Repeat("a", MaxLink),
	} {
		if _, err := s.Apply(Update{Repo: "/r", Ref: "REG-1", Link: sp(bad)}); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if kept, _ := s.Get("/r|REG-1"); kept.Link != "https://tracker.example.com/browse/REG-1" {
		t.Fatalf("a refused link must not replace the stored one: %+v", kept)
	}
	if cleared, _ := s.Apply(Update{Repo: "/r", Ref: "REG-1", Link: sp("")}); cleared.Link != "" {
		t.Fatal("an empty link clears it")
	}
	if _, ok := s.Get("/r|REG-1"); ok {
		t.Fatal("a ticket with nothing left is not kept")
	}
}

func TestHistoryRecordsEachChangeOfState(t *testing.T) {
	s, _ := newStore(t)
	for _, to := range []State{StateActive, StateBlocked, StateBlocked, StateActive, StateDone} {
		if _, err := s.Apply(Update{Repo: "/r", Ref: "REG-1", State: st(to)}); err != nil {
			t.Fatal(err)
		}
	}
	tk, _ := s.Get("/r|REG-1")
	var got []string
	for _, h := range tk.History {
		got = append(got, string(h.From)+">"+string(h.To))
	}
	want := []string{">active", "active>blocked", "blocked>active", "active>done"} // the repeated "blocked" is not a change
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("history = %v, want %v", got, want)
	}
	for i := 1; i < len(tk.History); i++ {
		if !tk.History[i].At.After(tk.History[i-1].At) {
			t.Fatal("transitions are in time order")
		}
	}
	if _, err := s.Apply(Update{Repo: "/r", Ref: "REG-1", NextStep: sp("x")}); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.Get("/r|REG-1"); len(again.History) != 4 {
		t.Fatal("editing text is not a transition")
	}
}

func TestHistoryIsBoundedAndAPromotedTicketStartsParked(t *testing.T) {
	s, _ := newStore(t)
	for i := 0; i < MaxHistory+20; i++ {
		to := StateActive
		if i%2 == 1 {
			to = StateParked
		}
		if _, err := s.Apply(Update{Repo: "/r", Ref: "REG-1", State: st(to)}); err != nil {
			t.Fatal(err)
		}
	}
	if tk, _ := s.Get("/r|REG-1"); len(tk.History) != MaxHistory {
		t.Fatalf("history grew to %d", len(tk.History))
	}

	d, _ := s.AddDiscovery("/r", "REG-2", "later", SourceCLI)
	_, born, err := s.Triage("/r|REG-2", d.ID, TriagePromote)
	if err != nil || born == nil || len(born.History) != 1 || born.History[0].To != StateParked {
		t.Fatalf("%+v %v", born, err)
	}
}
