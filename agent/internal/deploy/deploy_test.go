package deploy_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/borgim/sagansync/agent/internal/deploy"
	"github.com/borgim/sagansync/agent/internal/events"
	"github.com/borgim/sagansync/agent/internal/runtime"
	"github.com/borgim/sagansync/agent/internal/runtime/fakert"
	"github.com/borgim/sagansync/agent/internal/state"
	"github.com/borgim/sagansync/agent/internal/testutil"
	"github.com/borgim/sagansync/agent/internal/testutil/testdeploy"
)

func errCode(err error) string {
	var oe *deploy.OpError
	if errors.As(err, &oe) {
		return oe.Code
	}
	return ""
}

func fetch(t *testing.T, upstream string) string {
	t.Helper()
	resp, err := http.Get("http://" + upstream + "/")
	if err != nil {
		t.Fatalf("GET %s: %v", upstream, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func releases(t *testing.T, h *testdeploy.Harness, ws string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(h.SrvDir, "app", ws, "releases"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range entries {
		ids = append(ids, e.Name())
	}
	return ids
}

func TestDeployFirstRelease(t *testing.T) {
	h := testdeploy.New(t)
	h.RT.BuildLines = []string{"STEP 1/2: FROM node"}
	rec, err := h.Deploy(t, testdeploy.Request("production"))
	if err != nil {
		t.Fatal(err)
	}
	ws, ok := h.State.Get("app", "production")
	if !ok || ws.Host != "app.test" || ws.Mode != state.ModeDeploy || ws.HostPort == 0 {
		t.Fatalf("state = %+v, %v", ws, ok)
	}
	if !strings.HasSuffix(ws.Release, "-abc1234") {
		t.Errorf("release id %q should end with the short sha", ws.Release)
	}
	if got, want := h.Routes.Get("app.test"), fmt.Sprintf("127.0.0.1:%d", ws.HostPort); got != want {
		t.Fatalf("route = %q, want %q", got, want)
	}
	if body := fetch(t, h.Routes.Get("app.test")); body != deploy.ImageName("app", "production", ws.Release) {
		t.Fatalf("app served %q", body)
	}
	if got := rec.Steps(); !slices.Equal(got, []string{"extract", "build", "start", "health", "tls"}) {
		t.Errorf("steps = %v", got)
	}
	if !rec.Has("log", "") {
		t.Error("build log lines were not streamed")
	}
	last := rec.Last()
	if last.Type != "done" || last.URL != "https://app.test" || last.Release != ws.Release || last.HostPort != ws.HostPort {
		t.Errorf("last event = %+v", last)
	}
	if _, err := os.Stat(filepath.Join(h.SrvDir, "app", "production", "releases", ws.Release, "Dockerfile")); err != nil {
		t.Errorf("release files missing: %v", err)
	}
	c, _ := h.RT.Container(ws.Container)
	if c.Spec.Labels[runtime.LabelProject] != "app" || c.Spec.Labels[runtime.LabelManaged] != "true" {
		t.Errorf("labels = %v", c.Spec.Labels)
	}
}

func TestRedeploySwapsAndDrainsOld(t *testing.T) {
	h := testdeploy.New(t)
	if _, err := h.Deploy(t, testdeploy.Request("production")); err != nil {
		t.Fatal(err)
	}
	first, _ := h.State.Get("app", "production")
	rec, err := h.Deploy(t, testdeploy.Request("production"))
	if err != nil {
		t.Fatal(err)
	}
	second, _ := h.State.Get("app", "production")
	if second.Release == first.Release || second.Container == first.Container {
		t.Fatalf("release did not change: %+v", second)
	}
	if body := fetch(t, h.Routes.Get("app.test")); body != deploy.ImageName("app", "production", second.Release) {
		t.Fatalf("route serves %q", body)
	}
	if _, ok := h.RT.Container(first.Container); ok {
		t.Fatal("old container was not removed")
	}
	if !slices.Contains(rec.Steps(), "drain") {
		t.Errorf("steps = %v, want a drain step", rec.Steps())
	}
}

func TestFailedBuildKeepsCurrentRelease(t *testing.T) {
	h := testdeploy.New(t)
	_, _ = h.Deploy(t, testdeploy.Request("production"))
	first, _ := h.State.Get("app", "production")
	h.RT.BuildErr = errors.New("npm ERR! missing script: build")
	_, err := h.Deploy(t, testdeploy.Request("production"))
	if errCode(err) != deploy.CodeBuild || !strings.Contains(err.Error(), "missing script") {
		t.Fatalf("err = %v", err)
	}
	if now, _ := h.State.Get("app", "production"); now.Release != first.Release {
		t.Fatal("state changed after a failed build")
	}
	if c, ok := h.RT.Container(first.Container); !ok || !c.Running {
		t.Fatal("current container affected by a failed build")
	}
	if ids := releases(t, h, "production"); !slices.Equal(ids, []string{first.Release}) {
		t.Fatalf("releases = %v", ids)
	}
}

func TestCrashingContainerIsRolledBack(t *testing.T) {
	h := testdeploy.New(t)
	_, _ = h.Deploy(t, testdeploy.Request("production"))
	first, _ := h.State.Get("app", "production")
	h.RT.Behave = func(runtime.ContainerSpec) fakert.Behavior { return fakert.Behavior{Crash: true} }
	_, err := h.Deploy(t, testdeploy.Request("production"))
	var oe *deploy.OpError
	if !errors.As(err, &oe) || oe.Code != deploy.CodeHealth || !strings.Contains(oe.Msg, "exited with code 1") {
		t.Fatalf("err = %v", err)
	}
	if len(oe.Logs) == 0 || !strings.HasPrefix(oe.Logs[0], "log from sagan_app_production_") {
		t.Errorf("logs = %v", oe.Logs)
	}
	if names := h.RT.Names(); !slices.Equal(names, []string{first.Container}) {
		t.Fatalf("containers = %v, want only %s", names, first.Container)
	}
	if body := fetch(t, h.Routes.Get("app.test")); body != deploy.ImageName("app", "production", first.Release) {
		t.Fatalf("route changed to %q", body)
	}
}

func TestUnhealthyContainerTimesOut(t *testing.T) {
	h := testdeploy.New(t)
	h.RT.Behave = func(runtime.ContainerSpec) fakert.Behavior { return fakert.Behavior{Status: 500} }
	_, err := h.Deploy(t, testdeploy.Request("production"))
	if errCode(err) != deploy.CodeHealth || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("err = %v", err)
	}
	if h.Routes.Len() != 0 {
		t.Fatal("a route was registered for an unhealthy release")
	}
	if len(releases(t, h, "production")) != 0 {
		t.Fatal("failed release directory left behind")
	}
}

func TestConcurrentDeploySameWorkspaceIsBusy(t *testing.T) {
	h := testdeploy.New(t)
	entered, unblock := make(chan struct{}), make(chan struct{})
	var once sync.Once
	h.RT.BuildHook = func(string) { once.Do(func() { close(entered); <-unblock }) }
	archive := testutil.App(t)
	errc := make(chan error, 1)
	go func() {
		errc <- h.D.Deploy(context.Background(), testdeploy.Request("production"), archive, &events.Recorder{})
	}()
	<-entered
	_, err := h.Deploy(t, testdeploy.Request("production"))
	if errCode(err) != deploy.CodeBusy {
		t.Fatalf("err = %v, want busy", err)
	}
	close(unblock)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestDeploysToDifferentWorkspacesRunConcurrently(t *testing.T) {
	h := testdeploy.New(t)
	var both sync.WaitGroup
	both.Add(2)
	h.RT.BuildHook = func(string) { both.Done(); both.Wait() } // deadlocks if deploys are serialized
	a, b := testutil.App(t), testutil.App(t)
	errc := make(chan error, 2)
	go func() {
		errc <- h.D.Deploy(context.Background(), testdeploy.Request("production"), a, &events.Recorder{})
	}()
	go func() { errc <- h.D.Deploy(context.Background(), testdeploy.Request("feat-x"), b, &events.Recorder{}) }()
	for i := 0; i < 2; i++ {
		select {
		case err := <-errc:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("deploys to different workspaces blocked each other")
		}
	}
	if h.Routes.Get("app.test") == "" || h.Routes.Get("feat-x.app.test") == "" {
		t.Fatal("missing routes")
	}
}

func TestTruncatedUploadLeavesNoRelease(t *testing.T) {
	h := testdeploy.New(t)
	_, _ = h.Deploy(t, testdeploy.Request("production"))
	first, _ := h.State.Get("app", "production")
	full := testutil.App(t).Bytes()
	err := h.D.Deploy(context.Background(), testdeploy.Request("production"), bytes.NewReader(full[:len(full)/2]), &events.Recorder{})
	if errCode(err) != deploy.CodeArchive {
		t.Fatalf("err = %v, want invalid_archive", err)
	}
	if ids := releases(t, h, "production"); !slices.Equal(ids, []string{first.Release}) {
		t.Fatalf("releases = %v", ids)
	}
	if body := fetch(t, h.Routes.Get("app.test")); body != deploy.ImageName("app", "production", first.Release) {
		t.Fatal("current release affected by a truncated upload")
	}
}

func TestRedeployWhenPreviousContainerVanished(t *testing.T) {
	h := testdeploy.New(t)
	_, _ = h.Deploy(t, testdeploy.Request("production"))
	first, _ := h.State.Get("app", "production")
	if err := h.RT.Remove(context.Background(), first.Container); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Deploy(t, testdeploy.Request("production")); err != nil {
		t.Fatalf("deploy after the old container vanished: %v", err)
	}
}

func TestHostConflict(t *testing.T) {
	h := testdeploy.New(t)
	one := testdeploy.Request("production")
	one.Project, one.Domain = "one", "shared.test"
	if _, err := h.Deploy(t, one); err != nil {
		t.Fatal(err)
	}
	two := one
	two.Project = "two"
	if _, err := h.Deploy(t, two); errCode(err) != deploy.CodeHostConflict {
		t.Fatalf("err = %v, want host_conflict", err)
	}
}

func TestRetentionKeepsThreeReleases(t *testing.T) {
	h := testdeploy.New(t)
	for i := 0; i < 5; i++ {
		if _, err := h.Deploy(t, testdeploy.Request("production")); err != nil {
			t.Fatal(err)
		}
	}
	if ids := releases(t, h, "production"); len(ids) != 3 {
		t.Fatalf("releases = %v", ids)
	}
	if n := len(h.RT.RemovedImages()); n != 2 {
		t.Fatalf("removed images = %v", h.RT.RemovedImages())
	}
}

func TestTLSFailureIsAWarning(t *testing.T) {
	h := testdeploy.New(t)
	h.Certs.Err = errors.New("rate limited")
	rec, err := h.Deploy(t, testdeploy.Request("production"))
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Has("warn", "tls_pending") || rec.Last().Type != "done" {
		t.Fatalf("events = %+v", rec.Events)
	}
}

func TestDeployWithoutDomain(t *testing.T) {
	h := testdeploy.New(t)
	req := testdeploy.Request("production")
	req.Domain = ""
	rec, err := h.Deploy(t, req)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Last().URL != "" || rec.Last().HostPort == 0 || h.Routes.Len() != 0 || len(h.Certs.Hosts()) != 0 {
		t.Fatalf("last = %+v, routes = %d", rec.Last(), h.Routes.Len())
	}
	if slices.Contains(rec.Steps(), "tls") {
		t.Error("tls step without a domain")
	}
}

func TestInvalidRequests(t *testing.T) {
	h := testdeploy.New(t)
	mutate := []func(*deploy.Request){
		func(r *deploy.Request) { r.Project = "Bad!" },
		func(r *deploy.Request) { r.Workspace = "" },
		func(r *deploy.Request) { r.InternalPort = 0 },
		func(r *deploy.Request) { r.Domain = "https://x.com" },
		func(r *deploy.Request) { r.HealthPath = "health" },
		func(r *deploy.Request) { r.HealthTimeout = -time.Second },
	}
	for i, m := range mutate {
		req := testdeploy.Request("production")
		m(&req)
		if _, err := h.Deploy(t, req); errCode(err) != deploy.CodeInvalid {
			t.Errorf("case %d: err = %v, want invalid", i, err)
		}
	}
}

// A host claimed by another workspace while this deploy was building must not
// be taken over when the new release is activated.
func TestHostClaimedDuringBuildIsNotTakenOver(t *testing.T) {
	h := testdeploy.New(t)
	one := testdeploy.Request("production")
	one.Project, one.Domain = "one", "shared.test"
	two := one
	two.Project = "two"
	h.RT.BuildHook = func(string) {
		_ = h.State.Put("one", "production", state.Workspace{Host: "shared.test", Mode: state.ModeDeploy,
			Release: "r0", Container: "sagan_one_production_r0", HostPort: 40000})
		h.Routes.Set("shared.test", "127.0.0.1:40000")
	}
	if _, err := h.Deploy(t, two); errCode(err) != deploy.CodeHostConflict {
		t.Fatalf("err = %v, want host_conflict", err)
	}
	if got := h.Routes.Get("shared.test"); got != "127.0.0.1:40000" {
		t.Errorf("route = %q, want the owner's upstream", got)
	}
	if _, ok := h.State.Get("two", "production"); ok {
		t.Error("two/production was saved")
	}
	if names := h.RT.Names(); len(names) != 0 {
		t.Errorf("containers left behind: %v", names)
	}
}
