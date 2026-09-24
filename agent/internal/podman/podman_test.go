package podman

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/borgim/sagansync/agent/internal/runtime"
)

func newTest(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewWithBaseURL(srv.URL, srv.Client())
}

func TestBuildStreamsLogLines(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v4.0.0/libpod/build" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("t") != "localhost/sagan_app_production:r1" {
			t.Errorf("tag = %q", r.URL.Query().Get("t"))
		}
		if r.Header.Get("Content-Type") != "application/x-tar" {
			t.Errorf("content-type = %q", r.Header.Get("Content-Type"))
		}
		if b, _ := io.ReadAll(r.Body); string(b) != "TAR" {
			t.Errorf("body = %q", b)
		}
		io.WriteString(w, `{"stream":"STEP 1/2: FROM x\n"}{"stream":"STEP 2/2\nCOMMIT\n"}`)
	})
	var lines []string
	err := c.Build(context.Background(), strings.NewReader("TAR"), "localhost/sagan_app_production:r1", func(l string) { lines = append(lines, l) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(lines, "|") != "STEP 1/2: FROM x|STEP 2/2|COMMIT" {
		t.Fatalf("lines = %q", lines)
	}
}

func TestBuildReportsStreamError(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"stream":"STEP 1\n"}{"error":"npm ERR! missing script"}`)
	})
	err := c.Build(context.Background(), strings.NewReader(""), "t:1", func(string) {})
	if err == nil || !strings.Contains(err.Error(), "npm ERR! missing script") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateSendsSpec(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4.0.0/libpod/containers/create" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		pm := body["portmappings"].([]any)[0].(map[string]any)
		checks := map[string]bool{
			"name":              body["name"] == "sagan_app_production_r1",
			"image":             body["image"] == "localhost/sagan_app_production:r1",
			"no_new_privileges": body["no_new_privileges"] == true,
			"host_ip":           pm["host_ip"] == "127.0.0.1",
			"container_port":    pm["container_port"] == float64(3000),
			"random host port":  pm["host_port"] == nil,
			"env":               body["env"].(map[string]any)["A"] == "1",
			"label":             body["labels"].(map[string]any)[runtime.LabelManaged] == "true",
			"command":           body["command"].([]any)[0] == "npm",
			"bind mount":        body["mounts"].([]any)[0].(map[string]any)["destination"] == "/app",
			"named volume":      body["volumes"].([]any)[0].(map[string]any)["Dest"] == "/app/node_modules",
			"k8s-file logs":     body["log_configuration"].(map[string]any)["driver"] == "k8s-file",
			"log size cap":      body["log_configuration"].(map[string]any)["size"] == float64(10<<20),
		}
		for name, ok := range checks {
			if !ok {
				t.Errorf("create body: %s is wrong: %v", name, body)
			}
		}
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"Id":"abc"}`)
	})
	err := c.Create(context.Background(), runtime.ContainerSpec{
		Name: "sagan_app_production_r1", Image: "localhost/sagan_app_production:r1",
		Command: []string{"npm", "run", "dev"}, Env: map[string]string{"A": "1"}, InternalPort: 3000,
		Labels:  map[string]string{runtime.LabelManaged: "true"},
		Binds:   []runtime.Bind{{Source: "/srv/sagan/app/x/dev", Dest: "/app"}},
		Volumes: []runtime.Volume{{Name: "sagan_app_x_node_modules", Dest: "/app/node_modules"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestInspectParsesStateAndPort(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4.0.0/libpod/containers/c1/json" {
			t.Errorf("path = %s", r.URL.Path)
		}
		io.WriteString(w, `{"Name":"c1","State":{"Running":true,"ExitCode":0},
			"Config":{"Labels":{"sagan.project":"app"}},
			"NetworkSettings":{"Ports":{"3000/tcp":[{"HostIp":"127.0.0.1","HostPort":"41873"}]}}}`)
	})
	info, err := c.Inspect(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if !info.Running || info.HostPort != 41873 || info.Labels["sagan.project"] != "app" || info.Name != "c1" {
		t.Fatalf("info = %+v", info)
	}
}

func TestNotFoundMapsToErrNotFound(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"cause":"no such container","message":"no container with name c1","response":404}`)
	})
	ctx := context.Background()
	if _, err := c.Inspect(ctx, "c1"); !errors.Is(err, runtime.ErrNotFound) {
		t.Errorf("Inspect: %v", err)
	}
	if err := c.Remove(ctx, "c1"); !errors.Is(err, runtime.ErrNotFound) {
		t.Errorf("Remove: %v", err)
	}
	if ok, err := c.ImageExists(ctx, "x:1"); ok || err != nil {
		t.Errorf("ImageExists = %v, %v", ok, err)
	}
}

func TestStartStopAcceptNotModified(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4.0.0/libpod/containers/c1/start":
			w.WriteHeader(http.StatusNotModified)
		case "/v4.0.0/libpod/containers/c1/stop":
			if r.URL.Query().Get("timeout") != "10" {
				t.Errorf("timeout = %q", r.URL.Query().Get("timeout"))
			}
			w.WriteHeader(http.StatusNoContent)
		}
	})
	if err := c.Start(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Stop(context.Background(), "c1", 10e9); err != nil {
		t.Fatal(err)
	}
}

func frame(stream byte, s string) []byte {
	h := make([]byte, 8)
	h[0] = stream
	binary.BigEndian.PutUint32(h[4:], uint32(len(s)))
	return append(h, s...)
}

func TestLogsDemultiplexes(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("tail") != "50" || q.Get("stdout") != "true" || q.Get("stderr") != "true" {
			t.Errorf("query = %v", q)
		}
		w.Write(append(frame(1, "hello\n"), frame(2, "oops\n")...))
	})
	var out bytes.Buffer
	if err := c.Logs(context.Background(), "c1", 50, false, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "hello\noops\n" {
		t.Fatalf("logs = %q", out.String())
	}
}

func TestLogsPassesRawStreamThrough(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "plain tty output\n") })
	var out bytes.Buffer
	if err := c.Logs(context.Background(), "c1", 10, false, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "plain tty output\n" {
		t.Fatalf("logs = %q", out.String())
	}
}

func TestListFiltersManagedContainers(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		var f map[string][]string
		if err := json.Unmarshal([]byte(r.URL.Query().Get("filters")), &f); err != nil || f["label"][0] != "sagan.managed=true" {
			t.Errorf("filters = %q", r.URL.Query().Get("filters"))
		}
		if r.URL.Query().Get("all") != "true" {
			t.Error("all=true missing")
		}
		io.WriteString(w, `[{"Names":["c1"],"State":"running","ExitCode":0,"Labels":{"sagan.managed":"true"},"Ports":[{"host_port":41000}]},
			{"Names":["c2"],"State":"exited","ExitCode":1,"Labels":{"sagan.managed":"true"},"Ports":null}]`)
	})
	list, err := c.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || !list[0].Running || list[0].HostPort != 41000 || list[1].Running || list[1].ExitCode != 1 {
		t.Fatalf("list = %+v", list)
	}
}
