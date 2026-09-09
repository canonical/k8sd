package cilium

import (
	"testing"

	apiv1_annotations "github.com/canonical/k8s-snap-api/v2/api/annotations/cilium"
	. "github.com/onsi/gomega"
)

func TestInternalConfig(t *testing.T) {
	for _, tc := range []struct {
		name           string
		annotations    map[string]string
		expectedConfig config
		expectError    bool
	}{
		{
			name:        "Empty",
			annotations: map[string]string{},
			expectedConfig: config{
				devices:             "",
				directRoutingDevice: "",
				vlanBPFBypass:       nil,
				cniExclusive:        false,
				tunnelPort:          ciliumDefaultVXLANPort,
			},
			expectError: false,
		},
		{
			name: "Valid",
			annotations: map[string]string{
				apiv1_annotations.AnnotationDevices:             "eth+ lxdbr+",
				apiv1_annotations.AnnotationDirectRoutingDevice: "eth0",
				apiv1_annotations.AnnotationVLANBPFBypass:       "1,2,3",
				apiv1_annotations.AnnotationCNIExclusive:        "true",
				apiv1_annotations.AnnotationSCTPEnabled:         "true",
			},
			expectedConfig: config{
				devices:             "eth+ lxdbr+",
				directRoutingDevice: "eth0",
				vlanBPFBypass:       []int{1, 2, 3},
				cniExclusive:        true,
				sctpEnabled:         true,
				tunnelPort:          ciliumDefaultVXLANPort,
			},
			expectError: false,
		},
		{
			name: "Cilum exclusive CNI",
			annotations: map[string]string{
				apiv1_annotations.AnnotationCNIExclusive: "true",
			},
			expectedConfig: config{
				devices:             "",
				directRoutingDevice: "",
				vlanBPFBypass:       nil,
				cniExclusive:        true,
				tunnelPort:          ciliumDefaultVXLANPort,
			},
			expectError: false,
		},
		{
			name: "Cilum custom VXLAN port",
			annotations: map[string]string{
				apiv1_annotations.AnnotationTunnelPort: "8473",
			},
			expectedConfig: config{
				tunnelPort: 8473,
			},
			expectError: false,
		},
		{
			name: "Cilum SCTP",
			annotations: map[string]string{
				apiv1_annotations.AnnotationSCTPEnabled: "true",
			},
			expectedConfig: config{
				devices:             "",
				directRoutingDevice: "",
				vlanBPFBypass:       nil,
				cniExclusive:        false,
				sctpEnabled:         true,
				tunnelPort:          ciliumDefaultVXLANPort,
			},
			expectError: false,
		},
		{
			name: "Single valid VLAN",
			annotations: map[string]string{
				apiv1_annotations.AnnotationVLANBPFBypass: "1",
			},
			expectedConfig: config{
				vlanBPFBypass: []int{1},
				tunnelPort:    ciliumDefaultVXLANPort,
			},
			expectError: false,
		},
		{
			name: "Multiple valid VLANs",
			annotations: map[string]string{
				apiv1_annotations.AnnotationVLANBPFBypass: "1,2,3,4,5",
			},
			expectedConfig: config{
				vlanBPFBypass: []int{1, 2, 3, 4, 5},
				tunnelPort:    ciliumDefaultVXLANPort,
			},
			expectError: false,
		},
		{
			name: "Wildcard VLAN",
			annotations: map[string]string{
				apiv1_annotations.AnnotationVLANBPFBypass: "0",
			},
			expectedConfig: config{
				vlanBPFBypass: []int{0},
				tunnelPort:    ciliumDefaultVXLANPort,
			},
			expectError: false,
		},
		{
			name: "Invalid VLAN tag format",
			annotations: map[string]string{
				apiv1_annotations.AnnotationVLANBPFBypass: "abc",
			},
			expectError: true,
		},
		{
			name: "VLAN tag out of range",
			annotations: map[string]string{
				apiv1_annotations.AnnotationVLANBPFBypass: "4095",
			},
			expectError: true,
		},
		{
			name: "VLAN tag negative",
			annotations: map[string]string{
				apiv1_annotations.AnnotationVLANBPFBypass: "-1",
			},
			expectError: true,
		},
		{
			name: "Duplicate VLAN tags",
			annotations: map[string]string{
				apiv1_annotations.AnnotationVLANBPFBypass: "1,2,2,3",
			},
			expectedConfig: config{
				vlanBPFBypass: []int{1, 2, 3},
				tunnelPort:    ciliumDefaultVXLANPort,
			},
			expectError: false,
		},
		{
			name: "Mixed spaces and commas",
			annotations: map[string]string{
				apiv1_annotations.AnnotationVLANBPFBypass: " 1, 2,3 ,4 , 5 ",
			},
			expectedConfig: config{
				vlanBPFBypass: []int{1, 2, 3, 4, 5},
				tunnelPort:    ciliumDefaultVXLANPort,
			},
			expectError: false,
		},
		{
			name: "Invalid mixed with valid",
			annotations: map[string]string{
				apiv1_annotations.AnnotationVLANBPFBypass: "1,abc,3",
			},
			expectError: true,
		},
		{
			name:        "Nil annotations",
			annotations: nil,
			expectedConfig: config{
				tunnelPort: ciliumDefaultVXLANPort,
			},
			expectError: false,
		},
		{
			name: "VLAN with curly braces",
			annotations: map[string]string{
				apiv1_annotations.AnnotationVLANBPFBypass: "{1,2,3}",
			},
			expectedConfig: config{
				vlanBPFBypass: []int{1, 2, 3},
				tunnelPort:    ciliumDefaultVXLANPort,
			},
			expectError: false,
		},
		{
			name: "Valid cluster ID and name",
			annotations: map[string]string{
				apiv1_annotations.AnnotationClusterID:   "1",
				apiv1_annotations.AnnotationClusterName: "my-cluster",
			},
			expectedConfig: config{
				tunnelPort:  ciliumDefaultVXLANPort,
				clusterID:   1,
				clusterName: "my-cluster",
			},
			expectError: false,
		},
		{
			name: "Cluster name only",
			annotations: map[string]string{
				apiv1_annotations.AnnotationClusterName: "my-cluster",
			},
			expectedConfig: config{
				tunnelPort:  ciliumDefaultVXLANPort,
				clusterName: "my-cluster",
			},
			expectError: false,
		},
		{
			name: "Cluster ID not a number",
			annotations: map[string]string{
				apiv1_annotations.AnnotationClusterID: "abc",
			},
			expectError: true,
		},
		{
			name: "Cluster ID out of range",
			annotations: map[string]string{
				apiv1_annotations.AnnotationClusterID: "256",
			},
			expectError: true,
		},
		{
			name: "Cluster ID negative",
			annotations: map[string]string{
				apiv1_annotations.AnnotationClusterID: "-1",
			},
			expectError: true,
		},
		{
			name: "Cluster ID zero",
			annotations: map[string]string{
				apiv1_annotations.AnnotationClusterID: "0",
			},
			expectedConfig: config{
				tunnelPort: ciliumDefaultVXLANPort,
			},
			expectError: false,
		},
		{
			name: "Cluster name too long",
			annotations: map[string]string{
				apiv1_annotations.AnnotationClusterName: "this-cluster-name-is-definitely-too-long",
			},
			expectError: true,
		},
		{
			name: "Cluster name with invalid characters",
			annotations: map[string]string{
				apiv1_annotations.AnnotationClusterName: "My_Cluster",
			},
			expectError: true,
		},
		{
			name: "Cluster name with leading dash",
			annotations: map[string]string{
				apiv1_annotations.AnnotationClusterName: "-my-cluster",
			},
			expectError: true,
		},
		{
			name: "Cluster name with trailing dash",
			annotations: map[string]string{
				apiv1_annotations.AnnotationClusterName: "my-cluster-",
			},
			expectError: true,
		},
		{
			name: "Non-zero cluster ID without cluster name",
			annotations: map[string]string{
				apiv1_annotations.AnnotationClusterID: "1",
			},
			expectError: true,
		},
		{
			name: "Non-zero cluster ID with default cluster name",
			annotations: map[string]string{
				apiv1_annotations.AnnotationClusterID:   "1",
				apiv1_annotations.AnnotationClusterName: "default",
			},
			expectError: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			parsed, err := internalConfig(tc.annotations)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(parsed).To(Equal(tc.expectedConfig))
			}
		})
	}
}
