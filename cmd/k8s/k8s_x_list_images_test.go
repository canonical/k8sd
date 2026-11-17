package k8s_test

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestK8sListImagesCmd(t *testing.T) {
	t.Run("ListImages", func(t *testing.T) {
		g := NewWithT(t)
		cli := newTestCLI()

		code, err := cli.execute("list-images")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(code).To(Equal(0))
		g.Expect(cli.Stdout.String()).To(ContainSubstring("cilium"))
	})
}
