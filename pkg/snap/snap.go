package snap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/canonical/k8sd/pkg/client/etcd"
	"github.com/canonical/k8sd/pkg/client/helm"
	"github.com/canonical/k8sd/pkg/client/k8sd"
	"github.com/canonical/k8sd/pkg/client/kubernetes"
	"github.com/canonical/k8sd/pkg/client/snapd"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/log"
	"github.com/canonical/k8sd/pkg/utils"
	"github.com/canonical/k8sd/pkg/utils/checks"
	"github.com/moby/sys/mountinfo"
	"gopkg.in/yaml.v3"
	versionutil "k8s.io/apimachinery/pkg/util/version"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

const (
	ContainerdBaseDir         = ".containerd-base-dir"
	KubeProxyDisabledLockFile = "kube-proxy-disabled"
)

var kubeProxyStateMu sync.Mutex

// WithKubeProxyStateLock serializes kube-proxy enable/disable transitions with background service restarts.
func WithKubeProxyStateLock(fn func() error) error {
	kubeProxyStateMu.Lock()
	defer kubeProxyStateMu.Unlock()
	return fn()
}

// ClearKubeProxyDisabledIfTargeted removes the disabled marker when kube-proxy is started or restarted.
func ClearKubeProxyDisabledIfTargeted(lockFilesDir string, names []string) error {
	if lockFilesDir == "" {
		return nil
	}
	for _, name := range names {
		if serviceName(name) == "k8s.kube-proxy" {
			fname := filepath.Join(lockFilesDir, KubeProxyDisabledLockFile)
			if err := os.Remove(fname); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("failed to clear kube-proxy disabled marker: %w", err)
			}
			return nil
		}
	}
	return nil
}

type SnapOpts struct {
	SnapInstanceName  string
	SnapDir           string
	SnapCommonDir     string
	SnapRevision      string
	SnapdSocketPath   string
	RunCommand        func(ctx context.Context, command []string, opts ...func(c *exec.Cmd)) error
	ContainerdBaseDir string
}

// snap implements the Snap interface.
type snap struct {
	snapDir           string
	snapCommonDir     string
	snapInstanceName  string
	snapRevision      string
	snapdSocketPath   string
	runCommand        func(ctx context.Context, command []string, opts ...func(c *exec.Cmd)) error
	runHostCommand    func(ctx context.Context, command []string, opts ...func(c *exec.Cmd)) error
	containerdBaseDir string
}

// NewSnap creates a new interface with the K8s snap.
// NewSnap accepts the $SNAP, $SNAP_COMMON, directories, and a number of options.
func NewSnap(opts SnapOpts) *snap {
	// runCommand keeps the snap environment and runs snap-internal binaries
	// ($SNAP/bin/...). runHostCommand runs host binaries (snapctl) with a
	// sanitized environment and is kept separate so future snap-internal calls
	// do not need per-call environment overrides.
	runCommand := utils.RunCommand
	runHostCommand := utils.RunHostCommand
	if opts.RunCommand != nil {
		runCommand = opts.RunCommand
		runHostCommand = opts.RunCommand
	}
	s := &snap{
		snapDir:          opts.SnapDir,
		snapCommonDir:    opts.SnapCommonDir,
		snapInstanceName: opts.SnapInstanceName,
		snapRevision:     strings.TrimSpace(opts.SnapRevision),
		snapdSocketPath:  opts.SnapdSocketPath,
		runCommand:       runCommand,
		runHostCommand:   runHostCommand,
	}

	containerdBaseDir := opts.ContainerdBaseDir
	if containerdBaseDir == "" {
		containerdBaseDir = "/"
		if s.Strict() {
			containerdBaseDir = opts.SnapCommonDir
		}
	}
	s.containerdBaseDir = containerdBaseDir

	return s
}

func (s *snap) newSnapdClient() (*snapd.Client, error) {
	client, err := snapd.NewClientWithSocket(s.snapdSocketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create snapd client: %w", err)
	}
	return client, nil
}

