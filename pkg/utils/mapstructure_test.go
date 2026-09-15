package utils_test

import (
	"reflect"
	"testing"

	"github.com/canonical/k8sd/pkg/utils"
	. "github.com/onsi/gomega"
)

func TestYAMLToMapSliceHookFunc(t *testing.T) {
	for _, tc := range []struct {
		name       string
		fromKind   reflect.Kind
		toKind     reflect.Kind
		data       any
		expectPass bool
		expect     any
	}{
		{
			name:       "NotAString",
			fromKind:   reflect.Slice,
			toKind:     reflect.Slice,
			data:       "irrelevant",
			expectPass: true,
			expect:     "irrelevant",
		},
		{
			name:       "NotASlice",
			fromKind:   reflect.String,
			toKind:     reflect.Map,
			data:       "irrelevant",
			expectPass: true,
			expect:     "irrelevant",
		},
		{
			name:       "Empty",
			fromKind:   reflect.String,
			toKind:     reflect.Slice,
			data:       "",
			expectPass: true,
			expect:     "",
		},
		{
			name:     "ListOfObjects",
			fromKind: reflect.String,
			toKind:   reflect.Slice,
			data: "" +
				"- target:\n" +
				"    kind: DaemonSet\n" +
				"    name: cilium\n" +
				"  strategic-merge: |\n" +
				"    spec:\n" +
				"      foo: bar\n",
			expectPass: true,
			expect: []map[string]any{
				{
					"target": map[any]any{
						"kind": "DaemonSet",
						"name": "cilium",
					},
					"strategic-merge": "spec:\n  foo: bar\n",
				},
			},
		},
		{
			name:       "InvalidYAML",
			fromKind:   reflect.String,
			toKind:     reflect.Slice,
			data:       "not: [valid",
			expectPass: true,
			expect:     "not: [valid",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			result, err := utils.YAMLToMapSliceHookFunc(tc.fromKind, tc.toKind, tc.data)
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(result).To(Equal(tc.expect))
		})
	}
}
