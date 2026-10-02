package proc

import (
	"os/exec"
	"strconv"
)

func prepare(*exec.Cmd) {}

// killTree uses taskkill /T to end the process and its descendants.
func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run(); err != nil {
		return cmd.Process.Kill() // taskkill unavailable or raced: at least stop the child itself
	}
	return nil
}
