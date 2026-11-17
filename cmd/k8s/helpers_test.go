package k8s_test

import (
	"bytes"

	"github.com/canonical/k8sd/cmd/k8s"
	cmdutil "github.com/canonical/k8sd/cmd/util"
	"github.com/canonical/k8sd/pkg/client/k8sd"
	k8sdmock "github.com/canonical/k8sd/pkg/client/k8sd/mock"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
)

type exitPanic int

type mockSnapWrapper struct {
	*snapmock.Snap
	k8sdClientErr error
}

func (m *mockSnapWrapper) K8sdClient(address string) (k8sd.Client, error) {
	if m.k8sdClientErr != nil {
		return nil, m.k8sdClientErr
	}
	return m.Snap.K8sdClient(address)
}

type testCLI struct {
	Stdout        *bytes.Buffer
	Stderr        *bytes.Buffer
	Stdin         *bytes.Buffer
	MockClient    *k8sdmock.Mock
	MockSnap      *snapmock.Snap
	SnapWrapper   *mockSnapWrapper
	Env           cmdutil.ExecutionEnvironment
	K8sdClientErr error
}

func newTestCLI() *testCLI {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	stdin := &bytes.Buffer{}
	mockClient := &k8sdmock.Mock{
		NodeStatusInitialized: true,
	}
	mockSnap := &snapmock.Snap{
		Mock: snapmock.Mock{
			K8sdClient: mockClient,
		},
	}
	wrapper := &mockSnapWrapper{
		Snap: mockSnap,
	}
	tcli := &testCLI{
		Stdout:      stdout,
		Stderr:      stderr,
		Stdin:       stdin,
		MockClient:  mockClient,
		MockSnap:    mockSnap,
		SnapWrapper: wrapper,
	}
	tcli.Env = cmdutil.ExecutionEnvironment{
		Stdout: stdout,
		Stderr: stderr,
		Stdin:  stdin,
		Getuid: func() int { return 0 },
		Snap:   wrapper,
		Exit: func(code int) {
			panic(exitPanic(code))
		},
	}
	return tcli
}

func (c *testCLI) execute(args ...string) (exitCode int, err error) {
	c.SnapWrapper.k8sdClientErr = c.K8sdClientErr
	cmd := k8s.NewRootCmd(c.Env)
	cmd.SetArgs(args)
	defer func() {
		if r := recover(); r != nil {
			if code, ok := r.(exitPanic); ok {
				exitCode = int(code)
			} else {
				panic(r)
			}
		}
	}()
	err = cmd.Execute()
	if err != nil && exitCode == 0 {
		exitCode = 1
	}
	return exitCode, err
}
