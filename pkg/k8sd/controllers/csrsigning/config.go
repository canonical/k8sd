package csrsigning

import (
	apiv1_annotations "github.com/canonical/k8s-snap-api/v2/api/annotations/csrsigning"
	"github.com/canonical/k8sd/pkg/k8sd/types"
)

type internalConfig struct {
	autoApprove bool
}

func internalConfigFromAnnotations(annotations types.Annotations) internalConfig {
	var cfg internalConfig
	if v, ok := annotations.Get(apiv1_annotations.AnnotationAutoApprove); ok && v == "true" {
		cfg.autoApprove = true
	}
	return cfg
}
