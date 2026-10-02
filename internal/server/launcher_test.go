package server

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestTerminalCommand(t *testing.T) {
	tests := []struct {
		name     string
		goos     string
		dir      string
		resumeID string
		want     spec
	}{
		{"windows plain", "windows", `C:\my repo`, "",
			spec{Name: "powershell.exe", Args: []string{"-NoExit"}, Dir: `C:\my repo`, NewConsole: true}},
		{"windows resume", "windows", `C:\r`, "abc-123",
			spec{Name: "powershell.exe", Args: []string{"-NoExit", "-Command", "claude --resume abc-123"}, Dir: `C:\r`, NewConsole: true}},
		{"darwin plain", "darwin", "/Users/me/repo", "",
			spec{Name: "open", Args: []string{"-a", "Terminal", "/Users/me/repo"}}},
		{"darwin resume quotes dir", "darwin", "/Users/me/it's", "abc-123",
			spec{Name: "osascript", Args: []string{
				"-e", `tell application "Terminal" to do script "cd '/Users/me/it'\\''s' && claude --resume abc-123"`,
				"-e", `tell application "Terminal" to activate`}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := terminalCommand(tt.goos, tt.dir, tt.resumeID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

// The directory is untrusted (git worktree paths, transcript cwd). On Windows
// it must travel as the process working directory, never as an argument that a
// shell could re-parse: cmd.exe treats an unquoted & as a command separator.
func TestWindowsTerminalNeverPutsTheDirOnACommandLine(t *testing.T) {
	for _, dir := range []string{`C:\dev\a&calc`, `C:\dev\a|b`, `C:\dev\%PATH%`, `C:\dev\a^b`, `C:\dev\(x)`} {
		for _, id := range []string{"", "abc-123"} {
			got, err := terminalCommand("windows", dir, id)
			if err != nil {
				t.Fatal(err)
			}
			if got.Dir != dir {
				t.Errorf("dir must be the working directory, got %q", got.Dir)
			}
			if strings.EqualFold(got.Name, "cmd") || strings.EqualFold(got.Name, "cmd.exe") {
				t.Errorf("cmd.exe must not be involved: %+v", got)
			}
			for _, a := range got.Args {
				if strings.Contains(a, dir) {
					t.Errorf("dir leaked into an argument: %q", a)
				}
			}
		}
	}
}

// Real process, hostile directory: PowerShell must start with the directory
// as its working directory, and no second command may run.
func TestStartInHostileDirectoryOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only: exercises CreateProcess with a working directory")
	}
	root := t.TempDir()
	hostile := filepath.Join(root, "a&echo-injected")
	if err := os.Mkdir(hostile, 0o755); err != nil {
		t.Fatal(err)
	}
	where := filepath.Join(root, "where.txt")
	injected := filepath.Join(hostile, "echo-injected")

	sp, err := terminalCommand("windows", hostile, "")
	if err != nil {
		t.Fatal(err)
	}
	// same Dir/NewConsole as the real action, but a command that exits
	sp.Args = []string{"-NoProfile", "-Command", "(Get-Location).ProviderPath | Set-Content -LiteralPath '" + where + "'"}
	if err := start(sp); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(where); err == nil && len(data) > 0 {
			got := strings.TrimSpace(string(data))
			// compare identity, not text: Windows may report the same directory in its 8.3 short form
			gotInfo, err1 := os.Stat(got)
			wantInfo, err2 := os.Stat(hostile)
			if err1 != nil || err2 != nil || !os.SameFile(gotInfo, wantInfo) {
				t.Fatalf("working directory = %q, want %q", got, hostile)
			}
			if _, err := os.Stat(injected); err == nil {
				t.Fatal("something was executed from the directory name")
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("powershell did not report its working directory in time")
}

func TestTerminalCommandRejects(t *testing.T) {
	for _, id := range []string{"a b", "a;rm -rf", "$(x)", "a&b", "`x`", "a\"b", "a\nb"} {
		if _, err := terminalCommand("windows", `C:\r`, id); err == nil {
			t.Errorf("session id %q must be rejected", id)
		}
	}
	if _, err := terminalCommand("plan9", "/x", ""); err == nil {
		t.Error("unsupported OS must error")
	}
}

func TestValidateDir(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := validateDir(dir); err != nil {
		t.Errorf("a real directory must pass: %v", err)
	}
	for name, bad := range map[string]string{
		"empty":     "",
		"relative":  "some/rel",
		"missing":   filepath.Join(dir, "nope"),
		"file":      file,
		"newline":   dir + "\nx",
		"carriage":  dir + "\rx",
		"nul":       dir + "\x00x",
		"escape":    dir + "\x1bx",
		"delete":    dir + "\x7fx",
		"dot-relat": ".." + string(filepath.Separator) + "x",
	} {
		if err := validateDir(bad); err == nil {
			t.Errorf("%s: %q must be rejected", name, bad)
		}
	}
}

func TestBatchSafe(t *testing.T) {
	for _, ok := range []string{`C:\Users\me\repo`, `C:\Program Files (x86)\a-b_c.d`, `C:\dev\proyecto ñandú`, `/Users/me/it's`} {
		if err := batchSafe(ok); err != nil {
			t.Errorf("%q should be allowed: %v", ok, err)
		}
	}
	for _, bad := range []string{`C:\a&calc`, `C:\a|b`, `C:\a<b`, `C:\a>b`, `C:\a^b`, `C:\%PATH%`, `C:\a"b`, "C:\\a\nb", `C:\a!b`} {
		if err := batchSafe(bad); err == nil {
			t.Errorf("%q must be rejected for batch launchers", bad)
		}
	}
}

// .cmd/.bat shims re-parse their arguments with cmd.exe rules, so a hostile
// directory must be refused there; a real executable is not affected.
func TestEditorCommand(t *testing.T) {
	hostile := `C:\dev\a&calc`

	for _, shim := range []string{`C:\x\cursor.cmd`, `C:\x\code.CMD`, `C:\x\run.bat`} {
		if _, err := editorCommand(shim, hostile); err == nil {
			t.Errorf("%s must refuse %q", shim, hostile)
		}
		got, err := editorCommand(shim, `C:\dev\fine`)
		if err != nil || got.Name != shim || !reflect.DeepEqual(got.Args, []string{`C:\dev\fine`}) {
			t.Errorf("%s with a safe dir: %+v %v", shim, got, err)
		}
	}

	got, err := editorCommand(`C:\x\Cursor.exe`, hostile)
	if err != nil || got.Name != `C:\x\Cursor.exe` || !reflect.DeepEqual(got.Args, []string{hostile}) {
		t.Errorf("a real .exe takes any valid path as one argument: %+v %v", got, err)
	}
}
