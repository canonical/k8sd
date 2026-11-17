package k8s_test

import (
	"context"
	"testing"

	"github.com/canonical/k8sd/pkg/k8sd/features"
	"github.com/canonical/k8sd/pkg/snap"
	. "github.com/onsi/gomega"
)

type mockStatusChecks struct {
	dnsCalls     int
	networkCalls int
	checkDNSErr  error
}

func (m *mockStatusChecks) CheckDNS(_ context.Context, _ snap.Snap) error {
	m.dnsCalls++
	return m.checkDNSErr
}

func (m *mockStatusChecks) CheckNetwork(_ context.Context, _ snap.Snap) error {
	m.networkCalls++
	return nil
}

func TestK8sXWaitForCmd(t *testing.T) {
	origStatusChecks := features.StatusChecks
	defer func() {
		features.StatusChecks = origStatusChecks
	}()

	t.Run("WaitForDNSSuccess", func(t *testing.T) {
		g := NewWithT(t)
		mock := &mockStatusChecks{}
		features.StatusChecks = mock

		cli := newTestCLI()
		code, err := cli.execute("x-wait-for", "dns")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(code).To(Equal(0))
		g.Expect(mock.dnsCalls).To(BeNumerically(">=", 1))
	})

	t.Run("WaitForDNSTimeout", func(t *testing.T) {
		g := NewWithT(t)
		mock := &mockStatusChecks{}
		features.StatusChecks = mock

		cli := newTestCLI()
		code, _ := cli.execute("x-wait-for", "dns", "--timeout=10ms")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Error: DNS did not become ready"))
	})

	t.Run("WaitForNetworkTimeout", func(t *testing.T) {
		g := NewWithT(t)
		mock := &mockStatusChecks{}
		features.StatusChecks = mock

		cli := newTestCLI()
		code, _ := cli.execute("x-wait-for", "network", "--timeout=10ms")
		g.Expect(code).To(Equal(1))
		g.Expect(cli.Stderr.String()).To(ContainSubstring("Error: network did not become ready"))
	})
}