func qualifyServiceNames(names []string) []string {
	qualified := make([]string, len(names))
	for i, name := range names {
		qualified[i] = serviceName(name)
	}
	return qualified
}

// StartServices starts k8s services. The names can be either prefixed or not.
func (s *snap) StartServices(ctx context.Context, names []string, extraSnapArgs ...string) error {
	log.FromContext(ctx).V(1).WithCallDepth(1).Info("Starting services", "services", names)
	if err := ClearKubeProxyDisabledIfTargeted(s.LockFilesDir(), names); err != nil {
		return err
	}

	opts := snapd.StartOptions{Enable: true}
	wait := true
	for _, arg := range extraSnapArgs {
		switch arg {
		case "--enable":
			opts.Enable = true
		case "--no-wait":
			wait = false
		default:
			return fmt.Errorf("unsupported snap start argument %q", arg)
		}
	}
	if s.Strict() {
		cmd := []string{"snapctl", "start"}
		if opts.Enable {
			cmd = append(cmd, "--enable")
		}
		cmd = append(cmd, qualifyServiceNames(names)...)
		return s.runHostCommand(ctx, cmd)
	}

	client, err := s.newSnapdClient()
	if err != nil {
		return err
	}

	changeID, err := client.StartServices(ctx, qualifyServiceNames(names), opts)
	if err != nil {
		return fmt.Errorf("failed to start services %v: %w", names, err)
	}
	if !wait {
		return nil
	}
	return client.WaitChange(ctx, changeID)
}

// StopServices stops k8s services. The names can be either prefixed or not.
func (s *snap) StopServices(ctx context.Context, names []string, extraSnapArgs ...string) error {
	log.FromContext(ctx).V(1).WithCallDepth(1).Info("Stopping services", "services", names)

	opts := snapd.StopOptions{Disable: true}
	wait := true
	for _, arg := range extraSnapArgs {
		switch arg {
		case "--disable":
			opts.Disable = true
		case "--no-wait":
			wait = false
		default:
			return fmt.Errorf("unsupported snap stop argument %q", arg)
		}
	}
	if s.Strict() {
		cmd := []string{"snapctl", "stop"}
		if opts.Disable {
			cmd = append(cmd, "--disable")
		}
		cmd = append(cmd, qualifyServiceNames(names)...)
		return s.runHostCommand(ctx, cmd)
	}

	client, err := s.newSnapdClient()
	if err != nil {
		return err
	}

	changeID, err := client.StopServices(ctx, qualifyServiceNames(names), opts)
	if err != nil {
		return fmt.Errorf("failed to stop services %v: %w", names, err)
	}
	if !wait {
		return nil
	}
	return client.WaitChange(ctx, changeID)
}

// RestartServices restarts k8s services. The names can be either prefixed or not.
func (s *snap) RestartServices(ctx context.Context, names []string, extraSnapArgs ...string) error {
	log.FromContext(ctx).V(1).WithCallDepth(1).Info("Restarting services", "services", names)
	if err := ClearKubeProxyDisabledIfTargeted(s.LockFilesDir(), names); err != nil {
		return err
	}

	var opts snapd.RestartOptions
	wait := true
	for _, arg := range extraSnapArgs {
		switch arg {
		case "--reload":
			opts.Reload = true
		case "--no-wait":
			wait = false
		default:
			return fmt.Errorf("unsupported snap restart argument %q", arg)
		}
	}
	if s.Strict() {
		cmd := []string{"snapctl", "restart"}
		if opts.Reload {
			cmd = append(cmd, "--reload")
		}
		cmd = append(cmd, qualifyServiceNames(names)...)
		return s.runHostCommand(ctx, cmd)
	}

	client, err := s.newSnapdClient()
	if err != nil {
		return err
	}

	changeID, err := client.RestartServices(ctx, qualifyServiceNames(names), opts)
	if err != nil {
		return fmt.Errorf("failed to restart services %v: %w", names, err)
	}
	if !wait {
		return nil
	}
	return client.WaitChange(ctx, changeID)
}

