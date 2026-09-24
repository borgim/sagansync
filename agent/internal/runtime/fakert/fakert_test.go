package fakert

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/borgim/sagansync/agent/internal/runtime"
)

func TestHealthyContainerServesHTTP(t *testing.T) {
	f := New()
	defer f.Close()
	ctx := context.Background()
	if err := f.Build(ctx, strings.NewReader("tar"), "localhost/img:1", func(string) {}); err != nil {
		t.Fatal(err)
	}
	spec := runtime.ContainerSpec{Name: "c1", Image: "localhost/img:1", Labels: map[string]string{runtime.LabelManaged: "true"}}
	if err := f.Create(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := f.Start(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	info, err := f.Inspect(ctx, "c1")
	if err != nil || !info.Running || info.HostPort == 0 {
		t.Fatalf("Inspect = %+v, %v", info, err)
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/health", info.HostPort))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "localhost/img:1" {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
	var logs bytes.Buffer
	_ = f.Logs(ctx, "c1", 50, false, &logs)
	if logs.String() != "log from c1\n" {
		t.Fatalf("logs = %q", logs.String())
	}
	if err := f.Remove(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Inspect(ctx, "c1"); !errors.Is(err, runtime.ErrNotFound) {
		t.Fatalf("Inspect after Remove = %v", err)
	}
}

func TestCrashAndBuildFailure(t *testing.T) {
	f := New()
	defer f.Close()
	ctx := context.Background()
	f.BuildErr = errors.New("npm ERR")
	if err := f.Build(ctx, strings.NewReader(""), "localhost/bad:1", func(string) {}); err == nil {
		t.Fatal("Build ignored BuildErr")
	}
	f.BuildErr = nil
	_ = f.Build(ctx, strings.NewReader(""), "localhost/img:1", func(string) {})
	f.Behave = func(runtime.ContainerSpec) Behavior { return Behavior{Crash: true} }
	_ = f.Create(ctx, runtime.ContainerSpec{Name: "c1", Image: "localhost/img:1"})
	_ = f.Start(ctx, "c1")
	info, _ := f.Inspect(ctx, "c1")
	if info.Running || info.ExitCode != 1 {
		t.Fatalf("crashed container = %+v", info)
	}
	if err := f.Create(ctx, runtime.ContainerSpec{Name: "c2", Image: "localhost/missing:1"}); err == nil {
		t.Fatal("Create accepted a missing image")
	}
}
