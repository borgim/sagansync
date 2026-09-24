// Package envstore keeps the environment variables of each workspace. Values
// arrive from the CLI through stdin and are only readable by the sagan user.
package envstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/borgim/sagansync/agent/internal/validate"
)

type Store struct{ dir string }

func New(dir string) *Store { return &Store{dir: dir} }

func (s *Store) path(p, w string) string { return filepath.Join(s.dir, p, w+".json") }

func (s *Store) Get(p, w string) (map[string]string, error) {
	b, err := os.ReadFile(s.path(p, w))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("envstore: %s is corrupt: %w", s.path(p, w), err)
	}
	return m, nil
}

func (s *Store) Set(p, w string, kv map[string]string) error {
	for k, v := range kv {
		if err := validate.EnvKey("env", k); err != nil {
			return err
		}
		if strings.ContainsRune(v, 0) {
			return fmt.Errorf("%w: env: value of %s contains a NUL byte", validate.ErrInvalid, k)
		}
	}
	m, err := s.Get(p, w)
	if err != nil {
		return err
	}
	for k, v := range kv {
		m[k] = v
	}
	return s.write(p, w, m)
}

func (s *Store) Unset(p, w string, keys []string) error {
	m, err := s.Get(p, w)
	if err != nil {
		return err
	}
	for _, k := range keys {
		delete(m, k)
	}
	return s.write(p, w, m)
}

func (s *Store) Delete(p, w string) error {
	err := os.Remove(s.path(p, w))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Store) write(p, w string, m map[string]string) error {
	path := s.path(p, w)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func Keys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