// Refresh refreshes the snap to a different track, revision or custom snap.
func (s *snap) Refresh(ctx context.Context, to types.RefreshOpts) (string, error) {
	if s.Strict() {
		return "", fmt.Errorf("refresh operation not available on strictly confined deployments")
	}

	client, err := s.newSnapdClient()
	if err != nil {
		return "", err
	}

	var changeID string
	switch {
	case to.Channel != "":
		changeID, err = client.Refresh(ctx, s.snapInstanceName, snapd.RefreshOptions{
			Channel: to.Channel,
			Amend:   true,
		})
	case to.Revision != "":
		changeID, err = client.Refresh(ctx, s.snapInstanceName, snapd.RefreshOptions{
			Revision: to.Revision,
			Amend:    true,
		})
	case to.LocalPath != "":
		changeID, err = client.InstallPath(ctx, to.LocalPath, snapd.InstallPathOptions{
			Name:      s.snapInstanceName,
			Classic:   true,
			Dangerous: true,
		})
	default:
		return "", fmt.Errorf("empty refresh options")
	}

	if err != nil {
		return "", fmt.Errorf("failed to refresh snap: %w", err)
	}

	return changeID, nil
}

// RefreshStatus returns the status of a refresh operation.
func (s *snap) RefreshStatus(ctx context.Context, changeID string) (*types.RefreshStatus, error) {
	if s.Strict() {
		return nil, fmt.Errorf("refresh status operation not available on strictly confined deployments")
	}

	client, err := s.newSnapdClient()
	if err != nil {
		return nil, err
	}

	status, err := client.GetRefreshStatusWithContext(ctx, changeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get snapd refresh status: %w", err)
	}

	return status, nil
}

// PostRefreshLockPath returns the path to the post-refresh lock file.
func (s *snap) PostRefreshLockPath() string {
	return filepath.Join(s.LockFilesDir(), "post-refresh")
}

type snapcraftYml struct {
	Confinement string `yaml:"confinement"`
}

func (s *snap) Strict() bool {
	var meta snapcraftYml
	contents, err := os.ReadFile(filepath.Join(s.snapDir, "meta", "snap.yaml"))
	if err != nil {
		return false
	}
	if err := yaml.Unmarshal([]byte(contents), &meta); err != nil {
		return false
	}
	return meta.Confinement == "strict"
}

func (s *snap) OnLXD(ctx context.Context) (bool, error) {
	mounts, err := mountinfo.GetMounts(mountinfo.FSTypeFilter("fuse.lxcfs"))
	if err != nil {
		return false, fmt.Errorf("failed to check for lxcfs mounts: %w", err)
	}
	return len(mounts) > 0, nil
}

func (s *snap) UID() int {
	return 0
}

func (s *snap) GID() int {
	return 0
}

func (s *snap) Hostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "dev"
	}
	return hostname
}

func (s *snap) SetContainerdBaseDir(baseDir string) {
	s.containerdBaseDir = baseDir
}

func (s *snap) GetContainerdBaseDir() string {
	return s.containerdBaseDir
}

func (s *snap) ContainerdConfigDir() string {
	return filepath.Join(s.containerdBaseDir, "etc", "containerd")
}

func (s *snap) ContainerdRootDir() string {
	return filepath.Join(s.containerdBaseDir, "var", "lib", "containerd")
}

func (s *snap) ContainerdSocketDir() string {
	return filepath.Join(s.containerdBaseDir, "run", "containerd")
}

func (s *snap) ContainerdSocketPath() string {
	return filepath.Join(s.containerdBaseDir, "run", "containerd", "containerd.sock")
}

func (s *snap) ContainerdStateDir() string {
	return filepath.Join(s.containerdBaseDir, "run", "containerd")
}

func (s *snap) SystemTuningConfigDir() string {
	return "/etc/sysctl.d"
}

func (s *snap) SystemConfigDirs() []string {
	return []string{
		"/etc/sysctl.d", "/run/sysctl.d", "/usr/local/lib/sysctl.d", "/usr/lib/sysctl.d", "/lib/sysctl.d", "/etc/sysctl.conf",
	}
}

