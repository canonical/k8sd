package k8s_test

import (
	"bytes"
	"testing"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/cmd/k8s"
	cmdutil "github.com/canonical/k8sd/cmd/util"
	k8sdmock "github.com/canonical/k8sd/pkg/client/k8sd/mock"
	"github.com/canonical/k8sd/pkg/k8sd/features"
	snapmock "github.com/canonical/k8sd/pkg/snap/mock"
	"github.com/canonical/k8sd/pkg/utils"
	. "github.com/onsi/gomega"
)

func TestDisableCmd(t *testing.T) {
	tests := []struct {
		name           string
		funcs          []string
		expectedCall   apiv2.SetClusterConfigRequest
		expectedCode   int
		expectedStdout string
		expectedStderr string
	}{
		{
			name:           "empty",
			funcs:          []string{},
			expectedStderr: "Error: requires at least 1 arg",
			expectedCode:   1,
		},
		{
			name:  "one",
			funcs: []string{string(features.Gateway)},
			expectedCall: apiv2.SetClusterConfigRequest{
				Config: apiv2.UserFacingClusterConfig{
					Gateway: apiv2.GatewayConfig{Enabled: utils.Pointer(false)},
				},
			},
			expectedStdout: "disabled",
		},
		{
			name:  "multiple",
			funcs: []string{string(features.LoadBalancer), string(features.Gateway)},
			expectedCall: apiv2.SetClusterConfigRequest{
				Config: apiv2.UserFacingClusterConfig{
					Gateway:      apiv2.GatewayConfig{Enabled: utils.Pointer(false)},
					LoadBalancer: apiv2.LoadBalancerConfig{Enabled: utils.Pointer(false)},
				},
			},
			expectedStdout: "disabled",
		},
		{
			name:           "unknown",
			funcs:          []string{"unknownFunc"},
			expectedStderr: "Error: Cannot disable",
			expectedCode:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			mockClient := &k8sdmock.Mock{
				NodeStatusInitialized: true,
			}
			var returnCode int
			env := cmdutil.ExecutionEnvironment{
				Stdout: stdout,
				Stderr: stderr,
				Getuid: func() int { return 0 },
				Snap: &snapmock.Snap{
					Mock: snapmock.Mock{
						K8sdClient: mockClient,
					},
				},
				Exit: func(rc int) { returnCode = rc },
			}
			cmd := k8s.NewRootCmd(env)

			cmd.SetArgs(append([]string{"disable"}, tt.funcs...))
			cmd.Execute()

			g.Expect(stdout.String()).To(ContainSubstring(tt.expectedStdout))
			g.Expect(stderr.String()).To(ContainSubstring(tt.expectedStderr))
			g.Expect(returnCode).To(Equal(tt.expectedCode))

			if tt.expectedCode == 0 {
				g.Expect(mockClient.SetClusterConfigCalledWith).To(Equal(tt.expectedCall))
			}
		})
	}
}
