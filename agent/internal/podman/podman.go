// Package podman is a small client for the parts of the Podman REST API that
// sagand needs. It deliberately avoids the official bindings and their large
// dependency tree.
package podman

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/borgim/sagansync/agent/internal/runtime"
)

const apiPrefix = "/v4.0.0/libpod"

type Client struct {
	hc   *http.Client
	base string
}

var _ runtime.Runtime = (*Client)(nil)

// New talks to the Podman API socket, e.g. /run/user/1001/podman/podman.sock.
func New(socket string) *Client {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}}
	return &Client{hc: &http.Client{Transport: tr}, base: "http://podman" + apiPrefix}
}

// NewWithBaseURL is used by tests to point the client at an httptest server.
func NewWithBaseURL(base string, hc *http.Client) *Client {
	return &Client{hc: hc, base: strings.TrimSuffix(base, "/") + apiPrefix}
}

// call performs a request and returns the response only when its status is one
// of ok. Otherwise it closes the body and returns an error; 404 wraps
// runtime.ErrNotFound.
func (c *Client) call(ctx context.Context, method, path string, q url.Values, body io.Reader, ctype string, ok ...int) (*http.Response, error) {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("podman %s %s: %w", method, path, err)
	}
	for _, code := range ok {
		if resp.StatusCode == code {
			return resp, nil
		}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var e struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(b, &e)
	msg := e.Message
	if msg == "" {
		msg = strings.TrimSpace(string(b))
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("podman: %s: %w", msg, runtime.ErrNotFound)
	}
	return nil, fmt.Errorf("podman %s %s: %s (HTTP %d)", method, path, msg, resp.StatusCode)
}

func (c *Client) Build(ctx context.Context, contextTar io.Reader, tag string, log func(string)) error {
	q := url.Values{"t": {tag}, "rm": {"true"}}
	resp, err := c.call(ctx, http.MethodPost, "/build", q, contextTar, "application/x-tar", http.StatusOK)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	for {
		var m struct {
			Stream string `json:"stream"`
			Error  string `json:"error"`
		}
		err := dec.Decode(&m)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("podman build: reading output: %w", err)
		}
		if m.Error != "" {
			return fmt.Errorf("podman build: %s", strings.TrimSpace(m.Error))
		}
		for _, line := range strings.Split(strings.TrimRight(m.Stream, "\n"), "\n") {
			if line != "" {
				log(line)
			}
		}
	}
}

func (c *Client) ImageExists(ctx context.Context, tag string) (bool, error) {
	resp, err := c.call(ctx, http.MethodGet, "/images/"+tag+"/exists", nil, nil, "", http.StatusNoContent)
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	resp.Body.Close()
	return true, nil
}

type portMapping struct {
	HostIP        string `json:"host_ip"`
	ContainerPort uint16 `json:"container_port"`
	HostPort      uint16 `json:"host_port,omitempty"` // omitted: Podman picks a free port
	Protocol      string `json:"protocol"`
}

type mount struct {
	Destination string   `json:"destination"`
	Source      string   `json:"source"`
	Type        string   `json:"type"`
	Options     []string `json:"options,omitempty"`
}

type namedVolume struct {
	Name string `json:"Name"`
	Dest string `json:"Dest"`
}

