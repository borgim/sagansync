package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/borgim/sagansync/agent/internal/testutil"
)

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(b) != want {
		t.Fatalf("%s = %q, want %q", path, b, want)
	}
}

func TestExtractWritesFilesAndDirs(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "rel")
	err := Extract(testutil.TarGz(t,
		testutil.Entry{Name: "./", Type: tar.TypeDir},
		testutil.Entry{Name: "src/", Type: tar.TypeDir},
		testutil.Entry{Name: "./src/index.js", Body: "console.log(1)"},
		testutil.Entry{Name: "deep/nested/file.txt", Body: "x"},
		testutil.Entry{Name: "run.sh", Body: "#!/bin/sh", Mode: 0o755},
	), dest, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(dest, "src", "index.js"), "console.log(1)")
	assertFile(t, filepath.Join(dest, "deep", "nested", "file.txt"), "x")
	fi, err := os.Stat(filepath.Join(dest, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o100 == 0 {
		t.Error("run.sh lost its executable bit")
	}
}

func TestExtractAllowsLinksInside(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "rel")
	err := Extract(testutil.TarGz(t,
		testutil.Entry{Name: "real/file.txt", Body: "hi"},
		testutil.Entry{Name: "link", Type: tar.TypeSymlink, Link: "real"},
		testutil.Entry{Name: "hard.txt", Type: tar.TypeLink, Link: "real/file.txt"},
		testutil.Entry{Name: "link/through.txt", Body: "via link"},
	), dest, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(dest, "real", "through.txt"), "via link")
	assertFile(t, filepath.Join(dest, "hard.txt"), "hi")
	if target, err := os.Readlink(filepath.Join(dest, "link")); err != nil || target != "real" {
		t.Fatalf("Readlink = %q, %v; want real", target, err)
	}
}

func TestExtractRejectsUnsafeEntries(t *testing.T) {
	cases := map[string][]testutil.Entry{
		"absolute path":       {{Name: "/etc/passwd", Body: "x"}},
		"parent traversal":    {{Name: "../evil", Body: "x"}},
		"nested traversal":    {{Name: "a/../../evil", Body: "x"}},
		"absolute symlink":    {{Name: "l", Type: tar.TypeSymlink, Link: "/etc"}},
		"symlink with dotdot": {{Name: "l", Type: tar.TypeSymlink, Link: "../outside"}},
		"chained symlink escape": {
			{Name: "x", Type: tar.TypeDir},
			{Name: "x/l", Type: tar.TypeSymlink, Link: "."},
			{Name: "x/l/m", Type: tar.TypeSymlink, Link: "sub/../.."},
		},
		"hardlink outside":    {{Name: "h", Type: tar.TypeLink, Link: "../../etc/passwd"}},
		"hardlink to missing": {{Name: "h", Type: tar.TypeLink, Link: "nope"}},
		"char device":         {{Name: "dev", Type: tar.TypeChar}},
		"fifo":                {{Name: "fifo", Type: tar.TypeFifo}},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			err := Extract(testutil.TarGz(t, entries...), filepath.Join(parent, "rel"), DefaultLimits)
			if !errors.Is(err, ErrInvalidArchive) {
				t.Fatalf("err = %v, want ErrInvalidArchive", err)
			}
			if _, err := os.Lstat(filepath.Join(parent, "evil")); err == nil {
				t.Fatal("a file was written outside the release directory")
			}
		})
	}
}

func TestExtractEnforcesLimits(t *testing.T) {
	two := func() *bytes.Buffer {
		return testutil.TarGz(t, testutil.Entry{Name: "a", Body: "12345"}, testutil.Entry{Name: "b", Body: "12345"})
	}
	if err := Extract(two(), filepath.Join(t.TempDir(), "r"), Limits{MaxBytes: 8, MaxEntries: 10}); !errors.Is(err, ErrInvalidArchive) {
		t.Errorf("byte limit: err = %v, want ErrInvalidArchive", err)
	}
	if err := Extract(two(), filepath.Join(t.TempDir(), "r"), Limits{MaxBytes: 1 << 20, MaxEntries: 1}); !errors.Is(err, ErrInvalidArchive) {
		t.Errorf("entry limit: err = %v, want ErrInvalidArchive", err)
	}
}

func TestExtractRejectsGarbageAndTruncatedInput(t *testing.T) {
	if err := Extract(strings.NewReader("not a tarball"), filepath.Join(t.TempDir(), "r"), DefaultLimits); !errors.Is(err, ErrInvalidArchive) {
		t.Errorf("garbage: err = %v", err)
	}
	b := testutil.App(t).Bytes()
	if err := Extract(bytes.NewReader(b[:len(b)/2]), filepath.Join(t.TempDir(), "r"), DefaultLimits); !errors.Is(err, ErrInvalidArchive) {
		t.Errorf("truncated: err = %v", err)
	}
}

func TestTarDirRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rel")
	if err := Extract(testutil.TarGz(t,
		testutil.Entry{Name: "Dockerfile", Body: "FROM x"},
		testutil.Entry{Name: "src/a.js", Body: "a"},
		testutil.Entry{Name: "link", Type: tar.TypeSymlink, Link: "src"},
	), dir, DefaultLimits); err != nil {
		t.Fatal(err)
	}
	rc := TarDir(dir)
	defer rc.Close()
	tr := tar.NewReader(rc)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
		if hdr.Name == "link" && hdr.Linkname != "src" {
			t.Errorf("link target = %q", hdr.Linkname)
		}
	}
	sort.Strings(names)
	want := []string{"Dockerfile", "link", "src/", "src/a.js"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("names = %v, want %v", names, want)
	}
}

// git archive starts every tarball with a pax global header that carries the
// commit id; it holds no file and must be skipped.
func TestExtractSkipsPaxGlobalHeader(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(tw.WriteHeader(&tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header",
		PAXRecords: map[string]string{"comment": "abc1234def"}}))
	must(tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "app/index.js", Mode: 0o644, Size: 2}))
	_, err := tw.Write([]byte("ok"))
	must(err)
	must(tw.Close())
	must(gz.Close())

	dest := t.TempDir()
	if err := Extract(&buf, dest, DefaultLimits); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "app", "index.js")); err != nil || string(b) != "ok" {
		t.Fatalf("index.js = %q, %v", b, err)
	}
	if _, err := os.Lstat(filepath.Join(dest, "pax_global_header")); !os.IsNotExist(err) {
		t.Error("pax_global_header was written as a file")
	}
}
