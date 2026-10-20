//go:build unix

package media

import (
	"os/exec"
	"syscall"
)

func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// A negative pid signals the whole group: ffmpeg and magick may fork.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
