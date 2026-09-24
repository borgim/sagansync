package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestTLS(t *testing.T, allowed func(string) bool) *TLS {
	t.Helper()
	tl, err := NewTLS(TLSOptions{StorageDir: t.TempDir(), Email: "ops@example.com", CA: "https://127.0.0.1:14000/dir"}, allowed)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tl.Close)
	return tl
}

func TestOnlyKnownHostsGetCertificates(t *testing.T) {
	tl := newTestTLS(t, func(h string) bool { return h == "app.test" })
	decide := tl.magic.OnDemand.DecisionFunc
	if err := decide(context.Background(), "app.test"); err != nil {
		t.Errorf("known host refused: %v", err)
	}
	if err := decide(context.Background(), "evil.test"); err == nil {
		t.Error("unknown host allowed")
	}
}

func TestHTTPHandlerPassesOrdinaryRequests(t *testing.T) {
	tl := newTestTLS(t, func(string) bool { return true })
	called := false
	h := tl.HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://app.test/x", nil))
	if !called {
		t.Fatal("ordinary request did not reach the next handler")
	}
}

func TestTLSConfigAdvertisesHTTP2(t *testing.T) {
	cfg := newTestTLS(t, func(string) bool { return true }).TLSConfig()
	if len(cfg.NextProtos) < 2 || cfg.NextProtos[0] != "h2" || cfg.NextProtos[1] != "http/1.1" {
		t.Fatalf("NextProtos = %v", cfg.NextProtos)
	}
	if cfg.GetCertificate == nil {
		t.Fatal("GetCertificate not set")
	}
}

func TestBadRootCAIsAnError(t *testing.T) {
	if _, err := NewTLS(TLSOptions{StorageDir: t.TempDir(), RootCAPath: "/does/not/exist.pem"}, func(string) bool { return true }); err == nil {
		t.Error("missing root CA file accepted")
	}
	junk := filepath.Join(t.TempDir(), "junk.pem")
	os.WriteFile(junk, []byte("not a certificate"), 0o600)
	if _, err := NewTLS(TLSOptions{StorageDir: t.TempDir(), RootCAPath: junk}, func(string) bool { return true }); err == nil {
		t.Error("root CA without certificates accepted")
	}
}

func TestEnsureObtainsAndReportsFailure(t *testing.T) {
	tl := newTestTLS(t, func(string) bool { return true }) // CA at 127.0.0.1:14000 is not running
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	errs := make(chan error, 4)
	for _, h := range []string{"a.test", "b.test", "c.test", "d.test"} {
		go func(h string) { errs <- tl.Ensure(ctx, h) }(h)
	}
	for i := 0; i < 4; i++ {
		if err := <-errs; err == nil {
			t.Fatal("Ensure returned nil although no certificate could be obtained")
		}
	}
}
