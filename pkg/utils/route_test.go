package utils

import (
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
)

func TestGetDefaultRouteDevice(t *testing.T) {
	header := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n"

	for _, tc := range []struct {
		name        string
		content     string
		expectDev   string
		expectError bool
	}{
		{
			name: "single default route",
			content: header +
				"eth0\t00000000\t0164440A\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" +
				"eth0\t0064440A\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n",
			expectDev: "eth0",
		},
		{
			name: "lowest metric wins",
			content: header +
				"eth1\t00000000\t0164440A\t0003\t0\t0\t200\t00000000\t0\t0\t0\n" +
				"eth0\t00000000\t0164440A\t0003\t0\t0\t100\t00000000\t0\t0\t0\n",
			expectDev: "eth0",
		},
		{
			name: "non-default routes are ignored",
			content: header +
				"eth0\t0064440A\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n" +
				"eth1\t00000000\t0164440A\t0003\t0\t0\t0\t00000000\t0\t0\t0\n",
			expectDev: "eth1",
		},
		{
			name:        "no default route",
			content:     header + "eth0\t0064440A\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n",
			expectError: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			path := filepath.Join(t.TempDir(), "route")
			g.Expect(WriteFile(path, []byte(tc.content), 0o600)).To(Succeed())

			dev, err := getDefaultRouteDevice(path)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(dev).To(Equal(tc.expectDev))
		})
	}
}
