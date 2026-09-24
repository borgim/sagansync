package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/borgim/sagansync/agent/internal/api"
	"github.com/borgim/sagansync/agent/internal/deploy"
	"github.com/borgim/sagansync/agent/internal/events"
	"github.com/borgim/sagansync/agent/internal/testutil"
	"github.com/borgim/sagansync/agent/internal/testutil/testdeploy"
)

func newServer(t *testing.T) (*httptest.Server, *testdeploy.Harness) {
	t.Helper()
	h := testdeploy.New(t)
	srv := httptest.NewServer(api.NewServer(h.D, "1.2.3").Handler())
	t.Cleanup(srv.Close)
	return srv, h
}

func deployQuery() url.Values {
	return url.Values{"project": {"app"}, "workspace": {"production"}, "domain": {"app.test"}, "port": {"3000"},
		"healthPath": {"/health"}, "healthTimeout": {"5"}, "sha": {"abc1234"}}
}

func do(t *testing.T, srv *httptest.Server, method, path string, q url.Values, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path+"?"+q.Encode(), body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func readEvents(t *testing.T, r io.Reader) []events.Event {
	t.Helper()
	var out []events.Event
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		var e events.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("bad event line %q: %v", sc.Text(), err)
		}
		out = append(out, e)
	}
	return out
}

func TestVersion(t *testing.T) {
	srv, _ := newServer(t)
	resp := do(t, srv, "GET", "/v1/version", nil, nil)
	var v api.VersionInfo
	json.NewDecoder(resp.Body).Decode(&v)
	if v.Version != "1.2.3" || v.Protocol != events.Protocol {
		t.Fatalf("version = %+v", v)
	}
}

func TestDeployStreamsEventsAndDone(t *testing.T) {
	srv, h := newServer(t)
	resp := do(t, srv, "POST", "/v1/deploy", deployQuery(), testutil.App(t))
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("status %d, content-type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	evs := readEvents(t, resp.Body)
	last := evs[len(evs)-1]
	if last.Type != "done" || last.URL != "https://app.test" {
		t.Fatalf("last = %+v", last)
	}
	if _, ok := h.State.Get("app", "production"); !ok {
		t.Fatal("deploy not recorded")
	}
}

func TestDeployErrorsAreEvents(t *testing.T) {
	srv, _ := newServer(t)
	q := deployQuery()
	q.Set("port", "abc")
	evs := readEvents(t, do(t, srv, "POST", "/v1/deploy", q, testutil.App(t)).Body)
	if last := evs[len(evs)-1]; last.Type != "error" || last.Code != deploy.CodeInvalid {
		t.Fatalf("last = %+v", last)
	}
	q = deployQuery()
	q.Set("project", "Bad!")
	evs = readEvents(t, do(t, srv, "POST", "/v1/deploy", q, testutil.App(t)).Body)
	if last := evs[len(evs)-1]; last.Code != deploy.CodeInvalid || events.ExitCode(last) != 2 {
		t.Fatalf("last = %+v", last)
	}
}

func TestDevRequiresJSONCommand(t *testing.T) {
	srv, _ := newServer(t)
	q := deployQuery()
	q.Set("workspace", "feat-x")
	q.Set("command", "npm run dev")
	evs := readEvents(t, do(t, srv, "POST", "/v1/dev", q, testutil.App(t)).Body)
	if last := evs[len(evs)-1]; last.Code != deploy.CodeInvalid {
		t.Fatalf("last = %+v", last)
	}
	q.Set("command", `["npm","run","dev"]`)
	evs = readEvents(t, do(t, srv, "POST", "/v1/dev", q, testutil.App(t)).Body)
	if last := evs[len(evs)-1]; last.Type != "done" {
		t.Fatalf("last = %+v", last)
	}
}

func TestListAndLogsAndRemove(t *testing.T) {
	srv, _ := newServer(t)
	readEvents(t, do(t, srv, "POST", "/v1/deploy", deployQuery(), testutil.App(t)).Body)

	var list []deploy.Status
	json.NewDecoder(do(t, srv, "GET", "/v1/list", nil, nil).Body).Decode(&list)
	if len(list) != 1 || list[0].Workspace != "production" || !list[0].Running {
		t.Fatalf("list = %+v", list)
	}
	ws := url.Values{"project": {"app"}, "workspace": {"production"}}
	b, _ := io.ReadAll(do(t, srv, "GET", "/v1/logs", ws, nil).Body)
	if !strings.HasPrefix(string(b), "log from ") {
		t.Fatalf("logs = %q", b)
	}
	evs := readEvents(t, do(t, srv, "POST", "/v1/remove", ws, nil).Body)
	if last := evs[len(evs)-1]; last.Type != "done" {
		t.Fatalf("remove = %+v", last)
	}
	resp := do(t, srv, "GET", "/v1/logs", ws, nil)
	var e struct{ Code, Message string }
	json.NewDecoder(resp.Body).Decode(&e)
	if resp.StatusCode != 404 || e.Code != deploy.CodeNotFound {
		t.Fatalf("logs after remove: %d %+v", resp.StatusCode, e)
	}
}

func TestFilesRequireDevMode(t *testing.T) {
	srv, _ := newServer(t)
	q := url.Values{"project": {"app"}, "workspace": {"production"}, "path": {"a.js"}}
	resp := do(t, srv, "PUT", "/v1/files", q, strings.NewReader("x"))
	var e struct{ Code string }
	json.NewDecoder(resp.Body).Decode(&e)
	if resp.StatusCode != 404 || e.Code != deploy.CodeNotFound {
		t.Fatalf("got %d %+v", resp.StatusCode, e)
	}
}

func TestEnvRoundTrip(t *testing.T) {
	srv, _ := newServer(t)
	ws := url.Values{"project": {"app"}, "workspace": {"production"}}
	if resp := do(t, srv, "POST", "/v1/env", ws, strings.NewReader(`{"B":"2","A":"line1\nline2"}`)); resp.StatusCode != 204 {
		t.Fatalf("set: %d", resp.StatusCode)
	}
	var keys struct{ Keys []string }
	json.NewDecoder(do(t, srv, "GET", "/v1/env", ws, nil).Body).Decode(&keys)
	if strings.Join(keys.Keys, ",") != "A,B" {
		t.Fatalf("keys = %v", keys.Keys)
	}
	unset := url.Values{"project": {"app"}, "workspace": {"production"}, "key": {"A"}}
	if resp := do(t, srv, "DELETE", "/v1/env", unset, nil); resp.StatusCode != 204 {
		t.Fatalf("unset: %d", resp.StatusCode)
	}
	if resp := do(t, srv, "POST", "/v1/env", ws, strings.NewReader(`not json`)); resp.StatusCode != 400 {
		t.Fatalf("bad body: %d", resp.StatusCode)
	}
	if resp := do(t, srv, "POST", "/v1/env", ws, strings.NewReader(`{"1BAD":"x"}`)); resp.StatusCode != 400 {
		t.Fatalf("bad key: %d", resp.StatusCode)
	}
}

func TestListenOverUnixSocket(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "sgd") // short path: macOS limits socket paths to 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s.sock")
	if err := os.WriteFile(sock, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := api.Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	fi, _ := os.Stat(sock)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("socket mode = %v", fi.Mode().Perm())
	}
	h := testdeploy.New(t)
	srv := &http.Server{Handler: api.NewServer(h.D, "1.2.3").Handler()}
	go srv.Serve(l)
	defer srv.Close()
	resp, err := api.NewClient(sock).Do(context.Background(), "GET", "/v1/version", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
