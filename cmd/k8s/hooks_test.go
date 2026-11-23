package k8s

import (
	"bytes"
	"errors"
	"testing"

	cmdutil "github.com/canonical/k8sd/cmd/util"
	"github.com/canonical/k8sd/pkg/snap/mock"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
)

func TestHookInitializeFormatter(t *testing.T) {
	g := NewWithT(t)
	stderr := &bytes.Buffer{}
	var exitCode int
	env := cmdutil.ExecutionEnvironment{
		Stderr: stderr,
		Exit: func(code int) {
			exitCode = code
		},
	}
	format := "invalid-format"
	hook := hookInitializeFormatter(env, &format)
	cmd := &cobra.Command{}
	cmd.SetErr(stderr)

	hook(cmd, nil)

	g.Expect(exitCode).To(Equal(1))
	g.Expect(stderr.String()).To(HaveSuffix("\n"))
	g.Expect(stderr.String()).To(Equal("Error: Unknown --output-format \"invalid-format\". It must be one of \"plain\" (default), \"json\" or \"yaml\".\n"))
}

func TestHookRequireRoot(t *testing.T) {
	g := NewWithT(t)
	stderr := &bytes.Buffer{}
	var exitCode int
	env := cmdutil.ExecutionEnvironment{
		Stderr: stderr,
		Getuid: func() int { return 1000 },
		Exit: func(code int) {
			exitCode = code
		},
	}
	hook := hookRequireRoot(env)
	cmd := &cobra.Command{}
	cmd.SetErr(stderr)

	hook(cmd, nil)

	g.Expect(exitCode).To(Equal(1))
	g.Expect(stderr.String()).To(HaveSuffix("\n"))
	g.Expect(stderr.String()).To(Equal("You do not have enough permissions. Please re-run the command with sudo.\n"))
}

func TestHookCheckLXDStderrNewline(t *testing.T) {
	g := NewWithT(t)
	stderr := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetErr(stderr)

	env := cmdutil.ExecutionEnvironment{
		Stderr: stderr,
	}
	hook := hookCheckLXDWith(env, func() (bool, error) {
		return true, errors.New("permission denied")
	})
	hook(cmd, nil)

	g.Expect(stderr.String()).To(Equal("Failed to check if running inside LXD container: permission denied\n"))
}

func TestHookCheckLXDStrictSkipped(t *testing.T) {
	g := NewWithT(t)
	stderr := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetErr(stderr)

	mockSnap := &mock.Snap{
		Mock: mock.Mock{Strict: true},
	}
	env := cmdutil.ExecutionEnvironment{
		Stderr: stderr,
		Snap:   mockSnap,
	}
	checked := false
	hook := hookCheckLXDWith(env, func() (bool, error) {
		checked = true
		return true, nil
	})
	hook(cmd, nil)

	g.Expect(checked).To(BeFalse())
	g.Expect(stderr.String()).To(BeEmpty())
}
