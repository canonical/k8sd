package metrics_server

import (
	"fmt"

	"github.com/canonical/k8sd/pkg/k8sd/images"
)

func init() {
	images.Register(
		fmt.Sprintf("%s:%s", imageRepo, imageTag),
	)
}
