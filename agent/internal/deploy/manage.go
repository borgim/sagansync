package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/borgim/sagansync/agent/internal/envstore"
	"github.com/borgim/sagansync/agent/internal/runtime"
	"github.com/borgim/sagansync/agent/internal/validate"
)

func checkNames(p, w string) error {
	if err := validate.Name("project", p); err != nil {
		return invalid(err)
	}
	if err := validate.Name("workspace", w); err != nil {
		return invalid(err)
	}
	return nil
}

func notFound(p, w string) error {
	return &OpError{Code: CodeNotFound, Msg: fmt.Sprintf("workspace %s/%s does not exist", p, w)}
}

// Remove deletes a workspace: route, container, images, volume, files, env
// and state.
func (d *Deployer) Remove(ctx context.Context, p, w string) error {
	if err := checkNames(p, w); err != nil {
		return err
	}
	unlock, err := d.lock(p, w)
	if err != nil {
		return err
	}
	defer unlock()
	ws, ok := d.st.Get(p, w)
	if !ok {
		return notFound(p, w)
	}
	_ = d.rt.Stop(ctx, ws.Container, d.cfg.StopTimeout)
	if err := d.rt.Remove(ctx, ws.Container); err != nil && !errors.Is(err, runtime.ErrNotFound) {
		return internalErr(err)
	}
	// The route goes only once the container is gone: if removing it fails,
	// the workspace stays routed and reconciliation can bring it back.
	if ws.Host != "" {
		d.routes.Delete(ws.Host)
	}
	entries, _ := os.ReadDir(d.releasesDir(p, w))
	for _, e := range entries {
		_ = d.rt.RemoveImage(ctx, ImageName(p, w, e.Name()))
	}
	_ = d.rt.RemoveImage(ctx, ImageName(p, w, "dev"))
	_ = d.rt.RemoveVolume(ctx, volumeName(p, w))
	if err := os.RemoveAll(d.workspaceDir(p, w)); err != nil {
		return internalErr(err)
	}
	if err := d.env.Delete(p, w); err != nil {
		return internalErr(err)
	}
	if err := d.st.Delete(p, w); err != nil {
		return internalErr(err)
	}
	return nil
}

type Status struct {
	Project   string    `json:"project"`
	Workspace string    `json:"workspace"`
	Mode      string    `json:"mode"`
	Release   string    `json:"release"`
	URL       string    `json:"url,omitempty"`
	HostPort  int       `json:"hostPort"`
	Running   bool      `json:"running"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (d *Deployer) List(ctx context.Context) []Status {
	out := []Status{}
	for _, e := range d.st.All() {
		info, err := d.rt.Inspect(ctx, e.WS.Container)
		out = append(out, Status{Project: e.Project, Workspace: e.Workspace, Mode: e.WS.Mode,
			Release: e.WS.Release, URL: urlFor(e.WS.Host), HostPort: e.WS.HostPort,
			Running: err == nil && info.Running, UpdatedAt: e.WS.UpdatedAt})
	}
	return out
}

func (d *Deployer) Logs(ctx context.Context, p, w string, tail int, follow bool, out io.Writer) error {
	if err := checkNames(p, w); err != nil {
		return err
	}
	ws, ok := d.st.Get(p, w)
	if !ok {
		return notFound(p, w)
	}
	return d.rt.Logs(ctx, ws.Container, tail, follow, out)
}

func envErr(err error) error {
	if errors.Is(err, validate.ErrInvalid) {
		return invalid(err)
	}
	return internalErr(err)
}

func (d *Deployer) EnvKeys(p, w string) ([]string, error) {
	if err := checkNames(p, w); err != nil {
		return nil, err
	}
	m, err := d.env.Get(p, w)
	if err != nil {
		return nil, internalErr(err)
	}
	return envstore.Keys(m), nil
}

func (d *Deployer) EnvSet(p, w string, kv map[string]string) error {
	if err := checkNames(p, w); err != nil {
		return err
	}
	if err := d.env.Set(p, w, kv); err != nil {
		return envErr(err)
	}
	return nil
}

func (d *Deployer) EnvUnset(p, w string, keys []string) error {
	if err := checkNames(p, w); err != nil {
		return err
	}
	if err := d.env.Unset(p, w, keys); err != nil {
		return envErr(err)
	}
	return nil
}
