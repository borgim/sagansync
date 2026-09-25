package state

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func sample(host string) Workspace {
	return Workspace{Host: host, InternalPort: 3000, Mode: ModeDeploy, Release: "r1",
		Container: "sagan_app_production_r1", HostPort: 41000,
		UpdatedAt: time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)}
}

func TestOpenMissingFileIsEmpty(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.All()) != 0 {
		t.Fatalf("All() = %v, want empty", s.All())
	}
}

func TestPutPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, _ := Open(path)
	if err := s.Put("app", "production", sample("app.test")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put("app", "feat-x", sample("feat-x.app.test")); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ws, ok := reopened.Get("app", "production")
	if !ok || ws.Host != "app.test" || ws.HostPort != 41000 {
		t.Fatalf("Get = %+v, %v", ws, ok)
	}
	all := reopened.All()
	if len(all) != 2 || all[0].Workspace != "feat-x" || all[1].Workspace != "production" {
		t.Fatalf("All() = %+v", all)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("state file mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestDeleteRemovesWorkspace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, _ := Open(path)
	_ = s.Put("app", "production", sample("app.test"))
	if err := s.Delete("app", "production"); err != nil {
		t.Fatal(err)
	}
	reopened, _ := Open(path)
	if _, ok := reopened.Get("app", "production"); ok {
		t.Fatal("workspace still present after Delete")
	}
	if err := s.Delete("app", "never"); err != nil {
		t.Fatalf("deleting a missing workspace: %v", err)
	}
}

func TestLeftoverTempFileIsIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, _ := Open(path)
	_ = s.Put("app", "production", sample("app.test"))
	if err := os.WriteFile(path+".tmp", []byte("{half"), 0o600); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.Get("app", "production"); !ok {
		t.Fatal("state lost")
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("leftover .tmp was not removed")
	}
}

func TestCorruptStateIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("Open accepted a corrupt state file")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "{not json" {
		t.Fatal("Open modified the corrupt file")
	}
}

func TestHostOwner(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "state.json"))
	_ = s.Put("app", "production", sample("app.test"))
	e, ok := s.HostOwner("app.test")
	if !ok || e.Project != "app" || e.Workspace != "production" {
		t.Fatalf("HostOwner = %+v, %v", e, ok)
	}
	if _, ok := s.HostOwner("other.test"); ok {
		t.Fatal("unknown host has an owner")
	}
	if _, ok := s.HostOwner(""); ok {
		t.Fatal("empty host must never match")
	}
}

func TestLocks(t *testing.T) {
	l := NewLocks()
	unlock, ok := l.TryLock("app", "production")
	if !ok {
		t.Fatal("first TryLock failed")
	}
	if _, ok := l.TryLock("app", "production"); ok {
		t.Fatal("second TryLock on the same workspace succeeded")
	}
	other, ok := l.TryLock("app", "feat-x")
	if !ok {
		t.Fatal("different workspace was blocked")
	}
	other()
	unlock()
	again, ok := l.TryLock("app", "production")
	if !ok {
		t.Fatal("TryLock after unlock failed")
	}
	again()
}

func TestPutIfHostFreeRefusesAHostOwnedElsewhere(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "state.json"))
	if err := s.PutIfHostFree("one", "production", sample("shared.test")); err != nil {
		t.Fatal(err)
	}
	err := s.PutIfHostFree("two", "production", sample("shared.test"))
	var taken *HostTakenError
	if !errors.As(err, &taken) || taken.Owner.Project != "one" || taken.Owner.Workspace != "production" {
		t.Fatalf("err = %v, want HostTakenError owned by one/production", err)
	}
	if _, ok := s.Get("two", "production"); ok {
		t.Error("the refused workspace was saved")
	}
	// The owner itself can keep writing its host.
	if err := s.PutIfHostFree("one", "production", sample("shared.test")); err != nil {
		t.Fatalf("owner rewrite: %v", err)
	}
}
