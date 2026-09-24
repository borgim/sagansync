package gateway

import (
	"errors"
	"slices"
	"testing"
)

func TestSplit(t *testing.T) {
	cases := map[string][]string{
		`a b`:                       {"a", "b"},
		`  a   b  `:                 {"a", "b"},
		`'a b' c`:                   {"a b", "c"},
		`"a \"b\" \\c"`:             {`a "b" \c`},
		`'it'"'"'s'`:                {"it's"},
		`''`:                        {""},
		`a\ b`:                      {"a b"},
		`x;rm -rf /`:                {"x;rm", "-rf", "/"},
		`$(id) | cat`:               {"$(id)", "|", "cat"},
		`--command '["npm","run"]'`: {"--command", `["npm","run"]`},
	}
	for in, want := range cases {
		got, err := Split(in)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("Split(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{`'open`, `"open`, `trailing\`} {
		if _, err := Split(in); err == nil {
			t.Errorf("Split(%q) accepted malformed input", in)
		}
	}
}

func TestParse(t *testing.T) {
	allowed := map[string][]string{
		"sagand deploy --project app":        {"deploy", "--project", "app"},
		"/usr/local/bin/sagand list":         {"list"},
		"version":                            {"version"},
		"sagand put app feat-x 'src/a b.ts'": {"put", "app", "feat-x", "src/a b.ts"},
	}
	for in, want := range allowed {
		got, err := Parse(in)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	denied := []string{"", "   ", "sagand", "bash", "bash -i", "sh -c id", "sagand daemon", "sagand gateway",
		"scp -t /tmp", "rsync --server . /tmp", "internal-sftp", "'unterminated"}
	for _, in := range denied {
		if _, err := Parse(in); !errors.Is(err, ErrDenied) {
			t.Errorf("Parse(%q) = %v, want ErrDenied", in, err)
		}
	}
}
