package platform

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

type CommandRunner struct {
	Timeout time.Duration
}

type CommandResult struct {
	Command  string
	Args     []string
	Stdout   string
	Stderr   string
	ExitCode int
}

func (r CommandRunner) Run(ctx context.Context, command string, args ...string) (CommandResult, error) {
	if strings.Contains(command, ".sh") || command == "sh" || command == "bash" {
		return CommandResult{}, errors.New("SHELL_COMMAND_FORBIDDEN")
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := CommandResult{
		Command: command,
		Args:    append([]string(nil), args...),
		Stdout:  strings.TrimSpace(stdout.String()),
		Stderr:  strings.TrimSpace(stderr.String()),
	}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, err
	}
	return result, nil
}
