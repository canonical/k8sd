package cmdutil

import (
	"os"
	"testing"

	. "github.com/onsi/gomega"
)

func TestInLXDContainer(t *testing.T) {
	t.Run("PermissionError", func(t *testing.T) {
		g := NewWithT(t)
		isLXD, err := inLXDContainer(func(string) ([]byte, error) {
			return nil, os.ErrPermission
		})
		g.Expect(isLXD).To(BeTrue())
		g.Expect(err).To(MatchError("cannot access /proc/1/environ to check if running in LXD container: permission denied"))
	})

	t.Run("ReadError", func(t *testing.T) {
		g := NewWithT(t)
		isLXD, err := inLXDContainer(func(string) ([]byte, error) {
			return nil, os.ErrNotExist
		})
		g.Expect(isLXD).To(BeFalse())
		g.Expect(err).To(MatchError("cannot read /proc/1/environ to check if running in LXD container: file does not exist"))
	})

	t.Run("LXDDetected", func(t *testing.T) {
		g := NewWithT(t)
		isLXD, err := inLXDContainer(func(string) ([]byte, error) {
			return []byte("PATH=/usr/bin\x00container=lxc\x00"), nil
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(isLXD).To(BeTrue())
	})

	t.Run("NotLXD", func(t *testing.T) {
		g := NewWithT(t)
		isLXD, err := inLXDContainer(func(string) ([]byte, error) {
			return []byte("PATH=/usr/bin\x00"), nil
		})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(isLXD).To(BeFalse())
	})
}

func TestValidateRootOwnershipNonexistent(t *testing.T) {
	g := NewWithT(t)
	err := ValidateRootOwnership("/nonexistent/file/for/test")
	g.Expect(err).To(HaveOccurred())
}
