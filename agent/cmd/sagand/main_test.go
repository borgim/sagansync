package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/borgim/sagansync/agent/internal/api"
	"github.com/borgim/sagansync/agent/internal/events"
	"github.com/borgim/sagansync/agent/internal/testutil"
	"github.com/borgim/sagansync/agent/internal/testutil/testdeploy"
)

// startDaemon serves a fake-backed API on a Unix socket and points
// SAGAND_SOCKET at it.
func startDaemon(t *testing.T) *testdeploy.Harness {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "sgd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	h := testdeploy.New(t)
	l, err := api.Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: api.NewServer(h.D, "test").Handler()}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	t.Setenv("SAGAND_SOCKET", sock)
	return h
}

func lastEvent(t *testing.T, out *bytes.Buffer) events.Event {
	t.Helper()
	var last events.Event
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		if err := json.Unmarshal(sc.Bytes(), &last); err != nil {
			t.Fatalf("stdout line %q is not an event: %v", sc.Text(), err)
		}
	}
	return last
}

func TestGatewayDeniesShellAndDaemon(t *testing.T) {
	for _, cmd := range []string{"", "bash -i", "sagand daemon", "scp -t /tmp"} {
		t.Setenv("SSH_ORIGINAL_COMMAND", cmd)
		var out, errb bytes.Buffer
		if code := run([]string{"gateway"}, strings.NewReader(""), &out, &errb); code != 126 {
			t.Errorf("%q: exit %d, want 126", cmd, code)
		}
		if !strings.Contains(errb.String(), "not allowed") {
			t.Errorf("%q: stderr = %q", cmd, errb.String())
		}
	}
}

func TestVersionAndHost(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"version"}, nil, &out, &out); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var v api.VersionInfo
	json.Unmarshal(out.Bytes(), &v)
	if v.Protocol != events.Protocol || v.Version == "" {
		t.Fatalf("version = %+v", v)
	}
	out.Reset()
	code := run([]string{"host", "--project", "barbervip", "--workspace", "feat-login", "--domain", "api.pedroborgim.com.br",
		"--preview-domain", "pedroborgim.com.br"}, nil, &out, &out)
	var h struct{ Host string }
	json.Unmarshal(out.Bytes(), &h)
	if code != 0 || h.Host != "feat-login-barbervip.pedroborgim.com.br" {
		t.Fatalf("host = %q (exit %d)", h.Host, code)
	}
}

func TestDeployThroughGateway(t *testing.T) {
	h := startDaemon(t)
	t.Setenv("SSH_ORIGINAL_COMMAND", "sagand deploy --project app --workspace production --domain app.test --port 3000 --health-path /health --health-timeout 5 --sha abc1234")
	var out, errb bytes.Buffer
	code := run([]string{"gateway"}, testutil.App(t), &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
	if last := lastEvent(t, &out); last.Type != "done" || last.URL != "https://app.test" {
		t.Fatalf("last = %+v", last)
	}
	if _, ok := h.State.Get("app", "production"); !ok {
		t.Fatal("deploy not recorded")
	}

	t.Setenv("SSH_ORIGINAL_COMMAND", "sagand list")
	out.Reset()
	if code := run([]string{"gateway"}, nil, &out, &errb); code != 0 || !strings.Contains(out.String(), `"workspace":"production"`) {
		t.Fatalf("list: exit %d, %q", code, out.String())
	}
}

func TestValidationErrorExitsWith2(t *testing.T) {
	startDaemon(t)
	var out bytes.Buffer
	code := run([]string{"deploy", "--project", "Bad!", "--workspace", "production", "--port", "3000"}, testutil.App(t), &out, &out)
	if code != 2 || lastEvent(t, &out).Code != "invalid" {
		t.Fatalf("exit %d, out %q", code, out.String())
	}
	out.Reset()
	if code := run([]string{"deploy", "--no-such-flag"}, nil, &out, &out); code != 2 {
		t.Fatalf("unknown flag: exit %d", code)
	}
}

func TestEnvSetFromStdin(t *testing.T) {
	startDaemon(t)
	var out bytes.Buffer
	if code := run([]string{"env", "set", "--project", "app", "--workspace", "production"}, strings.NewReader(`{"A":"1"}`), &out, &out); code != 0 {
		t.Fatalf("set: exit %d, %q", code, out.String())
	}
	out.Reset()
	if code := run([]string{"env", "list", "--project", "app", "--workspace", "production"}, nil, &out, &out); code != 0 || !strings.Contains(out.String(), `"A"`) {
		t.Fatalf("list: exit %d, %q", code, out.String())
	}
}

func TestDaemonUnavailable(t *testing.T) {
	t.Setenv("SAGAND_SOCKET", "/tmp/sagand-does-not-exist.sock")
	var out bytes.Buffer
	if code := run([]string{"list"}, nil, &out, &out); code != 1 || lastEvent(t, &out).Code != "daemon_unavailable" {
		t.Fatalf("exit %d, out %q", code, out.String())
	}
}

func TestLoadDaemonConfig(t *testing.T) {
	cfg, err := loadDaemonConfig(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil || cfg.Socket != "/run/sagand/sagand.sock" || cfg.HTTPSAddr != ":443" || cfg.SrvDir != "/srv/sagan" {
		t.Fatalf("defaults = %+v, %v", cfg, err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"acmeCA":"https://pebble:14000/dir","acmeEmail":"ops@example.com"}`), 0o600)
	cfg, err = loadDaemonConfig(path)
	if err != nil || cfg.AcmeCA != "https://pebble:14000/dir" || cfg.AcmeEmail != "ops@example.com" || cfg.HTTPAddr != ":80" {
		t.Fatalf("overlay = %+v, %v", cfg, err)
	}
	os.WriteFile(path, []byte(`{broken`), 0o600)
	if _, err := loadDaemonConfig(path); err == nil {
		t.Fatal("broken config accepted")
	}
}

func TestPublicServersTimeOutIdleConnections(t *testing.T) {
	s := publicServer(":443", http.NotFoundHandler(), nil)
	if s.IdleTimeout <= 0 || s.ReadHeaderTimeout <= 0 {
		t.Fatalf("IdleTimeout = %v, ReadHeaderTimeout = %v; both must be set on internet-facing servers", s.IdleTimeout, s.ReadHeaderTimeout)
	}
}

// The CLI sends "--" before the keys, so a key can never be read as a flag.
func TestEnvUnsetKeysAfterDoubleDash(t *testing.T) {
	startDaemon(t)
	var out bytes.Buffer
	ws := []string{"--project", "app", "--workspace", "production"}
	if code := run(append([]string{"env", "set"}, ws...), strings.NewReader(`{"A":"1","B":"2"}`), &out, &out); code != 0 {
		t.Fatalf("set: exit %d, %q", code, out.String())
	}
	out.Reset()
	if code := run(append(append([]string{"env", "unset"}, ws...), "--", "A"), nil, &out, &out); code != 0 {
		t.Fatalf("unset: exit %d, %q", code, out.String())
	}
	out.Reset()
	if code := run(append([]string{"env", "list"}, ws...), nil, &out, &out); code != 0 || !strings.Contains(out.String(), `["B"]`) {
		t.Fatalf("list: exit %d, %q", code, out.String())
	}
}
