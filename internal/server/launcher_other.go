//go:build !windows

package server

import "os/exec"

// setNewConsole is a no-op outside Windows.
func setNewConsole(*exec.Cmd) {}
