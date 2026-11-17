package cmdutil_test

import (
	"testing"

	cmdutil "github.com/canonical/k8sd/cmd/util"
	. "github.com/onsi/gomega"
)

func TestInLXDContainer(t *testing.T) {
	g := NewWithT(t)
	_, err := cmdutil.InLXDContainer()
	if err != nil {
		g.Expect(err.Error()).To(Not(ContainSubstring("cannnot")))
		g.Expect(err.Error()).To(Not(ContainSubstring("runing")))
		g.Expect(err.Error()).To(ContainSubstring("to check if running in LXD container"))
	}
}

func TestValidateRootOwnershipNonexistent(t *testing.T) {
	g := NewWithT(t)
	err := cmdutil.ValidateRootOwnership("/nonexistent/file/for/test")
	g.Expect(err).To(HaveOccurred())
}
