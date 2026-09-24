// Package deploy orchestrates builds, zero-downtime swaps and workspace
// lifecycle on top of a container runtime.
package deploy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/borgim/sagansync/agent/internal/domains"
	"github.com/borgim/sagansync/agent/internal/envstore"
	"github.com/borgim/sagansync/agent/internal/events"
	"github.com/borgim/sagansync/agent/internal/release"
	"github.com/borgim/sagansync/agent/internal/runtime"
	"github.com/borgim/sagansync/agent/internal/state"
	"github.com/borgim/sagansync/agent/internal/validate"
)

const (
	CodeInvalid          = "invalid"
	CodeBusy             = "busy"
	CodeBuild            = "build_failed"
	CodeHealth           = "health_failed"
	CodeArchive          = "invalid_archive"
	CodeHostConflict     = "host_conflict"
	CodeNotFound         = "not_found"
	CodeProductionLocked = "production_locked"
	CodeInternal         = "internal"
)

// OpError is an operation failure the CLI can show as is.
type OpError struct {
	Code string
	Msg  string
	Logs []string
}

func (e *OpError) Error() string { return e.Code + ": " + e.Msg }

func invalid(err error) error     { return &OpError{Code: CodeInvalid, Msg: err.Error()} }
func internalErr(err error) error { return &OpError{Code: CodeInternal, Msg: err.Error()} }

type Routes interface {
	Set(host, upstream string)
	Delete(host string)
}

type Certs interface {
	Ensure(ctx context.Context, host string) error
}

type Config struct {
	SrvDir           string
	Keep             int
	Drain            time.Duration
	StopTimeout      time.Duration
	HealthInterval   time.Duration
	DevHealthTimeout time.Duration
	LogTail          int
	Now              func() time.Time
}

func DefaultConfig(srvDir string) Config {
	return Config{SrvDir: srvDir, Keep: 3, Drain: 10 * time.Second, StopTimeout: 10 * time.Second,
		HealthInterval: 500 * time.Millisecond, DevHealthTimeout: 30 * time.Second, LogTail: 50, Now: time.Now}
}

type Deployer struct {
	rt     runtime.Runtime
	st     *state.Store
	locks  *state.Locks
	env    *envstore.Store
	routes Routes
	certs  Certs
	cfg    Config
}

func New(rt runtime.Runtime, st *state.Store, env *envstore.Store, routes Routes, certs Certs, cfg Config) *Deployer {
	return &Deployer{rt: rt, st: st, locks: state.NewLocks(), env: env, routes: routes, certs: certs, cfg: cfg}
}

// Names use "_" as separator, which project and workspace names cannot
// contain, so different (project, workspace) pairs never collide.
func ContainerName(p, w, id string) string { return "sagan_" + p + "_" + w + "_" + id }
func ImageName(p, w, tag string) string    { return "localhost/sagan_" + p + "_" + w + ":" + tag }
func volumeName(p, w string) string        { return "sagan_" + p + "_" + w + "_node_modules" }

func labels(p, w, id string) map[string]string {
	return map[string]string{runtime.LabelManaged: "true", runtime.LabelProject: p,
		runtime.LabelWorkspace: w, runtime.LabelRelease: id}
}

func (d *Deployer) workspaceDir(p, w string) string { return filepath.Join(d.cfg.SrvDir, p, w) }
func (d *Deployer) releasesDir(p, w string) string {
	return filepath.Join(d.workspaceDir(p, w), "releases")
}
func (d *Deployer) devDir(p, w string) string { return filepath.Join(d.workspaceDir(p, w), "dev") }

type Request struct {
	Project       string
	Workspace     string
	Domain        string
	PreviewDomain string
	InternalPort  int
	HealthPath    string
	HealthTimeout time.Duration
	Sha           string
}

