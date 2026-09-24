package release

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/borgim/sagansync/agent/internal/validate"
)

var shaRe = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// NewID returns YYYYMMDD-HHMMSS-<sha7> in UTC, or "nogit" instead of the sha.
func NewID(now time.Time, sha string) string {
	short := "nogit"
	if shaRe.MatchString(sha) {
		short = sha[:7]
	}
	return now.UTC().Format("20060102-150405") + "-" + short
}

// Allocate creates the directory for a new release and returns its final id.
func Allocate(releasesDir, id string) (string, error) {
	if err := os.MkdirAll(releasesDir, 0o755); err != nil {
		return "", err
	}
	for i := 1; i < 100; i++ {
		candidate := id
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d", id, i)
		}
		err := os.Mkdir(filepath.Join(releasesDir, candidate), 0o755)
		if err == nil {
			return candidate, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("could not allocate a directory for release %s", id)
}

// Prune deletes all but the keep newest releases, never deleting current.
func Prune(releasesDir string, keep int, current string) ([]string, error) {
	entries, err := os.ReadDir(releasesDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	var removed []string
	for i, id := range ids {
		if i < keep || id == current {
			continue
		}
		if err := os.RemoveAll(filepath.Join(releasesDir, id)); err != nil {
			return removed, err
		}
		removed = append(removed, id)
	}
	return removed, nil
}

// WriteFile atomically replaces root/rel with the content of r.
func WriteFile(root, rel string, r io.Reader) error {
	if err := validate.RelPath("path", rel); err != nil {
		return err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	target := filepath.Join(realRoot, filepath.FromSlash(rel))
	if err := safeParent(realRoot, target); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".sagan-put-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, io.LimitReader(r, DefaultLimits.MaxBytes+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > DefaultLimits.MaxBytes {
		return bad("file larger than %d bytes", DefaultLimits.MaxBytes)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}

// RemoveFile deletes root/rel (a file or a whole directory) if it exists.
func RemoveFile(root, rel string) error {
	if err := validate.RelPath("path", rel); err != nil {
		return err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	target := filepath.Join(realRoot, filepath.FromSlash(rel))
	if err := checkParent(realRoot, target, false); err != nil {
		return err
	}
	return os.RemoveAll(target)
}
