//go:build !unix

package media

import "os/exec"

func setProcessGroup(*exec.Cmd) {}
