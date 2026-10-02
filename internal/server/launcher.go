package server

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Launcher opens things on the user's machine. Only the OS-backed
// implementation touches processes; tests use a fake.
type Launcher interface {
	Terminal(dir string) error
	Editor(dir string) error
	Resume(dir, sessionID string) error
	EditorName() string // "cursor", "code" or "" when none is on PATH
}

// OSLauncher launches terminals and editors for the current OS.
//
// Every directory it receives comes from git or a Claude transcript, so it is
// treated as untrusted: it is validated first and, on Windows, never placed on
// a command line that cmd.exe could re-parse.
type OSLauncher struct{}

var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// spec is a process to start. Dir is the working directory (a path travels
// there, not through argument parsing, wherever that is possible).
type spec struct {
	Name       string
	Args       []string
	Dir        string
	NewConsole bool // Windows: give the process its own console window
}

func (OSLauncher) Terminal(dir string) error { return launchTerminal(dir, "") }

func (OSLauncher) Resume(dir, sessionID string) error { return launchTerminal(dir, sessionID) }

func launchTerminal(dir, sessionID string) error {
	if err := validateDir(dir); err != nil {
		return err
	}
	sp, err := terminalCommand(runtime.GOOS, dir, sessionID)
	if err != nil {
		return err
	}
	return start(sp)
}

func (l OSLauncher) Editor(dir string) error {
	if err := validateDir(dir); err != nil {
		return err
	}
	_, path := findEditor()
	if path == "" {
		return badRequest("neither cursor nor code is on PATH")
	}
	sp, err := editorCommand(path, dir)
	if err != nil {
		return err
	}
	return start(sp)
}

func (OSLauncher) EditorName() string {
	name, _ := findEditor()
	return name
}

func findEditor() (name, path string) {
	for _, n := range []string{"cursor", "code"} {
		if p, err := exec.LookPath(n); err == nil {
			return n, p
		}
	}
	return "", ""
}

func start(sp spec) error {
	cmd := exec.Command(sp.Name, sp.Args...)
	cmd.Dir = sp.Dir
	if sp.NewConsole {
		setNewConsole(cmd)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // reap; the terminal outlives us
	return nil
}

// validateDir accepts only an existing, absolute directory without control
// characters.
func validateDir(dir string) error {
	if dir == "" || !filepath.IsAbs(dir) {
		return badRequest(fmt.Sprintf("refusing non-absolute directory %q", dir))
	}
	for _, r := range dir {
		if r < 0x20 || r == 0x7f {
			return badRequest(fmt.Sprintf("refusing directory with control characters: %q", dir))
		}
	}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return badRequest(fmt.Sprintf("not a directory: %q", dir))
	}
	return nil
}

// batchSafe refuses paths with characters cmd.exe treats specially. .cmd/.bat
// launchers (the editors' PATH shims) re-parse their arguments with those
// rules, so an unquoted & or | would start another command.
func batchSafe(dir string) error {
	if i := strings.IndexAny(dir, "&|<>^%\"!\r\n"); i >= 0 {
		return badRequest(fmt.Sprintf("refusing directory with shell metacharacter %q for a batch launcher: %q", dir[i], dir))
	}
	return nil
}

// editorCommand builds the editor process. A real executable receives the
// path as one argument; a .cmd/.bat shim only if the path is batch-safe.
func editorCommand(path, dir string) (spec, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cmd", ".bat":
		if err := batchSafe(dir); err != nil {
			return spec{}, err
		}
	}
	return spec{Name: path, Args: []string{dir}}, nil
}

// terminalCommand builds the process that opens a terminal in dir, optionally
// running `claude --resume <id>`. The id is validated because it ends up in a
// shell command line.
func terminalCommand(goos, dir, resumeID string) (spec, error) {
	if resumeID != "" && !safeID.MatchString(resumeID) {
		return spec{}, badRequest(fmt.Sprintf("refusing unsafe session id %q", resumeID))
	}
	switch goos {
	case "windows":
		// No cmd.exe: the directory is the working directory of the new console.
		sp := spec{Name: "powershell.exe", Args: []string{"-NoExit"}, Dir: dir, NewConsole: true}
		if resumeID != "" {
			sp.Args = append(sp.Args, "-Command", "claude --resume "+resumeID)
		}
		return sp, nil
	case "darwin":
		if resumeID == "" {
			return spec{Name: "open", Args: []string{"-a", "Terminal", dir}}, nil
		}
		script := "cd " + shellQuote(dir) + " && claude --resume " + resumeID
		return spec{Name: "osascript", Args: []string{
			"-e", `tell application "Terminal" to do script "` + appleScriptEscape(script) + `"`,
			"-e", `tell application "Terminal" to activate`}}, nil
	}
	return spec{}, fmt.Errorf("opening a terminal is not supported on %s", goos)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func appleScriptEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}
