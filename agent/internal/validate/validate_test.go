package validate

import (
	"errors"
	"strings"
	"testing"
)

func check(t *testing.T, fn func(string) error, ok, bad []string) {
	t.Helper()
	for _, s := range ok {
		if err := fn(s); err != nil {
			t.Errorf("%q: got %v, want nil", s, err)
		}
	}
	for _, s := range bad {
		if err := fn(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: got %v, want ErrInvalid", s, err)
		}
	}
}

func TestName(t *testing.T) {
	check(t, func(s string) error { return Name("project", s) },
		[]string{"a", "app", "feat-login", "a1-b2", strings.Repeat("a", 40)},
		[]string{"", "-a", "a-", "A", "a_b", "a.b", "a b", "a;rm", "$(x)", strings.Repeat("a", 41)})
}

func TestDomain(t *testing.T) {
	check(t, func(s string) error { return Domain("domain", s) },
		[]string{"example.com", "api.pedroborgim.com.br", "feat-x-app.example.com", "a.b"},
		[]string{"", "localhost", "https://example.com", "example.com:443", "*.example.com",
			"-a.com", "a..com", "Example.com", strings.Repeat("a", 64) + ".com",
			strings.Repeat("a.", 127) + "com"})
}

func TestPort(t *testing.T) {
	for _, p := range []int{1, 3000, 65535} {
		if err := Port("port", p); err != nil {
			t.Errorf("%d: %v", p, err)
		}
	}
	for _, p := range []int{0, -1, 65536} {
		if err := Port("port", p); !errors.Is(err, ErrInvalid) {
			t.Errorf("%d: got %v, want ErrInvalid", p, err)
		}
	}
}

func TestHealthPath(t *testing.T) {
	check(t, func(s string) error { return HealthPath("healthPath", s) },
		[]string{"", "/", "/health", "/api/health?x=1"},
		[]string{"health", "/a b", "/a\nb", "/" + strings.Repeat("a", 200)})
}

func TestEnvKey(t *testing.T) {
	check(t, func(s string) error { return EnvKey("env", s) },
		[]string{"A", "_x", "DATABASE_URL", "a1"},
		[]string{"", "1A", "A-B", "A B", "A=B"})
}

func TestRelPath(t *testing.T) {
	check(t, func(s string) error { return RelPath("path", s) },
		[]string{"a", "a/b", "src/index.ts", ".env"},
		[]string{"", "/a", "../a", "a/../../b", "a/./b", "a//b", ".", "..", "a/", "a\x00b"})
}
