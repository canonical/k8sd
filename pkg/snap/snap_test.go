package snap_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/snap"
	"github.com/canonical/k8sd/pkg/snap/mock"
	. "github.com/onsi/gomega"
)

type recordedAppCall struct {
	Action  string   `json:"action"`
	Names   []string `json:"names"`
	Enable  bool     `json:"enable"`
	Disable bool     `json:"disable"`
	Reload  bool     `json:"reload"`
}

type fakeSnapdServer struct {
	mu           sync.Mutex
	appCalls     []recordedAppCall
	changePolls  int
	failApps     bool
	refreshCalls []map[string]any
	sideloaded   string
}

func startFakeSnapdSocket(t *testing.T) (string, *fakeSnapdServer) {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "snapd.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	state := &fakeSnapdServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/apps", func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.failApps {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"error","status-code":400,"result":{"message":"boom"}}`))
			return
		}
		var call recordedAppCall
		_ = json.NewDecoder(r.Body).Decode(&call)
		state.appCalls = append(state.appCalls, call)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"type":"async","status-code":202,"change":"10"}`))
	})
	mux.HandleFunc("/v2/changes/10", func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		state.changePolls++
		state.mu.Unlock()
		_, _ = w.Write([]byte(`{"type":"sync","status-code":200,"result":{"status":"Done","ready":true}}`))
	})
	mux.HandleFunc("/v2/snaps/k8s", func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		state.refreshCalls = append(state.refreshCalls, body)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"type":"async","status-code":202,"change":"21"}`))
	})
	mux.HandleFunc("/v2/snaps", func(w http.ResponseWriter, r *http.Request) {
		mr, err := r.MultipartReader()
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				break
			}
			data, _ := io.ReadAll(p)
			if p.FormName() == "snap" {
				state.mu.Lock()
				state.sideloaded = string(data)
				state.mu.Unlock()
			}
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"type":"async","status-code":202,"change":"22"}`))
	})

	srv := &http.Server{Handler: mux}
	go func() {
		_ = srv.Serve(ln)
	}()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = ln.Close()
	})
	return sockPath, state
}

