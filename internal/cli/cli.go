// Package cli is the command line that Claude (or you) runs from inside a
// worktree to talk to the running workstation: note a discovery, leave the next
// step, read what was decided about the ticket. It resolves the current
// directory to its worktree with git and sends only that path; the server
// decides which ticket it is, so there is one rule for that, and it never
// writes the ticket file itself.
package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"workstation/internal/gitx"
	"workstation/internal/server"
)

// Commands lists the subcommands, for the main program to dispatch on.
var Commands = []string{"discover", "next", "ticket"}

// IsCommand reports whether name is one of the subcommands.
func IsCommand(name string) bool {
	for _, c := range Commands {
		if c == name {
			return true
		}
	}
	return false
}

// DefaultAddr is where workstation listens unless told otherwise.
const DefaultAddr = "127.0.0.1:7420"

const maxReply = 1 << 20

// Env is what a command needs from the outside, so tests can replace it.
type Env struct {
	Stdout, Stderr io.Writer
	Cwd            string
	Getenv         func(string) string
	Client         *http.Client
	TopLevel       func(dir string) (string, error)
}

// DefaultEnv is the real process environment.
func DefaultEnv() Env {
	cwd, _ := os.Getwd()
	return Env{
		Stdout: os.Stdout, Stderr: os.Stderr, Cwd: cwd, Getenv: os.Getenv,
		Client:   &http.Client{Timeout: 15 * time.Second},
		TopLevel: gitx.TopLevel,
	}
}

const usage = `usage:
  workstation discover "text"   note something that came up and is NOT part of this ticket (do not fix it)
  workstation next "text"       leave the next step for whoever continues this ticket
  workstation ticket            print the ticket of this worktree: state, next step, open discoveries

flags (before the text): -addr host:port   where workstation listens (default ` + DefaultAddr + `, or $WORKSTATION_ADDR)
`

// Run executes one subcommand and returns the process exit code.
func Run(args []string, env Env) int {
	if len(args) == 0 || !IsCommand(args[0]) {
		fmt.Fprint(env.Stderr, usage)
		return 2
	}
	cmd := args[0]

	fs := flag.NewFlagSet("workstation "+cmd, flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	defaultAddr := DefaultAddr
	if v := env.Getenv("WORKSTATION_ADDR"); v != "" {
		defaultAddr = v
	}
	addr := fs.String("addr", defaultAddr, "address workstation listens on (loopback only)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	text := strings.TrimSpace(strings.Join(fs.Args(), " "))

	if err := server.RequireLoopback(*addr); err != nil {
		fmt.Fprintln(env.Stderr, "workstation:", err)
		return 2
	}
	if (cmd == "discover" || cmd == "next") && text == "" {
		fmt.Fprintf(env.Stderr, "workstation %s: some text is needed\n", cmd)
		return 2
	}
	if cmd == "ticket" && text != "" {
		fmt.Fprintln(env.Stderr, "workstation ticket: takes no text")
		return 2
	}

	worktree, err := env.TopLevel(env.Cwd)
	if err != nil {
		fmt.Fprintf(env.Stderr, "workstation: %s is not inside a git worktree\n", env.Cwd)
		return 1
	}
	c := client{base: "http://" + *addr, http: env.Client}

	switch cmd {
	case "discover":
		err = c.discover(worktree, text, env.Stdout)
	case "next":
		err = c.next(worktree, text, env.Stdout)
	case "ticket":
		err = c.show(worktree, env.Stdout)
	}
	if err != nil {
		fmt.Fprintln(env.Stderr, "workstation:", err)
		return 1
	}
	return 0
}

type client struct {
	base string
	http *http.Client
}

// ticketReply is the part of the server's answer the commands print.
type ticketReply struct {
	Ref           string `json:"ref"`
	Title         string `json:"title"`
	State         string `json:"state"`
	Branch        string `json:"branch"`
	NextStep      string `json:"nextStep"`
	BlockedReason string `json:"blockedReason"`
	Discoveries   []struct {
		ID     string `json:"id"`
		Text   string `json:"text"`
		Status string `json:"status"`
	} `json:"discoveries"`
}

func (c client) do(req *http.Request) ([]byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return nil, fmt.Errorf("cannot reach workstation at %s: is it running? (%v)", c.base, ue.Err)
		}
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxReply))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("this workstation has no ticket support (is it an old version?)")
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s", strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (c client) post(path string, v any) ([]byte, error) {
	b, _ := json.Marshal(v)
	req, err := http.NewRequest(http.MethodPost, c.base+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req)
}

func (c client) discover(worktree, text string, out io.Writer) error {
	body, err := c.post("/api/tickets/discoveries", map[string]any{"worktree": worktree, "text": text, "source": "cli"})
	if err != nil {
		return err
	}
	var t ticketReply
	_ = json.Unmarshal(body, &t)
	fmt.Fprintf(out, "noted as a discovery of %s: %s\nIt is not part of this ticket: do not fix it now. The human decides later.\n", label(t), text)
	return nil
}

func (c client) next(worktree, text string, out io.Writer) error {
	body, err := c.post("/api/tickets", map[string]any{"worktree": worktree, "nextStep": text})
	if err != nil {
		return err
	}
	var t ticketReply
	_ = json.Unmarshal(body, &t)
	fmt.Fprintf(out, "next step of %s saved\n", label(t))
	return nil
}

func (c client) show(worktree string, out io.Writer) error {
	req, err := http.NewRequest(http.MethodGet, c.base+"/api/tickets/current?worktree="+url.QueryEscape(worktree), nil)
	if err != nil {
		return err
	}
	body, err := c.do(req)
	if err != nil {
		return err
	}
	var t ticketReply
	if err := json.Unmarshal(body, &t); err != nil {
		return fmt.Errorf("unexpected answer from workstation: %w", err)
	}
	fmt.Fprintf(out, "ticket: %s\nstate:  %s\n", label(t), t.State)
	if t.Branch != "" {
		fmt.Fprintf(out, "branch: %s\n", t.Branch)
	}
	if t.BlockedReason != "" {
		fmt.Fprintf(out, "blocked: %s\n", t.BlockedReason)
	}
	if t.NextStep != "" {
		fmt.Fprintf(out, "next step:\n  %s\n", strings.ReplaceAll(t.NextStep, "\n", "\n  "))
	} else {
		fmt.Fprintln(out, "next step: (none written yet)")
	}
	if len(t.Discoveries) > 0 {
		fmt.Fprintln(out, "open discoveries (not part of this ticket):")
		for _, d := range t.Discoveries {
			fmt.Fprintf(out, "  - %s\n", d.Text)
		}
	}
	return nil
}

// label is the ticket's key, or its title / branch when it has no key.
func label(t ticketReply) string {
	switch {
	case t.Ref != "" && !strings.Contains(t.Ref, ":"):
		return t.Ref
	case t.Title != "":
		return t.Title
	case t.Branch != "":
		return t.Branch
	}
	return strings.TrimPrefix(t.Ref, "branch:")
}
