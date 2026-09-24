package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/borgim/sagansync/agent/internal/events"
	"github.com/borgim/sagansync/agent/internal/release"
	"github.com/borgim/sagansync/agent/internal/runtime"
	"github.com/borgim/sagansync/agent/internal/state"
	"github.com/borgim/sagansync/agent/internal/validate"
)

type DevRequest struct {
	Request
	Command []string
	Build   bool
	Force   bool
}

// Dev runs the workspace from a bind-mounted copy of the sources, so later
// PutFile/RemoveFile calls show up in the running container.
func (d *Deployer) Dev(ctx context.Context, req DevRequest, archive io.Reader, em events.Emitter) error {
	if err := req.Request.check(); err != nil {
		return err
	}
	if req.Workspace == "production" && !req.Force {
		return &OpError{Code: CodeProductionLocked, Msg: "dev mode on production requires --force"}
	}
	if len(req.Command) == 0 {
		return &OpError{Code: CodeInvalid, Msg: "dev requires a command"}
	}
	host, err := d.resolveHost(req.Request)
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
	dir := d.devDir(p, w)
	if err := os.RemoveAll(dir); err != nil {
		return internalErr(err)
	}
	if err := release.Extract(archive, dir, release.DefaultLimits); err != nil {
		return &OpError{Code: CodeArchive, Msg: err.Error()}
	}
	image := ImageName(p, w, "dev")
	exists, err := d.rt.ImageExists(ctx, image)
	if err != nil {
		return internalErr(err)
	}
	if req.Build || !exists {
		em.Emit(events.Step("build"))
		if err := d.build(ctx, dir, image, em); err != nil {
			return err
		}
	}
	now := d.cfg.Now().UTC()
	id := fmt.Sprintf("dev-%s-%03d", now.Format("20060102-150405"), now.Nanosecond()/int(time.Millisecond))
	spec, err := d.devSpec(p, w, id, req.InternalPort, req.Command)
	if err != nil {
		return internalErr(err)
	}
	port, err := d.activate(ctx, activation{req: req.Request, host: host, id: id, spec: spec, mode: state.ModeDev,
		command: req.Command, healthTimeout: d.cfg.DevHealthTimeout, strict: false}, em)
	if err != nil {
		return err
	}
	em.Emit(events.Done(urlFor(host), id, port))
	return nil
}

func (d *Deployer) devSpec(p, w, id string, port int, command []string) (runtime.ContainerSpec, error) {
	env, err := d.env.Get(p, w)
	if err != nil {
		return runtime.ContainerSpec{}, err
	}
	if _, ok := env["NODE_ENV"]; !ok {
		env["NODE_ENV"] = "development"
	}
	return runtime.ContainerSpec{
		Name: ContainerName(p, w, id), Image: ImageName(p, w, "dev"), Command: command, Env: env,
		InternalPort: port, Labels: labels(p, w, id),
		Binds:   []runtime.Bind{{Source: d.devDir(p, w), Dest: "/app"}},
		Volumes: []runtime.Volume{{Name: volumeName(p, w), Dest: "/app/node_modules"}},
	}, nil
}

func (d *Deployer) devRoot(p, w string) (string, error) {
	if err := validate.Name("project", p); err != nil {
		return "", invalid(err)
	}
	if err := validate.Name("workspace", w); err != nil {
		return "", invalid(err)
	}
	ws, ok := d.st.Get(p, w)
	if !ok || ws.Mode != state.ModeDev {
		return "", &OpError{Code: CodeNotFound, Msg: fmt.Sprintf("%s/%s is not running in dev mode", p, w)}
	}
	return d.devDir(p, w), nil
}

func fileErr(err error) error {
	if errors.Is(err, validate.ErrInvalid) || errors.Is(err, release.ErrInvalidArchive) {
		return invalid(err)
	}
	return internalErr(err)
}

func (d *Deployer) PutFile(p, w, rel string, r io.Reader) error {
	root, err := d.devRoot(p, w)
	if err != nil {
		return err
	}
	if err := release.WriteFile(root, rel, r); err != nil {
		return fileErr(err)
	}
	return nil
}

func (d *Deployer) RemoveFile(p, w, rel string) error {
	root, err := d.devRoot(p, w)
	if err != nil {
		return err
	}
	if err := release.RemoveFile(root, rel); err != nil {
		return fileErr(err)
	}
	return nil
}
