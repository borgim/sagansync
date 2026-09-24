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

// afterCheck runs between path validation and the filesystem changes. Tests
// use it to swap a directory for a symlink, like a racing container would.
var afterCheck = func() {}

// WriteFile atomically replaces root/rel with the content of r, keeping the
// mode of the file it replaces. The dev container can modify root while this
// runs, so every filesystem operation goes through os.Root, which refuses to
// leave root even if a directory is swapped for a symlink mid-way.
func WriteFile(root, rel string, r io.Reader) error {
	realRoot, relOS, err := resolve(root, rel)
	if err != nil {
		return err
	}
	afterCheck()
	rt, err := os.OpenRoot(realRoot)
	if err != nil {
		return err
	}
	defer rt.Close()
	dir := filepath.Dir(relOS)
	if err := rt.MkdirAll(dir, 0o755); err != nil {
		return bad("creating %s: %v", dir, err)
	}
	mode := os.FileMode(0o644)
	if fi, err := rt.Lstat(relOS); err == nil && fi.Mode().IsRegular() {
		mode = fi.Mode().Perm()
	}
	tmp := filepath.Join(dir, fmt.Sprintf(".sagan-put-%d-%d", os.Getpid(), time.Now().UnixNano()))
	f, err := rt.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return bad("creating %s: %v", rel, err)
	}
	defer rt.Remove(tmp)
	n, err := io.Copy(f, io.LimitReader(r, DefaultLimits.MaxBytes+1))
	if err == nil {
		err = f.Chmod(mode) // OpenFile's mode is reduced by the umask
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > DefaultLimits.MaxBytes {
		return bad("file larger than %d bytes", DefaultLimits.MaxBytes)
	}
	if err := rt.Rename(tmp, relOS); err != nil {
		return bad("replacing %s: %v", rel, err)
	}
	return nil
}

// RemoveFile deletes root/rel (a file or a whole directory) if it exists,
// through os.Root for the same reason as WriteFile.
func RemoveFile(root, rel string) error {
	realRoot, relOS, err := resolve(root, rel)
	if err != nil {
		return err
	}
	afterCheck()
	rt, err := os.OpenRoot(realRoot)
	if err != nil {
		return err
	}
	defer rt.Close()
	if err := rt.RemoveAll(relOS); err != nil {
		return bad("removing %s: %v", rel, err)
	}
	return nil
}

// resolve validates rel and rejects, with a clear error, parents that already
// point outside root. os.Root enforces the same rule against later changes.
func resolve(root, rel string) (realRoot, relOS string, err error) {
	if err := validate.RelPath("path", rel); err != nil {
		return "", "", err
	}
	if realRoot, err = filepath.EvalSymlinks(root); err != nil {
		return "", "", err
	}
	relOS = filepath.FromSlash(rel)
	if err := checkParent(realRoot, filepath.Join(realRoot, relOS), false); err != nil {
		return "", "", err
	}
	return realRoot, relOS, nil
}