func (r *Request) check() error {
	if r.HealthTimeout == 0 {
		r.HealthTimeout = 60 * time.Second
	}
	checks := []error{
		validate.Name("project", r.Project),
		validate.Name("workspace", r.Workspace),
		validate.Port("internalPort", r.InternalPort),
		validate.HealthPath("healthPath", r.HealthPath),
	}
	if r.Domain != "" {
		checks = append(checks, validate.Domain("domain", r.Domain))
	}
	if r.PreviewDomain != "" {
		checks = append(checks, validate.Domain("previewDomain", r.PreviewDomain))
	}
	for _, err := range checks {
		if err != nil {
			return invalid(err)
		}
	}
	if r.HealthTimeout < 0 || r.HealthTimeout > 10*time.Minute {
		return &OpError{Code: CodeInvalid, Msg: "healthTimeout must be positive and at most 10m"}
	}
	return nil
}

func (d *Deployer) resolveHost(r Request) (string, error) {
	host := domains.Host(r.Project, r.Workspace, r.Domain, r.PreviewDomain)
	if host == "" {
		return "", nil
	}
	if err := validate.Domain("host", host); err != nil {
		return "", invalid(err)
	}
	if e, ok := d.st.HostOwner(host); ok && (e.Project != r.Project || e.Workspace != r.Workspace) {
		return "", &OpError{Code: CodeHostConflict, Msg: fmt.Sprintf("%s is already used by %s/%s", host, e.Project, e.Workspace)}
	}
	return host, nil
}

func urlFor(host string) string {
	if host == "" {
		return ""
	}
	return "https://" + host
}

func (d *Deployer) lock(p, w string) (func(), error) {
	unlock, ok := d.locks.TryLock(p, w)
	if !ok {
		return nil, &OpError{Code: CodeBusy, Msg: fmt.Sprintf("another operation is running on %s/%s", p, w)}
	}
	return unlock, nil
}

// Deploy builds the uploaded snapshot and swaps traffic to it only after it is
// healthy. On any failure the running release is left untouched.
func (d *Deployer) Deploy(ctx context.Context, req Request, archive io.Reader, em events.Emitter) error {
	if err := req.check(); err != nil {
		return err
	}
	host, err := d.resolveHost(req)
	if err != nil {
		return err
	}
	unlock, err := d.lock(req.Project, req.Workspace)
	if err != nil {
		return err
	}
	defer unlock()
	p, w := req.Project, req.Workspace

	em.Emit(events.Step("extract"))
	relDir := d.releasesDir(p, w)
	id, err := release.Allocate(relDir, release.NewID(d.cfg.Now(), req.Sha))
	if err != nil {
		return internalErr(err)
	}
	dir := filepath.Join(relDir, id)
	if err := release.Extract(archive, dir, release.DefaultLimits); err != nil {
		os.RemoveAll(dir)
		return &OpError{Code: CodeArchive, Msg: err.Error()}
	}

	em.Emit(events.Step("build"))
	image := ImageName(p, w, id)
	if err := d.build(ctx, dir, image, em); err != nil {
		os.RemoveAll(dir)
		return err
	}
	spec, err := d.deploySpec(p, w, id, req.InternalPort)
	if err != nil {
		os.RemoveAll(dir)
		return internalErr(err)
	}
	port, err := d.activate(ctx, activation{req: req, host: host, id: id, spec: spec, mode: state.ModeDeploy,
		healthPath: req.HealthPath, healthTimeout: req.HealthTimeout, strict: true, drain: d.cfg.Drain}, em)
	if err != nil {
		os.RemoveAll(dir)
		_ = d.rt.RemoveImage(ctx, image)
		return err
	}
	removed, _ := release.Prune(relDir, d.cfg.Keep, id)
	for _, old := range removed {
		_ = d.rt.RemoveImage(ctx, ImageName(p, w, old))
	}
	em.Emit(events.Done(urlFor(host), id, port))
	return nil
}

func (d *Deployer) build(ctx context.Context, dir, image string, em events.Emitter) error {
	tr := release.TarDir(dir)
	defer tr.Close()
	err := d.rt.Build(ctx, tr, image, func(line string) { em.Emit(events.Log("build", line)) })
	if err != nil {
		return &OpError{Code: CodeBuild, Msg: err.Error()}
	}
	return nil
}

