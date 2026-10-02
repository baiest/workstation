//go:build !windows

package claude

import (
	"errors"
	"syscall"
)

// PidAlive reports whether a process with this pid is currently running.
func PidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
