// Package testutil builds fixtures shared by the agent's tests.
package testutil

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

// Entry describes one member of a test archive.
type Entry struct {
	Name string
	Body string
	Type byte   // tar.TypeReg when zero
	Link string // target of symlinks and hardlinks
	Mode int64  // 0644 (0755 for dirs) when zero
}

// TarGz builds a gzip-compressed tar archive in memory.
func TarGz(t testing.TB, entries ...Entry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typ := e.Type
		if typ == 0 {
			typ = tar.TypeReg
		}
		mode := e.Mode
		if mode == 0 {
			mode = 0o644
			if typ == tar.TypeDir {
				mode = 0o755
			}
		}
		hdr := &tar.Header{Name: e.Name, Typeflag: typ, Linkname: e.Link, Mode: mode}
		if typ == tar.TypeReg {
			hdr.Size = int64(len(e.Body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header %q: %v", e.Name, err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.Body)); err != nil {
				t.Fatalf("tar body %q: %v", e.Name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

// App is a minimal deployable project: a Dockerfile and a Node server.
func App(t testing.TB) *bytes.Buffer {
	return TarGz(t,
		Entry{Name: "Dockerfile", Body: "FROM docker.io/library/node:22-alpine\nCOPY . /app\nCMD [\"node\", \"/app/server.js\"]\n"},
		Entry{Name: "server.js", Body: "require('http').createServer((q, s) => s.end('ok')).listen(3000)\n"},
	)
}
