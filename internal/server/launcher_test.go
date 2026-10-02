package server

import (
	"reflect"
	"testing"
)

func TestTerminalArgv(t *testing.T) {
	tests := []struct {
		name     string
		goos     string
		dir      string
		resumeID string
		want     []string
	}{
		{"windows plain", "windows", `C:\my repo`, "",
			[]string{"cmd", "/c", "start", "", "/D", `C:\my repo`, "powershell", "-NoExit"}},
		{"windows resume", "windows", `C:\r`, "abc-123",
			[]string{"cmd", "/c", "start", "", "/D", `C:\r`, "powershell", "-NoExit", "-Command", "claude --resume abc-123"}},
		{"darwin plain", "darwin", "/Users/me/repo", "",
			[]string{"open", "-a", "Terminal", "/Users/me/repo"}},
		{"darwin resume quotes dir", "darwin", "/Users/me/it's", "abc-123",
			[]string{"osascript",
				"-e", `tell application "Terminal" to do script "cd '/Users/me/it'\\''s' && claude --resume abc-123"`,
				"-e", `tell application "Terminal" to activate`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := terminalArgv(tt.goos, tt.dir, tt.resumeID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestTerminalArgvRejects(t *testing.T) {
	for _, id := range []string{"a b", "a;rm -rf", "$(x)", "a&b", "`x`", "a\"b"} {
		if _, err := terminalArgv("windows", `C:\r`, id); err == nil {
			t.Errorf("session id %q must be rejected", id)
		}
	}
	if _, err := terminalArgv("plan9", "/x", ""); err == nil {
		t.Error("unsupported OS must error")
	}
}
