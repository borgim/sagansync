// Package fakert is an in-memory runtime.Runtime for tests. Started
// containers are real HTTP servers on 127.0.0.1, so health checks and the
// proxy can talk to them.
package fakert

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/borgim/sagansync/agent/internal/runtime"
)

// Behavior controls what a container does when it starts.
type Behavior struct {
	Crash  bool // exits immediately with code 1
	Status int  // HTTP status served on every path; 200 when zero
}

type Container struct {
	Spec     runtime.ContainerSpec
	Running  bool
	ExitCode int
	HostPort int
}

type entry struct {
	c   Container
	srv *httptest.Server
}

func (e *entry) stop() {
	if e.srv != nil {
		e.srv.Close()
		e.srv = nil
	}
	e.c.Running = false
	e.c.HostPort = 0
}

type Fake struct {
	mu             sync.Mutex
	images         map[string]bool
	containers     map[string]*entry
	builds         []string
	removedImages  []string
	removedVolumes []string

	// Test knobs: set them before the code under test runs.
	BuildErr   error
	BuildLines []string
	BuildHook   func(tag string)
	Behave      func(spec runtime.ContainerSpec) Behavior
	InspectHook func(name string) // called before Inspect, outside the lock
}

var _ runtime.Runtime = (*Fake)(nil)

func New() *Fake {
	return &Fake{images: map[string]bool{}, containers: map[string]*entry{}}
}

func (f *Fake) Build(_ context.Context, r io.Reader, tag string, log func(string)) error {
	if _, err := io.Copy(io.Discard, r); err != nil {
		return err
	}
	if f.BuildHook != nil {
		f.BuildHook(tag)
	}
	for _, l := range f.BuildLines {
		log(l)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.BuildErr != nil {
		return f.BuildErr
	}
	f.images[tag] = true
	f.builds = append(f.builds, tag)
	return nil
}

func (f *Fake) ImageExists(_ context.Context, tag string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.images[tag], nil
}

func (f *Fake) Create(_ context.Context, spec runtime.ContainerSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.images[spec.Image] {
		return fmt.Errorf("image %s: %w", spec.Image, runtime.ErrNotFound)
	}
	if _, ok := f.containers[spec.Name]; ok {
		return fmt.Errorf("container %s already exists", spec.Name)
	}
	f.containers[spec.Name] = &entry{c: Container{Spec: spec}}
	return nil
}

func (f *Fake) Start(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.containers[name]
	if !ok {
		return fmt.Errorf("container %s: %w", name, runtime.ErrNotFound)
	}
	if e.c.Running {
		return nil
	}
	var b Behavior
	if f.Behave != nil {
		b = f.Behave(e.c.Spec)
	}
	if b.Crash {
		e.c.ExitCode = 1
		return nil
	}
	status := b.Status
	if status == 0 {
		status = http.StatusOK
	}
	body := e.c.Spec.Image
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	u, _ := url.Parse(e.srv.URL)
	e.c.HostPort, _ = strconv.Atoi(u.Port())
	e.c.Running = true
	e.c.ExitCode = 0
	return nil
}

func (f *Fake) Stop(_ context.Context, name string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.containers[name]
	if !ok {
		return fmt.Errorf("container %s: %w", name, runtime.ErrNotFound)
	}
	e.stop()
	return nil
}

func (f *Fake) Remove(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.containers[name]
	if !ok {
		return fmt.Errorf("container %s: %w", name, runtime.ErrNotFound)
	}
	e.stop()
	delete(f.containers, name)
	return nil
}

func (f *Fake) Inspect(_ context.Context, name string) (runtime.ContainerInfo, error) {
	if f.InspectHook != nil {
		f.InspectHook(name)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.containers[name]
	if !ok {
		return runtime.ContainerInfo{}, fmt.Errorf("container %s: %w", name, runtime.ErrNotFound)
	}
	return info(name, e), nil
}

func info(name string, e *entry) runtime.ContainerInfo {
	return runtime.ContainerInfo{Name: name, Running: e.c.Running, ExitCode: e.c.ExitCode,
		HostPort: e.c.HostPort, Labels: e.c.Spec.Labels}
}

func (f *Fake) Logs(_ context.Context, name string, _ int, _ bool, w io.Writer) error {
	f.mu.Lock()
	_, ok := f.containers[name]
	f.mu.Unlock()
	if !ok {
		return fmt.Errorf("container %s: %w", name, runtime.ErrNotFound)
	}
	_, err := fmt.Fprintf(w, "log from %s\n", name)
	return err
}

func (f *Fake) List(context.Context) ([]runtime.ContainerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []runtime.ContainerInfo
	for name, e := range f.containers {
		if e.c.Spec.Labels[runtime.LabelManaged] == "true" {
			out = append(out, info(name, e))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *Fake) RemoveImage(_ context.Context, tag string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.images[tag] {
		return fmt.Errorf("image %s: %w", tag, runtime.ErrNotFound)
	}
	delete(f.images, tag)
	f.removedImages = append(f.removedImages, tag)
	return nil
}

func (f *Fake) RemoveVolume(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removedVolumes = append(f.removedVolumes, name)
	return nil
}

func (f *Fake) Container(name string) (Container, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.containers[name]
	if !ok {
		return Container{}, false
	}
	return e.c, true
}

func (f *Fake) Names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for n := range f.containers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (f *Fake) Builds() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.builds...)
}

func (f *Fake) RemovedImages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.removedImages...)
}

func (f *Fake) RemovedVolumes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.removedVolumes...)
}

// Close stops every fake container's HTTP server.
func (f *Fake) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.containers {
		e.stop()
	}
}
