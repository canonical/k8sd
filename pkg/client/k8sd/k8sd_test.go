package k8sd

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("InvalidStateDir", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// An invalid path like /dev/null/sub will fail to create filesystem
		client, err := New("/dev/null/sub", "")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to initialize microcluster app"))
		g.Expect(client).To(BeNil())
	})

	t.Run("RemoteClient_Success", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		dir := t.TempDir()
		client, err := New(dir, "https://127.0.0.1:8443")
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(client).ToNot(BeNil())
		g.Expect(client.app).ToNot(BeNil())
		g.Expect(client.client).ToNot(BeNil())
	})

	t.Run("LocalClient_Success", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		dir := t.TempDir()
		client, err := New(dir, "")
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(client).ToNot(BeNil())
		g.Expect(client.app).ToNot(BeNil())
		g.Expect(client.client).ToNot(BeNil())
	})
}