func (s *snap) SystemMinConfig() map[string]string {
	return map[string]string{
		"fs.inotify.max_user_instances": "1024",
		"fs.inotify.max_user_watches":   "1048576",
	}
}

func (s *snap) CNIConfDir() string {
	return "/etc/cni/net.d"
}

func (s *snap) CNIBinDir() string {
	if s.Strict() {
		return filepath.Join(s.snapCommonDir, "opt", "cni", "bin")
	}
	return "/opt/cni/bin"
}

func (s *snap) CNIPluginsBinary() string {
	return filepath.Join(s.snapDir, "bin", "cni")
}

func (s *snap) CNIPlugins() []string {
	return []string{
		"dhcp",
		"host-local",
		"static",
		"bridge",
		"host-device",
		"ipvlan",
		"loopback",
		"macvlan",
		"ptp",
		"vlan",
		"bandwidth",
		"firewall",
		"portmap",
		"sbr",
		"tuning",
		"vrf",
	}
}

func (s *snap) KubernetesConfigDir() string {
	return "/etc/kubernetes"
}

func (s *snap) KubernetesPKIDir() string {
	return "/etc/kubernetes/pki"
}

func (s *snap) EtcdPKIDir() string {
	return "/etc/kubernetes/pki/etcd"
}

func (s *snap) KubeletRootDir() string {
	return "/var/lib/kubelet"
}

func (s *snap) K8sdStateDir() string {
	return filepath.Join(s.snapCommonDir, "var", "lib", "k8sd", "state")
}

func (s *snap) EtcdDir() string {
	return filepath.Join(s.snapCommonDir, "var", "lib", "etcd")
}

func (s *snap) ServiceArgumentsDir() string {
	return filepath.Join(s.snapCommonDir, "args")
}

func (s *snap) ServiceExtraConfigDir() string {
	return filepath.Join(s.snapCommonDir, "args", "conf.d")
}

func (s *snap) LockFilesDir() string {
	return filepath.Join(s.snapCommonDir, "lock")
}

func (s *snap) NodeTokenFile() string {
	return filepath.Join(s.snapCommonDir, "node-token")
}

func (s *snap) NodeKubernetesVersion(ctx context.Context) (*versionutil.Version, error) {
	bomPath := filepath.Join(s.snapDir, "bom.json")
	data, err := os.ReadFile(bomPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read bom.json: %w", err)
	}

	var bom struct {
		Components struct {
			Kubernetes struct {
				Version string `json:"version"`
			} `json:"kubernetes"`
		} `json:"components"`
	}

	if err := json.Unmarshal(data, &bom); err != nil {
		return nil, fmt.Errorf("failed to unmarshal bom.json: %w", err)
	}

	if bom.Components.Kubernetes.Version == "" {
		return nil, fmt.Errorf("kubernetes version not found in bom.json")
	}

	versionStr := bom.Components.Kubernetes.Version

	version, err := versionutil.Parse(versionStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse node Kubernetes version %q: %w", versionStr, err)
	}

	return version, nil
}

func (s *snap) ContainerdExtraConfigDir() string {
	return filepath.Join(s.containerdBaseDir, "etc", "containerd", "conf.d")
}

func (s *snap) ContainerdRegistryConfigDir() string {
	return filepath.Join(s.containerdBaseDir, "etc", "containerd", "hosts.d")
}

func (s *snap) K8sBinDir() string {
	return filepath.Join(s.snapDir, "bin")
}

func (s *snap) K8sInspectScriptPath() string {
	return filepath.Join(s.snapDir, "k8s", "scripts", "inspect.sh")
}

func (s *snap) restClientGetter(path string, namespace string) genericclioptions.RESTClientGetter {
	flags := &genericclioptions.ConfigFlags{
		KubeConfig: utils.Pointer(path),
	}
	if namespace != "" {
		flags.Namespace = &namespace
	}
	return flags
}

func (s *snap) KubernetesClient(namespace string) (*kubernetes.Client, error) {
	return kubernetes.NewClient(s.restClientGetter(filepath.Join(s.KubernetesConfigDir(), "admin.conf"), namespace))
}

