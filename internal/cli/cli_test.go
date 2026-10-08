package cli

import (
	"bytes"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"workstation/internal/server"
	"workstation/internal/tickets"
	"workstation/internal/workspace"
)

type noLauncher struct{}

func (noLauncher) Desktop() error           { return nil }
func (noLauncher) Terminal(string) error    { return nil }
func (noLauncher) Editor(string) error      { return nil }
func (noLauncher) Resume(_, _ string) error { return nil }
func (noLauncher) Prompt(_, _ string) error { return nil }
func (noLauncher) EditorName() string       { return "" }

// start runs the real server on a loopback port with one repo and one worktree.
func start(t *testing.T) (addr string, store *tickets.Store, worktree string) {
	t.Helper()
	worktree = t.TempDir()
	ws := workspace.Workspace{Repos: []workspace.Repo{{
		Name: "r", Path: "/r",
		Worktrees: []workspace.Worktree{{Name: "wt", Path: worktree, Branch: "feature/REG-5-thing", Ticket: "REG-5"}},
	}}}
	store = tickets.New("")
	h := server.New(func() (workspace.Workspace, error) { return ws, nil }, noLauncher{}, fstest.MapFS{}, server.WithTickets(store, tickets.Keyer{}))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback listener:", err)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return ln.Addr().String(), store, worktree
}

func env(addr, worktree string) (Env, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	return Env{
		Stdout: &out, Stderr: &errb, Cwd: worktree + "/sub",
		Getenv:   func(string) string { return "" },
		Client:   http.DefaultClient,
		TopLevel: func(string) (string, error) { return worktree, nil },
	}, &out, &errb
}

func TestDiscoverNextAndTicketThroughTheServer(t *testing.T) {
	addr, store, wt := start(t)

	e, out, errb := env(addr, wt)
	if code := Run([]string{"discover", "-addr", addr, "refresh", "tokens", "never", "expire"}, e); code != 0 {
		t.Fatalf("discover: %d %s", code, errb)
	}
	if !strings.Contains(out.String(), "REG-5") || !strings.Contains(out.String(), "do not fix") {
		t.Fatalf("discover should say where it went and that it is not for fixing: %q", out)
	}

	e, out, errb = env(addr, wt)
	if code := Run([]string{"next", "-addr", addr, "write the migration"}, e); code != 0 {
		t.Fatalf("next: %d %s", code, errb)
	}

	tk, ok := store.Get("/r|REG-5")
	if !ok || tk.NextStep != "write the migration" || len(tk.Discoveries) != 1 || tk.Discoveries[0].Source != "cli" || tk.Discoveries[0].Text != "refresh tokens never expire" {
		t.Fatalf("stored ticket: %+v ok=%v", tk, ok)
	}

	e, out, errb = env(addr, wt)
	if code := Run([]string{"ticket", "-addr", addr}, e); code != 0 {
		t.Fatalf("ticket: %d %s", code, errb)
	}
	for _, want := range []string{"ticket: REG-5", "state:  inbox", "branch: feature/REG-5-thing", "write the migration", "refresh tokens never expire"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("ticket output lacks %q:\n%s", want, out)
		}
	}
}

func TestTheAddressCanComeFromTheEnvironment(t *testing.T) {
	addr, _, wt := start(t)
	e, _, errb := env(addr, wt)
	e.Getenv = func(k string) string {
		if k == "WORKSTATION_ADDR" {
			return addr
		}
		return ""
	}
	if code := Run([]string{"ticket"}, e); code != 0 {
		t.Fatalf("%d %s", code, errb)
	}
}

func TestRefusesWhatItShouldNotDo(t *testing.T) {
	addr, store, wt := start(t)
	cases := map[string][]string{
		"no command":        {},
		"unknown command":   {"explode"},
		"no text":           {"discover", "-addr", addr},
		"text for ticket":   {"ticket", "-addr", addr, "hello"},
		"not a loopback":    {"discover", "-addr", "10.0.0.5:7420", "x"},
		"not host and port": {"next", "-addr", "nope", "x"},
		"bad flag":          {"next", "-nope", "x"},
	}
	for name, args := range cases {
		e, _, errb := env(addr, wt)
		if code := Run(args, e); code == 0 {
			t.Errorf("%s: expected a failure, stderr=%q", name, errb)
		}
	}
	if n := len(store.Snapshot().Tickets); n != 0 {
		t.Fatalf("refused commands stored %d tickets", n)
	}
}

func TestOutsideAGitWorktree(t *testing.T) {
	addr, _, wt := start(t)
	e, _, errb := env(addr, wt)
	e.TopLevel = func(string) (string, error) { return "", errors.New("not a git repository") }
	if code := Run([]string{"ticket", "-addr", addr}, e); code != 1 || !strings.Contains(errb.String(), "not inside a git worktree") {
		t.Fatalf("%d %q", code, errb)
	}
}

func TestAWorktreeTheServerDoesNotKnow(t *testing.T) {
	addr, store, _ := start(t)
	e, _, errb := env(addr, t.TempDir())
	if code := Run([]string{"discover", "-addr", addr, "x"}, e); code != 1 || !strings.Contains(errb.String(), "unknown worktree") {
		t.Fatalf("%d %q", code, errb)
	}
	if len(store.Snapshot().Tickets) != 0 {
		t.Fatal("nothing may be stored for an unknown worktree")
	}
}

func TestServerNotRunning(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens there now
	e, _, errb := env(addr, t.TempDir())
	if code := Run([]string{"ticket", "-addr", addr}, e); code != 1 || !strings.Contains(errb.String(), "is it running") {
		t.Fatalf("%d %q", code, errb)
	}
}

func TestLabelPrefersTheKey(t *testing.T) {
	for _, c := range []struct {
		in   ticketReply
		want string
	}{
		{ticketReply{Ref: "REG-1", Title: "T"}, "REG-1"},
		{ticketReply{Ref: "branch:fix/typo", Title: "Typo"}, "Typo"},
		{ticketReply{Ref: "branch:fix/typo", Branch: "fix/typo"}, "fix/typo"},
		{ticketReply{Ref: "branch:fix/typo"}, "fix/typo"},
	} {
		if got := label(c.in); got != c.want {
			t.Errorf("label(%+v) = %q, want %q", c.in, got, c.want)
		}
	}
}
