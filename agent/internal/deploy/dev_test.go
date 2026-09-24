package deploy_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/borgim/sagansync/agent/internal/deploy"
	"github.com/borgim/sagansync/agent/internal/events"
	"github.com/borgim/sagansync/agent/internal/runtime"
	"github.com/borgim/sagansync/agent/internal/runtime/fakert"
	"github.com/borgim/sagansync/agent/internal/state"
	"github.com/borgim/sagansync/agent/internal/testutil"
	"github.com/borgim/sagansync/agent/internal/testutil/testdeploy"
)

func devReq(ws string) deploy.DevRequest {
	return deploy.DevRequest{Request: testdeploy.Request(ws), Command: []string{"npm", "run", "dev"}}
}

func startDev(t *testing.T, h *testdeploy.Harness, req deploy.DevRequest) (*events.Recorder, error) {
	t.Helper()
	rec := &events.Recorder{}
	return rec, h.D.Dev(context.Background(), req, testutil.App(t), rec)
}

func TestDevRunsWithBindMount(t *testing.T) {
	h := testdeploy.New(t)
	rec, err := startDev(t, h, devReq("feat-x"))
	if err != nil {
		t.Fatal(err)
	}
	ws, _ := h.State.Get("app", "feat-x")
	if ws.Mode != state.ModeDev || !slices.Equal(ws.Command, []string{"npm", "run", "dev"}) {
		t.Fatalf("state = %+v", ws)
	}
	c, _ := h.RT.Container(ws.Container)
	if c.Spec.Image != deploy.ImageName("app", "feat-x", "dev") {
		t.Errorf("image = %s", c.Spec.Image)
	}
	if len(c.Spec.Binds) != 1 || c.Spec.Binds[0].Dest != "/app" || !strings.HasSuffix(c.Spec.Binds[0].Source, filepath.Join("app", "feat-x", "dev")) {
		t.Errorf("binds = %+v", c.Spec.Binds)
	}
	if len(c.Spec.Volumes) != 1 || c.Spec.Volumes[0].Dest != "/app/node_modules" {
		t.Errorf("volumes = %+v", c.Spec.Volumes)
	}
	if c.Spec.Env["NODE_ENV"] != "development" {
		t.Errorf("env = %v", c.Spec.Env)
	}
	if rec.Last().Type != "done" || rec.Last().URL != "https://feat-x.app.test" {
		t.Errorf("last = %+v", rec.Last())
	}
}

func TestDevReusesImageUnlessBuildRequested(t *testing.T) {
	h := testdeploy.New(t)
	if _, err := startDev(t, h, devReq("feat-x")); err != nil {
		t.Fatal(err)
	}
	if _, err := startDev(t, h, devReq("feat-x")); err != nil {
		t.Fatal(err)
	}
	if n := len(h.RT.Builds()); n != 1 {
		t.Fatalf("builds = %v, want 1", h.RT.Builds())
	}
	req := devReq("feat-x")
	req.Build = true
	if _, err := startDev(t, h, req); err != nil {
		t.Fatal(err)
	}
	if n := len(h.RT.Builds()); n != 2 {
		t.Fatalf("builds = %v, want 2", h.RT.Builds())
	}
	if n := len(h.RT.Names()); n != 1 {
		t.Fatalf("containers = %v, old dev containers must be removed", h.RT.Names())
	}
}

func TestDevProductionLockAndValidation(t *testing.T) {
	h := testdeploy.New(t)
	if _, err := startDev(t, h, devReq("production")); errCode(err) != deploy.CodeProductionLocked {
		t.Fatalf("err = %v, want production_locked", err)
	}
	forced := devReq("production")
	forced.Force = true
	if _, err := startDev(t, h, forced); err != nil {
		t.Fatalf("forced dev on production: %v", err)
	}
	noCmd := devReq("feat-y")
	noCmd.Command = nil
	if _, err := startDev(t, h, noCmd); errCode(err) != deploy.CodeInvalid {
		t.Fatalf("err = %v, want invalid", err)
	}
}

func TestDevCrashFails(t *testing.T) {
	h := testdeploy.New(t)
	h.RT.Behave = func(runtime.ContainerSpec) fakert.Behavior { return fakert.Behavior{Crash: true} }
	if _, err := startDev(t, h, devReq("feat-x")); errCode(err) != deploy.CodeHealth {
		t.Fatalf("err = %v, want health_failed for a crashed dev container", err)
	}
}

func TestPutAndRemoveFilesInDevMode(t *testing.T) {
	h := testdeploy.New(t)
	if _, err := startDev(t, h, devReq("feat-x")); err != nil {
		t.Fatal(err)
	}
	if err := h.D.PutFile("app", "feat-x", "src/new.js", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(h.SrvDir, "app", "feat-x", "dev", "src", "new.js")
	if b, err := os.ReadFile(path); err != nil || string(b) != "x" {
		t.Fatalf("file = %q, %v", b, err)
	}
	if err := h.D.RemoveFile("app", "feat-x", "src/new.js"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file still exists")
	}
	if err := h.D.PutFile("app", "feat-x", "../escape", strings.NewReader("x")); errCode(err) != deploy.CodeInvalid {
		t.Fatalf("err = %v, want invalid", err)
	}
}

func TestPutFileRequiresDevMode(t *testing.T) {
	h := testdeploy.New(t)
	_, _ = h.Deploy(t, testdeploy.Request("production"))
	if err := h.D.PutFile("app", "production", "a.js", strings.NewReader("x")); errCode(err) != deploy.CodeNotFound {
		t.Fatalf("err = %v, want not_found", err)
	}
	if err := h.D.PutFile("app", "ghost", "a.js", strings.NewReader("x")); errCode(err) != deploy.CodeNotFound {
		t.Fatalf("err = %v, want not_found", err)
	}
}

func TestDeployAfterDevReturnsToDeployMode(t *testing.T) {
	h := testdeploy.New(t)
	_, _ = startDev(t, h, devReq("feat-x"))
	if _, err := h.Deploy(t, testdeploy.Request("feat-x")); err != nil {
		t.Fatal(err)
	}
	if ws, _ := h.State.Get("app", "feat-x"); ws.Mode != state.ModeDeploy {
		t.Fatalf("mode = %s", ws.Mode)
	}
	if n := len(h.RT.Names()); n != 1 {
		t.Fatalf("containers = %v", h.RT.Names())
	}
}
