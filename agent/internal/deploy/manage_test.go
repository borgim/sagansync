package deploy_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/borgim/sagansync/agent/internal/deploy"
	"github.com/borgim/sagansync/agent/internal/testutil/testdeploy"
)

func TestRemoveDeletesEverything(t *testing.T) {
	h := testdeploy.New(t)
	_, _ = h.Deploy(t, testdeploy.Request("feat-x"))
	ws, _ := h.State.Get("app", "feat-x")
	_ = h.D.EnvSet("app", "feat-x", map[string]string{"A": "1"})
	if err := h.D.Remove(context.Background(), "app", "feat-x"); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.State.Get("app", "feat-x"); ok {
		t.Error("still in state")
	}
	if h.Routes.Get("feat-x.app.test") != "" {
		t.Error("route still present")
	}
	if _, ok := h.RT.Container(ws.Container); ok {
		t.Error("container still present")
	}
	if !slices.Contains(h.RT.RemovedImages(), deploy.ImageName("app", "feat-x", ws.Release)) {
		t.Errorf("removed images = %v", h.RT.RemovedImages())
	}
	if !slices.Contains(h.RT.RemovedVolumes(), "sagan_app_feat-x_node_modules") {
		t.Errorf("removed volumes = %v", h.RT.RemovedVolumes())
	}
	if _, err := os.Stat(filepath.Join(h.SrvDir, "app", "feat-x")); !os.IsNotExist(err) {
		t.Error("workspace directory still present")
	}
	if keys, _ := h.D.EnvKeys("app", "feat-x"); len(keys) != 0 {
		t.Errorf("env keys = %v", keys)
	}
	if err := h.D.Remove(context.Background(), "app", "feat-x"); errCode(err) != deploy.CodeNotFound {
		t.Fatalf("second remove: %v", err)
	}
}

func TestListReportsWorkspaces(t *testing.T) {
	h := testdeploy.New(t)
	_, _ = h.Deploy(t, testdeploy.Request("production"))
	_, _ = startDev(t, h, devReq("feat-x"))
	list := h.D.List(context.Background())
	if len(list) != 2 {
		t.Fatalf("list = %+v", list)
	}
	if list[0].Workspace != "feat-x" || list[0].Mode != "dev" || !list[0].Running || list[0].URL != "https://feat-x.app.test" {
		t.Errorf("list[0] = %+v", list[0])
	}
	if list[1].Workspace != "production" || list[1].Mode != "deploy" || list[1].HostPort == 0 {
		t.Errorf("list[1] = %+v", list[1])
	}
}

func TestLogs(t *testing.T) {
	h := testdeploy.New(t)
	_, _ = h.Deploy(t, testdeploy.Request("production"))
	var out bytes.Buffer
	if err := h.D.Logs(context.Background(), "app", "production", 100, false, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "log from sagan_app_production_") {
		t.Fatalf("logs = %q", out.String())
	}
	if err := h.D.Logs(context.Background(), "app", "ghost", 100, false, &out); errCode(err) != deploy.CodeNotFound {
		t.Fatalf("err = %v", err)
	}
}

func TestEnvIsInjectedOnNextDeploy(t *testing.T) {
	h := testdeploy.New(t)
	if err := h.D.EnvSet("app", "production", map[string]string{"API_KEY": "s3cr3t", "B": "2"}); err != nil {
		t.Fatal(err)
	}
	_, _ = h.Deploy(t, testdeploy.Request("production"))
	ws, _ := h.State.Get("app", "production")
	c, _ := h.RT.Container(ws.Container)
	if c.Spec.Env["API_KEY"] != "s3cr3t" {
		t.Fatalf("env = %v", c.Spec.Env)
	}
	if keys, _ := h.D.EnvKeys("app", "production"); !slices.Equal(keys, []string{"API_KEY", "B"}) {
		t.Fatalf("keys = %v", keys)
	}
	if err := h.D.EnvUnset("app", "production", []string{"B"}); err != nil {
		t.Fatal(err)
	}
	if keys, _ := h.D.EnvKeys("app", "production"); !slices.Equal(keys, []string{"API_KEY"}) {
		t.Fatalf("keys = %v", keys)
	}
}

func TestEnvValidation(t *testing.T) {
	h := testdeploy.New(t)
	if err := h.D.EnvSet("app", "production", map[string]string{"1BAD": "x"}); errCode(err) != deploy.CodeInvalid {
		t.Fatalf("err = %v", err)
	}
	if err := h.D.EnvSet("../etc", "production", map[string]string{"A": "x"}); errCode(err) != deploy.CodeInvalid {
		t.Fatalf("err = %v", err)
	}
}
