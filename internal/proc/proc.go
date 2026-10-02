// Package proc makes a timed-out child process take its descendants with it.
//
// exec.CommandContext only kills the process it started. git and gh spawn
// helpers (shells, credential helpers, ssh), which would otherwise outlive the
// timeout, keep running and hold the working directory open.
package proc

import "os/exec"

// KillTreeOnCancel makes cancelling cmd's context kill the whole process tree.
// Call it after exec.CommandContext and before Start.
func KillTreeOnCancel(cmd *exec.Cmd) {
	prepare(cmd)
	cmd.Cancel = func() error { return killTree(cmd) }
}
