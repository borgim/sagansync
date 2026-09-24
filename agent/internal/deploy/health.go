package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

var healthClient = &http.Client{
	Timeout:       2 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// waitHealthy polls the container until it is healthy, exits, or timeout
// passes. It returns the container's host port whenever it is known.
func (d *Deployer) waitHealthy(ctx context.Context, name, path string, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	for {
		info, err := d.rt.Inspect(ctx, name)
		if err != nil {
			return 0, fmt.Errorf("inspect container: %w", err)
		}
		if !info.Running {
			return 0, fmt.Errorf("container exited with code %d", info.ExitCode)
		}
		if info.HostPort == 0 {
			return 0, errors.New("container has no published port")
		}
		perr := probe(ctx, info.HostPort, path)
		if perr == nil {
			return info.HostPort, nil
		}
		if time.Now().After(deadline) {
			return info.HostPort, fmt.Errorf("not healthy after %s: %v", timeout, perr)
		}
		if err := sleep(ctx, d.cfg.HealthInterval); err != nil {
			return info.HostPort, err
		}
	}
}

func probe(ctx context.Context, port int, path string) error {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if path == "" {
		return probeTCP(ctx, addr)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		return err
	}
	resp, err := healthClient.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("health check returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// probeTCP treats a connection that stays open as healthy. Rootless Podman's
// port forwarder accepts connections even when nothing listens inside the
// container and closes them right after, so a successful dial is not enough.
func probeTCP(ctx context.Context, addr string) error {
	dctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var dialer net.Dialer
	c, err := dialer.DialContext(dctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	var b [1]byte
	_, err = c.Read(b[:])
	var ne net.Error
	if err == nil || (errors.As(err, &ne) && ne.Timeout()) {
		return nil
	}
	return fmt.Errorf("connection closed by the container: %v", err)
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
