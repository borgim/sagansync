package domains

import (
	"strings"
	"testing"
)

func TestHost(t *testing.T) {
	cases := []struct {
		name, project, workspace, domain, preview, want string
	}{
		{"production uses domain", "app", "production", "api.example.com", "example.com", "api.example.com"},
		{"production without domain", "app", "production", "", "example.com", ""},
		{"branch without preview", "app", "feat-x", "api.example.com", "", "feat-x.api.example.com"},
		{"staging without preview", "app", "staging", "api.example.com", "", "staging.api.example.com"},
		{"branch with preview", "barbervip", "feat-login", "api.pedroborgim.com.br", "pedroborgim.com.br", "feat-login-barbervip.pedroborgim.com.br"},
		{"staging with preview", "app", "staging", "api.example.com", "example.com", "staging-app.example.com"},
		{"no domains", "app", "feat-x", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Host(c.project, c.workspace, c.domain, c.preview); got != c.want {
				t.Fatalf("Host() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestHostTruncatesLongLabels(t *testing.T) {
	w, p := strings.Repeat("w", 40), strings.Repeat("p", 40)
	got := Host(p, w, "", "example.com")
	label := strings.TrimSuffix(got, ".example.com")
	if len(label) != 63 {
		t.Fatalf("label %q has length %d, want 63", label, len(label))
	}
	if want := w + "-" + strings.Repeat("p", 14) + "-"; !strings.HasPrefix(label, want) {
		t.Fatalf("label %q should start with %q", label, want)
	}
	if again := Host(p, w, "", "example.com"); again != got {
		t.Fatalf("not deterministic: %q vs %q", got, again)
	}
	if other := Host(p[:39]+"q", w, "", "example.com"); other == got {
		t.Fatalf("different projects produced the same host %q", got)
	}
}
