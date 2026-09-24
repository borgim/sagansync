// Package events is the line-delimited JSON protocol between sagand and the CLI.
package events

import (
	"encoding/json"
	"io"
	"sync"
)

// Protocol is bumped on breaking changes; the CLI refuses a mismatch.
const Protocol = 1

type Event struct {
	V        int      `json:"v"`
	Type     string   `json:"type"`
	Name     string   `json:"name,omitempty"`
	Stream   string   `json:"stream,omitempty"`
	Line     string   `json:"line,omitempty"`
	URL      string   `json:"url,omitempty"`
	Release  string   `json:"release,omitempty"`
	HostPort int      `json:"hostPort,omitempty"`
	Code     string   `json:"code,omitempty"`
	Message  string   `json:"message,omitempty"`
	Logs     []string `json:"logs,omitempty"`
}

type Emitter interface {
	Emit(Event)
}

func Step(name string) Event        { return Event{Type: "step", Name: name} }
func Log(stream, line string) Event { return Event{Type: "log", Stream: stream, Line: line} }
func Warn(code, msg string) Event   { return Event{Type: "warn", Code: code, Message: msg} }
func Fail(code, msg string, logs []string) Event {
	return Event{Type: "error", Code: code, Message: msg, Logs: logs}
}
func Done(url, release string, hostPort int) Event {
	return Event{Type: "done", URL: url, Release: release, HostPort: hostPort}
}

// ExitCode maps the terminal event of a stream to a process exit code.
func ExitCode(e Event) int {
	switch {
	case e.Type == "done":
		return 0
	case e.Type == "error" && e.Code == "invalid":
		return 2
	default:
		return 1
	}
}

// Writer emits events as JSON lines, flushing after each one when the
// underlying writer supports it (http.ResponseWriter does).
type Writer struct {
	mu  sync.Mutex
	w   io.Writer
	enc *json.Encoder
}

func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w, enc: json.NewEncoder(w)}
}

func (w *Writer) Emit(e Event) {
	e.V = Protocol
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.enc.Encode(e)
	if f, ok := w.w.(interface{ Flush() }); ok {
		f.Flush()
	}
}

// Recorder keeps events in memory. It is meant for tests.
type Recorder struct {
	mu     sync.Mutex
	Events []Event
}

func (r *Recorder) Emit(e Event) {
	e.V = Protocol
	r.mu.Lock()
	r.Events = append(r.Events, e)
	r.mu.Unlock()
}

func (r *Recorder) Steps() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.Events {
		if e.Type == "step" {
			out = append(out, e.Name)
		}
	}
	return out
}

func (r *Recorder) Last() Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.Events) == 0 {
		return Event{}
	}
	return r.Events[len(r.Events)-1]
}

func (r *Recorder) Has(typ, code string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.Events {
		if e.Type == typ && e.Code == code {
			return true
		}
	}
	return false
}
