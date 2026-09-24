// Package runtime is the container engine interface the deployer depends on.
package runtime

import (
	"context"
	"errors"
	"io"
	"time"
)

var ErrNotFound = errors.New("not found")

const (
	LabelManaged   = "sagan.managed"
	LabelProject   = "sagan.project"
	LabelWorkspace = "sagan.workspace"
	LabelRelease   = "sagan.release"
)

type Bind struct{ Source, Dest string }

type Volume struct{ Name, Dest string }

type ContainerSpec struct {
	Name         string
	Image        string
	Command      []string
	Env          map[string]string
	InternalPort int
	Labels       map[string]string
	Binds        []Bind
	Volumes      []Volume
}

type ContainerInfo struct {
	Name     string
	Running  bool
	ExitCode int
	HostPort int
	Labels   map[string]string
}

type Runtime interface {
	Build(ctx context.Context, contextTar io.Reader, tag string, log func(line string)) error
	ImageExists(ctx context.Context, tag string) (bool, error)
	Create(ctx context.Context, spec ContainerSpec) error
	Start(ctx context.Context, name string) error
	Stop(ctx context.Context, name string, timeout time.Duration) error
	Remove(ctx context.Context, name string) error
	Inspect(ctx context.Context, name string) (ContainerInfo, error)
	Logs(ctx context.Context, name string, tail int, follow bool, w io.Writer) error
	List(ctx context.Context) ([]ContainerInfo, error)
	RemoveImage(ctx context.Context, tag string) error
	RemoveVolume(ctx context.Context, name string) error
}
