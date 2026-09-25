// Package release stores uploaded project snapshots on disk safely.
package release

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/borgim/sagansync/agent/internal/validate"
)

// ErrInvalidArchive wraps every error caused by the archive's content.
var ErrInvalidArchive = errors.New("invalid archive")

type Limits struct {
	MaxBytes   int64
	MaxEntries int
}

var DefaultLimits = Limits{MaxBytes: 500 << 20, MaxEntries: 100_000}

func bad(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidArchive, fmt.Sprintf(format, args...))
}

// Extract unpacks a gzip-compressed tar stream into dest.
func Extract(r io.Reader, dest string, lim Limits) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	root, err := filepath.EvalSymlinks(dest)
	if err != nil {
		return err
	}
	gz, err := gzip.NewReader(r)
	if err != nil {
		return bad("not a gzip stream: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	for entries := 0; ; entries++ {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return bad("reading archive: %v", err)
		}
		if entries >= lim.MaxEntries {
			return bad("more than %d entries", lim.MaxEntries)
		}
		// git archive writes a pax global header holding the commit id.
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(hdr.Name, "./"), "/")
		if name == "" || name == "." {
			continue
		}
		if err := validate.RelPath("path", name); err != nil {
			return bad("unsafe path %q", hdr.Name)
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := safeParent(root, target); err != nil {
				return err
			}
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			total += hdr.Size
			if total > lim.MaxBytes {
				return bad("archive larger than %d bytes", lim.MaxBytes)
			}
			if err := writeRegular(root, target, tr, fileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := checkSymlinkTarget(hdr.Linkname); err != nil {
				return err
			}
			if err := prepare(root, target); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		case tar.TypeLink:
			src, err := hardlinkSource(root, hdr.Linkname)
			if err != nil {
				return err
			}
			if err := prepare(root, target); err != nil {
				return err
			}
			if err := os.Link(src, target); err != nil {
				return err
			}
		default:
			return bad("unsupported entry type %q for %q", hdr.Typeflag, name)
		}
	}
}

func fileMode(m int64) os.FileMode {
	mode := os.FileMode(m).Perm()
	if mode == 0 {
		return 0o644
	}
	return mode | 0o600
}

// within reports whether p (already free of symlinks) is inside root.
func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// checkParent makes sure target's parent directory resolves to a place inside
// root. The deepest existing ancestor is checked before anything is created,
// so a symlink pointing outside root never gets directories created through it.
func checkParent(root, target string, create bool) error {
	parent := filepath.Dir(target)
	existing := parent
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		next := filepath.Dir(existing)
		if next == existing {
			break
		}
		existing = next
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return bad("resolving %s: %v", existing, err)
	}
	if !within(root, real) {
		return bad("%s escapes the release directory", target)
	}
	if !create {
		return nil
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	if real, err = filepath.EvalSymlinks(parent); err != nil {
		return bad("resolving %s: %v", parent, err)
	}
	if !within(root, real) {
		return bad("%s escapes the release directory", target)
	}
	return nil
}

func safeParent(root, target string) error { return checkParent(root, target, true) }

// prepare creates target's parent inside root and removes whatever sits at
// target, so the caller can create it from scratch.
func prepare(root, target string) error {
	if err := safeParent(root, target); err != nil {
		return err
	}
	fi, err := os.Lstat(target)
	if err != nil {
		return nil
	}
	if fi.IsDir() {
		return bad("%s already exists as a directory", target)
	}
	return os.Remove(target)
}

func writeRegular(root, target string, r io.Reader, mode os.FileMode) error {
	if err := prepare(root, target); err != nil {
		return err
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return bad("reading %s: %v", filepath.Base(target), err)
	}
	return f.Close()
}

// checkSymlinkTarget only accepts relative targets without "..". Checking the
// resolved destination alone can be bypassed by chaining symlinks.
func checkSymlinkTarget(l string) error {
	if l == "" || strings.HasPrefix(l, "/") || strings.ContainsRune(l, 0) {
		return bad("unsafe symlink target %q", l)
	}
	for _, part := range strings.Split(l, "/") {
		if part == ".." {
			return bad("symlink target %q must not contain ..", l)
		}
	}
	return nil
}

func hardlinkSource(root, link string) (string, error) {
	name := strings.TrimPrefix(link, "./")
	if err := validate.RelPath("link", name); err != nil {
		return "", bad("unsafe hardlink target %q", link)
	}
	real, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		return "", bad("hardlink target %q not found", link)
	}
	if !within(root, real) {
		return "", bad("hardlink target %q escapes the release directory", link)
	}
	fi, err := os.Lstat(real)
	if err != nil || !fi.Mode().IsRegular() {
		return "", bad("hardlink target %q is not a regular file", link)
	}
	return real, nil
}
