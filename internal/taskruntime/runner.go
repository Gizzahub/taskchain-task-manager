package taskruntime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CommandRequest is one external command. Args are passed as an array.
// ProcessGroup runs the command in its own process group so cancelling the
// context stops its descendants too. run-finish sets it on the integration
// check. The integration run does not: that command must not be stopped.
type CommandRequest struct {
	Command      string
	Args         []string
	WorkDir      string
	Env          map[string]string
	ProcessGroup bool
}

// CommandResult separates stdout from stderr and records whether the
// process never started.
type CommandResult struct {
	Stdout     string
	Stderr     string
	ExitCode   int
	Command    string
	Args       []string
	WorkDir    string
	Executable string
	Error      string
	NotStarted bool
}

// CommandRunner executes an external command.
type CommandRunner interface {
	Run(context.Context, CommandRequest) (CommandResult, error)
}

// Executor is the process runner.
type Executor struct{}

func NewExecutor() *Executor { return &Executor{} }

func (e *Executor) Run(ctx context.Context, req CommandRequest) (CommandResult, error) {
	result := CommandResult{Command: req.Command, Args: append([]string(nil), req.Args...), WorkDir: req.WorkDir}
	joined := strings.Join(append([]string{req.Command}, req.Args...), " ")
	if err := ctx.Err(); err != nil {
		result.ExitCode, result.Error, result.NotStarted = 125, err.Error(), true
		return result, fmt.Errorf("command %s cancelled before start: %w", joined, err)
	}
	executable, err := exec.LookPath(req.Command)
	if err != nil {
		result.ExitCode, result.Error, result.NotStarted = 127, err.Error(), true
		return result, fmt.Errorf("resolve command %s: %w", req.Command, err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		result.ExitCode, result.Error, result.NotStarted = 127, err.Error(), true
		return result, fmt.Errorf("resolve command %s path: %w", req.Command, err)
	}
	result.Executable = executable
	cmd := exec.CommandContext(ctx, executable, req.Args...)
	cmd.Dir = req.WorkDir
	cmd.Env = os.Environ()
	for key, value := range req.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if req.ProcessGroup {
		prepareProcessGroup(cmd)
		if err = cmd.Start(); err == nil {
			stop := forwardSignals(cmd.Process.Pid)
			err = cmd.Wait()
			stop()
		}
	} else {
		err = cmd.Run()
	}
	result.Stdout, result.Stderr = stdout.String(), stderr.String()
	if err == nil {
		return result, nil
	}
	if ctx.Err() != nil {
		result.Error = ctx.Err().Error()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode, result.NotStarted = 125, cmd.Process == nil
		}
		return result, fmt.Errorf("command %s cancelled: %w", joined, ctx.Err())
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	result.ExitCode, result.Error, result.NotStarted = 127, err.Error(), true
	return result, fmt.Errorf("start command %s: %w", req.Command, err)
}
