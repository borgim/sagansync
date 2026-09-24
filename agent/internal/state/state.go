// Package state persists what sagand is running. The daemon is the source of
// truth; containers are reconciled against this file.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	ModeDeploy = "deploy"
	ModeDev    = "dev"
)

type Workspace struct {
	Host          string    `json:"host,omitempty"`
	Domain        string    `json:"domain,omitempty"`
	PreviewDomain string    `json:"previewDomain,omitempty"`
	InternalPort  int       `json:"internalPort"`
	HealthPath    string    `json:"healthPath,omitempty"`
	Mode          string    `json:"mode"`
	Release       string    `json:"release"`
	Container     string    `json:"container"`
	Command       []string  `json:"command,omitempty"`
	HostPort      int       `json:"hostPort"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type Entry struct {
	Project   string
	Workspace string
	WS        Workspace
}

type project struct {
	Workspaces map[string]Workspace `json:"workspaces"`
}

type file struct {
	Version  int                `json:"version"`
	Projects map[string]project `json:"projects"`
}

type Store struct {
	path string
	mu   sync.Mutex
	data file
}

func Open(path string) (*Store, error) {
	_ = os.Remove(path + ".tmp")
	s := &Store{path: path, data: file{Version: 1, Projects: map[string]project{}}}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return nil, fmt.Errorf("state: %s is corrupt (fix or move it before starting sagand): %w", path, err)
	}
	if s.data.Projects == nil {
		s.data.Projects = map[string]project{}
	}
	return s, nil
}

func (s *Store) Get(p, w string) (Workspace, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws, ok := s.data.Projects[p].Workspaces[w]
	return ws, ok
}

func (s *Store) Put(p, w string, ws Workspace) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pr := s.data.Projects[p]
	if pr.Workspaces == nil {
		pr.Workspaces = map[string]Workspace{}
	}
	prev, had := pr.Workspaces[w]
	pr.Workspaces[w] = ws
	s.data.Projects[p] = pr
	if err := s.save(); err != nil {
		if had {
			pr.Workspaces[w] = prev
		} else {
			delete(pr.Workspaces, w)
		}
		return err
	}
	return nil
}

func (s *Store) Delete(p, w string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pr, ok := s.data.Projects[p]
	if !ok {
		return nil
	}
	prev, had := pr.Workspaces[w]
	if !had {
		return nil
	}
	delete(pr.Workspaces, w)
	if len(pr.Workspaces) == 0 {
		delete(s.data.Projects, p)
	}
	if err := s.save(); err != nil {
		pr.Workspaces[w] = prev
		s.data.Projects[p] = pr
		return err
	}
	return nil
}

func (s *Store) All() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Entry
	for p, pr := range s.data.Projects {
		for w, ws := range pr.Workspaces {
			out = append(out, Entry{Project: p, Workspace: w, WS: ws})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		return out[i].Workspace < out[j].Workspace
	})
	return out
}

func (s *Store) HostOwner(host string) (Entry, bool) {
	if host == "" {
		return Entry{}, false
	}
	for _, e := range s.All() {
		if e.WS.Host == host {
			return e, true
		}
	}
	return Entry{}, false
}

// save writes the whole state to a temp file, fsyncs it and renames it over
// the real file, so a crash leaves either the old or the new state.
func (s *Store) save() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}
