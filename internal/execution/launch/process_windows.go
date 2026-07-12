//go:build windows

package launch

import (
	"fmt"
	"os/exec"
	"strconv"
)

func configureRunnerProcess(_ *exec.Cmd) {}

func terminateRunnerProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run(); err != nil {
		if killErr := cmd.Process.Kill(); killErr != nil {
			return fmt.Errorf("terminate runner process tree: %w; terminate process: %v", err, killErr)
		}
	}
	return nil
}
