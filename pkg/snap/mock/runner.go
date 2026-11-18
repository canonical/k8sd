package mock

import (
	"context"
	"os/exec"
	"strings"
)

type Runner struct {
	CalledWithCommand []string
	Err               error
}

// Run is a mock implementation of CommandRunner.
func (m *Runner) Run(ctx context.Context, command []string, opts ...func(*exec.Cmd)) error {
	m.CalledWithCommand = append(m.CalledWithCommand, strings.Join(command, " "))
	return m.Err
}
