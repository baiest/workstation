package claude

import "syscall"

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
)

// PidAlive reports whether a process with this pid is currently running.
func PidAlive(pid int) bool {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)

	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
