// Command fakesagand serves the real sagand API on a Unix socket, backed by
// the fake container runtime. The CLI's contract tests run it together with
// the real `sagand gateway` client. It is never shipped.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/borgim/sagansync/agent/internal/api"
	"github.com/borgim/sagansync/agent/internal/deploy"
	"github.com/borgim/sagansync/agent/internal/envstore"
	"github.com/borgim/sagansync/agent/internal/proxy"
	"github.com/borgim/sagansync/agent/internal/runtime/fakert"
	"github.com/borgim/sagansync/agent/internal/state"
)

type noCerts struct{}

func (noCerts) Ensure(context.Context, string) error { return nil }

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: fakesagand <socket> <data-dir>")
		os.Exit(2)
	}
	sock, dir := os.Args[1], os.Args[2]
	st, err := state.Open(filepath.Join(dir, "state.json"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	rt := fakert.New()
	defer rt.Close()
	cfg := deploy.DefaultConfig(filepath.Join(dir, "srv"))
	cfg.Drain = 0
	cfg.StopTimeout = 0
	cfg.HealthInterval = 20 * time.Millisecond
	d := deploy.New(rt, st, envstore.New(filepath.Join(dir, "env")), proxy.NewRouter(), noCerts{}, cfg)
	l, err := api.Listen(sock)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("ready")
	if err := http.Serve(l, api.NewServer(d, "0.1.0").Handler()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
