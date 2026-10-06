package types

import (
	"fmt"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
)

// patchesFromAPI converts a list of public API Patches into internal Patches.
func patchesFromAPI(in *[]apiv2.Patch) (*[]Patch, error) {
	if in == nil {
		return nil, nil
	}
	out := make([]Patch, 0, len(*in))
	for i, p := range *in {
		if p.StrategicMerge != nil && p.JSON6902 != nil {
			return nil, fmt.Errorf("patches[%d]: exactly one of \"strategic-merge\" or \"json6902\" must be set, not both", i)
		}
		if p.StrategicMerge == nil && p.JSON6902 == nil {
			return nil, fmt.Errorf("patches[%d]: exactly one of \"strategic-merge\" or \"json6902\" must be set", i)
		}
		if p.Target.Kind == "" || p.Target.Name == "" {
			return nil, fmt.Errorf("patches[%d]: target.kind and target.name are required", i)
		}
		out = append(out, Patch{
			Target: PatchTarget{
				Kind:      p.Target.Kind,
				Name:      p.Target.Name,
				Namespace: p.Target.Namespace,
			},
			StrategicMerge: p.StrategicMerge,
			JSON6902:       p.JSON6902,
		})
	}
	return &out, nil
}

// patchesToAPI converts a list of internal Patches into public API Patches.
func patchesToAPI(in *[]Patch) *[]apiv2.Patch {
	if in == nil {
		return nil
	}
	out := make([]apiv2.Patch, 0, len(*in))
	for _, p := range *in {
		out = append(out, apiv2.Patch{
			Target: apiv2.PatchTarget{
				Kind:      p.Target.Kind,
				Name:      p.Target.Name,
				Namespace: p.Target.Namespace,
			},
			StrategicMerge: p.StrategicMerge,
			JSON6902:       p.JSON6902,
		})
	}
	return &out
}
