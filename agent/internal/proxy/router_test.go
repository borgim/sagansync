package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func backend(t *testing.T, name string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen-Host", r.Host)
		w.Header().Set("X-Seen-Forwarded-Host", r.Header.Get("X-Forwarded-Host"))
		w.Header().Set("X-Seen-Forwarded-Proto", r.Header.Get("X-Forwarded-Proto"))
		io.WriteString(w, name)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func upstreamOf(srv *httptest.Server) string { return strings.TrimPrefix(srv.URL, "http://") }

func get(t *testing.T, front *httptest.Server, host string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, front.URL+"/path?q=1", nil)
	req.Host = host
	resp, err := front.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestRouterProxiesKnownHost(t *testing.T) {
	r := NewRouter()
	r.Set("app.test", upstreamOf(backend(t, "v1")))
	front := httptest.NewServer(r)
	defer front.Close()
	resp, body := get(t, front, "app.test")
	if resp.StatusCode != 200 || body != "v1" {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
	if resp.Header.Get("X-Seen-Host") != "app.test" {
		t.Errorf("app saw Host %q, want app.test", resp.Header.Get("X-Seen-Host"))
	}
	if resp.Header.Get("X-Seen-Forwarded-Host") != "app.test" || resp.Header.Get("X-Seen-Forwarded-Proto") != "http" {
		t.Errorf("forwarded headers = %q %q", resp.Header.Get("X-Seen-Forwarded-Host"), resp.Header.Get("X-Seen-Forwarded-Proto"))
	}
}

func TestRouterUnknownHostIs404(t *testing.T) {
	front := httptest.NewServer(NewRouter())
	defer front.Close()
	if resp, _ := get(t, front, "nope.test"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestRouterDeadUpstreamIs502(t *testing.T) {
	dead := backend(t, "x")
	addr := upstreamOf(dead)
	dead.Close()
	r := NewRouter()
	r.Set("app.test", addr)
	front := httptest.NewServer(r)
	defer front.Close()
	if resp, _ := get(t, front, "app.test"); resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestRouterNormalizesHost(t *testing.T) {
	r := NewRouter()
	r.Set("app.test", upstreamOf(backend(t, "v1")))
	front := httptest.NewServer(r)
	defer front.Close()
	for _, h := range []string{"APP.test", "app.test:443", "app.test.", "App.Test.:8443"} {
		if resp, body := get(t, front, h); resp.StatusCode != 200 || body != "v1" {
			t.Errorf("Host %q: got %d %q", h, resp.StatusCode, body)
		}
	}
}

func TestRouterSwapUnderLoad(t *testing.T) {
	r := NewRouter()
	v1, v2 := backend(t, "v1"), backend(t, "v2")
	r.Set("app.test", upstreamOf(v1))
	front := httptest.NewServer(r)
	defer front.Close()

	var failures atomic.Int32
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				req, _ := http.NewRequest(http.MethodGet, front.URL, nil)
				req.Host = "app.test"
				resp, err := front.Client().Do(req)
				if err != nil {
					failures.Add(1)
					continue
				}
				b, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode != 200 || (string(b) != "v1" && string(b) != "v2") {
					failures.Add(1)
				}
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	r.Set("app.test", upstreamOf(v2))
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
	if n := failures.Load(); n != 0 {
		t.Fatalf("%d requests failed during the swap", n)
	}
	if _, body := get(t, front, "app.test"); body != "v2" {
		t.Fatalf("after swap got %q", body)
	}
}

func TestDeleteRemovesRoute(t *testing.T) {
	r := NewRouter()
	r.Set("app.test", "127.0.0.1:1")
	r.Delete("app.test")
	if _, ok := r.Lookup("app.test"); ok {
		t.Fatal("route still present")
	}
}

func TestRedirectHandler(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://App.test:80/a/b?x=1", nil)
	RedirectHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != "https://app.test/a/b?x=1" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
}
