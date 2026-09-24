package envstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/borgim/sagansync/agent/internal/validate"
)

func TestSetGetUnset(t *testing.T) {
	s := New(t.TempDir())
	if m, err := s.Get("app", "production"); err != nil || len(m) != 0 {
		t.Fatalf("Get on empty store = %v, %v", m, err)
	}
	if err := s.Set("app", "production", map[string]string{"B": "2", "A": "1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("app", "production", map[string]string{"C": "3"}); err != nil {
		t.Fatal(err)
	}
	m, _ := s.Get("app", "production")
	if strings.Join(Keys(m), ",") != "A,B,C" {
		t.Fatalf("keys = %v", Keys(m))
	}
	if err := s.Unset("app", "production", []string{"B", "MISSING"}); err != nil {
		t.Fatal(err)
	}
	m, _ = s.Get("app", "production")
	if strings.Join(Keys(m), ",") != "A,C" {
		t.Fatalf("keys after unset = %v", Keys(m))
	}
}

func TestValuesRoundTripExactly(t *testing.T) {
	s := New(t.TempDir())
	tricky := "line1\nline2 \"quoted\" $HOME a=b 'single' \\ ç"
	if err := s.Set("app", "production", map[string]string{"TRICKY": tricky}); err != nil {
		t.Fatal(err)
	}
	m, _ := s.Get("app", "production")
	if m["TRICKY"] != tricky {
		t.Fatalf("got %q, want %q", m["TRICKY"], tricky)
	}
}

func TestRejectsBadKeysAndValues(t *testing.T) {
	s := New(t.TempDir())
	for _, kv := range []map[string]string{{"1BAD": "x"}, {"A-B": "x"}, {"OK": "nul\x00byte"}} {
		if err := s.Set("app", "production", kv); !errors.Is(err, validate.ErrInvalid) {
			t.Errorf("Set(%v) = %v, want validate.ErrInvalid", kv, err)
		}
	}
}

func TestFilePermissionsAndDelete(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	_ = s.Set("app", "production", map[string]string{"SECRET": "x"})
	path := filepath.Join(dir, "app", "production.json")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	if err := s.Delete("app", "production"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("env file still exists")
	}
	if err := s.Delete("app", "production"); err != nil {
		t.Fatalf("second Delete: %v", err)
	}
}

func TestGetReturnsACopy(t *testing.T) {
	s := New(t.TempDir())
	_ = s.Set("app", "production", map[string]string{"A": "1"})
	m, _ := s.Get("app", "production")
	m["A"] = "changed"
	again, _ := s.Get("app", "production")
	if again["A"] != "1" {
		t.Fatal("mutating the returned map changed the store")
	}
}
