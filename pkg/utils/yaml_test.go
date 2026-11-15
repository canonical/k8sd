package utils_test

import (
	"testing"

	"github.com/canonical/k8sd/pkg/utils"
	. "github.com/onsi/gomega"
)

type testStrictConfig struct {
	Name  string `yaml:"name"`
	Count int    `yaml:"count"`
}

func TestUnmarshalStrict(t *testing.T) {
	g := NewWithT(t)

	t.Run("valid fields", func(t *testing.T) {
		input := []byte("name: test\ncount: 5\n")
		var cfg testStrictConfig
		err := utils.UnmarshalStrict(input, &cfg)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(cfg.Name).To(Equal("test"))
		g.Expect(cfg.Count).To(Equal(5))
	})

	t.Run("unknown fields fail", func(t *testing.T) {
		input := []byte("name: test\nunknown: value\n")
		var cfg testStrictConfig
		err := utils.UnmarshalStrict(input, &cfg)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("not found"))
	})

	t.Run("empty input succeeds", func(t *testing.T) {
		var cfg testStrictConfig
		err := utils.UnmarshalStrict([]byte(""), &cfg)
		g.Expect(err).To(Not(HaveOccurred()))
	})

	t.Run("whitespace input succeeds", func(t *testing.T) {
		var cfg testStrictConfig
		err := utils.UnmarshalStrict([]byte("  \n  "), &cfg)
		g.Expect(err).To(Not(HaveOccurred()))
	})
}
