package executor

import (
	"context"
	"os/exec"
	"strings"
)

// CommandExecutor interface for testable command execution
type CommandExecutor interface {
	RunCommand(ctx context.Context, name string, args ...string) (stdout, stderr string, err error)
}

// RealCommandExecutor implements CommandExecutor using actual exec
type RealCommandExecutor struct{}

func (r *RealCommandExecutor) RunCommand(ctx context.Context, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var outBuilder, errBuilder strings.Builder
	cmd.Stdout = &outBuilder
	cmd.Stderr = &errBuilder

	err := cmd.Run()
	return outBuilder.String(), errBuilder.String(), err
}
