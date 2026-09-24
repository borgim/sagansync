// Package testdeploy wires a Deployer to fakes for tests of deploy, api and cmd.
package testdeploy

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/borgim/sagansync/agent/internal/deploy"
	"github.com/borgim/sagansync/agent/internal/envstore"
	"github.com/borgim/sagansync/agent/internal/events"
	"github.com/borgim/sagansync/agent/internal/runtime/fakert"
	"github.com/borgim/sagansync/agent/internal/state"
	"github.com/borgim/sagansync/agent/internal/testutil"
)

type Routes struct {
	mu sync.Mutex
	m  map[string]string
}

func (r *Routes) Set(host, upstream string) { r.mu.Lock(); r.m[host] = upstream; r.mu.Unlock() }
func (r *Routes) Delete(host string)        { r.mu.Lock(); delete(r.m, host); r.mu.Unlock() }
func (r *Routes) Get(host string) string    { r.mu.Lock(); defer r.mu.Unlock(); return r.m[host] }
func (r *Routes) Len() int                  { r.mu.Lock(); defer r.mu.Unlock(); return len(r.m) }
func (r *Routes) Reset()                    { r.mu.Lock(); r.m = map[string]string{}; r.mu.Unlock() }

type Certs struct {
	mu    sync.Mutex
	Err   error
	hosts []string
}

func (c *Certs) Ensure(_ context.Context, host string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hosts = append(c.hosts, host)
	return c.Err
}

func (c *Certs) Hosts() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.hosts...)
}

type Harness struct {
	D      *deploy.Deployer
	RT     *fakert.Fake
	State  *state.Store
	Routes *Routes
	Certs  *Certs
	SrvDir string
}

// New builds a Deployer with short timings and a clock that advances one
// second per call, so release ids never collide.
func New(t testing.TB) *Harness {
	t.Helper()
	dir := t.TempDir()
	rt := fakert.New()
	t.Cleanup(rt.Close)
	st, err := state.Open(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	h := &Harness{RT: rt, State: st, Routes: &Routes{m: map[string]string{}}, Certs: &Certs{}, SrvDir: filepath.Join(dir, "srv")}
	cfg := deploy.DefaultConfig(h.SrvDir)
	cfg.Drain = 10 * time.Millisecond
	cfg.StopTimeout = 0
	cfg.HealthInterval = 10 * time.Millisecond
	cfg.DevHealthTimeout = 300 * time.Millisecond
	var tick atomic.Int64
	base := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	cfg.Now = func() time.Time { return base.Add(time.Duration(tick.Add(1)) * time.Second) }
	h.D = deploy.New(rt, st, envstore.New(filepath.Join(dir, "env")), h.Routes, h.Certs, cfg)
	return h
}

// Request is a valid deploy request for project "app" on domain "app.test".
func Request(workspace string) deploy.Request {
	return deploy.Request{Project: "app", Workspace: workspace, Domain: "app.test", InternalPort: 3000,
		HealthPath: "/health", HealthTimeout: 300 * time.Millisecond, Sha: "abc1234def"}
}

func (h *Harness) Deploy(t testing.TB, req deploy.Request) (*events.Recorder, error) {
	rec := &events.Recorder{}
	err := h.D.Deploy(context.Background(), req, testutil.App(t), rec)
	return rec, err
}
