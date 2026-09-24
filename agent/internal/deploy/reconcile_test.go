package deploy_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/borgim/sagansync/agent/internal/deploy"
	"github.com/borgim/sagansync/agent/internal/events"
	"github.com/borgim/sagansync/agent/internal/runtime"
	"github.com/borgim/sagansync/agent/internal/testutil"
	"github.com/borgim/sagansync/agent/internal/testutil/testdeploy"
)

func TestReconcileRestartsStoppedContainer(t *testing.T) {
	h := testdeploy.New(t)
	ctx := context.Background()
	_, _ = h.Deploy(t, testdeploy.Request("production"))
	ws, _ := h.State.Get("app", "production")
	if err := h.RT.Stop(ctx, ws.Container, 0); err != nil {
		t.Fatal(err)
	}
	h.Routes.Reset() // a freshly started daemon has an empty route table
	if err := h.D.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	c, ok := h.RT.Container(ws.Container)
	if !ok || !c.Running {
		t.Fatalf("container = %+v, %v", c, ok)
	}
	after, _ := h.State.Get("app", "production")
	if after.HostPort != c.HostPort {
		t.Errorf("state port %d, container port %d", after.HostPort, c.HostPort)
	}
	if got, want := h.Routes.Get("app.test"), fmt.Sprintf("127.0.0.1:%d", c.HostPort); got != want {
		t.Fatalf("route = %q, want %q", got, want)
	}
}

func TestReconcileRecreatesMissingContainer(t *testing.T) {
	h := testdeploy.New(t)
	ctx := context.Background()
	_, _ = h.Deploy(t, testdeploy.Request("production"))
	ws, _ := h.State.Get("app", "production")
	_ = h.RT.Remove(ctx, ws.Container)
	if err := h.D.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	c, ok := h.RT.Container(ws.Container)
	if !ok || !c.Running || c.Spec.Image != deploy.ImageName("app", "production", ws.Release) {
		t.Fatalf("container = %+v, %v", c, ok)
	}
}

func TestReconcileRecreatesDevContainer(t *testing.T) {
	h := testdeploy.New(t)
	ctx := context.Background()
	if _, err := startDev(t, h, devReq("feat-x")); err != nil {
		t.Fatal(err)
	}
	ws, _ := h.State.Get("app", "feat-x")
	_ = h.RT.Remove(ctx, ws.Container)
	if err := h.D.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	c, ok := h.RT.Container(ws.Container)
	if !ok || len(c.Spec.Binds) != 1 || strings.Join(c.Spec.Command, " ") != "npm run dev" {
		t.Fatalf("container = %+v, %v", c, ok)
	}
}

func orphan(t *testing.T, h *testdeploy.Harness, name, workspace, image string) {
	t.Helper()
	ctx := context.Background()
	spec := runtime.ContainerSpec{Name: name, Image: image, InternalPort: 3000, Labels: map[string]string{
		runtime.LabelManaged: "true", runtime.LabelProject: "app", runtime.LabelWorkspace: workspace}}
	if err := h.RT.Create(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := h.RT.Start(ctx, name); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileRemovesOrphans(t *testing.T) {
	h := testdeploy.New(t)
	_, _ = h.Deploy(t, testdeploy.Request("production"))
	ws, _ := h.State.Get("app", "production")
	orphan(t, h, "sagan_app_ghost_1", "ghost", deploy.ImageName("app", "production", ws.Release))
	orphan(t, h, "sagan_app_production_stale", "production", deploy.ImageName("app", "production", ws.Release))
	if err := h.D.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	names := h.RT.Names()
	if len(names) != 1 || names[0] != ws.Container {
		t.Fatalf("containers = %v, want only %s", names, ws.Container)
	}
}

func TestReconcileSkipsWorkspaceWithOperationInProgress(t *testing.T) {
	h := testdeploy.New(t)
	ctx := context.Background()
	if err := h.RT.Build(ctx, strings.NewReader(""), "localhost/seed:1", func(string) {}); err != nil {
		t.Fatal(err)
	}
	orphan(t, h, "sagan_app_production_inflight", "production", "localhost/seed:1")

	entered, unblock := make(chan struct{}), make(chan struct{})
	var once sync.Once
	h.RT.BuildHook = func(string) { once.Do(func() { close(entered); <-unblock }) }
	archive := testutil.App(t)
	errc := make(chan error, 1)
	go func() { errc <- h.D.Deploy(ctx, testdeploy.Request("production"), archive, &events.Recorder{}) }()
	<-entered

	if err := h.D.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.RT.Container("sagan_app_production_inflight"); !ok {
		t.Fatal("reconcile removed a container of a workspace with a deploy in progress")
	}
	close(unblock)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if err := h.D.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.RT.Container("sagan_app_production_inflight"); ok {
		t.Fatal("orphan survived once the workspace was free")
	}
}

// A deploy that finishes while Reconcile is busy with another workspace must
// not be rolled back by Reconcile's earlier snapshot of the state.
func TestReconcileDoesNotRollBackDeployFinishedDuringPass(t *testing.T) {
	h := testdeploy.New(t)
	ctx := context.Background()
	for _, ws := range []string{"alpha", "beta"} {
		if _, err := h.Deploy(t, testdeploy.Request(ws)); err != nil {
			t.Fatal(err)
		}
	}
	alpha, _ := h.State.Get("app", "alpha")
	blocked, unblock := make(chan struct{}), make(chan struct{})
	var once sync.Once
	h.RT.InspectHook = func(name string) {
		if name == alpha.Container {
			once.Do(func() { close(blocked); <-unblock })
		}
	}
	errc := make(chan error, 1)
	go func() { errc <- h.D.Reconcile(ctx) }()
	<-blocked
	if _, err := h.Deploy(t, testdeploy.Request("beta")); err != nil {
		t.Fatal(err)
	}
	fresh, _ := h.State.Get("app", "beta")
	close(unblock)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	after, _ := h.State.Get("app", "beta")
	if after.Release != fresh.Release {
		t.Fatalf("Reconcile rolled beta back from %s to %s", fresh.Release, after.Release)
	}
	if c, ok := h.RT.Container(fresh.Container); !ok || !c.Running {
		t.Fatal("Reconcile removed the freshly deployed container")
	}
}