func TestSnap(t *testing.T) {
	t.Run("NewSnap", func(t *testing.T) {
		t.Run("containerd path with opt", func(t *testing.T) {
			g := NewWithT(t)
			mockRunner := &mock.Runner{}
			snap := snap.NewSnap(snap.SnapOpts{
				RunCommand:        mockRunner.Run,
				ContainerdBaseDir: "/foo/lish",
			})

			g.Expect(snap.ContainerdSocketPath()).To(Equal(filepath.Join("/foo/lish", "run", "containerd", "containerd.sock")))
		})

		t.Run("containerd path classic", func(t *testing.T) {
			g := NewWithT(t)
			mockRunner := &mock.Runner{}
			snap := snap.NewSnap(snap.SnapOpts{
				RunCommand: mockRunner.Run,
			})

			g.Expect(snap.ContainerdSocketPath()).To(Equal(filepath.Join("/", "run", "containerd", "containerd.sock")))
		})

		t.Run("containerd path strict", func(t *testing.T) {
			g := NewWithT(t)
			// We're checking if the snap is strict in NewSnap, which checks the snap.yaml file.
			tmpDir, err := os.MkdirTemp("", "test-snap-k8s")
			g.Expect(err).To(Not(HaveOccurred()))
			defer os.RemoveAll(tmpDir)

			err = os.MkdirAll(filepath.Join(tmpDir, "meta"), 0o755)
			g.Expect(err).To(Not(HaveOccurred()))

			snapData := []byte("confinement: strict\n")
			err = os.WriteFile(filepath.Join(tmpDir, "meta", "snap.yaml"), snapData, 0o644)
			g.Expect(err).To(Not(HaveOccurred()))

			mockRunner := &mock.Runner{}
			snap := snap.NewSnap(snap.SnapOpts{
				SnapDir:       tmpDir,
				SnapCommonDir: tmpDir,
				RunCommand:    mockRunner.Run,
			})

			g.Expect(snap.ContainerdSocketPath()).To(Equal(filepath.Join(tmpDir, "run", "containerd", "containerd.sock")))
		})
	})

	t.Run("CNIBinDir", func(t *testing.T) {
		t.Run("Classic", func(t *testing.T) {
			g := NewWithT(t)
			s := snap.NewSnap(snap.SnapOpts{
				SnapDir:       "testdir",
				SnapCommonDir: "testdir",
			})
			g.Expect(s.CNIBinDir()).To(Equal("/opt/cni/bin"))
		})

		t.Run("Strict", func(t *testing.T) {
			g := NewWithT(t)
			tmpDir := t.TempDir()
			g.Expect(os.MkdirAll(filepath.Join(tmpDir, "meta"), 0o755)).To(Succeed())
			g.Expect(os.WriteFile(filepath.Join(tmpDir, "meta", "snap.yaml"), []byte("confinement: strict\n"), 0o644)).To(Succeed())

			commonDir := filepath.Join(tmpDir, "common")
			s := snap.NewSnap(snap.SnapOpts{
				SnapDir:       tmpDir,
				SnapCommonDir: commonDir,
			})
			g.Expect(s.CNIBinDir()).To(Equal(filepath.Join(commonDir, "opt", "cni", "bin")))
		})
	})

	t.Run("Start", func(t *testing.T) {
		g := NewWithT(t)
		sockPath, srv := startFakeSnapdSocket(t)
		s := snap.NewSnap(snap.SnapOpts{
			SnapDir:         "testdir",
			SnapCommonDir:   "testdir",
			SnapdSocketPath: sockPath,
		})

		err := s.StartServices(context.Background(), []string{"test-service"})
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(srv.appCalls).To(Equal([]recordedAppCall{{
			Action: "start",
			Names:  []string{"k8s.test-service"},
			Enable: true,
		}}))
		g.Expect(srv.changePolls).To(Equal(1))

		srv.appCalls = nil
		srv.changePolls = 0
		err = s.StartServices(context.Background(), []string{"test-service"}, "--no-wait")
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(srv.appCalls).To(HaveLen(1))
		g.Expect(srv.changePolls).To(Equal(0))

		t.Run("Strict", func(t *testing.T) {
			g := NewWithT(t)
			tmpDir := t.TempDir()
			g.Expect(os.MkdirAll(filepath.Join(tmpDir, "meta"), 0o755)).To(Succeed())
			g.Expect(os.WriteFile(filepath.Join(tmpDir, "meta", "snap.yaml"), []byte("confinement: strict\n"), 0o644)).To(Succeed())

			mockRunner := &mock.Runner{}
			strictSnap := snap.NewSnap(snap.SnapOpts{
				SnapDir:       tmpDir,
				SnapCommonDir: tmpDir,
				RunCommand:    mockRunner.Run,
			})

			err := strictSnap.StartServices(context.Background(), []string{"test-service"})
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(mockRunner.CalledWithCommand).To(ConsistOf("snapctl start --enable k8s.test-service"))

			mockRunner.CalledWithCommand = nil
			err = strictSnap.StartServices(context.Background(), []string{"test-service"}, "--no-wait")
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(mockRunner.CalledWithCommand).To(ConsistOf("snapctl start --enable k8s.test-service"))
		})

		t.Run("Fail", func(t *testing.T) {
			g := NewWithT(t)
			srv.failApps = true

			err := s.StartServices(context.Background(), []string{"test-service"})
			g.Expect(err).To(HaveOccurred())
		})
	})

	t.Run("Stop", func(t *testing.T) {
		g := NewWithT(t)
		sockPath, srv := startFakeSnapdSocket(t)
		s := snap.NewSnap(snap.SnapOpts{
			SnapDir:         "testdir",
			SnapCommonDir:   "testdir",
			SnapdSocketPath: sockPath,
		})
		err := s.StopServices(context.Background(), []string{"test-service"})
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(srv.appCalls).To(Equal([]recordedAppCall{{
			Action:  "stop",
			Names:   []string{"k8s.test-service"},
			Disable: true,
		}}))
		g.Expect(srv.changePolls).To(Equal(1))

		srv.appCalls = nil
		srv.changePolls = 0
		err = s.StopServices(context.Background(), []string{"test-service"}, "--no-wait")
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(srv.appCalls).To(HaveLen(1))
		g.Expect(srv.changePolls).To(Equal(0))

		t.Run("Strict", func(t *testing.T) {
			g := NewWithT(t)
			tmpDir := t.TempDir()
			g.Expect(os.MkdirAll(filepath.Join(tmpDir, "meta"), 0o755)).To(Succeed())
			g.Expect(os.WriteFile(filepath.Join(tmpDir, "meta", "snap.yaml"), []byte("confinement: strict\n"), 0o644)).To(Succeed())

			mockRunner := &mock.Runner{}
			strictSnap := snap.NewSnap(snap.SnapOpts{
				SnapDir:       tmpDir,
				SnapCommonDir: tmpDir,
				RunCommand:    mockRunner.Run,
			})

			err := strictSnap.StopServices(context.Background(), []string{"test-service"})
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(mockRunner.CalledWithCommand).To(ConsistOf("snapctl stop --disable k8s.test-service"))

			mockRunner.CalledWithCommand = nil
			err = strictSnap.StopServices(context.Background(), []string{"test-service"}, "--no-wait")
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(mockRunner.CalledWithCommand).To(ConsistOf("snapctl stop --disable k8s.test-service"))
		})

		t.Run("Fail", func(t *testing.T) {
			g := NewWithT(t)
			srv.failApps = true

			err := s.StopServices(context.Background(), []string{"test-service"})
			g.Expect(err).To(HaveOccurred())
		})
	})

	t.Run("Restart", func(t *testing.T) {
		g := NewWithT(t)
		sockPath, srv := startFakeSnapdSocket(t)
		s := snap.NewSnap(snap.SnapOpts{
			SnapDir:         "testdir",
			SnapCommonDir:   "testdir",
			SnapdSocketPath: sockPath,
		})

		err := s.RestartServices(context.Background(), []string{"test-service"})
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(srv.appCalls).To(Equal([]recordedAppCall{{
			Action: "restart",
			Names:  []string{"k8s.test-service"},
		}}))
		g.Expect(srv.changePolls).To(Equal(1))

		srv.appCalls = nil
		srv.changePolls = 0
		err = s.RestartServices(context.Background(), []string{"test-service"}, "--no-wait")
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(srv.appCalls).To(HaveLen(1))
		g.Expect(srv.changePolls).To(Equal(0))

		t.Run("Strict", func(t *testing.T) {
			g := NewWithT(t)
			tmpDir := t.TempDir()
			g.Expect(os.MkdirAll(filepath.Join(tmpDir, "meta"), 0o755)).To(Succeed())
			g.Expect(os.WriteFile(filepath.Join(tmpDir, "meta", "snap.yaml"), []byte("confinement: strict\n"), 0o644)).To(Succeed())

			mockRunner := &mock.Runner{}
			strictSnap := snap.NewSnap(snap.SnapOpts{
				SnapDir:       tmpDir,
				SnapCommonDir: tmpDir,
				RunCommand:    mockRunner.Run,
			})

			err := strictSnap.RestartServices(context.Background(), []string{"test-service"})
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(mockRunner.CalledWithCommand).To(ConsistOf("snapctl restart k8s.test-service"))

			mockRunner.CalledWithCommand = nil
			err = strictSnap.RestartServices(context.Background(), []string{"test-service"}, "--reload")
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(mockRunner.CalledWithCommand).To(ConsistOf("snapctl restart --reload k8s.test-service"))

			mockRunner.CalledWithCommand = nil
			err = strictSnap.RestartServices(context.Background(), []string{"test-service"}, "--reload", "--no-wait")
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(mockRunner.CalledWithCommand).To(ConsistOf("snapctl restart --reload k8s.test-service"))
		})

		t.Run("Fail", func(t *testing.T) {
			g := NewWithT(t)
			srv.failApps = true

			err := s.RestartServices(context.Background(), []string{"service"})
			g.Expect(err).To(HaveOccurred())
		})
	})

	t.Run("Refresh", func(t *testing.T) {
		g := NewWithT(t)
		sockPath, srv := startFakeSnapdSocket(t)
		s := snap.NewSnap(snap.SnapOpts{
			SnapInstanceName: "k8s",
			SnapDir:          "testdir",
			SnapCommonDir:    "testdir",
			SnapdSocketPath:  sockPath,
		})

		chg, err := s.Refresh(context.Background(), types.RefreshOpts{Channel: "1.36-classic/stable"})
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(chg).To(Equal("21"))
		g.Expect(srv.refreshCalls).To(HaveLen(1))
		g.Expect(srv.refreshCalls[0]["channel"]).To(Equal("1.36-classic/stable"))
		g.Expect(srv.refreshCalls[0]["amend"]).To(BeTrue())

		localSnap := filepath.Join(t.TempDir(), "k8s.snap")
		g.Expect(os.WriteFile(localSnap, []byte("snap-payload"), 0o644)).To(Succeed())
		chg, err = s.Refresh(context.Background(), types.RefreshOpts{LocalPath: localSnap})
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(chg).To(Equal("22"))
		g.Expect(srv.sideloaded).To(Equal("snap-payload"))
	})

	t.Run("PreInitChecks", func(t *testing.T) {
		g := NewWithT(t)
		// Replace the ContainerdSocketDir to avoid checking against a real containerd.sock that may be running.
		containerdDir, err := os.MkdirTemp("", "test-containerd")
		g.Expect(err).To(Not(HaveOccurred()))
		defer os.RemoveAll(containerdDir)

		mockRunner := &mock.Runner{}
		snap := snap.NewSnap(snap.SnapOpts{
			SnapDir:           "testdir",
			SnapCommonDir:     "testdir",
			RunCommand:        mockRunner.Run,
			ContainerdBaseDir: containerdDir,
		})
		conf := types.ClusterConfig{}
		serviceConfigs := types.K8sServiceConfigs{}

		err = snap.PreInitChecks(context.Background(), conf, serviceConfigs, true)
		g.Expect(err).To(Not(HaveOccurred()))
		expectedCalls := []string{}
		for _, binary := range []string{"kube-apiserver", "kube-controller-manager", "kube-scheduler", "kube-proxy", "kubelet"} {
			expectedCalls = append(expectedCalls, fmt.Sprintf("testdir/bin/%s --version", binary))
		}
		g.Expect(mockRunner.CalledWithCommand).To(ConsistOf(expectedCalls))

		t.Run("Fail port already in use", func(t *testing.T) {
			g := NewWithT(t)
			// Open a port which will be checked (kubelet).
			port := "9999"
			serviceConfigs := types.K8sServiceConfigs{
				ExtraNodeKubeletArgs: map[string]*string{"--port": &port},
			}
			l, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
			g.Expect(err).To(Not(HaveOccurred()))
			defer l.Close()

			err = snap.PreInitChecks(context.Background(), conf, serviceConfigs, true)
			g.Expect(err).To(HaveOccurred())
		})

		t.Run("Fail socket exists", func(t *testing.T) {
			g := NewWithT(t)
			// Create the containerd.sock file, which should cause the check to fail.
			err := os.MkdirAll(snap.ContainerdSocketDir(), 0o755)
			g.Expect(err).To(Not(HaveOccurred()))
			f, err := os.Create(snap.ContainerdSocketPath())
			g.Expect(err).To(Not(HaveOccurred()))
			f.Close()
			defer os.Remove(f.Name())

			err = snap.PreInitChecks(context.Background(), conf, serviceConfigs, true)
			g.Expect(err).To(HaveOccurred())
		})

		t.Run("Fail run command", func(t *testing.T) {
			g := NewWithT(t)
			mockRunner.Err = fmt.Errorf("some error")

			err := snap.PreInitChecks(context.Background(), conf, serviceConfigs, true)
			g.Expect(err).To(HaveOccurred())
		})
	})

	t.Run("NodeKubernetesVersion", func(t *testing.T) {
		t.Run("returns version from bom.json", func(t *testing.T) {
			g := NewWithT(t)

			tmpDir, err := os.MkdirTemp("", "test-bom-k8s")
			g.Expect(err).To(Not(HaveOccurred()))
			defer os.RemoveAll(tmpDir)

			bomContent := `{
				"components": {
					"kubernetes": {
						"version": "v1.32.3"
					}
				}
			}`
			err = os.WriteFile(filepath.Join(tmpDir, "bom.json"), []byte(bomContent), 0o644)
			g.Expect(err).To(Not(HaveOccurred()))

			snap := snap.NewSnap(snap.SnapOpts{
				SnapDir: tmpDir,
			})

			version, err := snap.NodeKubernetesVersion(context.Background())
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(version.String()).To(Equal("1.32.3"))
		})

		t.Run("fails when bom.json is missing", func(t *testing.T) {
			g := NewWithT(t)

			tmpDir, err := os.MkdirTemp("", "test-bom-missing")
			g.Expect(err).To(Not(HaveOccurred()))
			defer os.RemoveAll(tmpDir)

			snap := snap.NewSnap(snap.SnapOpts{
				SnapDir: tmpDir,
			})

			_, err = snap.NodeKubernetesVersion(context.Background())
			g.Expect(err).To(HaveOccurred())
		})

		t.Run("fails when bom.json is malformed", func(t *testing.T) {
			g := NewWithT(t)

			tmpDir, err := os.MkdirTemp("", "test-bom-bad")
			g.Expect(err).To(Not(HaveOccurred()))
			defer os.RemoveAll(tmpDir)

			err = os.WriteFile(filepath.Join(tmpDir, "bom.json"), []byte("not-json"), 0o644)
			g.Expect(err).To(Not(HaveOccurred()))

			snap := snap.NewSnap(snap.SnapOpts{
				SnapDir: tmpDir,
			})

			_, err = snap.NodeKubernetesVersion(context.Background())
			g.Expect(err).To(HaveOccurred())
		})
	})

	t.Run("Revision", func(t *testing.T) {
		t.Run("returns revision from SnapRevision", func(t *testing.T) {
			g := NewWithT(t)

			s := snap.NewSnap(snap.SnapOpts{
				SnapRevision: "42",
			})

			rev, err := s.Revision(context.Background())
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(rev).To(Equal("42"))
		})

		t.Run("trims whitespace from SnapRevision", func(t *testing.T) {
			g := NewWithT(t)

			s := snap.NewSnap(snap.SnapOpts{
				SnapRevision: "  1234\n",
			})

			rev, err := s.Revision(context.Background())
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(rev).To(Equal("1234"))
		})

		t.Run("falls back to snapd when SnapRevision is empty", func(t *testing.T) {
			g := NewWithT(t)

			s := snap.NewSnap(snap.SnapOpts{
				SnapRevision:     "",
				SnapdSocketPath:  filepath.Join(t.TempDir(), "missing.sock"),
				SnapInstanceName: "nonexistent-snap-name-xyz",
			})

			_, err := s.Revision(context.Background())
			g.Expect(err).To(HaveOccurred())
		})
	})
}
