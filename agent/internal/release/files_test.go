package release

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/borgim/sagansync/agent/internal/validate"
)

func TestNewID(t *testing.T) {
	now := time.Date(2026, 9, 24, 10, 15, 0, 0, time.FixedZone("BRT", -3*3600))
	cases := map[string]string{
		"a1b2c3d4e5": "20260924-131500-a1b2c3d",
		"":           "20260924-131500-nogit",
		"zzz":        "20260924-131500-nogit",
		"ABCDEF1":    "20260924-131500-nogit",
	}
	for sha, want := range cases {
		if got := NewID(now, sha); got != want {
			t.Errorf("NewID(%q) = %q, want %q", sha, got, want)
		}
	}
}

func TestAllocateAvoidsCollisions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "releases")
	a, err := Allocate(dir, "20260924-100000-abc1234")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Allocate(dir, "20260924-100000-abc1234")
	if err != nil {
		t.Fatal(err)
	}
	if a != "20260924-100000-abc1234" || b != "20260924-100000-abc1234-2" {
		t.Fatalf("got %q and %q", a, b)
	}
}

func TestPruneKeepsNewestAndCurrent(t *testing.T) {
	dir := t.TempDir()
	for i := 1; i <= 5; i++ {
		if err := os.Mkdir(filepath.Join(dir, "20260924-10000"+string(rune('0'+i))+"-aaaaaaa"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := Prune(dir, 3, "20260924-100001-aaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(removed, ",") != "20260924-100002-aaaaaaa" {
		t.Fatalf("removed = %v", removed)
	}
	entries, _ := os.ReadDir(dir)
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	sort.Strings(left)
	want := "20260924-100001-aaaaaaa,20260924-100003-aaaaaaa,20260924-100004-aaaaaaa,20260924-100005-aaaaaaa"
	if strings.Join(left, ",") != want {
		t.Fatalf("left = %v", left)
	}
}

func TestPruneMissingDirIsNoop(t *testing.T) {
	removed, err := Prune(filepath.Join(t.TempDir(), "nope"), 3, "")
	if err != nil || len(removed) != 0 {
		t.Fatalf("Prune = %v, %v", removed, err)
	}
}

func TestWriteAndRemoveFile(t *testing.T) {
	root := t.TempDir()
	if err := WriteFile(root, "src/new/file.ts", strings.NewReader("v1")); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(root, "src/new/file.ts", strings.NewReader("v2")); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(root, "src", "new", "file.ts"), "v2")
	if err := RemoveFile(root, "src/new"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "src", "new")); !os.IsNotExist(err) {
		t.Fatalf("src/new still exists: %v", err)
	}
	if err := RemoveFile(root, "never/existed.txt"); err != nil {
		t.Fatalf("removing a missing file: %v", err)
	}
}

func TestWriteFileRejectsBadPaths(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"../x", "/etc/passwd", "a/../../x", ""} {
		if err := WriteFile(root, rel, strings.NewReader("x")); !errors.Is(err, validate.ErrInvalid) {
			t.Errorf("WriteFile(%q) = %v, want validate.ErrInvalid", rel, err)
		}
	}
}

// A container can create symlinks inside its bind-mounted source directory.
// Writing or deleting through one must never touch files outside the root.
func TestFileOpsDoNotFollowSymlinksOutOfRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	victim := filepath.Join(outside, "victim.txt")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(root, "out/victim.txt", strings.NewReader("pwned")); !errors.Is(err, ErrInvalidArchive) {
		t.Fatalf("WriteFile through symlink = %v, want ErrInvalidArchive", err)
	}
	if err := WriteFile(root, "out/new/dir.txt", strings.NewReader("x")); !errors.Is(err, ErrInvalidArchive) {
		t.Fatalf("WriteFile creating dirs through symlink = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "new")); err == nil {
		t.Fatal("a directory was created outside the root")
	}
	if err := RemoveFile(root, "out/victim.txt"); !errors.Is(err, ErrInvalidArchive) {
		t.Fatalf("RemoveFile through symlink = %v, want ErrInvalidArchive", err)
	}
	assertFile(t, victim, "keep")
}

// swapToSymlink replaces root/dir with a symlink to outside once the path
// checks have passed, the way a process inside a dev container could.
func swapToSymlink(t *testing.T, root, dir, outside string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
		t.Fatal(err)
	}
	afterCheck = func() {
		os.RemoveAll(filepath.Join(root, dir))
		os.Symlink(outside, filepath.Join(root, dir))
	}
	t.Cleanup(func() { afterCheck = func() {} })
}

func TestWriteFileSurvivesSymlinkSwapRace(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	swapToSymlink(t, root, "src", outside)
	_ = WriteFile(root, "src/app.js", strings.NewReader("x"))
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("wrote %v outside the root", entries[0].Name())
	}
}

func TestRemoveFileSurvivesSymlinkSwapRace(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	victim := filepath.Join(outside, "app.js")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	swapToSymlink(t, root, "src", outside)
	_ = RemoveFile(root, "src/app.js")
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("file outside the root was removed: %v", err)
	}
}

func TestWriteFileKeepsExistingMode(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "start.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(root, "start.sh", strings.NewReader("#!/bin/sh\necho hi\n")); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(script)
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, want 0755", fi.Mode().Perm())
	}
}
