//go:build integration

package podman

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/borgim/sagansync/agent/internal/runtime"
)

func plainTar(t *testing.T, files map[string]string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	return &buf
}

// Run inside the VM: SAGAN_PODMAN_SOCKET=/run/user/$(id -u)/podman/podman.sock go test -tags integration ./internal/podman/
func TestAgainstRealPodman(t *testing.T) {
	sock := os.Getenv("SAGAN_PODMAN_SOCKET")
	if sock == "" {
		t.Skip("SAGAN_PODMAN_SOCKET not set")
	}
	c := New(sock)
	ctx := context.Background()
	tag, name := "localhost/sagan_itest_ws:1", "sagan_itest_ws_1"
	t.Cleanup(func() { _ = c.Remove(ctx, name); _ = c.RemoveImage(ctx, tag) })

	dockerfile := "FROM docker.io/library/busybox:1.36\n" +
		"CMD [\"sh\", \"-c\", \"echo started; mkdir -p /www && echo ok > /www/index.html && httpd -f -p 3000 -h /www\"]\n"
	var lines []string
	if err := c.Build(ctx, plainTar(t, map[string]string{"Dockerfile": dockerfile}), tag, func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatalf("build: %v\n%s", err, strings.Join(lines, "\n"))
	}
	if ok, err := c.ImageExists(ctx, tag); err != nil || !ok {
		t.Fatalf("ImageExists = %v, %v", ok, err)
	}
	spec := runtime.ContainerSpec{Name: name, Image: tag, InternalPort: 3000, Env: map[string]string{"A": "1"},
		Labels: map[string]string{runtime.LabelManaged: "true", runtime.LabelProject: "itest", runtime.LabelWorkspace: "ws"}}
	if err := c.Create(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := c.Start(ctx, name); err != nil {
		t.Fatal(err)
	}
	info, err := c.Inspect(ctx, name)
	if err != nil || !info.Running || info.HostPort == 0 {
		t.Fatalf("Inspect = %+v, %v", info, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", info.HostPort))
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if strings.TrimSpace(string(body)) == "ok" {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("container never served HTTP: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	var logs bytes.Buffer
	if err := c.Logs(ctx, name, 50, false, &logs); err != nil || !strings.Contains(logs.String(), "started") {
		t.Fatalf("Logs = %q, %v", logs.String(), err)
	}
	list, err := c.List(ctx)
	found := false
	for _, ci := range list {
		found = found || ci.Name == name
	}
	if err != nil || !found {
		t.Fatalf("List = %+v, %v", list, err)
	}
	if err := c.Stop(ctx, name, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if info, _ := c.Inspect(ctx, name); info.Running {
		t.Fatal("still running after Stop")
	}
	if err := c.Remove(ctx, name); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Inspect(ctx, name); !errors.Is(err, runtime.ErrNotFound) {
		t.Fatalf("Inspect after Remove = %v", err)
	}
}
