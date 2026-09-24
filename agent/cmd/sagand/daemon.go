package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/borgim/sagansync/agent/internal/api"
	"github.com/borgim/sagansync/agent/internal/deploy"
	"github.com/borgim/sagansync/agent/internal/envstore"
	"github.com/borgim/sagansync/agent/internal/podman"
	"github.com/borgim/sagansync/agent/internal/proxy"
	"github.com/borgim/sagansync/agent/internal/state"
)

type daemonConfig struct {
	Socket       string `json:"socket"`
	SrvDir       string `json:"srvDir"`
	StateDir     string `json:"stateDir"`
	PodmanSocket string `json:"podmanSocket"`
	HTTPAddr     string `json:"httpAddr"`
	HTTPSAddr    string `json:"httpsAddr"`
	AcmeEmail    string `json:"acmeEmail"`
	AcmeCA       string `json:"acmeCA"`
	AcmeRootCA   string `json:"acmeRootCA"`
}

// loadDaemonConfig returns the defaults overlaid with the JSON file, if any.
func loadDaemonConfig(path string) (daemonConfig, error) {
	cfg := daemonConfig{
		Socket: defaultSocket, SrvDir: "/srv/sagan", StateDir: "/var/lib/sagand",
		PodmanSocket: fmt.Sprintf("/run/user/%d/podman/podman.sock", os.Getuid()),
		HTTPAddr:     ":80", HTTPSAddr: ":443",
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// publicServer builds an internet-facing server. Without IdleTimeout, idle
// keep-alive connections would stay open forever, letting anyone exhaust the
// daemon's file descriptors.
func publicServer(addr string, h http.Handler, tlsCfg *tls.Config) *http.Server {
	return &http.Server{Addr: addr, Handler: h, TLSConfig: tlsCfg,
		ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
}

func runDaemon(args []string, stderr io.Writer) int {
	fset := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fset.SetOutput(stderr)
	cfgPath := fset.String("config", "/var/lib/sagand/config.json", "config file")
	if err := fset.Parse(args); err != nil {
		return 2
	}
	logger := log.New(stderr, "sagand: ", log.LstdFlags)
	cfg, err := loadDaemonConfig(*cfgPath)
	if err != nil {
		logger.Print(err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := serve(ctx, cfg, logger); err != nil {
		logger.Print(err)
		return 1
	}
	return 0
}

func serve(ctx context.Context, cfg daemonConfig, logger *log.Logger) error {
	st, err := state.Open(filepath.Join(cfg.StateDir, "state.json"))
	if err != nil {
		return err
	}
	router := proxy.NewRouter()
	tlsm, err := proxy.NewTLS(proxy.TLSOptions{StorageDir: filepath.Join(cfg.StateDir, "certs"),
		Email: cfg.AcmeEmail, CA: cfg.AcmeCA, RootCAPath: cfg.AcmeRootCA},
		func(host string) bool { _, ok := st.HostOwner(host); return ok })
	if err != nil {
		return err
	}
	defer tlsm.Close()
	dep := deploy.New(podman.New(cfg.PodmanSocket), st, envstore.New(filepath.Join(cfg.StateDir, "env")),
		router, tlsm, deploy.DefaultConfig(cfg.SrvDir))

	if err := dep.Reconcile(ctx); err != nil {
		logger.Printf("reconcile: %v", err)
	}
	go func() {
		t := time.NewTicker(60 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := dep.Reconcile(ctx); err != nil {
					logger.Printf("reconcile: %v", err)
				}
			}
		}
	}()

	httpSrv := publicServer(cfg.HTTPAddr, tlsm.HTTPHandler(proxy.RedirectHandler()), nil)
	httpsSrv := publicServer(cfg.HTTPSAddr, router, tlsm.TLSConfig())
	apiSrv := &http.Server{Handler: api.NewServer(dep, version).Handler(), ReadHeaderTimeout: 10 * time.Second}
	l, err := api.Listen(cfg.Socket)
	if err != nil {
		return err
	}

	errc := make(chan error, 3)
	go func() { errc <- httpSrv.ListenAndServe() }()
	go func() { errc <- httpsSrv.ListenAndServeTLS("", "") }()
	go func() { errc <- apiSrv.Serve(l) }()
	logger.Printf("sagand %s started (api %s, http %s, https %s)", version, cfg.Socket, cfg.HTTPAddr, cfg.HTTPSAddr)

	var runErr error
	select {
	case <-ctx.Done():
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = err
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, s := range []*http.Server{httpSrv, httpsSrv, apiSrv} {
		_ = s.Shutdown(shutdownCtx)
	}
	return runErr
}
