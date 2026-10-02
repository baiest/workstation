package server

import (
	"os/exec"
	"syscall"
)

// createNewConsole is CREATE_NEW_CONSOLE from the Win32 CreateProcess flags;
// package syscall does not export it.
const createNewConsole = 0x00000010

// setNewConsole gives the child its own console window (instead of cmd.exe's
// `start`, whose command line would re-parse the directory).
func setNewConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewConsole}
}
