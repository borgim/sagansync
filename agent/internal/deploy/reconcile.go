package deploy

import (
	"context"
	"errors"
	"fmt"

	"github.com/borgim/sagansync/agent/internal/runtime"
	"github.com/borgim/sagansync/agent/internal/state"
)

// Reconcile makes the runtime match the state: it runs at startup (after a
// reboot nothing is running) and periodically. Workspaces with an operation in
// progress are skipped.
func (d *Deployer) Reconcile(ctx context.Context) error {
	var errs []error
	for _, e := range d.st.All() {
		unlock, ok := d.locks.TryLock(e.Project, e.Workspace)
		if !ok {
			continue
		}
		if err := d.reconcileOne(ctx, e); err != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %w", e.Project, e.Workspace, err))
		}
		unlock()
	}
	list, err := d.rt.List(ctx)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, c := range list {
		p, w := c.Labels[runtime.LabelProject], c.Labels[runtime.LabelWorkspace]
		unlock, ok := d.locks.TryLock(p, w)
		if !ok {
			continue
		}
		// Re-read the state under the lock: a deploy may have finished after
		// the list was taken and made this container the current one.
		if ws, ok := d.st.Get(p, w); !ok || ws.Container != c.Name {
			_ = d.rt.Stop(ctx, c.Name, d.cfg.StopTimeout)
			if err := d.rt.Remove(ctx, c.Name); err != nil && !errors.Is(err, runtime.ErrNotFound) {
				errs = append(errs, err)
			}
		}
		unlock()
	}
	return errors.Join(errs...)
}

func (d *Deployer) reconcileOne(ctx context.Context, e state.Entry) error {
	ws := e.WS
	info, err := d.rt.Inspect(ctx, ws.Container)
	switch {
	case errors.Is(err, runtime.ErrNotFound):
		var spec runtime.ContainerSpec
		if ws.Mode == state.ModeDev {
			spec, err = d.devSpec(e.Project, e.Workspace, ws.Release, ws.InternalPort, ws.Command)
		} else {
			spec, err = d.deploySpec(e.Project, e.Workspace, ws.Release, ws.InternalPort)
		}
		if err != nil {
			return err
		}
		if err := d.rt.Create(ctx, spec); err != nil {
			return fmt.Errorf("recreate container: %w", err)
		}
	case err != nil:
		return err
	}
	if !info.Running {
		if err := d.rt.Start(ctx, ws.Container); err != nil {
			return fmt.Errorf("start container: %w", err)
		}
		if info, err = d.rt.Inspect(ctx, ws.Container); err != nil {
			return err
		}
	}
	if info.HostPort != 0 && info.HostPort != ws.HostPort {
		ws.HostPort = info.HostPort
		if err := d.st.Put(e.Project, e.Workspace, ws); err != nil {
			return err
		}
	}
	if ws.Host != "" && ws.HostPort != 0 {
		d.routes.Set(ws.Host, fmt.Sprintf("127.0.0.1:%d", ws.HostPort))
	}
	return nil
}