type specgen struct {
	Name            string            `json:"name"`
	Image           string            `json:"image"`
	Command         []string          `json:"command,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	PortMappings    []portMapping     `json:"portmappings,omitempty"`
	Mounts          []mount           `json:"mounts,omitempty"`
	Volumes         []namedVolume     `json:"volumes,omitempty"`
	NoNewPrivileges bool              `json:"no_new_privileges"`
}

func (c *Client) Create(ctx context.Context, spec runtime.ContainerSpec) error {
	body := specgen{
		Name: spec.Name, Image: spec.Image, Command: spec.Command, Env: spec.Env, Labels: spec.Labels,
		NoNewPrivileges: true,
		PortMappings:    []portMapping{{HostIP: "127.0.0.1", ContainerPort: uint16(spec.InternalPort), Protocol: "tcp"}},
	}
	for _, b := range spec.Binds {
		body.Mounts = append(body.Mounts, mount{Destination: b.Dest, Source: b.Source, Type: "bind", Options: []string{"rbind", "rw"}})
	}
	for _, v := range spec.Volumes {
		body.Volumes = append(body.Volumes, namedVolume{Name: v.Name, Dest: v.Dest})
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := c.call(ctx, http.MethodPost, "/containers/create", nil, strings.NewReader(string(b)), "application/json", http.StatusCreated)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *Client) Start(ctx context.Context, name string) error {
	return c.simple(ctx, http.MethodPost, "/containers/"+name+"/start", nil, http.StatusNoContent, http.StatusNotModified)
}

func (c *Client) Stop(ctx context.Context, name string, timeout time.Duration) error {
	q := url.Values{"timeout": {strconv.Itoa(int(timeout.Seconds()))}}
	return c.simple(ctx, http.MethodPost, "/containers/"+name+"/stop", q, http.StatusNoContent, http.StatusNotModified)
}

func (c *Client) Remove(ctx context.Context, name string) error {
	q := url.Values{"force": {"true"}, "v": {"true"}}
	return c.simple(ctx, http.MethodDelete, "/containers/"+name, q, http.StatusOK, http.StatusNoContent)
}

func (c *Client) RemoveImage(ctx context.Context, tag string) error {
	return c.simple(ctx, http.MethodDelete, "/images/"+tag, url.Values{"force": {"true"}}, http.StatusOK)
}

func (c *Client) RemoveVolume(ctx context.Context, name string) error {
	return c.simple(ctx, http.MethodDelete, "/volumes/"+name, url.Values{"force": {"true"}}, http.StatusNoContent)
}

func (c *Client) simple(ctx context.Context, method, path string, q url.Values, ok ...int) error {
	resp, err := c.call(ctx, method, path, q, nil, "", ok...)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return nil
}

func (c *Client) Inspect(ctx context.Context, name string) (runtime.ContainerInfo, error) {
	resp, err := c.call(ctx, http.MethodGet, "/containers/"+name+"/json", nil, nil, "", http.StatusOK)
	if err != nil {
		return runtime.ContainerInfo{}, err
	}
	defer resp.Body.Close()
	var r struct {
		Name  string
		State struct {
			Running  bool
			ExitCode int
		}
		Config struct {
			Labels map[string]string
		}
		NetworkSettings struct {
			Ports map[string][]struct {
				HostIP   string `json:"HostIp"`
				HostPort string `json:"HostPort"`
			}
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return runtime.ContainerInfo{}, fmt.Errorf("podman inspect %s: %w", name, err)
	}
	info := runtime.ContainerInfo{Name: strings.TrimPrefix(r.Name, "/"), Running: r.State.Running,
		ExitCode: r.State.ExitCode, Labels: r.Config.Labels}
	for key, bindings := range r.NetworkSettings.Ports {
		if strings.HasSuffix(key, "/tcp") && len(bindings) > 0 {
			info.HostPort, _ = strconv.Atoi(bindings[0].HostPort)
			break
		}
	}
	return info, nil
}

func (c *Client) Logs(ctx context.Context, name string, tail int, follow bool, w io.Writer) error {
	q := url.Values{"stdout": {"true"}, "stderr": {"true"}, "tail": {strconv.Itoa(tail)}, "follow": {strconv.FormatBool(follow)}}
	resp, err := c.call(ctx, http.MethodGet, "/containers/"+name+"/logs", q, nil, "", http.StatusOK)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return demux(resp.Body, w)
}

// demux strips Docker-style stream frames: an 8-byte header ([stream, 0, 0, 0,
// size uint32 big endian]) before each chunk. Containers with a TTY send a
// raw stream instead, which is copied as is.
func demux(r io.Reader, w io.Writer) error {
	br := bufio.NewReader(r)
	hdr, err := br.Peek(8)
	if err != nil || hdr[0] > 2 || hdr[1] != 0 || hdr[2] != 0 || hdr[3] != 0 {
		_, err = io.Copy(w, br)
		return err
	}
	var h [8]byte
	for {
		if _, err := io.ReadFull(br, h[:]); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if _, err := io.CopyN(w, br, int64(binary.BigEndian.Uint32(h[4:]))); err != nil {
			return err
		}
	}
}

func (c *Client) List(ctx context.Context) ([]runtime.ContainerInfo, error) {
	filters, _ := json.Marshal(map[string][]string{"label": {runtime.LabelManaged + "=true"}})
	q := url.Values{"all": {"true"}, "filters": {string(filters)}}
	resp, err := c.call(ctx, http.MethodGet, "/containers/json", q, nil, "", http.StatusOK)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var raw []struct {
		Names    []string
		State    string
		ExitCode int
		Labels   map[string]string
		Ports    []struct {
			HostPort int `json:"host_port"`
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("podman list: %w", err)
	}
	out := make([]runtime.ContainerInfo, 0, len(raw))
	for _, r := range raw {
		info := runtime.ContainerInfo{Running: r.State == "running", ExitCode: r.ExitCode, Labels: r.Labels}
		if len(r.Names) > 0 {
			info.Name = r.Names[0]
		}
		if len(r.Ports) > 0 {
			info.HostPort = r.Ports[0].HostPort
		}
		out = append(out, info)
	}
	return out, nil
}

func isNotFound(err error) bool { return errors.Is(err, runtime.ErrNotFound) }
