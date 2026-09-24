// Package api exposes the deployer over HTTP on a Unix socket that only the
// sagan user can open.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/borgim/sagansync/agent/internal/deploy"
	"github.com/borgim/sagansync/agent/internal/events"
)

type VersionInfo struct {
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
}

type Server struct {
	d       *deploy.Deployer
	version string
}

func NewServer(d *deploy.Deployer, version string) *Server { return &Server{d: d, version: version} }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/version", s.handleVersion)
	mux.HandleFunc("POST /v1/deploy", s.handleDeploy)
	mux.HandleFunc("POST /v1/dev", s.handleDev)
	mux.HandleFunc("POST /v1/remove", s.handleRemove)
	mux.HandleFunc("GET /v1/list", s.handleList)
	mux.HandleFunc("GET /v1/logs", s.handleLogs)
	mux.HandleFunc("PUT /v1/files", s.handlePutFile)
	mux.HandleFunc("DELETE /v1/files", s.handleDeleteFile)
	mux.HandleFunc("GET /v1/env", s.handleEnvList)
	mux.HandleFunc("POST /v1/env", s.handleEnvSet)
	mux.HandleFunc("DELETE /v1/env", s.handleEnvUnset)
	return mux
}

// Listen replaces any stale socket file and restricts the new one to its owner.
func Listen(socket string) (net.Listener, error) {
	if err := os.Remove(socket); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	l, err := net.Listen("unix", socket)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func asOpError(err error) *deploy.OpError {
	var oe *deploy.OpError
	if errors.As(err, &oe) {
		return oe
	}
	return &deploy.OpError{Code: deploy.CodeInternal, Msg: err.Error()}
}

func writeError(w http.ResponseWriter, err error) {
	oe := asOpError(err)
	status := http.StatusInternalServerError
	switch oe.Code {
	case deploy.CodeInvalid:
		status = http.StatusBadRequest
	case deploy.CodeProductionLocked:
		status = http.StatusForbidden
	case deploy.CodeNotFound:
		status = http.StatusNotFound
	case deploy.CodeBusy, deploy.CodeHostConflict:
		status = http.StatusConflict
	}
	writeJSON(w, status, map[string]string{"code": oe.Code, "message": oe.Msg})
}

// stream runs a long operation and reports it as NDJSON events. The operation
// keeps running if the client disconnects, so a deploy is never left half done.
func (s *Server) stream(w http.ResponseWriter, r *http.Request, run func(context.Context, events.Emitter) error) {
	_ = http.NewResponseController(w).EnableFullDuplex()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	em := events.NewWriter(w)
	if err := run(context.WithoutCancel(r.Context()), em); err != nil {
		oe := asOpError(err)
		em.Emit(events.Fail(oe.Code, oe.Msg, oe.Logs))
	}
}

func parseRequest(q url.Values) (deploy.Request, error) {
	port, err := strconv.Atoi(q.Get("port"))
	if err != nil {
		return deploy.Request{}, &deploy.OpError{Code: deploy.CodeInvalid, Msg: "port must be a number"}
	}
	timeout := 60
	if v := q.Get("healthTimeout"); v != "" {
		if timeout, err = strconv.Atoi(v); err != nil || timeout < 1 {
			return deploy.Request{}, &deploy.OpError{Code: deploy.CodeInvalid, Msg: "healthTimeout must be a positive number of seconds"}
		}
	}
	return deploy.Request{Project: q.Get("project"), Workspace: q.Get("workspace"), Domain: q.Get("domain"),
		PreviewDomain: q.Get("previewDomain"), InternalPort: port, HealthPath: q.Get("healthPath"),
		HealthTimeout: time.Duration(timeout) * time.Second, Sha: q.Get("sha")}, nil
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, VersionInfo{Version: s.version, Protocol: events.Protocol})
}

func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	s.stream(w, r, func(ctx context.Context, em events.Emitter) error {
		req, err := parseRequest(r.URL.Query())
		if err != nil {
			return err
		}
		return s.d.Deploy(ctx, req, r.Body, em)
	})
}

func (s *Server) handleDev(w http.ResponseWriter, r *http.Request) {
	s.stream(w, r, func(ctx context.Context, em events.Emitter) error {
		q := r.URL.Query()
		req, err := parseRequest(q)
		if err != nil {
			return err
		}
		var cmd []string
		if err := json.Unmarshal([]byte(q.Get("command")), &cmd); err != nil {
			return &deploy.OpError{Code: deploy.CodeInvalid, Msg: "command must be a JSON array of strings"}
		}
		return s.d.Dev(ctx, deploy.DevRequest{Request: req, Command: cmd,
			Build: q.Get("build") == "true", Force: q.Get("force") == "true"}, r.Body, em)
	})
}

func (s *Server) handleRemove(w http.ResponseWriter, r *http.Request) {
	s.stream(w, r, func(ctx context.Context, em events.Emitter) error {
		q := r.URL.Query()
		if err := s.d.Remove(ctx, q.Get("project"), q.Get("workspace")); err != nil {
			return err
		}
		em.Emit(events.Done("", "", 0))
		return nil
	})
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.d.List(r.Context()))
}

type flushWriter struct {
	w     http.ResponseWriter
	rc    *http.ResponseController
	wrote bool
}

func (f *flushWriter) Write(p []byte) (int, error) {
	if !f.wrote {
		f.w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		f.wrote = true
	}
	n, err := f.w.Write(p)
	_ = f.rc.Flush()
	return n, err
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	tail, err := strconv.Atoi(q.Get("tail"))
	if err != nil || tail <= 0 {
		tail = 100
	}
	out := &flushWriter{w: w, rc: http.NewResponseController(w)}
	err = s.d.Logs(r.Context(), q.Get("project"), q.Get("workspace"), tail, q.Get("follow") == "true", out)
	if err != nil && !out.wrote {
		writeError(w, err)
	}
}

func (s *Server) handlePutFile(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if err := s.d.PutFile(q.Get("project"), q.Get("workspace"), q.Get("path"), r.Body); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if err := s.d.RemoveFile(q.Get("project"), q.Get("workspace"), q.Get("path")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleEnvList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	keys, err := s.d.EnvKeys(q.Get("project"), q.Get("workspace"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]string{"keys": keys})
}

func (s *Server) handleEnvSet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var kv map[string]string
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&kv); err != nil {
		writeError(w, &deploy.OpError{Code: deploy.CodeInvalid, Msg: "body must be a JSON object of strings"})
		return
	}
	if err := s.d.EnvSet(q.Get("project"), q.Get("workspace"), kv); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleEnvUnset(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if err := s.d.EnvUnset(q.Get("project"), q.Get("workspace"), q["key"]); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