func (s *snap) KubernetesNodeClient(namespace string) (*kubernetes.Client, error) {
	return kubernetes.NewClient(s.restClientGetter(filepath.Join(s.KubernetesConfigDir(), "kubelet.conf"), namespace))
}

func (s *snap) HelmClient() helm.Client {
	return helm.NewClient(
		filepath.Join(s.snapDir, "k8s", "manifests"),
		func(namespace string) genericclioptions.RESTClientGetter {
			return s.restClientGetter(filepath.Join(s.KubernetesConfigDir(), "admin.conf"), namespace)
		},
		5*time.Minute,
		10,
	)
}

func (s *snap) EtcdClient(endpoints []string) (*etcd.Client, error) {
	return etcd.NewClient(
		s.EtcdPKIDir(),
		endpoints,
	)
}

func (s *snap) K8sdClient(address string) (k8sd.Client, error) {
	return k8sd.New(filepath.Join(s.snapCommonDir, "var", "lib", "k8sd", "state"), address)
}

func (s *snap) SnapctlGet(ctx context.Context, args ...string) ([]byte, error) {
	var b bytes.Buffer
	if err := s.runHostCommand(ctx, append([]string{"snapctl", "get"}, args...), func(c *exec.Cmd) { c.Stdout = &b }); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (s *snap) SnapctlSet(ctx context.Context, args ...string) error {
	return s.runHostCommand(ctx, append([]string{"snapctl", "set"}, args...))
}

func (s *snap) Revision(ctx context.Context) (string, error) {
	if s.snapRevision != "" {
		return s.snapRevision, nil
	}

	client, err := s.newSnapdClient()
	if err != nil {
		return "", err
	}

	snap, err := client.GetSnapInfoWithContext(ctx, s.snapInstanceName)
	if err != nil {
		return "", fmt.Errorf("failed to get snap info: %w", err)
	}

	if snap.StatusCode != 200 {
		return "", fmt.Errorf("failed to get snap info: snapd returned with error code %d", snap.StatusCode)
	}

	if snap.Result.Revision == "" {
		return "", fmt.Errorf("failed to get snap revision: got empty string")
	}

	return snap.Result.Revision, nil
}

func (s *snap) PreInitChecks(ctx context.Context, config types.ClusterConfig, serviceConfigs types.K8sServiceConfigs, isControlPlane bool) error {
	if err := checks.CheckK8sServicePorts(config, serviceConfigs, isControlPlane); err != nil {
		return fmt.Errorf("Encountered error(s) while verifying port availability for Kubernetes services: %w", err)
	}

	// NOTE(neoaggelos): in some environments the Kubernetes might hang when running for the first time
	// This works around the issue by running them once during the install hook
	for _, binary := range []string{"kube-apiserver", "kube-controller-manager", "kube-scheduler", "kube-proxy", "kubelet"} {
		if err := s.runCommand(ctx, []string{filepath.Join(s.snapDir, "bin", binary), "--version"}, func(c *exec.Cmd) { c.Stdout = nil; c.Stderr = nil }); err != nil {
			return fmt.Errorf("%q binary could not run: %w", binary, err)
		}
	}

	// check if the containerd path already exists, signaling the fact that another containerd instance
	// is already running on this node, which will conflict with the snap.
	// Checks the directories instead of the containerd.sock file, since this file does not exist if
	// containerd is not running/stopped.
	if _, err := os.Stat(s.ContainerdSocketDir()); err == nil {
		return fmt.Errorf("The path '%s' required for the containerd socket already exists, meaning there"+
			" is likely another service running containerd on this node, which will conflict with kubernetes."+
			" Please stop any other running containerd service and remove the existing directory at '%s' before"+
			" bootstrapping or joining a cluster.\n"+
			"Dev-only: You can change the k8s containerd install path using --containerd-base-dir. Note that you"+
			" may have to update the containerd configs of any operators on this node as well.\n", s.ContainerdSocketDir(), s.ContainerdSocketDir())
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("Encountered an error while checking '%s': %w", s.ContainerdSocketDir(), err)
	}

	return nil
}

var _ Snap = &snap{}
