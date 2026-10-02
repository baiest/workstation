package server

import (
	"errors"
	"fmt"
	"os/exec"
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
type OSLauncher struct{}

var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func (OSLauncher) Terminal(dir string) error { return start(terminalArgv(runtime.GOOS, dir, "")) }

func (OSLauncher) Resume(dir, sessionID string) error {
	return start(terminalArgv(runtime.GOOS, dir, sessionID))
}

func (l OSLauncher) Editor(dir string) error {
	name := l.EditorName()
	if name == "" {
		return errors.New("neither cursor nor code is on PATH")
	}
	return start([]string{name, dir}, nil)
}

func (OSLauncher) EditorName() string {
	for _, name := range []string{"cursor", "code"} {
		if _, err := exec.LookPath(name); err == nil {
			return name
		}
	}
	return ""
}

func start(argv []string, err error) error {
	if err != nil {
		return err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // reap; the terminal outlives us
	return nil
}

// terminalArgv builds the command that opens a terminal in dir, optionally
// running `claude --resume <id>`. The id is validated because it ends up in a
// shell command line.
func terminalArgv(goos, dir, resumeID string) ([]string, error) {
	if resumeID != "" && !safeID.MatchString(resumeID) {
		return nil, fmt.Errorf("refusing unsafe session id %q", resumeID)
	}
	switch goos {
	case "windows":
		argv := []string{"cmd", "/c", "start", "", "/D", dir, "powershell", "-NoExit"}
		if resumeID != "" {
			argv = append(argv, "-Command", "claude --resume "+resumeID)
		}
		return argv, nil
	case "darwin":
		if resumeID == "" {
			return []string{"open", "-a", "Terminal", dir}, nil
		}
		script := "cd " + shellQuote(dir) + " && claude --resume " + resumeID
		return []string{"osascript",
			"-e", `tell application "Terminal" to do script "` + appleScriptEscape(script) + `"`,
			"-e", `tell application "Terminal" to activate`}, nil
	}
	return nil, fmt.Errorf("opening a terminal is not supported on %s", goos)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func appleScriptEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}
