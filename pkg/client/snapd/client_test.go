package snapd

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func startFakeSnapd(t *testing.T, handler http.Handler) *Client {
	t.Helper()

	sockPath := filepath.Join(t.TempDir(), "snapd.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: handler}
	go func() {
		_ = srv.Serve(ln)
	}()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = ln.Close()
	})

	client, err := NewClientWithSocket(sockPath)
	if err != nil {
		t.Fatal(err)
	}
	client.SetPollInterval(5 * time.Millisecond)
	return client
}

func TestAppOperationsAndWaitChange(t *testing.T) {
	var polls atomic.Int32
	var gotInst appInstruction

	client := startFakeSnapd(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/apps":
			if err := json.NewDecoder(r.Body).Decode(&gotInst); err != nil {
				t.Errorf("decode body: %v", err)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"type":"async","status-code":202,"change":"42"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/changes/42":
			n := polls.Add(1)
			if n < 2 {
				_, _ = w.Write([]byte(`{"type":"sync","status-code":200,"result":{"status":"Doing","ready":false}}`))
				return
			}
			_, _ = w.Write([]byte(`{"type":"sync","status-code":200,"result":{"status":"Done","ready":true}}`))
		default:
			http.NotFound(w, r)
		}
	}))

	changeID, err := client.StartServices(context.Background(), []string{"k8s.etcd"}, StartOptions{Enable: true})
	if err != nil {
		t.Fatalf("StartServices error: %v", err)
	}
	if changeID != "42" {
		t.Fatalf("expected changeID 42, got %q", changeID)
	}
	if gotInst.Action != "start" || !gotInst.Enable || len(gotInst.Names) != 1 || gotInst.Names[0] != "k8s.etcd" {
		t.Fatalf("unexpected app instruction: %+v", gotInst)
	}

	if err := client.WaitChange(context.Background(), changeID); err != nil {
		t.Fatalf("WaitChange error: %v", err)
	}
	if polls.Load() < 2 {
		t.Fatalf("expected at least 2 change polls, got %d", polls.Load())
	}
}

func TestWaitChangeSurfacesChangeError(t *testing.T) {
	client := startFakeSnapd(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"type":"sync","status-code":200,"result":{"status":"Error","ready":true,"err":"unit failed to start"}}`))
	}))

	err := client.WaitChange(context.Background(), "99")
	if err == nil {
		t.Fatal("expected error when snapd change fails")
	}
}

func TestRefreshAndInstallPath(t *testing.T) {
	var refreshReq snapActionRequest
	var gotAction, gotName, gotClassic, gotDangerous, gotFile string

	client := startFakeSnapd(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/snaps/k8s":
			if err := json.NewDecoder(r.Body).Decode(&refreshReq); err != nil {
				t.Errorf("decode refresh body: %v", err)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"type":"async","status-code":202,"change":"77"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v2/snaps":
			mr, err := r.MultipartReader()
			if err != nil {
				t.Errorf("multipart reader: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			for {
				part, err := mr.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Errorf("next part: %v", err)
					break
				}
				data, _ := io.ReadAll(part)
				switch part.FormName() {
				case "action":
					gotAction = string(data)
				case "name":
					gotName = string(data)
				case "classic":
					gotClassic = string(data)
				case "dangerous":
					gotDangerous = string(data)
				case "snap":
					gotFile = string(data)
				}
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"type":"async","status-code":202,"change":"88"}`))
		default:
			http.NotFound(w, r)
		}
	}))

	chg, err := client.Refresh(context.Background(), "k8s", RefreshOptions{Channel: "1.36-classic/stable", Amend: true})
	if err != nil || chg != "77" {
		t.Fatalf("Refresh got (%q, %v)", chg, err)
	}
	if refreshReq.Action != "refresh" || refreshReq.Channel != "1.36-classic/stable" || !refreshReq.Amend {
		t.Fatalf("unexpected refresh payload: %+v", refreshReq)
	}

	snapFile := filepath.Join(t.TempDir(), "k8s.snap")
	if err := os.WriteFile(snapFile, []byte("fake-squashfs-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	chg, err = client.InstallPath(context.Background(), snapFile, InstallPathOptions{
		Name:      "k8s",
		Classic:   true,
		Dangerous: true,
	})
	if err != nil || chg != "88" {
		t.Fatalf("InstallPath got (%q, %v)", chg, err)
	}
	if gotAction != "install" || gotName != "k8s" || gotClassic != "true" || gotDangerous != "true" || gotFile != "fake-squashfs-bytes" {
		t.Fatalf("unexpected multipart fields: action=%q name=%q classic=%q dangerous=%q file=%q", gotAction, gotName, gotClassic, gotDangerous, gotFile)
	}
}
