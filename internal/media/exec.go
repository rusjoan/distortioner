package media

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// CommandError is returned when an external tool exits unsuccessfully.
type CommandError struct {
	Tool   string
	Err    error
	Stderr string
}

func (e *CommandError) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("%s: %v", e.Tool, e.Err)
	}
	return fmt.Sprintf("%s: %v: %s", e.Tool, e.Err, e.Stderr)
}

func (e *CommandError) Unwrap() error { return e.Err }

const stderrTail = 1024

// ToolObserver receives every run of an external tool: its name, whether it
// succeeded, wall time and CPU time (user + system).
type ToolObserver func(tool string, ok bool, wall, cpu time.Duration)

var toolObserver atomic.Pointer[ToolObserver]

// SetToolObserver installs the observer, e.g. for metrics.
func SetToolObserver(o ToolObserver) { toolObserver.Store(&o) }

func observeTool(cmd *exec.Cmd, start time.Time, err error) {
	o := toolObserver.Load()
	if o == nil {
		return
	}
	var cpu time.Duration
	if ps := cmd.ProcessState; ps != nil {
		cpu = ps.UserTime() + ps.SystemTime()
	}
	(*o)(filepath.Base(cmd.Path), err == nil, time.Since(start), cpu)
}

// run executes a tool in its own process group so that cancelling ctx kills
// it together with any children, and returns stdout.
func run(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	setProcessGroup(cmd)
	cmd.WaitDelay = 5 * time.Second
	if env != nil {
		cmd.Env = env
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	observeTool(cmd, start, err)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > stderrTail {
			msg = "…" + msg[len(msg)-stderrTail:]
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) || errors.Is(err, exec.ErrNotFound) {
			return nil, &CommandError{Tool: name, Err: err, Stderr: msg}
		}
		return nil, &CommandError{Tool: name, Err: err}
	}
	return stdout.Bytes(), nil
}

// runProgress runs ffmpeg like run and reports how far it got: ffmpeg
// writes its position to stdout (-progress), compared with the expected
// output length in seconds. onProgress gets permille, at most 999 until
// ffmpeg exits.
func runProgress(ctx context.Context, name string, args []string, expected float64, onProgress func(done, total int)) error {
	if onProgress == nil || expected <= 0 {
		_, err := run(ctx, nil, name, args...)
		return err
	}
	cmd := exec.CommandContext(ctx, name, append([]string{"-progress", "pipe:1", "-nostats"}, args...)...)
	setProcessGroup(cmd)
	cmd.WaitDelay = 5 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return &CommandError{Tool: name, Err: err}
	}
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if us, ok := strings.CutPrefix(scanner.Text(), "out_time_us="); ok {
			if v, err := strconv.ParseInt(us, 10, 64); err == nil && v > 0 {
				onProgress(min(int(float64(v)/1e6/expected*1000), 999), 1000)
			}
		}
	}
	err = cmd.Wait()
	observeTool(cmd, start, err)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > stderrTail {
			msg = "…" + msg[len(msg)-stderrTail:]
		}
		return &CommandError{Tool: name, Err: err, Stderr: msg}
	}
	onProgress(1000, 1000)
	return nil
}
