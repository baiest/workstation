package claude

import (
	"os"
	"path/filepath"
	"runtime"
)

// DefaultCLIRoot is the Claude Code config dir: $CLAUDE_CONFIG_DIR or ~/.claude.
func DefaultCLIRoot() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// DesktopRoots lists candidate Claude Desktop data dirs for this OS. Missing
// ones are harmless. Only the Windows MSIX path was verified on a real machine.
func DesktopRoots() []string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		roots := []string{filepath.Join(os.Getenv("APPDATA"), "Claude")}
		msix, _ := filepath.Glob(filepath.Join(os.Getenv("LOCALAPPDATA"), "Packages", "Claude_*", "LocalCache", "Roaming", "Claude"))
		return append(roots, msix...)
	case "darwin":
		return []string{filepath.Join(home, "Library", "Application Support", "Claude")}
	default:
		return []string{filepath.Join(home, ".config", "Claude")}
	}
}