func (d *Deployer) deploySpec(p, w, id string, port int) (runtime.ContainerSpec, error) {
	env, err := d.env.Get(p, w)
	if err != nil {
		return runtime.ContainerSpec{}, err
	}
	return runtime.ContainerSpec{Name: ContainerName(p, w, id), Image: ImageName(p, w, id), Env: env,
		InternalPort: port, Labels: labels(p, w, id)}, nil
}

type activation struct {
	req           Request
	host          string
	id            string
	spec          runtime.ContainerSpec
	mode          string
	command       []string
	healthPath    string
	healthTimeout time.Duration
	strict        bool // a failed health check aborts (deploy) or only warns (dev)
	drain         time.Duration
}

// activate starts the new container, waits for it, points the route at it,
// records it in the state and only then retires the previous container.
func (d *Deployer) activate(ctx context.Context, a activation, em events.Emitter) (int, error) {
	p, w := a.req.Project, a.req.Workspace
	em.Emit(events.Step("start"))
	if err := d.rt.Create(ctx, a.spec); err != nil {
		return 0, internalErr(fmt.Errorf("create container: %w", err))
	}
	if err := d.rt.Start(ctx, a.spec.Name); err != nil {
		return 0, d.abort(ctx, a.spec.Name, fmt.Errorf("start container: %w", err))
	}
	em.Emit(events.Step("health"))
	port, err := d.waitHealthy(ctx, a.spec.Name, a.healthPath, a.healthTimeout)
	if err != nil {
		if a.strict || port == 0 {
			return 0, d.abort(ctx, a.spec.Name, err)
		}
		em.Emit(events.Warn(CodeHealth, err.Error()))
	}

	prev, hadPrev := d.st.Get(p, w)
	if a.host != "" {
		d.routes.Set(a.host, fmt.Sprintf("127.0.0.1:%d", port))
	}
	ws := state.Workspace{Host: a.host, Domain: a.req.Domain, PreviewDomain: a.req.PreviewDomain,
		InternalPort: a.req.InternalPort, HealthPath: a.req.HealthPath, Mode: a.mode, Release: a.id,
		Container: a.spec.Name, Command: a.command, HostPort: port, UpdatedAt: d.cfg.Now()}
	if err := d.st.Put(p, w, ws); err != nil {
		if a.host != "" {
			if hadPrev && prev.Host == a.host {
				d.routes.Set(a.host, fmt.Sprintf("127.0.0.1:%d", prev.HostPort))
			} else {
				d.routes.Delete(a.host)
			}
		}
		_ = d.rt.Remove(ctx, a.spec.Name)
		return 0, internalErr(fmt.Errorf("save state: %w", err))
	}
	if hadPrev && prev.Host != "" && prev.Host != a.host {
		d.routes.Delete(prev.Host)
	}
	if a.host != "" {
		em.Emit(events.Step("tls"))
		tctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		if err := d.certs.Ensure(tctx, a.host); err != nil {
			em.Emit(events.Warn("tls_pending", fmt.Sprintf("certificate for %s not ready yet (retrying in the background): %v", a.host, err)))
		}
		cancel()
	}
	if hadPrev && prev.Container != "" && prev.Container != a.spec.Name {
		em.Emit(events.Step("drain"))
		_ = sleep(ctx, a.drain)
		_ = d.rt.Stop(ctx, prev.Container, d.cfg.StopTimeout)
		_ = d.rt.Remove(ctx, prev.Container)
	}
	return port, nil
}

// abort collects the failed container's last log lines and removes it.
func (d *Deployer) abort(ctx context.Context, name string, cause error) error {
	logs := d.tail(ctx, name)
	_ = d.rt.Remove(ctx, name)
	return &OpError{Code: CodeHealth, Msg: cause.Error(), Logs: logs}
}

func (d *Deployer) tail(ctx context.Context, name string) []string {
	var buf bytes.Buffer
	_ = d.rt.Logs(ctx, name, d.cfg.LogTail, false, &buf)
	text := strings.TrimRight(buf.String(), "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > d.cfg.LogTail {
		lines = lines[len(lines)-d.cfg.LogTail:]
	}
	return lines
}
