package helmtest

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/canonical/k8sd/pkg/client/helm"
	"helm.sh/helm/v4/pkg/action"
	chartcommon "helm.sh/helm/v4/pkg/chart/common"
	kubefake "helm.sh/helm/v4/pkg/kube/fake"
	"helm.sh/helm/v4/pkg/storage"
	"helm.sh/helm/v4/pkg/storage/driver"
)

// NewMemoryClient returns a helm.Client backed by in-memory storage and a fake kube client.
func NewMemoryClient(manifestsBaseDir string, namespace string) (helm.Client, *storage.Storage) {
	mem := driver.NewMemory()
	mem.SetNamespace(namespace)
	store := storage.Init(mem)

	client := helm.NewClientWithActionConfig(
		manifestsBaseDir,
		time.Minute,
		10,
		func(_ context.Context, _ string) (*action.Configuration, error) {
			cfg := action.NewConfiguration(
				action.ConfigurationSetLogger(slog.NewTextHandler(io.Discard, nil)),
			)
			cfg.Releases = store
			cfg.Capabilities = chartcommon.DefaultCapabilities.Copy()
			cfg.KubeClient = &kubefake.PrintingKubeClient{
				Out: io.Discard,
			}
			return cfg, nil
		},
	)
	return client, store
}
