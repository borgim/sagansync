# sagand (agente) — Plano de Implementação (plano 1 de 3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Construir o binário Go `sagand`: o daemon que faz TLS, proxy, deploy sem downtime, estado e reconciliação, e o cliente/gateway SSH que conversa com ele por socket Unix.

**Architecture:** Módulo Go em `agent/`, com pacotes pequenos em `internal/`: `validate`, `domains`, `events`, `release`, `state`, `envstore`, `runtime` (interface + fake), `podman` (cliente REST), `proxy` (rotas + certmagic), `deploy` (orquestração), `api` (HTTP no socket) e `gateway` (forced command). `cmd/sagand` junta tudo. A orquestração depende da interface `runtime.Runtime`, testada com um fake que sobe servidores `httptest` reais.

**Tech Stack:** Go 1.24, stdlib, `github.com/caddyserver/certmagic`, API REST do Podman (`/v4.0.0/libpod`).

**Spec:** `docs/superpowers/specs/2026-09-24-sagansync-v0.1-agent-design.md`

**Planos seguintes:** plano 2 (CLI em TypeScript) e plano 3 (provision, teste ponta a ponta na VM Lima, release). Este plano entrega o agente testado de forma isolada; a integração com o Podman real roda na VM no plano 3.

## Global Constraints

- Módulo `github.com/borgim/sagansync/agent`, diretiva `go 1.24`.
- Única dependência externa direta: `github.com/caddyserver/certmagic`. Nada de cobra, nada das bindings oficiais do Podman.
- **Nenhum `os/exec`** e nenhum shell no agente.
- API do Podman: prefixo `/v4.0.0/libpod`, via socket Unix (Podman ≥ 4.3).
- Nomes de projeto/workspace: `^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`.
- Container `sagan_<p>_<w>_<id>`; imagem `localhost/sagan_<p>_<w>:<id>`; imagem de dev `localhost/sagan_<p>_<w>:dev`; volume `sagan_<p>_<w>_node_modules`.
- Labels: `sagan.managed=true`, `sagan.project`, `sagan.workspace`, `sagan.release`.
- Id de release: `YYYYMMDD-HHMMSS-<sha7>` (UTC), `nogit` quando o sha não for hex de 7–40 caracteres.
- Caminhos padrão: socket `/run/sagand/sagand.sock` (sobrescrito por `SAGAND_SOCKET`), `/srv/sagan`, `/var/lib/sagand/{state.json,env/,certs/,config.json}`.
- Tempos: health a cada 500 ms, `healthTimeout` padrão 60 s, dreno 10 s, stop 10 s, 3 releases mantidas, 50 linhas de log na falha, health do dev 30 s (só TCP), reconciliação a cada 60 s, `Ensure` de TLS até 60 s.
- Limites do tar: 500 MB descompactados, 100.000 entradas.
- Eventos: JSON por linha, `"v":1`, tipos `step`, `log`, `warn`, `done`, `error`.
- Códigos de saída do cliente: 0 sucesso, 1 erro de operação, 2 validação (`code == "invalid"`), 126 comando negado pelo gateway.
- `go vet ./...` e `go test -race ./...` passam no macOS e no Linux (os testes com a build tag `integration` só rodam na VM).

## Review Focus

1. **Dois deploys em workspaces diferentes ao mesmo tempo** precisam dar certo; a trava é por workspace, não global. Teste na Task 12 (`TestDeploysToDifferentWorkspacesRunConcurrently`).
2. **Header `Host` com maiúsculas, porta ou ponto final** (`APP.test:443`, `app.test.`) precisa rotear igual. Teste na Task 10 (`TestRouterNormalizesHost`).
3. **Container anterior sumiu** (alguém rodou `podman rm` à mão) e o próximo deploy precisa funcionar. Teste na Task 12 (`TestRedeployWhenPreviousContainerVanished`).
4. **Upload interrompido no meio** (SSH caiu) não pode deixar release parcial nem derrubar o app atual. Teste na Task 12 (`TestTruncatedUploadLeavesNoRelease`).
5. **Valores de env com quebra de linha, aspas, `$` e `=`** precisam chegar intactos ao container. Teste na Task 7 (`TestValuesRoundTripExactly`).

## Mapa de arquivos

```
agent/
├── go.mod, go.sum
├── cmd/sagand/
│   ├── main.go           # dispatch: daemon | gateway | comandos do cliente
│   ├── client.go         # comandos do cliente (falam com o socket)
│   ├── daemon.go         # config e servidores do daemon
│   └── main_test.go
└── internal/
    ├── validate/         # regras de entrada
    ├── domains/          # workspace → hostname
    ├── events/           # protocolo de eventos (Writer, Recorder, ExitCode)
    ├── release/          # extração segura, TarDir, WriteFile/RemoveFile, Allocate/Prune
    ├── state/            # state.json atômico + Locks por workspace
    ├── envstore/         # env por workspace (JSON 0600)
    ├── runtime/          # interface Runtime, tipos, labels
    │   └── fakert/       # fake com servidores httptest reais
    ├── podman/           # cliente REST do Podman
    ├── proxy/            # Router, RedirectHandler, TLS (certmagic)
    ├── deploy/           # Deploy, Dev, arquivos, Remove, List, Logs, Env, Reconcile
    ├── api/              # servidor HTTP no socket + cliente
    ├── gateway/          # SSH_ORIGINAL_COMMAND → comando permitido
    └── testutil/
        ├── tarball.go    # monta tar.gz em memória para testes
        └── testdeploy/   # Deployer montado com fakes, usado por deploy, api e cmd
.github/workflows/ci.yml
```

---

### Task 1: Módulo Go e pacote `validate`

**Files:**
- Create: `agent/go.mod`
- Create: `agent/internal/validate/validate.go`
- Test: `agent/internal/validate/validate_test.go`

**Interfaces:**
- Produces:
  - `var ErrInvalid error`
  - `func Name(field, s string) error`
  - `func Domain(field, s string) error`
  - `func Port(field string, p int) error`
  - `func HealthPath(field, s string) error`
  - `func EnvKey(field, s string) error`
  - `func RelPath(field, s string) error`
  - Todos os erros envolvem `ErrInvalid` (`errors.Is(err, ErrInvalid)`).

- [ ] **Step 1: Criar o módulo**

```bash
mkdir -p agent && cd agent && go mod init github.com/borgim/sagansync/agent && go mod edit -go=1.24
```

- [ ] **Step 2: Escrever o teste que falha**

`agent/internal/validate/validate_test.go`:

```go
package validate

import (
	"errors"
	"strings"
	"testing"
)

func check(t *testing.T, fn func(string) error, ok, bad []string) {
	t.Helper()
	for _, s := range ok {
		if err := fn(s); err != nil {
			t.Errorf("%q: got %v, want nil", s, err)
		}
	}
	for _, s := range bad {
		if err := fn(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: got %v, want ErrInvalid", s, err)
		}
	}
}

func TestName(t *testing.T) {
	check(t, func(s string) error { return Name("project", s) },
		[]string{"a", "app", "feat-login", "a1-b2", strings.Repeat("a", 40)},
		[]string{"", "-a", "a-", "A", "a_b", "a.b", "a b", "a;rm", "$(x)", strings.Repeat("a", 41)})
}

func TestDomain(t *testing.T) {
	check(t, func(s string) error { return Domain("domain", s) },
		[]string{"example.com", "api.pedroborgim.com.br", "feat-x-app.example.com", "a.b"},
		[]string{"", "localhost", "https://example.com", "example.com:443", "*.example.com",
			"-a.com", "a..com", "Example.com", strings.Repeat("a", 64) + ".com",
			strings.Repeat("a.", 127) + "com"})
}

func TestPort(t *testing.T) {
	for _, p := range []int{1, 3000, 65535} {
		if err := Port("port", p); err != nil {
			t.Errorf("%d: %v", p, err)
		}
	}
	for _, p := range []int{0, -1, 65536} {
		if err := Port("port", p); !errors.Is(err, ErrInvalid) {
			t.Errorf("%d: got %v, want ErrInvalid", p, err)
		}
	}
}

func TestHealthPath(t *testing.T) {
	check(t, func(s string) error { return HealthPath("healthPath", s) },
		[]string{"", "/", "/health", "/api/health?x=1"},
		[]string{"health", "/a b", "/a\nb", "/" + strings.Repeat("a", 200)})
}

func TestEnvKey(t *testing.T) {
	check(t, func(s string) error { return EnvKey("env", s) },
		[]string{"A", "_x", "DATABASE_URL", "a1"},
		[]string{"", "1A", "A-B", "A B", "A=B"})
}

func TestRelPath(t *testing.T) {
	check(t, func(s string) error { return RelPath("path", s) },
		[]string{"a", "a/b", "src/index.ts", ".env"},
		[]string{"", "/a", "../a", "a/../../b", "a/./b", "a//b", ".", "..", "a/", "a\x00b"})
}
```

- [ ] **Step 3: Rodar e ver falhar**

Run: `cd agent && go test ./internal/validate/`
Expected: FAIL com `undefined: Name` (e as demais funções).

- [ ] **Step 4: Implementar**

`agent/internal/validate/validate.go`:

```go
// Package validate holds the input rules the agent enforces on everything it
// receives from the CLI. The agent never trusts the client.
package validate

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// ErrInvalid is wrapped by every validation error.
var ErrInvalid = errors.New("invalid input")

var (
	nameRe   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)
	labelRe  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

func invalid(field, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalid, field, fmt.Sprintf(format, args...))
}

// Name validates project and workspace names.
func Name(field, s string) error {
	if !nameRe.MatchString(s) {
		return invalid(field, "%q must be 1-40 characters of a-z, 0-9 and '-', not starting or ending with '-'", s)
	}
	return nil
}

// Domain validates a hostname such as "api.example.com".
func Domain(field, s string) error {
	if len(s) == 0 || len(s) > 253 {
		return invalid(field, "%q must be 1-253 characters", s)
	}
	labels := strings.Split(s, ".")
	if len(labels) < 2 {
		return invalid(field, "%q must have at least two labels", s)
	}
	for _, l := range labels {
		if !labelRe.MatchString(l) {
			return invalid(field, "%q has an invalid label %q", s, l)
		}
	}
	return nil
}

// Port validates a TCP port number.
func Port(field string, p int) error {
	if p < 1 || p > 65535 {
		return invalid(field, "%d is not a valid port", p)
	}
	return nil
}

// HealthPath validates an optional HTTP path such as "/health".
func HealthPath(field, s string) error {
	if s == "" {
		return nil
	}
	control := strings.IndexFunc(s, func(r rune) bool { return r <= ' ' || r == 0x7f }) >= 0
	if !strings.HasPrefix(s, "/") || len(s) > 200 || control {
		return invalid(field, "%q must start with '/', contain no spaces and be at most 200 characters", s)
	}
	return nil
}

// EnvKey validates an environment variable name.
func EnvKey(field, s string) error {
	if !envKeyRe.MatchString(s) {
		return invalid(field, "%q is not a valid environment variable name", s)
	}
	return nil
}

// RelPath validates a clean, relative, slash-separated path that stays inside
// its root: no leading '/', no '..', no '.' segments and no NUL bytes.
func RelPath(field, s string) error {
	if s == "" || strings.HasPrefix(s, "/") || strings.ContainsRune(s, 0) {
		return invalid(field, "%q is not a relative path", s)
	}
	c := path.Clean(s)
	if c != s || c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return invalid(field, "%q must be a clean path inside the project", s)
	}
	return nil
}
```

- [ ] **Step 5: Rodar e ver passar**

Run: `cd agent && go test -race ./internal/validate/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add agent/go.mod agent/internal/validate
git commit -m "feat(agent): add Go module and input validation rules"
```

---

### Task 2: Pacote `domains`

**Files:**
- Create: `agent/internal/domains/domains.go`
- Test: `agent/internal/domains/domains_test.go`

**Interfaces:**
- Produces: `func Host(project, workspace, domain, previewDomain string) string`. Retorna `""` quando o workspace não tem hostname público. Não valida (quem chama valida o resultado com `validate.Domain`).

- [ ] **Step 1: Escrever o teste que falha**

`agent/internal/domains/domains_test.go`:

```go
package domains

import (
	"strings"
	"testing"
)

func TestHost(t *testing.T) {
	cases := []struct {
		name, project, workspace, domain, preview, want string
	}{
		{"production uses domain", "app", "production", "api.example.com", "example.com", "api.example.com"},
		{"production without domain", "app", "production", "", "example.com", ""},
		{"branch without preview", "app", "feat-x", "api.example.com", "", "feat-x.api.example.com"},
		{"staging without preview", "app", "staging", "api.example.com", "", "staging.api.example.com"},
		{"branch with preview", "barbervip", "feat-login", "api.pedroborgim.com.br", "pedroborgim.com.br", "feat-login-barbervip.pedroborgim.com.br"},
		{"staging with preview", "app", "staging", "api.example.com", "example.com", "staging-app.example.com"},
		{"no domains", "app", "feat-x", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Host(c.project, c.workspace, c.domain, c.preview); got != c.want {
				t.Fatalf("Host() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestHostTruncatesLongLabels(t *testing.T) {
	w, p := strings.Repeat("w", 40), strings.Repeat("p", 40)
	got := Host(p, w, "", "example.com")
	label := strings.TrimSuffix(got, ".example.com")
	if len(label) != 63 {
		t.Fatalf("label %q has length %d, want 63", label, len(label))
	}
	if want := w + "-" + strings.Repeat("p", 14) + "-"; !strings.HasPrefix(label, want) {
		t.Fatalf("label %q should start with %q", label, want)
	}
	if again := Host(p, w, "", "example.com"); again != got {
		t.Fatalf("not deterministic: %q vs %q", got, again)
	}
	if other := Host(p[:39]+"q", w, "", "example.com"); other == got {
		t.Fatalf("different projects produced the same host %q", got)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd agent && go test ./internal/domains/`
Expected: FAIL com `undefined: Host`

- [ ] **Step 3: Implementar**

`agent/internal/domains/domains.go`:

```go
// Package domains turns a project/workspace pair into its public hostname.
package domains

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
)

const maxLabel = 63

// Host returns the hostname a workspace is served on, or "" when it has none.
//
//	production              -> domain
//	other, previewDomain    -> <workspace>-<project>.<previewDomain>
//	other, no previewDomain -> <workspace>.<domain>
func Host(project, workspace, domain, previewDomain string) string {
	switch {
	case workspace == "production":
		return domain
	case previewDomain != "":
		return label(workspace+"-"+project) + "." + previewDomain
	case domain != "":
		return workspace + "." + domain
	}
	return ""
}

// label keeps a DNS label within 63 characters. Longer labels become their
// first 55 characters plus a short hash of the whole label, so the result is
// deterministic and different inputs stay distinct.
func label(s string) string {
	if len(s) <= maxLabel {
		return s
	}
	sum := sha1.Sum([]byte(s))
	return strings.TrimRight(s[:55], "-") + "-" + hex.EncodeToString(sum[:])[:7]
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd agent && go test -race ./internal/domains/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add agent/internal/domains
git commit -m "feat(agent): derive workspace hostnames with previewDomain support"
```

---

### Task 3: Pacote `events`

**Files:**
- Create: `agent/internal/events/events.go`
- Test: `agent/internal/events/events_test.go`

**Interfaces:**
- Produces:
  - `const Protocol = 1`
  - `type Event struct { V int; Type, Name, Stream, Line, URL, Release string; HostPort int; Code, Message string; Logs []string }` (tags JSON na implementação)
  - `type Emitter interface { Emit(Event) }`
  - `func NewWriter(w io.Writer) *Writer` — `(*Writer).Emit` grava uma linha JSON com `v:1`, é seguro para goroutines e chama `Flush()` se `w` tiver esse método.
  - `func Step(name string) Event`, `Log(stream, line string) Event`, `Warn(code, msg string) Event`, `Done(url, release string, hostPort int) Event`, `Fail(code, msg string, logs []string) Event`
  - `func ExitCode(e Event) int` — `done`→0, `error` com `Code=="invalid"`→2, resto→1.
  - `type Recorder struct { Events []Event }` com `Emit`, `Steps() []string`, `Last() Event`, `Has(typ, code string) bool`.

- [ ] **Step 1: Escrever o teste que falha**

`agent/internal/events/events_test.go`:

```go
package events

import (
	"bufio"
	"bytes"
	"encoding/json"
	"sync"
	"testing"
)

type flushBuffer struct {
	bytes.Buffer
	flushes int
}

func (f *flushBuffer) Flush() { f.flushes++ }

func TestWriterEmitsOneJSONLinePerEvent(t *testing.T) {
	var buf flushBuffer
	w := NewWriter(&buf)
	w.Emit(Step("build"))
	w.Emit(Done("https://app.test", "r1", 41000))

	sc := bufio.NewScanner(&buf.Buffer)
	var got []Event
	for sc.Scan() {
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("line %q is not JSON: %v", sc.Text(), err)
		}
		got = append(got, e)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if got[0].V != Protocol || got[0].Type != "step" || got[0].Name != "build" {
		t.Errorf("first event = %+v", got[0])
	}
	if got[1].Type != "done" || got[1].URL != "https://app.test" || got[1].HostPort != 41000 {
		t.Errorf("second event = %+v", got[1])
	}
	if buf.flushes != 2 {
		t.Errorf("flushes = %d, want 2", buf.flushes)
	}
}

func TestWriterIsSafeForConcurrentUse(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); w.Emit(Log("build", "line")) }()
	}
	wg.Wait()
	sc := bufio.NewScanner(&buf)
	n := 0
	for sc.Scan() {
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("interleaved output: %q", sc.Text())
		}
		n++
	}
	if n != 50 {
		t.Fatalf("got %d lines, want 50", n)
	}
}

func TestExitCode(t *testing.T) {
	cases := []struct {
		e    Event
		want int
	}{
		{Done("", "", 0), 0},
		{Fail("invalid", "bad", nil), 2},
		{Fail("build_failed", "x", nil), 1},
		{Step("build"), 1},
	}
	for _, c := range cases {
		if got := ExitCode(c.e); got != c.want {
			t.Errorf("ExitCode(%+v) = %d, want %d", c.e, got, c.want)
		}
	}
}

func TestRecorder(t *testing.T) {
	var r Recorder
	r.Emit(Step("extract"))
	r.Emit(Warn("tls_pending", "later"))
	r.Emit(Step("build"))
	r.Emit(Done("", "r1", 1))
	if got := r.Steps(); len(got) != 2 || got[0] != "extract" || got[1] != "build" {
		t.Errorf("Steps() = %v", got)
	}
	if !r.Has("warn", "tls_pending") || r.Has("warn", "other") {
		t.Error("Has() gave the wrong answer")
	}
	if r.Last().Type != "done" || r.Last().V != Protocol {
		t.Errorf("Last() = %+v", r.Last())
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd agent && go test ./internal/events/`
Expected: FAIL com `undefined: NewWriter`

- [ ] **Step 3: Implementar**

`agent/internal/events/events.go`:

```go
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
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd agent && gofmt -l ./internal/events; go test -race ./internal/events/`
Expected: nenhum arquivo listado pelo `gofmt` (se listar, rode `gofmt -w`); testes PASS.

- [ ] **Step 5: Commit**

```bash
git add agent/internal/events
git commit -m "feat(agent): add line-delimited JSON event protocol"
```

---

### Task 4: `testutil` e extração segura de tar (`release.Extract`, `release.TarDir`)

**Files:**
- Create: `agent/internal/testutil/tarball.go`
- Create: `agent/internal/release/extract.go`
- Create: `agent/internal/release/tardir.go`
- Test: `agent/internal/release/extract_test.go`

**Interfaces:**
- Consumes: `validate.RelPath` (Task 1).
- Produces:
  - `testutil.Entry{Name, Body string; Type byte; Link string; Mode int64}`, `testutil.TarGz(t testing.TB, entries ...Entry) *bytes.Buffer`, `testutil.App(t testing.TB) *bytes.Buffer` (Dockerfile + server.js).
  - `var release.ErrInvalidArchive error`
  - `type release.Limits struct { MaxBytes int64; MaxEntries int }`, `var release.DefaultLimits` (500 MB, 100.000).
  - `func release.Extract(r io.Reader, dest string, lim Limits) error` — `r` é tar.gz; erros de conteúdo envolvem `ErrInvalidArchive`.
  - `func release.TarDir(dir string) io.ReadCloser` — tar **sem** gzip do diretório (contexto de build do Podman).
  - Helpers internos usados na Task 5: `within`, `checkParent`, `safeParent`, `prepare`, `bad`.

**Regras (spec 4.5):** caminhos absolutos e `..` são rejeitados; o alvo de um symlink precisa ser relativo e sem `..`; o alvo de um hardlink precisa ser um arquivo regular já extraído dentro da release; o diretório pai de cada entrada, com symlinks resolvidos, precisa estar dentro da release; device, fifo e outros tipos especiais são rejeitados.

- [ ] **Step 1: Criar o helper de testes**

`agent/internal/testutil/tarball.go`:

```go
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
```

- [ ] **Step 2: Escrever o teste que falha**

`agent/internal/release/extract_test.go`:

```go
package release

import (
	"archive/tar"
	"bytes"
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
```

- [ ] **Step 3: Rodar e ver falhar**

Run: `cd agent && go test ./internal/release/`
Expected: FAIL com `undefined: Extract`

- [ ] **Step 4: Implementar a extração**

`agent/internal/release/extract.go`:

```go
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
```

- [ ] **Step 5: Implementar o `TarDir`**

`agent/internal/release/tardir.go`:

```go
package release

import (
	"archive/tar"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// TarDir streams dir as an uncompressed tar archive, the build context format
// the Podman API expects. Closing the reader stops the walk.
func TarDir(dir string) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil || rel == "." {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			link := ""
			if info.Mode()&os.ModeSymlink != 0 {
				if link, err = os.Readlink(p); err != nil {
					return err
				}
			}
			hdr, err := tar.FileInfoHeader(info, link)
			if err != nil {
				return err
			}
			hdr.Name = filepath.ToSlash(rel)
			if d.IsDir() {
				hdr.Name += "/"
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, f)
			f.Close()
			return err
		})
		if err == nil {
			err = tw.Close()
		}
		pw.CloseWithError(err)
	}()
	return pr
}
```

- [ ] **Step 6: Rodar e ver passar**

Run: `cd agent && go vet ./internal/... && go test -race ./internal/release/`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add agent/internal/testutil agent/internal/release
git commit -m "feat(agent): extract uploaded archives safely and stream build contexts"
```

---

### Task 5: Operações de arquivo da release (`Allocate`, `NewID`, `Prune`, `WriteFile`, `RemoveFile`)

**Files:**
- Create: `agent/internal/release/files.go`
- Test: `agent/internal/release/files_test.go`

**Interfaces:**
- Consumes: `checkParent`, `safeParent`, `within`, `bad`, `DefaultLimits` (Task 4); `validate.RelPath` (Task 1).
- Produces:
  - `func NewID(now time.Time, sha string) string`
  - `func Allocate(releasesDir, id string) (string, error)` — cria `releasesDir/<id>`; se já existir, tenta `<id>-2`, `<id>-3`...
  - `func Prune(releasesDir string, keep int, current string) ([]string, error)` — apaga as mais antigas além das `keep` mais novas (ordem lexicográfica), nunca apaga `current`; devolve os ids apagados.
  - `func WriteFile(root, rel string, r io.Reader) error` — grava de forma atômica (temp + rename); caminho inválido → `validate.ErrInvalid`; fuga via symlink → `ErrInvalidArchive`.
  - `func RemoveFile(root, rel string) error` — mesmas regras; remove arquivo ou diretório.

- [ ] **Step 1: Escrever o teste que falha**

`agent/internal/release/files_test.go`:

```go
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
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd agent && go test ./internal/release/`
Expected: FAIL com `undefined: NewID`

- [ ] **Step 3: Implementar**

`agent/internal/release/files.go`:

```go
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
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd agent && go test -race ./internal/release/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add agent/internal/release
git commit -m "feat(agent): allocate, prune and edit release files safely"
```

---

### Task 6: Pacote `state` (estado atômico e travas)

**Files:**
- Create: `agent/internal/state/state.go`
- Create: `agent/internal/state/locks.go`
- Test: `agent/internal/state/state_test.go`

**Interfaces:**
- Produces:
  - `const ModeDeploy = "deploy"`, `ModeDev = "dev"`
  - `type Workspace struct { Host, Domain, PreviewDomain string; InternalPort int; HealthPath, Mode, Release, Container string; Command []string; HostPort int; UpdatedAt time.Time }`
  - `type Entry struct { Project, Workspace string; WS Workspace }`
  - `func Open(path string) (*Store, error)` — arquivo ausente = estado vazio; `.tmp` que sobrou é apagado; JSON corrompido = erro (nunca apaga o estado).
  - `(*Store).Get(p, w string) (Workspace, bool)`, `Put(p, w string, ws Workspace) error`, `Delete(p, w string) error`, `All() []Entry` (ordenado por projeto e workspace), `HostOwner(host string) (Entry, bool)`
  - `func NewLocks() *Locks`, `(*Locks).TryLock(p, w string) (unlock func(), ok bool)`

- [ ] **Step 1: Escrever o teste que falha**

`agent/internal/state/state_test.go`:

```go
package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func sample(host string) Workspace {
	return Workspace{Host: host, InternalPort: 3000, Mode: ModeDeploy, Release: "r1",
		Container: "sagan_app_production_r1", HostPort: 41000,
		UpdatedAt: time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)}
}

func TestOpenMissingFileIsEmpty(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.All()) != 0 {
		t.Fatalf("All() = %v, want empty", s.All())
	}
}

func TestPutPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, _ := Open(path)
	if err := s.Put("app", "production", sample("app.test")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put("app", "feat-x", sample("feat-x.app.test")); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ws, ok := reopened.Get("app", "production")
	if !ok || ws.Host != "app.test" || ws.HostPort != 41000 {
		t.Fatalf("Get = %+v, %v", ws, ok)
	}
	all := reopened.All()
	if len(all) != 2 || all[0].Workspace != "feat-x" || all[1].Workspace != "production" {
		t.Fatalf("All() = %+v", all)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("state file mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestDeleteRemovesWorkspace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, _ := Open(path)
	_ = s.Put("app", "production", sample("app.test"))
	if err := s.Delete("app", "production"); err != nil {
		t.Fatal(err)
	}
	reopened, _ := Open(path)
	if _, ok := reopened.Get("app", "production"); ok {
		t.Fatal("workspace still present after Delete")
	}
	if err := s.Delete("app", "never"); err != nil {
		t.Fatalf("deleting a missing workspace: %v", err)
	}
}

func TestLeftoverTempFileIsIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, _ := Open(path)
	_ = s.Put("app", "production", sample("app.test"))
	if err := os.WriteFile(path+".tmp", []byte("{half"), 0o600); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.Get("app", "production"); !ok {
		t.Fatal("state lost")
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("leftover .tmp was not removed")
	}
}

func TestCorruptStateIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("Open accepted a corrupt state file")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "{not json" {
		t.Fatal("Open modified the corrupt file")
	}
}

func TestHostOwner(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "state.json"))
	_ = s.Put("app", "production", sample("app.test"))
	e, ok := s.HostOwner("app.test")
	if !ok || e.Project != "app" || e.Workspace != "production" {
		t.Fatalf("HostOwner = %+v, %v", e, ok)
	}
	if _, ok := s.HostOwner("other.test"); ok {
		t.Fatal("unknown host has an owner")
	}
	if _, ok := s.HostOwner(""); ok {
		t.Fatal("empty host must never match")
	}
}

func TestLocks(t *testing.T) {
	l := NewLocks()
	unlock, ok := l.TryLock("app", "production")
	if !ok {
		t.Fatal("first TryLock failed")
	}
	if _, ok := l.TryLock("app", "production"); ok {
		t.Fatal("second TryLock on the same workspace succeeded")
	}
	other, ok := l.TryLock("app", "feat-x")
	if !ok {
		t.Fatal("different workspace was blocked")
	}
	other()
	unlock()
	again, ok := l.TryLock("app", "production")
	if !ok {
		t.Fatal("TryLock after unlock failed")
	}
	again()
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd agent && go test ./internal/state/`
Expected: FAIL com `undefined: Open`

- [ ] **Step 3: Implementar o estado**

`agent/internal/state/state.go`:

```go
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
```

- [ ] **Step 4: Implementar as travas**

`agent/internal/state/locks.go`:

```go
package state

import "sync"

// Locks allows one operation per workspace at a time. Operations never queue:
// a busy workspace is reported to the caller right away.
type Locks struct {
	mu   sync.Mutex
	held map[string]bool
}

func NewLocks() *Locks { return &Locks{held: map[string]bool{}} }

func (l *Locks) TryLock(p, w string) (unlock func(), ok bool) {
	key := p + "\x00" + w
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held[key] {
		return nil, false
	}
	l.held[key] = true
	return func() {
		l.mu.Lock()
		delete(l.held, key)
		l.mu.Unlock()
	}, true
}
```

- [ ] **Step 5: Rodar e ver passar**

Run: `cd agent && go test -race ./internal/state/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add agent/internal/state
git commit -m "feat(agent): persist workspace state atomically with per-workspace locks"
```

---

### Task 7: Pacote `envstore`

**Files:**
- Create: `agent/internal/envstore/envstore.go`
- Test: `agent/internal/envstore/envstore_test.go`

**Interfaces:**
- Consumes: `validate.EnvKey`, `validate.ErrInvalid` (Task 1).
- Produces:
  - `func New(dir string) *Store`
  - `(*Store).Get(p, w string) (map[string]string, error)` — mapa novo a cada chamada (pode ser alterado por quem chama); ausente = vazio.
  - `(*Store).Set(p, w string, kv map[string]string) error` — mescla; chave inválida ou valor com NUL → `validate.ErrInvalid`.
  - `(*Store).Unset(p, w string, keys []string) error`, `(*Store).Delete(p, w string) error`
  - `func Keys(m map[string]string) []string` (ordenadas)
  - Arquivo: `<dir>/<p>/<w>.json`, modo `0600`, diretório `0700`. Os nomes `p`/`w` são validados por quem chama (Task 13).

- [ ] **Step 1: Escrever o teste que falha**

`agent/internal/envstore/envstore_test.go`:

```go
package envstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/borgim/sagansync/agent/internal/validate"
)

func TestSetGetUnset(t *testing.T) {
	s := New(t.TempDir())
	if m, err := s.Get("app", "production"); err != nil || len(m) != 0 {
		t.Fatalf("Get on empty store = %v, %v", m, err)
	}
	if err := s.Set("app", "production", map[string]string{"B": "2", "A": "1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("app", "production", map[string]string{"C": "3"}); err != nil {
		t.Fatal(err)
	}
	m, _ := s.Get("app", "production")
	if strings.Join(Keys(m), ",") != "A,B,C" {
		t.Fatalf("keys = %v", Keys(m))
	}
	if err := s.Unset("app", "production", []string{"B", "MISSING"}); err != nil {
		t.Fatal(err)
	}
	m, _ = s.Get("app", "production")
	if strings.Join(Keys(m), ",") != "A,C" {
		t.Fatalf("keys after unset = %v", Keys(m))
	}
}

func TestValuesRoundTripExactly(t *testing.T) {
	s := New(t.TempDir())
	tricky := "line1\nline2 \"quoted\" $HOME a=b 'single' \\ ç"
	if err := s.Set("app", "production", map[string]string{"TRICKY": tricky}); err != nil {
		t.Fatal(err)
	}
	m, _ := s.Get("app", "production")
	if m["TRICKY"] != tricky {
		t.Fatalf("got %q, want %q", m["TRICKY"], tricky)
	}
}

func TestRejectsBadKeysAndValues(t *testing.T) {
	s := New(t.TempDir())
	for _, kv := range []map[string]string{{"1BAD": "x"}, {"A-B": "x"}, {"OK": "nul\x00byte"}} {
		if err := s.Set("app", "production", kv); !errors.Is(err, validate.ErrInvalid) {
			t.Errorf("Set(%v) = %v, want validate.ErrInvalid", kv, err)
		}
	}
}

func TestFilePermissionsAndDelete(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	_ = s.Set("app", "production", map[string]string{"SECRET": "x"})
	path := filepath.Join(dir, "app", "production.json")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	if err := s.Delete("app", "production"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("env file still exists")
	}
	if err := s.Delete("app", "production"); err != nil {
		t.Fatalf("second Delete: %v", err)
	}
}

func TestGetReturnsACopy(t *testing.T) {
	s := New(t.TempDir())
	_ = s.Set("app", "production", map[string]string{"A": "1"})
	m, _ := s.Get("app", "production")
	m["A"] = "changed"
	again, _ := s.Get("app", "production")
	if again["A"] != "1" {
		t.Fatal("mutating the returned map changed the store")
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd agent && go test ./internal/envstore/`
Expected: FAIL com `undefined: New`

- [ ] **Step 3: Implementar**

`agent/internal/envstore/envstore.go`:

```go
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
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd agent && go test -race ./internal/envstore/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add agent/internal/envstore
git commit -m "feat(agent): store per-workspace environment variables"
```

---

### Task 8: Interface `runtime` e o fake `fakert`

**Files:**
- Create: `agent/internal/runtime/runtime.go`
- Create: `agent/internal/runtime/fakert/fakert.go`
- Test: `agent/internal/runtime/fakert/fakert_test.go`

**Interfaces:**
- Produces (pacote `runtime`):
  - `var ErrNotFound error`
  - Labels: `LabelManaged = "sagan.managed"`, `LabelProject = "sagan.project"`, `LabelWorkspace = "sagan.workspace"`, `LabelRelease = "sagan.release"`
  - `type Bind struct { Source, Dest string }`, `type Volume struct { Name, Dest string }`
  - `type ContainerSpec struct { Name, Image string; Command []string; Env map[string]string; InternalPort int; Labels map[string]string; Binds []Bind; Volumes []Volume }`
  - `type ContainerInfo struct { Name string; Running bool; ExitCode int; HostPort int; Labels map[string]string }`
  - `type Runtime interface` com os métodos abaixo. Todos devolvem erro envolvendo `ErrNotFound` quando o container, a imagem ou o volume não existe.
    ```go
    Build(ctx context.Context, contextTar io.Reader, tag string, log func(line string)) error
    ImageExists(ctx context.Context, tag string) (bool, error)
    Create(ctx context.Context, spec ContainerSpec) error
    Start(ctx context.Context, name string) error
    Stop(ctx context.Context, name string, timeout time.Duration) error
    Remove(ctx context.Context, name string) error        // força; remove volumes anônimos
    Inspect(ctx context.Context, name string) (ContainerInfo, error)
    Logs(ctx context.Context, name string, tail int, follow bool, w io.Writer) error
    List(ctx context.Context) ([]ContainerInfo, error)    // só label sagan.managed=true
    RemoveImage(ctx context.Context, tag string) error
    RemoveVolume(ctx context.Context, name string) error
    ```
- Produces (pacote `fakert`): `New() *Fake`; `type Behavior struct { Crash bool; Status int }`; `type Container struct { Spec runtime.ContainerSpec; Running bool; ExitCode, HostPort int }`; campos `BuildErr error`, `BuildLines []string`, `BuildHook func(tag string)`, `Behave func(runtime.ContainerSpec) Behavior`; métodos extras `Container(name) (Container, bool)`, `Names() []string`, `Builds() []string`, `RemovedImages() []string`, `RemovedVolumes() []string`, `Close()`.
  - Um container iniciado sem `Crash` sobe um `httptest.Server` real em `127.0.0.1` que responde `Status` (padrão 200) com o nome da imagem no corpo. `HostPort` é a porta desse servidor. `Logs` escreve `log from <nome>\n`.

- [ ] **Step 1: Escrever a interface**

`agent/internal/runtime/runtime.go`:

```go
// Package runtime is the container engine interface the deployer depends on.
package runtime

import (
	"context"
	"errors"
	"io"
	"time"
)

var ErrNotFound = errors.New("not found")

const (
	LabelManaged   = "sagan.managed"
	LabelProject   = "sagan.project"
	LabelWorkspace = "sagan.workspace"
	LabelRelease   = "sagan.release"
)

type Bind struct{ Source, Dest string }

type Volume struct{ Name, Dest string }

type ContainerSpec struct {
	Name         string
	Image        string
	Command      []string
	Env          map[string]string
	InternalPort int
	Labels       map[string]string
	Binds        []Bind
	Volumes      []Volume
}

type ContainerInfo struct {
	Name     string
	Running  bool
	ExitCode int
	HostPort int
	Labels   map[string]string
}

type Runtime interface {
	Build(ctx context.Context, contextTar io.Reader, tag string, log func(line string)) error
	ImageExists(ctx context.Context, tag string) (bool, error)
	Create(ctx context.Context, spec ContainerSpec) error
	Start(ctx context.Context, name string) error
	Stop(ctx context.Context, name string, timeout time.Duration) error
	Remove(ctx context.Context, name string) error
	Inspect(ctx context.Context, name string) (ContainerInfo, error)
	Logs(ctx context.Context, name string, tail int, follow bool, w io.Writer) error
	List(ctx context.Context) ([]ContainerInfo, error)
	RemoveImage(ctx context.Context, tag string) error
	RemoveVolume(ctx context.Context, name string) error
}
```

- [ ] **Step 2: Escrever o teste do fake que falha**

`agent/internal/runtime/fakert/fakert_test.go`:

```go
package fakert

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/borgim/sagansync/agent/internal/runtime"
)

func TestHealthyContainerServesHTTP(t *testing.T) {
	f := New()
	defer f.Close()
	ctx := context.Background()
	if err := f.Build(ctx, strings.NewReader("tar"), "localhost/img:1", func(string) {}); err != nil {
		t.Fatal(err)
	}
	spec := runtime.ContainerSpec{Name: "c1", Image: "localhost/img:1", Labels: map[string]string{runtime.LabelManaged: "true"}}
	if err := f.Create(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := f.Start(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	info, err := f.Inspect(ctx, "c1")
	if err != nil || !info.Running || info.HostPort == 0 {
		t.Fatalf("Inspect = %+v, %v", info, err)
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/health", info.HostPort))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "localhost/img:1" {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
	var logs bytes.Buffer
	_ = f.Logs(ctx, "c1", 50, false, &logs)
	if logs.String() != "log from c1\n" {
		t.Fatalf("logs = %q", logs.String())
	}
	if err := f.Remove(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Inspect(ctx, "c1"); !errors.Is(err, runtime.ErrNotFound) {
		t.Fatalf("Inspect after Remove = %v", err)
	}
}

func TestCrashAndBuildFailure(t *testing.T) {
	f := New()
	defer f.Close()
	ctx := context.Background()
	f.BuildErr = errors.New("npm ERR")
	if err := f.Build(ctx, strings.NewReader(""), "localhost/bad:1", func(string) {}); err == nil {
		t.Fatal("Build ignored BuildErr")
	}
	f.BuildErr = nil
	_ = f.Build(ctx, strings.NewReader(""), "localhost/img:1", func(string) {})
	f.Behave = func(runtime.ContainerSpec) Behavior { return Behavior{Crash: true} }
	_ = f.Create(ctx, runtime.ContainerSpec{Name: "c1", Image: "localhost/img:1"})
	_ = f.Start(ctx, "c1")
	info, _ := f.Inspect(ctx, "c1")
	if info.Running || info.ExitCode != 1 {
		t.Fatalf("crashed container = %+v", info)
	}
	if err := f.Create(ctx, runtime.ContainerSpec{Name: "c2", Image: "localhost/missing:1"}); err == nil {
		t.Fatal("Create accepted a missing image")
	}
}
```

- [ ] **Step 3: Rodar e ver falhar**

Run: `cd agent && go test ./internal/runtime/...`
Expected: FAIL com `undefined: New`

- [ ] **Step 4: Implementar o fake**

`agent/internal/runtime/fakert/fakert.go`:

```go
// Package fakert is an in-memory runtime.Runtime for tests. Started
// containers are real HTTP servers on 127.0.0.1, so health checks and the
// proxy can talk to them.
package fakert

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/borgim/sagansync/agent/internal/runtime"
)

// Behavior controls what a container does when it starts.
type Behavior struct {
	Crash  bool // exits immediately with code 1
	Status int  // HTTP status served on every path; 200 when zero
}

type Container struct {
	Spec     runtime.ContainerSpec
	Running  bool
	ExitCode int
	HostPort int
}

type entry struct {
	c   Container
	srv *httptest.Server
}

func (e *entry) stop() {
	if e.srv != nil {
		e.srv.Close()
		e.srv = nil
	}
	e.c.Running = false
	e.c.HostPort = 0
}

type Fake struct {
	mu             sync.Mutex
	images         map[string]bool
	containers     map[string]*entry
	builds         []string
	removedImages  []string
	removedVolumes []string

	// Test knobs: set them before the code under test runs.
	BuildErr   error
	BuildLines []string
	BuildHook  func(tag string)
	Behave     func(spec runtime.ContainerSpec) Behavior
}

var _ runtime.Runtime = (*Fake)(nil)

func New() *Fake {
	return &Fake{images: map[string]bool{}, containers: map[string]*entry{}}
}

func (f *Fake) Build(_ context.Context, r io.Reader, tag string, log func(string)) error {
	if _, err := io.Copy(io.Discard, r); err != nil {
		return err
	}
	if f.BuildHook != nil {
		f.BuildHook(tag)
	}
	for _, l := range f.BuildLines {
		log(l)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.BuildErr != nil {
		return f.BuildErr
	}
	f.images[tag] = true
	f.builds = append(f.builds, tag)
	return nil
}

func (f *Fake) ImageExists(_ context.Context, tag string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.images[tag], nil
}

func (f *Fake) Create(_ context.Context, spec runtime.ContainerSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.images[spec.Image] {
		return fmt.Errorf("image %s: %w", spec.Image, runtime.ErrNotFound)
	}
	if _, ok := f.containers[spec.Name]; ok {
		return fmt.Errorf("container %s already exists", spec.Name)
	}
	f.containers[spec.Name] = &entry{c: Container{Spec: spec}}
	return nil
}

func (f *Fake) Start(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.containers[name]
	if !ok {
		return fmt.Errorf("container %s: %w", name, runtime.ErrNotFound)
	}
	if e.c.Running {
		return nil
	}
	var b Behavior
	if f.Behave != nil {
		b = f.Behave(e.c.Spec)
	}
	if b.Crash {
		e.c.ExitCode = 1
		return nil
	}
	status := b.Status
	if status == 0 {
		status = http.StatusOK
	}
	body := e.c.Spec.Image
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	u, _ := url.Parse(e.srv.URL)
	e.c.HostPort, _ = strconv.Atoi(u.Port())
	e.c.Running = true
	e.c.ExitCode = 0
	return nil
}

func (f *Fake) Stop(_ context.Context, name string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.containers[name]
	if !ok {
		return fmt.Errorf("container %s: %w", name, runtime.ErrNotFound)
	}
	e.stop()
	return nil
}

func (f *Fake) Remove(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.containers[name]
	if !ok {
		return fmt.Errorf("container %s: %w", name, runtime.ErrNotFound)
	}
	e.stop()
	delete(f.containers, name)
	return nil
}

func (f *Fake) Inspect(_ context.Context, name string) (runtime.ContainerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.containers[name]
	if !ok {
		return runtime.ContainerInfo{}, fmt.Errorf("container %s: %w", name, runtime.ErrNotFound)
	}
	return info(name, e), nil
}

func info(name string, e *entry) runtime.ContainerInfo {
	return runtime.ContainerInfo{Name: name, Running: e.c.Running, ExitCode: e.c.ExitCode,
		HostPort: e.c.HostPort, Labels: e.c.Spec.Labels}
}

func (f *Fake) Logs(_ context.Context, name string, _ int, _ bool, w io.Writer) error {
	f.mu.Lock()
	_, ok := f.containers[name]
	f.mu.Unlock()
	if !ok {
		return fmt.Errorf("container %s: %w", name, runtime.ErrNotFound)
	}
	_, err := fmt.Fprintf(w, "log from %s\n", name)
	return err
}

func (f *Fake) List(context.Context) ([]runtime.ContainerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []runtime.ContainerInfo
	for name, e := range f.containers {
		if e.c.Spec.Labels[runtime.LabelManaged] == "true" {
			out = append(out, info(name, e))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *Fake) RemoveImage(_ context.Context, tag string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.images[tag] {
		return fmt.Errorf("image %s: %w", tag, runtime.ErrNotFound)
	}
	delete(f.images, tag)
	f.removedImages = append(f.removedImages, tag)
	return nil
}

func (f *Fake) RemoveVolume(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removedVolumes = append(f.removedVolumes, name)
	return nil
}

func (f *Fake) Container(name string) (Container, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.containers[name]
	if !ok {
		return Container{}, false
	}
	return e.c, true
}

func (f *Fake) Names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for n := range f.containers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (f *Fake) Builds() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.builds...)
}

func (f *Fake) RemovedImages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.removedImages...)
}

func (f *Fake) RemovedVolumes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.removedVolumes...)
}

// Close stops every fake container's HTTP server.
func (f *Fake) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.containers {
		e.stop()
	}
}
```

- [ ] **Step 5: Rodar e ver passar**

Run: `cd agent && go test -race ./internal/runtime/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add agent/internal/runtime
git commit -m "feat(agent): define the container runtime interface and a test fake"
```

---

### Task 9: Cliente REST do Podman

**Files:**
- Create: `agent/internal/podman/podman.go`
- Test: `agent/internal/podman/podman_test.go`
- Test: `agent/internal/podman/podman_integration_test.go` (build tag `integration`)

**Interfaces:**
- Consumes: `runtime.Runtime`, `runtime.ContainerSpec`, `runtime.ContainerInfo`, `runtime.ErrNotFound`, `runtime.LabelManaged` (Task 8).
- Produces: `func New(socket string) *Client` (socket Unix); `func NewWithBaseURL(base string, hc *http.Client) *Client` (testes com `httptest`). `*Client` implementa `runtime.Runtime`.

**Endpoints usados** (todos com prefixo `/v4.0.0/libpod`):

| Método | Endpoint | Sucesso |
| --- | --- | --- |
| Build | `POST /build?t=<tag>&rm=true`, corpo tar, `Content-Type: application/x-tar` | 200; corpo = objetos JSON `{"stream":...}` ou `{"error":...}` em sequência |
| ImageExists | `GET /images/<tag>/exists` | 204 = existe, 404 = não |
| Create | `POST /containers/create` (JSON) | 201 |
| Start | `POST /containers/<name>/start` | 204 ou 304 |
| Stop | `POST /containers/<name>/stop?timeout=<s>` | 204 ou 304 |
| Remove | `DELETE /containers/<name>?force=true&v=true` | 200 ou 204 |
| Inspect | `GET /containers/<name>/json` | 200 |
| Logs | `GET /containers/<name>/logs?stdout=true&stderr=true&tail=<n>&follow=<bool>` | 200; stream multiplexado (cabeçalho de 8 bytes por frame) |
| List | `GET /containers/json?all=true&filters={"label":["sagan.managed=true"]}` | 200 |
| RemoveImage | `DELETE /images/<tag>?force=true` | 200 |
| RemoveVolume | `DELETE /volumes/<name>?force=true` | 204 |

Os nomes de imagem contêm `/` (`localhost/...`). O roteador do Podman aceita `{name:.*}` nesses endpoints, então o nome vai **sem** escapar a barra.

- [ ] **Step 1: Escrever o teste que falha**

`agent/internal/podman/podman_test.go`:

```go
package podman

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/borgim/sagansync/agent/internal/runtime"
)

func newTest(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewWithBaseURL(srv.URL, srv.Client())
}

func TestBuildStreamsLogLines(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v4.0.0/libpod/build" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("t") != "localhost/sagan_app_production:r1" {
			t.Errorf("tag = %q", r.URL.Query().Get("t"))
		}
		if r.Header.Get("Content-Type") != "application/x-tar" {
			t.Errorf("content-type = %q", r.Header.Get("Content-Type"))
		}
		if b, _ := io.ReadAll(r.Body); string(b) != "TAR" {
			t.Errorf("body = %q", b)
		}
		io.WriteString(w, `{"stream":"STEP 1/2: FROM x\n"}{"stream":"STEP 2/2\nCOMMIT\n"}`)
	})
	var lines []string
	err := c.Build(context.Background(), strings.NewReader("TAR"), "localhost/sagan_app_production:r1", func(l string) { lines = append(lines, l) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(lines, "|") != "STEP 1/2: FROM x|STEP 2/2|COMMIT" {
		t.Fatalf("lines = %q", lines)
	}
}

func TestBuildReportsStreamError(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"stream":"STEP 1\n"}{"error":"npm ERR! missing script"}`)
	})
	err := c.Build(context.Background(), strings.NewReader(""), "t:1", func(string) {})
	if err == nil || !strings.Contains(err.Error(), "npm ERR! missing script") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateSendsSpec(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4.0.0/libpod/containers/create" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		pm := body["portmappings"].([]any)[0].(map[string]any)
		checks := map[string]bool{
			"name":              body["name"] == "sagan_app_production_r1",
			"image":             body["image"] == "localhost/sagan_app_production:r1",
			"no_new_privileges": body["no_new_privileges"] == true,
			"host_ip":           pm["host_ip"] == "127.0.0.1",
			"container_port":    pm["container_port"] == float64(3000),
			"random host port":  pm["host_port"] == nil,
			"env":               body["env"].(map[string]any)["A"] == "1",
			"label":             body["labels"].(map[string]any)[runtime.LabelManaged] == "true",
			"command":           body["command"].([]any)[0] == "npm",
			"bind mount":        body["mounts"].([]any)[0].(map[string]any)["destination"] == "/app",
			"named volume":      body["volumes"].([]any)[0].(map[string]any)["Dest"] == "/app/node_modules",
		}
		for name, ok := range checks {
			if !ok {
				t.Errorf("create body: %s is wrong: %v", name, body)
			}
		}
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"Id":"abc"}`)
	})
	err := c.Create(context.Background(), runtime.ContainerSpec{
		Name: "sagan_app_production_r1", Image: "localhost/sagan_app_production:r1",
		Command: []string{"npm", "run", "dev"}, Env: map[string]string{"A": "1"}, InternalPort: 3000,
		Labels:  map[string]string{runtime.LabelManaged: "true"},
		Binds:   []runtime.Bind{{Source: "/srv/sagan/app/x/dev", Dest: "/app"}},
		Volumes: []runtime.Volume{{Name: "sagan_app_x_node_modules", Dest: "/app/node_modules"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestInspectParsesStateAndPort(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4.0.0/libpod/containers/c1/json" {
			t.Errorf("path = %s", r.URL.Path)
		}
		io.WriteString(w, `{"Name":"c1","State":{"Running":true,"ExitCode":0},
			"Config":{"Labels":{"sagan.project":"app"}},
			"NetworkSettings":{"Ports":{"3000/tcp":[{"HostIp":"127.0.0.1","HostPort":"41873"}]}}}`)
	})
	info, err := c.Inspect(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if !info.Running || info.HostPort != 41873 || info.Labels["sagan.project"] != "app" || info.Name != "c1" {
		t.Fatalf("info = %+v", info)
	}
}

func TestNotFoundMapsToErrNotFound(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"cause":"no such container","message":"no container with name c1","response":404}`)
	})
	ctx := context.Background()
	if _, err := c.Inspect(ctx, "c1"); !errors.Is(err, runtime.ErrNotFound) {
		t.Errorf("Inspect: %v", err)
	}
	if err := c.Remove(ctx, "c1"); !errors.Is(err, runtime.ErrNotFound) {
		t.Errorf("Remove: %v", err)
	}
	if ok, err := c.ImageExists(ctx, "x:1"); ok || err != nil {
		t.Errorf("ImageExists = %v, %v", ok, err)
	}
}

func TestStartStopAcceptNotModified(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4.0.0/libpod/containers/c1/start":
			w.WriteHeader(http.StatusNotModified)
		case "/v4.0.0/libpod/containers/c1/stop":
			if r.URL.Query().Get("timeout") != "10" {
				t.Errorf("timeout = %q", r.URL.Query().Get("timeout"))
			}
			w.WriteHeader(http.StatusNoContent)
		}
	})
	if err := c.Start(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Stop(context.Background(), "c1", 10e9); err != nil {
		t.Fatal(err)
	}
}

func frame(stream byte, s string) []byte {
	h := make([]byte, 8)
	h[0] = stream
	binary.BigEndian.PutUint32(h[4:], uint32(len(s)))
	return append(h, s...)
}

func TestLogsDemultiplexes(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("tail") != "50" || q.Get("stdout") != "true" || q.Get("stderr") != "true" {
			t.Errorf("query = %v", q)
		}
		w.Write(append(frame(1, "hello\n"), frame(2, "oops\n")...))
	})
	var out bytes.Buffer
	if err := c.Logs(context.Background(), "c1", 50, false, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "hello\noops\n" {
		t.Fatalf("logs = %q", out.String())
	}
}

func TestLogsPassesRawStreamThrough(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "plain tty output\n") })
	var out bytes.Buffer
	if err := c.Logs(context.Background(), "c1", 10, false, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "plain tty output\n" {
		t.Fatalf("logs = %q", out.String())
	}
}

func TestListFiltersManagedContainers(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		var f map[string][]string
		if err := json.Unmarshal([]byte(r.URL.Query().Get("filters")), &f); err != nil || f["label"][0] != "sagan.managed=true" {
			t.Errorf("filters = %q", r.URL.Query().Get("filters"))
		}
		if r.URL.Query().Get("all") != "true" {
			t.Error("all=true missing")
		}
		io.WriteString(w, `[{"Names":["c1"],"State":"running","ExitCode":0,"Labels":{"sagan.managed":"true"},"Ports":[{"host_port":41000}]},
			{"Names":["c2"],"State":"exited","ExitCode":1,"Labels":{"sagan.managed":"true"},"Ports":null}]`)
	})
	list, err := c.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || !list[0].Running || list[0].HostPort != 41000 || list[1].Running || list[1].ExitCode != 1 {
		t.Fatalf("list = %+v", list)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd agent && go test ./internal/podman/`
Expected: FAIL com `undefined: NewWithBaseURL`

- [ ] **Step 3: Implementar**

`agent/internal/podman/podman.go`:

```go
// Package podman is a small client for the parts of the Podman REST API that
// sagand needs. It deliberately avoids the official bindings and their large
// dependency tree.
package podman

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/borgim/sagansync/agent/internal/runtime"
)

const apiPrefix = "/v4.0.0/libpod"

type Client struct {
	hc   *http.Client
	base string
}

var _ runtime.Runtime = (*Client)(nil)

// New talks to the Podman API socket, e.g. /run/user/1001/podman/podman.sock.
func New(socket string) *Client {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}}
	return &Client{hc: &http.Client{Transport: tr}, base: "http://podman" + apiPrefix}
}

// NewWithBaseURL is used by tests to point the client at an httptest server.
func NewWithBaseURL(base string, hc *http.Client) *Client {
	return &Client{hc: hc, base: strings.TrimSuffix(base, "/") + apiPrefix}
}

// call performs a request and returns the response only when its status is one
// of ok. Otherwise it closes the body and returns an error; 404 wraps
// runtime.ErrNotFound.
func (c *Client) call(ctx context.Context, method, path string, q url.Values, body io.Reader, ctype string, ok ...int) (*http.Response, error) {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("podman %s %s: %w", method, path, err)
	}
	for _, code := range ok {
		if resp.StatusCode == code {
			return resp, nil
		}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var e struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(b, &e)
	msg := e.Message
	if msg == "" {
		msg = strings.TrimSpace(string(b))
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("podman: %s: %w", msg, runtime.ErrNotFound)
	}
	return nil, fmt.Errorf("podman %s %s: %s (HTTP %d)", method, path, msg, resp.StatusCode)
}

func (c *Client) Build(ctx context.Context, contextTar io.Reader, tag string, log func(string)) error {
	q := url.Values{"t": {tag}, "rm": {"true"}}
	resp, err := c.call(ctx, http.MethodPost, "/build", q, contextTar, "application/x-tar", http.StatusOK)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	for {
		var m struct {
			Stream string `json:"stream"`
			Error  string `json:"error"`
		}
		err := dec.Decode(&m)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("podman build: reading output: %w", err)
		}
		if m.Error != "" {
			return fmt.Errorf("podman build: %s", strings.TrimSpace(m.Error))
		}
		for _, line := range strings.Split(strings.TrimRight(m.Stream, "\n"), "\n") {
			if line != "" {
				log(line)
			}
		}
	}
}

func (c *Client) ImageExists(ctx context.Context, tag string) (bool, error) {
	resp, err := c.call(ctx, http.MethodGet, "/images/"+tag+"/exists", nil, nil, "", http.StatusNoContent)
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	resp.Body.Close()
	return true, nil
}

type portMapping struct {
	HostIP        string `json:"host_ip"`
	ContainerPort uint16 `json:"container_port"`
	HostPort      uint16 `json:"host_port,omitempty"` // omitted: Podman picks a free port
	Protocol      string `json:"protocol"`
}

type mount struct {
	Destination string   `json:"destination"`
	Source      string   `json:"source"`
	Type        string   `json:"type"`
	Options     []string `json:"options,omitempty"`
}

type namedVolume struct {
	Name string `json:"Name"`
	Dest string `json:"Dest"`
}

type specgen struct {
	Name            string            `json:"name"`
	Image           string            `json:"image"`
	Command         []string          `json:"command,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	PortMappings    []portMapping     `json:"portmappings,omitempty"`
	Mounts          []mount           `json:"mounts,omitempty"`
	Volumes         []namedVolume     `json:"volumes,omitempty"`
	NoNewPrivileges bool              `json:"no_new_privileges"`
}

func (c *Client) Create(ctx context.Context, spec runtime.ContainerSpec) error {
	body := specgen{
		Name: spec.Name, Image: spec.Image, Command: spec.Command, Env: spec.Env, Labels: spec.Labels,
		NoNewPrivileges: true,
		PortMappings:    []portMapping{{HostIP: "127.0.0.1", ContainerPort: uint16(spec.InternalPort), Protocol: "tcp"}},
	}
	for _, b := range spec.Binds {
		body.Mounts = append(body.Mounts, mount{Destination: b.Dest, Source: b.Source, Type: "bind", Options: []string{"rbind", "rw"}})
	}
	for _, v := range spec.Volumes {
		body.Volumes = append(body.Volumes, namedVolume{Name: v.Name, Dest: v.Dest})
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := c.call(ctx, http.MethodPost, "/containers/create", nil, strings.NewReader(string(b)), "application/json", http.StatusCreated)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *Client) Start(ctx context.Context, name string) error {
	return c.simple(ctx, http.MethodPost, "/containers/"+name+"/start", nil, http.StatusNoContent, http.StatusNotModified)
}

func (c *Client) Stop(ctx context.Context, name string, timeout time.Duration) error {
	q := url.Values{"timeout": {strconv.Itoa(int(timeout.Seconds()))}}
	return c.simple(ctx, http.MethodPost, "/containers/"+name+"/stop", q, http.StatusNoContent, http.StatusNotModified)
}

func (c *Client) Remove(ctx context.Context, name string) error {
	q := url.Values{"force": {"true"}, "v": {"true"}}
	return c.simple(ctx, http.MethodDelete, "/containers/"+name, q, http.StatusOK, http.StatusNoContent)
}

func (c *Client) RemoveImage(ctx context.Context, tag string) error {
	return c.simple(ctx, http.MethodDelete, "/images/"+tag, url.Values{"force": {"true"}}, http.StatusOK)
}

func (c *Client) RemoveVolume(ctx context.Context, name string) error {
	return c.simple(ctx, http.MethodDelete, "/volumes/"+name, url.Values{"force": {"true"}}, http.StatusNoContent)
}

func (c *Client) simple(ctx context.Context, method, path string, q url.Values, ok ...int) error {
	resp, err := c.call(ctx, method, path, q, nil, "", ok...)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return nil
}

func (c *Client) Inspect(ctx context.Context, name string) (runtime.ContainerInfo, error) {
	resp, err := c.call(ctx, http.MethodGet, "/containers/"+name+"/json", nil, nil, "", http.StatusOK)
	if err != nil {
		return runtime.ContainerInfo{}, err
	}
	defer resp.Body.Close()
	var r struct {
		Name  string
		State struct {
			Running  bool
			ExitCode int
		}
		Config struct {
			Labels map[string]string
		}
		NetworkSettings struct {
			Ports map[string][]struct {
				HostIP   string `json:"HostIp"`
				HostPort string `json:"HostPort"`
			}
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return runtime.ContainerInfo{}, fmt.Errorf("podman inspect %s: %w", name, err)
	}
	info := runtime.ContainerInfo{Name: strings.TrimPrefix(r.Name, "/"), Running: r.State.Running,
		ExitCode: r.State.ExitCode, Labels: r.Config.Labels}
	for key, bindings := range r.NetworkSettings.Ports {
		if strings.HasSuffix(key, "/tcp") && len(bindings) > 0 {
			info.HostPort, _ = strconv.Atoi(bindings[0].HostPort)
			break
		}
	}
	return info, nil
}

func (c *Client) Logs(ctx context.Context, name string, tail int, follow bool, w io.Writer) error {
	q := url.Values{"stdout": {"true"}, "stderr": {"true"}, "tail": {strconv.Itoa(tail)}, "follow": {strconv.FormatBool(follow)}}
	resp, err := c.call(ctx, http.MethodGet, "/containers/"+name+"/logs", q, nil, "", http.StatusOK)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return demux(resp.Body, w)
}

// demux strips Docker-style stream frames: an 8-byte header ([stream, 0, 0, 0,
// size uint32 big endian]) before each chunk. Containers with a TTY send a
// raw stream instead, which is copied as is.
func demux(r io.Reader, w io.Writer) error {
	br := bufio.NewReader(r)
	hdr, err := br.Peek(8)
	if err != nil || hdr[0] > 2 || hdr[1] != 0 || hdr[2] != 0 || hdr[3] != 0 {
		_, err = io.Copy(w, br)
		return err
	}
	var h [8]byte
	for {
		if _, err := io.ReadFull(br, h[:]); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if _, err := io.CopyN(w, br, int64(binary.BigEndian.Uint32(h[4:]))); err != nil {
			return err
		}
	}
}

func (c *Client) List(ctx context.Context) ([]runtime.ContainerInfo, error) {
	filters, _ := json.Marshal(map[string][]string{"label": {runtime.LabelManaged + "=true"}})
	q := url.Values{"all": {"true"}, "filters": {string(filters)}}
	resp, err := c.call(ctx, http.MethodGet, "/containers/json", q, nil, "", http.StatusOK)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var raw []struct {
		Names    []string
		State    string
		ExitCode int
		Labels   map[string]string
		Ports    []struct {
			HostPort int `json:"host_port"`
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("podman list: %w", err)
	}
	out := make([]runtime.ContainerInfo, 0, len(raw))
	for _, r := range raw {
		info := runtime.ContainerInfo{Running: r.State == "running", ExitCode: r.ExitCode, Labels: r.Labels}
		if len(r.Names) > 0 {
			info.Name = r.Names[0]
		}
		if len(r.Ports) > 0 {
			info.HostPort = r.Ports[0].HostPort
		}
		out = append(out, info)
	}
	return out, nil
}

func isNotFound(err error) bool { return errors.Is(err, runtime.ErrNotFound) }
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd agent && go vet ./internal/podman/ && go test -race ./internal/podman/`
Expected: PASS

- [ ] **Step 5: Escrever o teste de integração (roda só na VM, no plano 3)**

`agent/internal/podman/podman_integration_test.go`:

```go
//go:build integration

package podman

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/borgim/sagansync/agent/internal/runtime"
)

func plainTar(t *testing.T, files map[string]string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	return &buf
}

// Run inside the VM: SAGAN_PODMAN_SOCKET=/run/user/$(id -u)/podman/podman.sock go test -tags integration ./internal/podman/
func TestAgainstRealPodman(t *testing.T) {
	sock := os.Getenv("SAGAN_PODMAN_SOCKET")
	if sock == "" {
		t.Skip("SAGAN_PODMAN_SOCKET not set")
	}
	c := New(sock)
	ctx := context.Background()
	tag, name := "localhost/sagan_itest_ws:1", "sagan_itest_ws_1"
	t.Cleanup(func() { _ = c.Remove(ctx, name); _ = c.RemoveImage(ctx, tag) })

	dockerfile := "FROM docker.io/library/busybox:1.36\n" +
		"CMD [\"sh\", \"-c\", \"echo started; mkdir -p /www && echo ok > /www/index.html && httpd -f -p 3000 -h /www\"]\n"
	var lines []string
	if err := c.Build(ctx, plainTar(t, map[string]string{"Dockerfile": dockerfile}), tag, func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatalf("build: %v\n%s", err, strings.Join(lines, "\n"))
	}
	if ok, err := c.ImageExists(ctx, tag); err != nil || !ok {
		t.Fatalf("ImageExists = %v, %v", ok, err)
	}
	spec := runtime.ContainerSpec{Name: name, Image: tag, InternalPort: 3000, Env: map[string]string{"A": "1"},
		Labels: map[string]string{runtime.LabelManaged: "true", runtime.LabelProject: "itest", runtime.LabelWorkspace: "ws"}}
	if err := c.Create(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := c.Start(ctx, name); err != nil {
		t.Fatal(err)
	}
	info, err := c.Inspect(ctx, name)
	if err != nil || !info.Running || info.HostPort == 0 {
		t.Fatalf("Inspect = %+v, %v", info, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", info.HostPort))
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if strings.TrimSpace(string(body)) == "ok" {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("container never served HTTP: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	var logs bytes.Buffer
	if err := c.Logs(ctx, name, 50, false, &logs); err != nil || !strings.Contains(logs.String(), "started") {
		t.Fatalf("Logs = %q, %v", logs.String(), err)
	}
	list, err := c.List(ctx)
	found := false
	for _, ci := range list {
		found = found || ci.Name == name
	}
	if err != nil || !found {
		t.Fatalf("List = %+v, %v", list, err)
	}
	if err := c.Stop(ctx, name, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if info, _ := c.Inspect(ctx, name); info.Running {
		t.Fatal("still running after Stop")
	}
	if err := c.Remove(ctx, name); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Inspect(ctx, name); !errors.Is(err, runtime.ErrNotFound) {
		t.Fatalf("Inspect after Remove = %v", err)
	}
}
```

Run (local, só para garantir que compila): `cd agent && go vet -tags integration ./internal/podman/`
Expected: sem erros.

- [ ] **Step 6: Commit**

```bash
git add agent/internal/podman
git commit -m "feat(agent): add a minimal Podman REST client"
```

---

### Task 10: Proxy — tabela de rotas e redirecionamento

**Files:**
- Create: `agent/internal/proxy/router.go`
- Test: `agent/internal/proxy/router_test.go`

**Interfaces:**
- Produces:
  - `func NewRouter() *Router`; `(*Router).Set(host, upstream string)`, `Delete(host string)`, `Lookup(host string) (string, bool)`; `*Router` implementa `http.Handler`.
  - O host é normalizado (minúsculas, sem porta, sem ponto final). Host desconhecido → 404. Upstream fora do ar → 502.
  - O header `Host` original é preservado para o app; `X-Forwarded-For/Proto/Host` são definidos.
  - `func RedirectHandler() http.Handler` — 308 para `https://<host><uri>`.
  - `*Router` satisfaz a interface `deploy.Routes` (Task 12): `Set(host, upstream string)` e `Delete(host string)`.

- [ ] **Step 1: Escrever o teste que falha**

`agent/internal/proxy/router_test.go`:

```go
package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func backend(t *testing.T, name string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen-Host", r.Host)
		w.Header().Set("X-Seen-Forwarded-Host", r.Header.Get("X-Forwarded-Host"))
		w.Header().Set("X-Seen-Forwarded-Proto", r.Header.Get("X-Forwarded-Proto"))
		io.WriteString(w, name)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func upstreamOf(srv *httptest.Server) string { return strings.TrimPrefix(srv.URL, "http://") }

func get(t *testing.T, front *httptest.Server, host string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, front.URL+"/path?q=1", nil)
	req.Host = host
	resp, err := front.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestRouterProxiesKnownHost(t *testing.T) {
	r := NewRouter()
	r.Set("app.test", upstreamOf(backend(t, "v1")))
	front := httptest.NewServer(r)
	defer front.Close()
	resp, body := get(t, front, "app.test")
	if resp.StatusCode != 200 || body != "v1" {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
	if resp.Header.Get("X-Seen-Host") != "app.test" {
		t.Errorf("app saw Host %q, want app.test", resp.Header.Get("X-Seen-Host"))
	}
	if resp.Header.Get("X-Seen-Forwarded-Host") != "app.test" || resp.Header.Get("X-Seen-Forwarded-Proto") != "http" {
		t.Errorf("forwarded headers = %q %q", resp.Header.Get("X-Seen-Forwarded-Host"), resp.Header.Get("X-Seen-Forwarded-Proto"))
	}
}

func TestRouterUnknownHostIs404(t *testing.T) {
	front := httptest.NewServer(NewRouter())
	defer front.Close()
	if resp, _ := get(t, front, "nope.test"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestRouterDeadUpstreamIs502(t *testing.T) {
	dead := backend(t, "x")
	addr := upstreamOf(dead)
	dead.Close()
	r := NewRouter()
	r.Set("app.test", addr)
	front := httptest.NewServer(r)
	defer front.Close()
	if resp, _ := get(t, front, "app.test"); resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestRouterNormalizesHost(t *testing.T) {
	r := NewRouter()
	r.Set("app.test", upstreamOf(backend(t, "v1")))
	front := httptest.NewServer(r)
	defer front.Close()
	for _, h := range []string{"APP.test", "app.test:443", "app.test.", "App.Test.:8443"} {
		if resp, body := get(t, front, h); resp.StatusCode != 200 || body != "v1" {
			t.Errorf("Host %q: got %d %q", h, resp.StatusCode, body)
		}
	}
}

func TestRouterSwapUnderLoad(t *testing.T) {
	r := NewRouter()
	v1, v2 := backend(t, "v1"), backend(t, "v2")
	r.Set("app.test", upstreamOf(v1))
	front := httptest.NewServer(r)
	defer front.Close()

	var failures atomic.Int32
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				req, _ := http.NewRequest(http.MethodGet, front.URL, nil)
				req.Host = "app.test"
				resp, err := front.Client().Do(req)
				if err != nil {
					failures.Add(1)
					continue
				}
				b, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode != 200 || (string(b) != "v1" && string(b) != "v2") {
					failures.Add(1)
				}
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	r.Set("app.test", upstreamOf(v2))
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
	if n := failures.Load(); n != 0 {
		t.Fatalf("%d requests failed during the swap", n)
	}
	if _, body := get(t, front, "app.test"); body != "v2" {
		t.Fatalf("after swap got %q", body)
	}
}

func TestDeleteRemovesRoute(t *testing.T) {
	r := NewRouter()
	r.Set("app.test", "127.0.0.1:1")
	r.Delete("app.test")
	if _, ok := r.Lookup("app.test"); ok {
		t.Fatal("route still present")
	}
}

func TestRedirectHandler(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://App.test:80/a/b?x=1", nil)
	RedirectHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != "https://app.test/a/b?x=1" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd agent && go test ./internal/proxy/`
Expected: FAIL com `undefined: NewRouter`

- [ ] **Step 3: Implementar**

`agent/internal/proxy/router.go`:

```go
// Package proxy routes public HTTP(S) traffic to workspace containers.
package proxy

import (
	"context"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
)

type upstreamKey struct{}

// Router maps hostnames to "127.0.0.1:<port>" upstreams. Swapping a route is
// atomic: new requests go to the new upstream while in-flight requests finish
// on the old one.
type Router struct {
	mu     sync.RWMutex
	routes map[string]string
	rp     *httputil.ReverseProxy
}

func NewRouter() *Router {
	r := &Router{routes: map[string]string{}}
	r.rp = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			up := pr.In.Context().Value(upstreamKey{}).(string)
			pr.SetURL(&url.URL{Scheme: "http", Host: up})
			pr.Out.Host = pr.In.Host // SetURL rewrites Host; the app needs the public one
			pr.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "502 bad gateway: the app is not responding", http.StatusBadGateway)
		},
	}
	return r
}

func normalize(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

func (r *Router) Set(host, upstream string) {
	r.mu.Lock()
	r.routes[normalize(host)] = upstream
	r.mu.Unlock()
}

func (r *Router) Delete(host string) {
	r.mu.Lock()
	delete(r.routes, normalize(host))
	r.mu.Unlock()
}

func (r *Router) Lookup(host string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	up, ok := r.routes[normalize(host)]
	return up, ok
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	up, ok := r.Lookup(req.Host)
	if !ok {
		http.Error(w, "404 page not found", http.StatusNotFound)
		return
	}
	r.rp.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), upstreamKey{}, up)))
}

// RedirectHandler sends plain HTTP requests to HTTPS.
func RedirectHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://"+normalize(r.Host)+r.URL.RequestURI(), http.StatusPermanentRedirect)
	})
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd agent && go test -race ./internal/proxy/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add agent/internal/proxy
git commit -m "feat(agent): add host-based reverse proxy with atomic route swaps"
```

---

### Task 11: Proxy — TLS automático com certmagic

**Files:**
- Modify: `agent/go.mod`, `agent/go.sum`
- Create: `agent/internal/proxy/tls.go`
- Test: `agent/internal/proxy/tls_test.go`

**Interfaces:**
- Produces:
  - `type TLSOptions struct { StorageDir, Email, CA, RootCAPath string }` — `CA` vazio = Let's Encrypt produção; `RootCAPath` = CA extra para falar com o servidor ACME (Pebble nos testes).
  - `func NewTLS(opts TLSOptions, allowed func(host string) bool) (*TLS, error)`
  - `(*TLS).TLSConfig() *tls.Config` (para o servidor :443), `(*TLS).HTTPHandler(next http.Handler) http.Handler` (desafio HTTP-01 no :80), `(*TLS).Ensure(ctx, host string) error` (emite/renova agora), `(*TLS).Close()`.
  - `*TLS` satisfaz a interface `deploy.Certs` (Task 12): `Ensure(ctx context.Context, host string) error`.
- A emissão real só é testada no plano 3 (Pebble). Aqui os testes cobrem configuração, a regra de quais hosts podem ter certificado e o repasse de requisições comuns.

- [ ] **Step 1: Adicionar a dependência e conferir a API**

```bash
cd agent && go get github.com/caddyserver/certmagic@latest
go doc github.com/caddyserver/certmagic OnDemandConfig
go doc github.com/caddyserver/certmagic ACMEIssuer.HTTPChallengeHandler
```

Expected: `OnDemandConfig` tem o campo `DecisionFunc func(ctx context.Context, name string) error` e `HTTPChallengeHandler(h http.Handler) http.Handler` existe. Se a assinatura de `DecisionFunc` for diferente na versão instalada, ajuste o código do Step 3 e o teste do Step 2 para ela.

- [ ] **Step 2: Escrever o teste que falha**

`agent/internal/proxy/tls_test.go`:

```go
package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func newTestTLS(t *testing.T, allowed func(string) bool) *TLS {
	t.Helper()
	tl, err := NewTLS(TLSOptions{StorageDir: t.TempDir(), Email: "ops@example.com", CA: "https://127.0.0.1:14000/dir"}, allowed)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tl.Close)
	return tl
}

func TestOnlyKnownHostsGetCertificates(t *testing.T) {
	tl := newTestTLS(t, func(h string) bool { return h == "app.test" })
	decide := tl.magic.OnDemand.DecisionFunc
	if err := decide(context.Background(), "app.test"); err != nil {
		t.Errorf("known host refused: %v", err)
	}
	if err := decide(context.Background(), "evil.test"); err == nil {
		t.Error("unknown host allowed")
	}
}

func TestHTTPHandlerPassesOrdinaryRequests(t *testing.T) {
	tl := newTestTLS(t, func(string) bool { return true })
	called := false
	h := tl.HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://app.test/x", nil))
	if !called {
		t.Fatal("ordinary request did not reach the next handler")
	}
}

func TestTLSConfigAdvertisesHTTP2(t *testing.T) {
	cfg := newTestTLS(t, func(string) bool { return true }).TLSConfig()
	if len(cfg.NextProtos) < 2 || cfg.NextProtos[0] != "h2" || cfg.NextProtos[1] != "http/1.1" {
		t.Fatalf("NextProtos = %v", cfg.NextProtos)
	}
	if cfg.GetCertificate == nil {
		t.Fatal("GetCertificate not set")
	}
}

func TestBadRootCAIsAnError(t *testing.T) {
	if _, err := NewTLS(TLSOptions{StorageDir: t.TempDir(), RootCAPath: "/does/not/exist.pem"}, func(string) bool { return true }); err == nil {
		t.Error("missing root CA file accepted")
	}
	junk := filepath.Join(t.TempDir(), "junk.pem")
	os.WriteFile(junk, []byte("not a certificate"), 0o600)
	if _, err := NewTLS(TLSOptions{StorageDir: t.TempDir(), RootCAPath: junk}, func(string) bool { return true }); err == nil {
		t.Error("root CA without certificates accepted")
	}
}
```

- [ ] **Step 3: Rodar e ver falhar**

Run: `cd agent && go test ./internal/proxy/`
Expected: FAIL com `undefined: NewTLS`

- [ ] **Step 4: Implementar**

`agent/internal/proxy/tls.go`:

```go
package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"

	"github.com/caddyserver/certmagic"
)

type TLSOptions struct {
	StorageDir string // where certificates and ACME accounts are kept
	Email      string // optional ACME account email
	CA         string // ACME directory URL; Let's Encrypt production when empty
	RootCAPath string // extra CA to trust when talking to the ACME server (tests)
}

// TLS obtains and serves certificates for hosts that sagand routes, and only
// for those, so unknown hostnames cannot trigger issuance.
type TLS struct {
	cache *certmagic.Cache
	magic *certmagic.Config
	acme  *certmagic.ACMEIssuer
}

func NewTLS(opts TLSOptions, allowed func(host string) bool) (*TLS, error) {
	issuer := certmagic.ACMEIssuer{CA: opts.CA, Email: opts.Email, Agreed: true}
	if issuer.CA == "" {
		issuer.CA = certmagic.LetsEncryptProductionCA
	}
	if opts.RootCAPath != "" {
		pem, err := os.ReadFile(opts.RootCAPath)
		if err != nil {
			return nil, fmt.Errorf("read ACME root CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificates found in %s", opts.RootCAPath)
		}
		issuer.TrustedRoots = pool
	}
	t := &TLS{}
	t.cache = certmagic.NewCache(certmagic.CacheOptions{
		GetConfigForCert: func(certmagic.Certificate) (*certmagic.Config, error) { return t.magic, nil },
	})
	t.magic = certmagic.New(t.cache, certmagic.Config{
		Storage: &certmagic.FileStorage{Path: opts.StorageDir},
		OnDemand: &certmagic.OnDemandConfig{
			DecisionFunc: func(_ context.Context, name string) error {
				if allowed(name) {
					return nil
				}
				return fmt.Errorf("%s is not served by sagand", name)
			},
		},
	})
	t.acme = certmagic.NewACMEIssuer(t.magic, issuer)
	t.magic.Issuers = []certmagic.Issuer{t.acme}
	return t, nil
}

func (t *TLS) TLSConfig() *tls.Config {
	cfg := t.magic.TLSConfig()
	cfg.NextProtos = append([]string{"h2", "http/1.1"}, cfg.NextProtos...)
	return cfg
}

// HTTPHandler answers ACME HTTP-01 challenges and passes everything else on.
func (t *TLS) HTTPHandler(next http.Handler) http.Handler { return t.acme.HTTPChallengeHandler(next) }

// Ensure obtains (or renews) the certificate for host right away.
func (t *TLS) Ensure(ctx context.Context, host string) error {
	return t.magic.ManageSync(ctx, []string{host})
}

func (t *TLS) Close() { t.cache.Stop() }
```

- [ ] **Step 5: Rodar e ver passar**

Run: `cd agent && go mod tidy && go vet ./internal/proxy/ && go test -race ./internal/proxy/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add agent/go.mod agent/go.sum agent/internal/proxy
git commit -m "feat(agent): obtain TLS certificates with certmagic for routed hosts only"
```

---

### Task 12: `deploy` — deploy sem downtime

**Files:**
- Create: `agent/internal/deploy/deploy.go`
- Create: `agent/internal/deploy/health.go`
- Create: `agent/internal/testutil/testdeploy/testdeploy.go`
- Test: `agent/internal/deploy/deploy_test.go`

**Interfaces:**
- Consumes: `validate` (T1), `domains.Host` (T2), `events` (T3), `release.*` (T4–5), `state.*` (T6), `envstore.*` (T7), `runtime.*` e `fakert` (T8).
- Produces (pacote `deploy`):
  - Códigos: `CodeInvalid = "invalid"`, `CodeBusy = "busy"`, `CodeBuild = "build_failed"`, `CodeHealth = "health_failed"`, `CodeArchive = "invalid_archive"`, `CodeHostConflict = "host_conflict"`, `CodeNotFound = "not_found"`, `CodeProductionLocked = "production_locked"`, `CodeInternal = "internal"`.
  - `type OpError struct { Code, Msg string; Logs []string }` (implementa `error`).
  - `type Routes interface { Set(host, upstream string); Delete(host string) }`
  - `type Certs interface { Ensure(ctx context.Context, host string) error }`
  - `type Config struct { SrvDir string; Keep int; Drain, StopTimeout, HealthInterval, DevHealthTimeout time.Duration; LogTail int; Now func() time.Time }` e `func DefaultConfig(srvDir string) Config`.
  - `func New(rt runtime.Runtime, st *state.Store, env *envstore.Store, routes Routes, certs Certs, cfg Config) *Deployer`
  - `type Request struct { Project, Workspace, Domain, PreviewDomain string; InternalPort int; HealthPath string; HealthTimeout time.Duration; Sha string }` (`HealthTimeout` 0 = 60 s)
  - `func (d *Deployer) Deploy(ctx context.Context, req Request, archive io.Reader, em events.Emitter) error` — emite `step` (extract, build, start, health, tls quando há host, drain quando há anterior), `log`, `warn`, e por fim `done`. **Não** emite `error`: devolve `*OpError` e quem chama (API) emite.
  - `func ContainerName(p, w, id string) string`, `func ImageName(p, w, tag string) string`
- Produces (pacote `testdeploy`): `type Harness struct { D *deploy.Deployer; RT *fakert.Fake; State *state.Store; Routes *Routes; Certs *Certs; SrvDir string }`, `func New(t testing.TB) *Harness`, `func Request(workspace string) deploy.Request`, `(*Harness).Deploy(t testing.TB, req deploy.Request) (*events.Recorder, error)`, `type Routes` (com `Set`, `Delete`, `Get`, `Len`, `Reset`), `type Certs` (com campo `Err error` e `Hosts() []string`).

**Health check TCP (sem `healthPath`):** o Podman rootless aceita a conexão na porta publicada mesmo quando nada escuta dentro do container, e depois fecha. Por isso "conectou" não basta: o probe conecta e tenta ler 1 byte com prazo de 300 ms. Se o prazo estourar (a conexão continuou aberta) ou chegar dado, está saudável; EOF ou reset significa que não há nada escutando.

- [ ] **Step 1: Criar o harness de testes**

`agent/internal/testutil/testdeploy/testdeploy.go`:

```go
// Package testdeploy wires a Deployer to fakes for tests of deploy, api and cmd.
package testdeploy

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/borgim/sagansync/agent/internal/deploy"
	"github.com/borgim/sagansync/agent/internal/envstore"
	"github.com/borgim/sagansync/agent/internal/events"
	"github.com/borgim/sagansync/agent/internal/runtime/fakert"
	"github.com/borgim/sagansync/agent/internal/state"
	"github.com/borgim/sagansync/agent/internal/testutil"
)

type Routes struct {
	mu sync.Mutex
	m  map[string]string
}

func (r *Routes) Set(host, upstream string) { r.mu.Lock(); r.m[host] = upstream; r.mu.Unlock() }
func (r *Routes) Delete(host string)        { r.mu.Lock(); delete(r.m, host); r.mu.Unlock() }
func (r *Routes) Get(host string) string    { r.mu.Lock(); defer r.mu.Unlock(); return r.m[host] }
func (r *Routes) Len() int                  { r.mu.Lock(); defer r.mu.Unlock(); return len(r.m) }
func (r *Routes) Reset()                    { r.mu.Lock(); r.m = map[string]string{}; r.mu.Unlock() }

type Certs struct {
	mu    sync.Mutex
	Err   error
	hosts []string
}

func (c *Certs) Ensure(_ context.Context, host string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hosts = append(c.hosts, host)
	return c.Err
}

func (c *Certs) Hosts() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.hosts...)
}

type Harness struct {
	D      *deploy.Deployer
	RT     *fakert.Fake
	State  *state.Store
	Routes *Routes
	Certs  *Certs
	SrvDir string
}

// New builds a Deployer with short timings and a clock that advances one
// second per call, so release ids never collide.
func New(t testing.TB) *Harness {
	t.Helper()
	dir := t.TempDir()
	rt := fakert.New()
	t.Cleanup(rt.Close)
	st, err := state.Open(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	h := &Harness{RT: rt, State: st, Routes: &Routes{m: map[string]string{}}, Certs: &Certs{}, SrvDir: filepath.Join(dir, "srv")}
	cfg := deploy.DefaultConfig(h.SrvDir)
	cfg.Drain = 10 * time.Millisecond
	cfg.StopTimeout = 0
	cfg.HealthInterval = 10 * time.Millisecond
	cfg.DevHealthTimeout = 300 * time.Millisecond
	var tick atomic.Int64
	base := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	cfg.Now = func() time.Time { return base.Add(time.Duration(tick.Add(1)) * time.Second) }
	h.D = deploy.New(rt, st, envstore.New(filepath.Join(dir, "env")), h.Routes, h.Certs, cfg)
	return h
}

// Request is a valid deploy request for project "app" on domain "app.test".
func Request(workspace string) deploy.Request {
	return deploy.Request{Project: "app", Workspace: workspace, Domain: "app.test", InternalPort: 3000,
		HealthPath: "/health", HealthTimeout: 300 * time.Millisecond, Sha: "abc1234def"}
}

func (h *Harness) Deploy(t testing.TB, req deploy.Request) (*events.Recorder, error) {
	rec := &events.Recorder{}
	err := h.D.Deploy(context.Background(), req, testutil.App(t), rec)
	return rec, err
}
```

- [ ] **Step 2: Escrever o teste que falha**

`agent/internal/deploy/deploy_test.go`:

```go
package deploy_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/borgim/sagansync/agent/internal/deploy"
	"github.com/borgim/sagansync/agent/internal/events"
	"github.com/borgim/sagansync/agent/internal/runtime"
	"github.com/borgim/sagansync/agent/internal/runtime/fakert"
	"github.com/borgim/sagansync/agent/internal/state"
	"github.com/borgim/sagansync/agent/internal/testutil"
	"github.com/borgim/sagansync/agent/internal/testutil/testdeploy"
)

func errCode(err error) string {
	var oe *deploy.OpError
	if errors.As(err, &oe) {
		return oe.Code
	}
	return ""
}

func fetch(t *testing.T, upstream string) string {
	t.Helper()
	resp, err := http.Get("http://" + upstream + "/")
	if err != nil {
		t.Fatalf("GET %s: %v", upstream, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func releases(t *testing.T, h *testdeploy.Harness, ws string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(h.SrvDir, "app", ws, "releases"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range entries {
		ids = append(ids, e.Name())
	}
	return ids
}

func TestDeployFirstRelease(t *testing.T) {
	h := testdeploy.New(t)
	h.RT.BuildLines = []string{"STEP 1/2: FROM node"}
	rec, err := h.Deploy(t, testdeploy.Request("production"))
	if err != nil {
		t.Fatal(err)
	}
	ws, ok := h.State.Get("app", "production")
	if !ok || ws.Host != "app.test" || ws.Mode != state.ModeDeploy || ws.HostPort == 0 {
		t.Fatalf("state = %+v, %v", ws, ok)
	}
	if !strings.HasSuffix(ws.Release, "-abc1234") {
		t.Errorf("release id %q should end with the short sha", ws.Release)
	}
	if got, want := h.Routes.Get("app.test"), fmt.Sprintf("127.0.0.1:%d", ws.HostPort); got != want {
		t.Fatalf("route = %q, want %q", got, want)
	}
	if body := fetch(t, h.Routes.Get("app.test")); body != deploy.ImageName("app", "production", ws.Release) {
		t.Fatalf("app served %q", body)
	}
	if got := rec.Steps(); !slices.Equal(got, []string{"extract", "build", "start", "health", "tls"}) {
		t.Errorf("steps = %v", got)
	}
	if !rec.Has("log", "") {
		t.Error("build log lines were not streamed")
	}
	last := rec.Last()
	if last.Type != "done" || last.URL != "https://app.test" || last.Release != ws.Release || last.HostPort != ws.HostPort {
		t.Errorf("last event = %+v", last)
	}
	if _, err := os.Stat(filepath.Join(h.SrvDir, "app", "production", "releases", ws.Release, "Dockerfile")); err != nil {
		t.Errorf("release files missing: %v", err)
	}
	c, _ := h.RT.Container(ws.Container)
	if c.Spec.Labels[runtime.LabelProject] != "app" || c.Spec.Labels[runtime.LabelManaged] != "true" {
		t.Errorf("labels = %v", c.Spec.Labels)
	}
}

func TestRedeploySwapsAndDrainsOld(t *testing.T) {
	h := testdeploy.New(t)
	if _, err := h.Deploy(t, testdeploy.Request("production")); err != nil {
		t.Fatal(err)
	}
	first, _ := h.State.Get("app", "production")
	rec, err := h.Deploy(t, testdeploy.Request("production"))
	if err != nil {
		t.Fatal(err)
	}
	second, _ := h.State.Get("app", "production")
	if second.Release == first.Release || second.Container == first.Container {
		t.Fatalf("release did not change: %+v", second)
	}
	if body := fetch(t, h.Routes.Get("app.test")); body != deploy.ImageName("app", "production", second.Release) {
		t.Fatalf("route serves %q", body)
	}
	if _, ok := h.RT.Container(first.Container); ok {
		t.Fatal("old container was not removed")
	}
	if !slices.Contains(rec.Steps(), "drain") {
		t.Errorf("steps = %v, want a drain step", rec.Steps())
	}
}

func TestFailedBuildKeepsCurrentRelease(t *testing.T) {
	h := testdeploy.New(t)
	_, _ = h.Deploy(t, testdeploy.Request("production"))
	first, _ := h.State.Get("app", "production")
	h.RT.BuildErr = errors.New("npm ERR! missing script: build")
	_, err := h.Deploy(t, testdeploy.Request("production"))
	if errCode(err) != deploy.CodeBuild || !strings.Contains(err.Error(), "missing script") {
		t.Fatalf("err = %v", err)
	}
	if now, _ := h.State.Get("app", "production"); now.Release != first.Release {
		t.Fatal("state changed after a failed build")
	}
	if c, ok := h.RT.Container(first.Container); !ok || !c.Running {
		t.Fatal("current container affected by a failed build")
	}
	if ids := releases(t, h, "production"); !slices.Equal(ids, []string{first.Release}) {
		t.Fatalf("releases = %v", ids)
	}
}

func TestCrashingContainerIsRolledBack(t *testing.T) {
	h := testdeploy.New(t)
	_, _ = h.Deploy(t, testdeploy.Request("production"))
	first, _ := h.State.Get("app", "production")
	h.RT.Behave = func(runtime.ContainerSpec) fakert.Behavior { return fakert.Behavior{Crash: true} }
	_, err := h.Deploy(t, testdeploy.Request("production"))
	var oe *deploy.OpError
	if !errors.As(err, &oe) || oe.Code != deploy.CodeHealth || !strings.Contains(oe.Msg, "exited with code 1") {
		t.Fatalf("err = %v", err)
	}
	if len(oe.Logs) == 0 || !strings.HasPrefix(oe.Logs[0], "log from sagan_app_production_") {
		t.Errorf("logs = %v", oe.Logs)
	}
	if names := h.RT.Names(); !slices.Equal(names, []string{first.Container}) {
		t.Fatalf("containers = %v, want only %s", names, first.Container)
	}
	if body := fetch(t, h.Routes.Get("app.test")); body != deploy.ImageName("app", "production", first.Release) {
		t.Fatalf("route changed to %q", body)
	}
}

func TestUnhealthyContainerTimesOut(t *testing.T) {
	h := testdeploy.New(t)
	h.RT.Behave = func(runtime.ContainerSpec) fakert.Behavior { return fakert.Behavior{Status: 500} }
	_, err := h.Deploy(t, testdeploy.Request("production"))
	if errCode(err) != deploy.CodeHealth || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("err = %v", err)
	}
	if h.Routes.Len() != 0 {
		t.Fatal("a route was registered for an unhealthy release")
	}
	if len(releases(t, h, "production")) != 0 {
		t.Fatal("failed release directory left behind")
	}
}

func TestConcurrentDeploySameWorkspaceIsBusy(t *testing.T) {
	h := testdeploy.New(t)
	entered, unblock := make(chan struct{}), make(chan struct{})
	var once sync.Once
	h.RT.BuildHook = func(string) { once.Do(func() { close(entered); <-unblock }) }
	archive := testutil.App(t)
	errc := make(chan error, 1)
	go func() {
		errc <- h.D.Deploy(context.Background(), testdeploy.Request("production"), archive, &events.Recorder{})
	}()
	<-entered
	_, err := h.Deploy(t, testdeploy.Request("production"))
	if errCode(err) != deploy.CodeBusy {
		t.Fatalf("err = %v, want busy", err)
	}
	close(unblock)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestDeploysToDifferentWorkspacesRunConcurrently(t *testing.T) {
	h := testdeploy.New(t)
	var both sync.WaitGroup
	both.Add(2)
	h.RT.BuildHook = func(string) { both.Done(); both.Wait() } // deadlocks if deploys are serialized
	a, b := testutil.App(t), testutil.App(t)
	errc := make(chan error, 2)
	go func() {
		errc <- h.D.Deploy(context.Background(), testdeploy.Request("production"), a, &events.Recorder{})
	}()
	go func() { errc <- h.D.Deploy(context.Background(), testdeploy.Request("feat-x"), b, &events.Recorder{}) }()
	for i := 0; i < 2; i++ {
		select {
		case err := <-errc:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("deploys to different workspaces blocked each other")
		}
	}
	if h.Routes.Get("app.test") == "" || h.Routes.Get("feat-x.app.test") == "" {
		t.Fatal("missing routes")
	}
}

func TestTruncatedUploadLeavesNoRelease(t *testing.T) {
	h := testdeploy.New(t)
	_, _ = h.Deploy(t, testdeploy.Request("production"))
	first, _ := h.State.Get("app", "production")
	full := testutil.App(t).Bytes()
	err := h.D.Deploy(context.Background(), testdeploy.Request("production"), bytes.NewReader(full[:len(full)/2]), &events.Recorder{})
	if errCode(err) != deploy.CodeArchive {
		t.Fatalf("err = %v, want invalid_archive", err)
	}
	if ids := releases(t, h, "production"); !slices.Equal(ids, []string{first.Release}) {
		t.Fatalf("releases = %v", ids)
	}
	if body := fetch(t, h.Routes.Get("app.test")); body != deploy.ImageName("app", "production", first.Release) {
		t.Fatal("current release affected by a truncated upload")
	}
}

func TestRedeployWhenPreviousContainerVanished(t *testing.T) {
	h := testdeploy.New(t)
	_, _ = h.Deploy(t, testdeploy.Request("production"))
	first, _ := h.State.Get("app", "production")
	if err := h.RT.Remove(context.Background(), first.Container); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Deploy(t, testdeploy.Request("production")); err != nil {
		t.Fatalf("deploy after the old container vanished: %v", err)
	}
}

func TestHostConflict(t *testing.T) {
	h := testdeploy.New(t)
	one := testdeploy.Request("production")
	one.Project, one.Domain = "one", "shared.test"
	if _, err := h.Deploy(t, one); err != nil {
		t.Fatal(err)
	}
	two := one
	two.Project = "two"
	if _, err := h.Deploy(t, two); errCode(err) != deploy.CodeHostConflict {
		t.Fatalf("err = %v, want host_conflict", err)
	}
}

func TestRetentionKeepsThreeReleases(t *testing.T) {
	h := testdeploy.New(t)
	for i := 0; i < 5; i++ {
		if _, err := h.Deploy(t, testdeploy.Request("production")); err != nil {
			t.Fatal(err)
		}
	}
	if ids := releases(t, h, "production"); len(ids) != 3 {
		t.Fatalf("releases = %v", ids)
	}
	if n := len(h.RT.RemovedImages()); n != 2 {
		t.Fatalf("removed images = %v", h.RT.RemovedImages())
	}
}

func TestTLSFailureIsAWarning(t *testing.T) {
	h := testdeploy.New(t)
	h.Certs.Err = errors.New("rate limited")
	rec, err := h.Deploy(t, testdeploy.Request("production"))
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Has("warn", "tls_pending") || rec.Last().Type != "done" {
		t.Fatalf("events = %+v", rec.Events)
	}
}

func TestDeployWithoutDomain(t *testing.T) {
	h := testdeploy.New(t)
	req := testdeploy.Request("production")
	req.Domain = ""
	rec, err := h.Deploy(t, req)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Last().URL != "" || rec.Last().HostPort == 0 || h.Routes.Len() != 0 || len(h.Certs.Hosts()) != 0 {
		t.Fatalf("last = %+v, routes = %d", rec.Last(), h.Routes.Len())
	}
	if slices.Contains(rec.Steps(), "tls") {
		t.Error("tls step without a domain")
	}
}

func TestInvalidRequests(t *testing.T) {
	h := testdeploy.New(t)
	mutate := []func(*deploy.Request){
		func(r *deploy.Request) { r.Project = "Bad!" },
		func(r *deploy.Request) { r.Workspace = "" },
		func(r *deploy.Request) { r.InternalPort = 0 },
		func(r *deploy.Request) { r.Domain = "https://x.com" },
		func(r *deploy.Request) { r.HealthPath = "health" },
		func(r *deploy.Request) { r.HealthTimeout = -time.Second },
	}
	for i, m := range mutate {
		req := testdeploy.Request("production")
		m(&req)
		if _, err := h.Deploy(t, req); errCode(err) != deploy.CodeInvalid {
			t.Errorf("case %d: err = %v, want invalid", i, err)
		}
	}
}
```

- [ ] **Step 3: Rodar e ver falhar**

Run: `cd agent && go test ./internal/deploy/`
Expected: FAIL com `undefined: deploy.DefaultConfig` (e demais símbolos).

- [ ] **Step 4: Implementar o health check**

`agent/internal/deploy/health.go`:

```go
package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

var healthClient = &http.Client{
	Timeout:       2 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// waitHealthy polls the container until it is healthy, exits, or timeout
// passes. It returns the container's host port whenever it is known.
func (d *Deployer) waitHealthy(ctx context.Context, name, path string, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	for {
		info, err := d.rt.Inspect(ctx, name)
		if err != nil {
			return 0, fmt.Errorf("inspect container: %w", err)
		}
		if !info.Running {
			return 0, fmt.Errorf("container exited with code %d", info.ExitCode)
		}
		if info.HostPort == 0 {
			return 0, errors.New("container has no published port")
		}
		perr := probe(ctx, info.HostPort, path)
		if perr == nil {
			return info.HostPort, nil
		}
		if time.Now().After(deadline) {
			return info.HostPort, fmt.Errorf("not healthy after %s: %v", timeout, perr)
		}
		if err := sleep(ctx, d.cfg.HealthInterval); err != nil {
			return info.HostPort, err
		}
	}
}

func probe(ctx context.Context, port int, path string) error {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if path == "" {
		return probeTCP(ctx, addr)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		return err
	}
	resp, err := healthClient.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("health check returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// probeTCP treats a connection that stays open as healthy. Rootless Podman's
// port forwarder accepts connections even when nothing listens inside the
// container and closes them right after, so a successful dial is not enough.
func probeTCP(ctx context.Context, addr string) error {
	dctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var dialer net.Dialer
	c, err := dialer.DialContext(dctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	var b [1]byte
	_, err = c.Read(b[:])
	var ne net.Error
	if err == nil || (errors.As(err, &ne) && ne.Timeout()) {
		return nil
	}
	return fmt.Errorf("connection closed by the container: %v", err)
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
```

- [ ] **Step 5: Implementar o deploy**

`agent/internal/deploy/deploy.go`:

```go
// Package deploy orchestrates builds, zero-downtime swaps and workspace
// lifecycle on top of a container runtime.
package deploy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/borgim/sagansync/agent/internal/domains"
	"github.com/borgim/sagansync/agent/internal/envstore"
	"github.com/borgim/sagansync/agent/internal/events"
	"github.com/borgim/sagansync/agent/internal/release"
	"github.com/borgim/sagansync/agent/internal/runtime"
	"github.com/borgim/sagansync/agent/internal/state"
	"github.com/borgim/sagansync/agent/internal/validate"
)

const (
	CodeInvalid          = "invalid"
	CodeBusy             = "busy"
	CodeBuild            = "build_failed"
	CodeHealth           = "health_failed"
	CodeArchive          = "invalid_archive"
	CodeHostConflict     = "host_conflict"
	CodeNotFound         = "not_found"
	CodeProductionLocked = "production_locked"
	CodeInternal         = "internal"
)

// OpError is an operation failure the CLI can show as is.
type OpError struct {
	Code string
	Msg  string
	Logs []string
}

func (e *OpError) Error() string { return e.Code + ": " + e.Msg }

func invalid(err error) error     { return &OpError{Code: CodeInvalid, Msg: err.Error()} }
func internalErr(err error) error { return &OpError{Code: CodeInternal, Msg: err.Error()} }

type Routes interface {
	Set(host, upstream string)
	Delete(host string)
}

type Certs interface {
	Ensure(ctx context.Context, host string) error
}

type Config struct {
	SrvDir           string
	Keep             int
	Drain            time.Duration
	StopTimeout      time.Duration
	HealthInterval   time.Duration
	DevHealthTimeout time.Duration
	LogTail          int
	Now              func() time.Time
}

func DefaultConfig(srvDir string) Config {
	return Config{SrvDir: srvDir, Keep: 3, Drain: 10 * time.Second, StopTimeout: 10 * time.Second,
		HealthInterval: 500 * time.Millisecond, DevHealthTimeout: 30 * time.Second, LogTail: 50, Now: time.Now}
}

type Deployer struct {
	rt     runtime.Runtime
	st     *state.Store
	locks  *state.Locks
	env    *envstore.Store
	routes Routes
	certs  Certs
	cfg    Config
}

func New(rt runtime.Runtime, st *state.Store, env *envstore.Store, routes Routes, certs Certs, cfg Config) *Deployer {
	return &Deployer{rt: rt, st: st, locks: state.NewLocks(), env: env, routes: routes, certs: certs, cfg: cfg}
}

// Names use "_" as separator, which project and workspace names cannot
// contain, so different (project, workspace) pairs never collide.
func ContainerName(p, w, id string) string { return "sagan_" + p + "_" + w + "_" + id }
func ImageName(p, w, tag string) string    { return "localhost/sagan_" + p + "_" + w + ":" + tag }
func volumeName(p, w string) string        { return "sagan_" + p + "_" + w + "_node_modules" }

func labels(p, w, id string) map[string]string {
	return map[string]string{runtime.LabelManaged: "true", runtime.LabelProject: p,
		runtime.LabelWorkspace: w, runtime.LabelRelease: id}
}

func (d *Deployer) workspaceDir(p, w string) string { return filepath.Join(d.cfg.SrvDir, p, w) }
func (d *Deployer) releasesDir(p, w string) string {
	return filepath.Join(d.workspaceDir(p, w), "releases")
}
func (d *Deployer) devDir(p, w string) string { return filepath.Join(d.workspaceDir(p, w), "dev") }

type Request struct {
	Project       string
	Workspace     string
	Domain        string
	PreviewDomain string
	InternalPort  int
	HealthPath    string
	HealthTimeout time.Duration
	Sha           string
}

func (r *Request) check() error {
	if r.HealthTimeout == 0 {
		r.HealthTimeout = 60 * time.Second
	}
	checks := []error{
		validate.Name("project", r.Project),
		validate.Name("workspace", r.Workspace),
		validate.Port("internalPort", r.InternalPort),
		validate.HealthPath("healthPath", r.HealthPath),
	}
	if r.Domain != "" {
		checks = append(checks, validate.Domain("domain", r.Domain))
	}
	if r.PreviewDomain != "" {
		checks = append(checks, validate.Domain("previewDomain", r.PreviewDomain))
	}
	for _, err := range checks {
		if err != nil {
			return invalid(err)
		}
	}
	if r.HealthTimeout < 0 || r.HealthTimeout > 10*time.Minute {
		return &OpError{Code: CodeInvalid, Msg: "healthTimeout must be positive and at most 10m"}
	}
	return nil
}

func (d *Deployer) resolveHost(r Request) (string, error) {
	host := domains.Host(r.Project, r.Workspace, r.Domain, r.PreviewDomain)
	if host == "" {
		return "", nil
	}
	if err := validate.Domain("host", host); err != nil {
		return "", invalid(err)
	}
	if e, ok := d.st.HostOwner(host); ok && (e.Project != r.Project || e.Workspace != r.Workspace) {
		return "", &OpError{Code: CodeHostConflict, Msg: fmt.Sprintf("%s is already used by %s/%s", host, e.Project, e.Workspace)}
	}
	return host, nil
}

func urlFor(host string) string {
	if host == "" {
		return ""
	}
	return "https://" + host
}

func (d *Deployer) lock(p, w string) (func(), error) {
	unlock, ok := d.locks.TryLock(p, w)
	if !ok {
		return nil, &OpError{Code: CodeBusy, Msg: fmt.Sprintf("another operation is running on %s/%s", p, w)}
	}
	return unlock, nil
}

// Deploy builds the uploaded snapshot and swaps traffic to it only after it is
// healthy. On any failure the running release is left untouched.
func (d *Deployer) Deploy(ctx context.Context, req Request, archive io.Reader, em events.Emitter) error {
	if err := req.check(); err != nil {
		return err
	}
	host, err := d.resolveHost(req)
	if err != nil {
		return err
	}
	unlock, err := d.lock(req.Project, req.Workspace)
	if err != nil {
		return err
	}
	defer unlock()
	p, w := req.Project, req.Workspace

	em.Emit(events.Step("extract"))
	relDir := d.releasesDir(p, w)
	id, err := release.Allocate(relDir, release.NewID(d.cfg.Now(), req.Sha))
	if err != nil {
		return internalErr(err)
	}
	dir := filepath.Join(relDir, id)
	if err := release.Extract(archive, dir, release.DefaultLimits); err != nil {
		os.RemoveAll(dir)
		return &OpError{Code: CodeArchive, Msg: err.Error()}
	}

	em.Emit(events.Step("build"))
	image := ImageName(p, w, id)
	if err := d.build(ctx, dir, image, em); err != nil {
		os.RemoveAll(dir)
		return err
	}
	spec, err := d.deploySpec(p, w, id, req.InternalPort)
	if err != nil {
		os.RemoveAll(dir)
		return internalErr(err)
	}
	port, err := d.activate(ctx, activation{req: req, host: host, id: id, spec: spec, mode: state.ModeDeploy,
		healthPath: req.HealthPath, healthTimeout: req.HealthTimeout, strict: true, drain: d.cfg.Drain}, em)
	if err != nil {
		os.RemoveAll(dir)
		_ = d.rt.RemoveImage(ctx, image)
		return err
	}
	removed, _ := release.Prune(relDir, d.cfg.Keep, id)
	for _, old := range removed {
		_ = d.rt.RemoveImage(ctx, ImageName(p, w, old))
	}
	em.Emit(events.Done(urlFor(host), id, port))
	return nil
}

func (d *Deployer) build(ctx context.Context, dir, image string, em events.Emitter) error {
	tr := release.TarDir(dir)
	defer tr.Close()
	err := d.rt.Build(ctx, tr, image, func(line string) { em.Emit(events.Log("build", line)) })
	if err != nil {
		return &OpError{Code: CodeBuild, Msg: err.Error()}
	}
	return nil
}

func (d *Deployer) deploySpec(p, w, id string, port int) (runtime.ContainerSpec, error) {
	env, err := d.env.Get(p, w)
	if err != nil {
		return runtime.ContainerSpec{}, err
	}
	return runtime.ContainerSpec{Name: ContainerName(p, w, id), Image: ImageName(p, w, id), Env: env,
		InternalPort: port, Labels: labels(p, w, id)}, nil
}

type activation struct {
	req           Request
	host          string
	id            string
	spec          runtime.ContainerSpec
	mode          string
	command       []string
	healthPath    string
	healthTimeout time.Duration
	strict        bool // a failed health check aborts (deploy) or only warns (dev)
	drain         time.Duration
}

// activate starts the new container, waits for it, points the route at it,
// records it in the state and only then retires the previous container.
func (d *Deployer) activate(ctx context.Context, a activation, em events.Emitter) (int, error) {
	p, w := a.req.Project, a.req.Workspace
	em.Emit(events.Step("start"))
	if err := d.rt.Create(ctx, a.spec); err != nil {
		return 0, internalErr(fmt.Errorf("create container: %w", err))
	}
	if err := d.rt.Start(ctx, a.spec.Name); err != nil {
		return 0, d.abort(ctx, a.spec.Name, fmt.Errorf("start container: %w", err))
	}
	em.Emit(events.Step("health"))
	port, err := d.waitHealthy(ctx, a.spec.Name, a.healthPath, a.healthTimeout)
	if err != nil {
		if a.strict || port == 0 {
			return 0, d.abort(ctx, a.spec.Name, err)
		}
		em.Emit(events.Warn(CodeHealth, err.Error()))
	}

	prev, hadPrev := d.st.Get(p, w)
	if a.host != "" {
		d.routes.Set(a.host, fmt.Sprintf("127.0.0.1:%d", port))
	}
	ws := state.Workspace{Host: a.host, Domain: a.req.Domain, PreviewDomain: a.req.PreviewDomain,
		InternalPort: a.req.InternalPort, HealthPath: a.req.HealthPath, Mode: a.mode, Release: a.id,
		Container: a.spec.Name, Command: a.command, HostPort: port, UpdatedAt: d.cfg.Now()}
	if err := d.st.Put(p, w, ws); err != nil {
		if a.host != "" {
			if hadPrev && prev.Host == a.host {
				d.routes.Set(a.host, fmt.Sprintf("127.0.0.1:%d", prev.HostPort))
			} else {
				d.routes.Delete(a.host)
			}
		}
		_ = d.rt.Remove(ctx, a.spec.Name)
		return 0, internalErr(fmt.Errorf("save state: %w", err))
	}
	if hadPrev && prev.Host != "" && prev.Host != a.host {
		d.routes.Delete(prev.Host)
	}
	if a.host != "" {
		em.Emit(events.Step("tls"))
		tctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		if err := d.certs.Ensure(tctx, a.host); err != nil {
			em.Emit(events.Warn("tls_pending", fmt.Sprintf("certificate for %s not ready yet (retrying in the background): %v", a.host, err)))
		}
		cancel()
	}
	if hadPrev && prev.Container != "" && prev.Container != a.spec.Name {
		em.Emit(events.Step("drain"))
		_ = sleep(ctx, a.drain)
		_ = d.rt.Stop(ctx, prev.Container, d.cfg.StopTimeout)
		_ = d.rt.Remove(ctx, prev.Container)
	}
	return port, nil
}

// abort collects the failed container's last log lines and removes it.
func (d *Deployer) abort(ctx context.Context, name string, cause error) error {
	logs := d.tail(ctx, name)
	_ = d.rt.Remove(ctx, name)
	return &OpError{Code: CodeHealth, Msg: cause.Error(), Logs: logs}
}

func (d *Deployer) tail(ctx context.Context, name string) []string {
	var buf bytes.Buffer
	_ = d.rt.Logs(ctx, name, d.cfg.LogTail, false, &buf)
	text := strings.TrimRight(buf.String(), "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > d.cfg.LogTail {
		lines = lines[len(lines)-d.cfg.LogTail:]
	}
	return lines
}
```

- [ ] **Step 6: Rodar e ver passar**

Run: `cd agent && go vet ./internal/... && go test -race ./internal/deploy/`
Expected: PASS. `TestDeploysToDifferentWorkspacesRunConcurrently` falharia por timeout se a trava fosse global.

- [ ] **Step 7: Commit**

```bash
git add agent/internal/deploy agent/internal/testutil/testdeploy
git commit -m "feat(agent): deploy releases with health checks and zero-downtime swaps"
```

---

### Task 13: `deploy` — dev, arquivos, remove, list, logs e env

**Files:**
- Create: `agent/internal/deploy/dev.go`
- Create: `agent/internal/deploy/manage.go`
- Test: `agent/internal/deploy/dev_test.go`
- Test: `agent/internal/deploy/manage_test.go`

**Interfaces:**
- Consumes: tudo da Task 12 (`Deployer`, `Request`, `activation`, `activate`, `build`, `lock`, `resolveHost`, `devDir`, `workspaceDir`, `releasesDir`, `ImageName`, `ContainerName`, `volumeName`, `labels`, `urlFor`, `invalid`, `internalErr`); `release.WriteFile/RemoveFile/ErrInvalidArchive`; `envstore.Keys`.
- Produces:
  - `type DevRequest struct { Request; Command []string; Build, Force bool }`
  - `func (d *Deployer) Dev(ctx context.Context, req DevRequest, archive io.Reader, em events.Emitter) error` — emite `done` no fim.
  - `func (d *Deployer) PutFile(p, w, rel string, r io.Reader) error`, `func (d *Deployer) RemoveFile(p, w, rel string) error` — só em workspace no modo dev.
  - `func (d *Deployer) Remove(ctx context.Context, p, w string) error`
  - `type Status struct { Project, Workspace, Mode, Release, URL string; HostPort int; Running bool; UpdatedAt time.Time }` (tags JSON em camelCase) e `func (d *Deployer) List(ctx context.Context) []Status`
  - `func (d *Deployer) Logs(ctx context.Context, p, w string, tail int, follow bool, out io.Writer) error`
  - `func (d *Deployer) EnvKeys(p, w string) ([]string, error)`, `EnvSet(p, w string, kv map[string]string) error`, `EnvUnset(p, w string, keys []string) error`
  - `func (d *Deployer) devSpec(p, w, id string, port int, command []string) (runtime.ContainerSpec, error)` (usado pela Task 14)

- [ ] **Step 1: Escrever os testes que falham**

`agent/internal/deploy/dev_test.go`:

```go
package deploy_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/borgim/sagansync/agent/internal/deploy"
	"github.com/borgim/sagansync/agent/internal/events"
	"github.com/borgim/sagansync/agent/internal/runtime"
	"github.com/borgim/sagansync/agent/internal/runtime/fakert"
	"github.com/borgim/sagansync/agent/internal/state"
	"github.com/borgim/sagansync/agent/internal/testutil"
	"github.com/borgim/sagansync/agent/internal/testutil/testdeploy"
)

func devReq(ws string) deploy.DevRequest {
	return deploy.DevRequest{Request: testdeploy.Request(ws), Command: []string{"npm", "run", "dev"}}
}

func startDev(t *testing.T, h *testdeploy.Harness, req deploy.DevRequest) (*events.Recorder, error) {
	t.Helper()
	rec := &events.Recorder{}
	return rec, h.D.Dev(context.Background(), req, testutil.App(t), rec)
}

func TestDevRunsWithBindMount(t *testing.T) {
	h := testdeploy.New(t)
	rec, err := startDev(t, h, devReq("feat-x"))
	if err != nil {
		t.Fatal(err)
	}
	ws, _ := h.State.Get("app", "feat-x")
	if ws.Mode != state.ModeDev || !slices.Equal(ws.Command, []string{"npm", "run", "dev"}) {
		t.Fatalf("state = %+v", ws)
	}
	c, _ := h.RT.Container(ws.Container)
	if c.Spec.Image != deploy.ImageName("app", "feat-x", "dev") {
		t.Errorf("image = %s", c.Spec.Image)
	}
	if len(c.Spec.Binds) != 1 || c.Spec.Binds[0].Dest != "/app" || !strings.HasSuffix(c.Spec.Binds[0].Source, filepath.Join("app", "feat-x", "dev")) {
		t.Errorf("binds = %+v", c.Spec.Binds)
	}
	if len(c.Spec.Volumes) != 1 || c.Spec.Volumes[0].Dest != "/app/node_modules" {
		t.Errorf("volumes = %+v", c.Spec.Volumes)
	}
	if c.Spec.Env["NODE_ENV"] != "development" {
		t.Errorf("env = %v", c.Spec.Env)
	}
	if rec.Last().Type != "done" || rec.Last().URL != "https://feat-x.app.test" {
		t.Errorf("last = %+v", rec.Last())
	}
}

func TestDevReusesImageUnlessBuildRequested(t *testing.T) {
	h := testdeploy.New(t)
	if _, err := startDev(t, h, devReq("feat-x")); err != nil {
		t.Fatal(err)
	}
	if _, err := startDev(t, h, devReq("feat-x")); err != nil {
		t.Fatal(err)
	}
	if n := len(h.RT.Builds()); n != 1 {
		t.Fatalf("builds = %v, want 1", h.RT.Builds())
	}
	req := devReq("feat-x")
	req.Build = true
	if _, err := startDev(t, h, req); err != nil {
		t.Fatal(err)
	}
	if n := len(h.RT.Builds()); n != 2 {
		t.Fatalf("builds = %v, want 2", h.RT.Builds())
	}
	if n := len(h.RT.Names()); n != 1 {
		t.Fatalf("containers = %v, old dev containers must be removed", h.RT.Names())
	}
}

func TestDevProductionLockAndValidation(t *testing.T) {
	h := testdeploy.New(t)
	if _, err := startDev(t, h, devReq("production")); errCode(err) != deploy.CodeProductionLocked {
		t.Fatalf("err = %v, want production_locked", err)
	}
	forced := devReq("production")
	forced.Force = true
	if _, err := startDev(t, h, forced); err != nil {
		t.Fatalf("forced dev on production: %v", err)
	}
	noCmd := devReq("feat-y")
	noCmd.Command = nil
	if _, err := startDev(t, h, noCmd); errCode(err) != deploy.CodeInvalid {
		t.Fatalf("err = %v, want invalid", err)
	}
}

func TestDevCrashFails(t *testing.T) {
	h := testdeploy.New(t)
	h.RT.Behave = func(runtime.ContainerSpec) fakert.Behavior { return fakert.Behavior{Crash: true} }
	if _, err := startDev(t, h, devReq("feat-x")); errCode(err) != deploy.CodeHealth {
		t.Fatalf("err = %v, want health_failed for a crashed dev container", err)
	}
}

func TestPutAndRemoveFilesInDevMode(t *testing.T) {
	h := testdeploy.New(t)
	if _, err := startDev(t, h, devReq("feat-x")); err != nil {
		t.Fatal(err)
	}
	if err := h.D.PutFile("app", "feat-x", "src/new.js", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(h.SrvDir, "app", "feat-x", "dev", "src", "new.js")
	if b, err := os.ReadFile(path); err != nil || string(b) != "x" {
		t.Fatalf("file = %q, %v", b, err)
	}
	if err := h.D.RemoveFile("app", "feat-x", "src/new.js"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file still exists")
	}
	if err := h.D.PutFile("app", "feat-x", "../escape", strings.NewReader("x")); errCode(err) != deploy.CodeInvalid {
		t.Fatalf("err = %v, want invalid", err)
	}
}

func TestPutFileRequiresDevMode(t *testing.T) {
	h := testdeploy.New(t)
	_, _ = h.Deploy(t, testdeploy.Request("production"))
	if err := h.D.PutFile("app", "production", "a.js", strings.NewReader("x")); errCode(err) != deploy.CodeNotFound {
		t.Fatalf("err = %v, want not_found", err)
	}
	if err := h.D.PutFile("app", "ghost", "a.js", strings.NewReader("x")); errCode(err) != deploy.CodeNotFound {
		t.Fatalf("err = %v, want not_found", err)
	}
}

func TestDeployAfterDevReturnsToDeployMode(t *testing.T) {
	h := testdeploy.New(t)
	_, _ = startDev(t, h, devReq("feat-x"))
	if _, err := h.Deploy(t, testdeploy.Request("feat-x")); err != nil {
		t.Fatal(err)
	}
	if ws, _ := h.State.Get("app", "feat-x"); ws.Mode != state.ModeDeploy {
		t.Fatalf("mode = %s", ws.Mode)
	}
	if n := len(h.RT.Names()); n != 1 {
		t.Fatalf("containers = %v", h.RT.Names())
	}
}
```

`agent/internal/deploy/manage_test.go`:

```go
package deploy_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/borgim/sagansync/agent/internal/deploy"
	"github.com/borgim/sagansync/agent/internal/testutil/testdeploy"
)

func TestRemoveDeletesEverything(t *testing.T) {
	h := testdeploy.New(t)
	_, _ = h.Deploy(t, testdeploy.Request("feat-x"))
	ws, _ := h.State.Get("app", "feat-x")
	_ = h.D.EnvSet("app", "feat-x", map[string]string{"A": "1"})
	if err := h.D.Remove(context.Background(), "app", "feat-x"); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.State.Get("app", "feat-x"); ok {
		t.Error("still in state")
	}
	if h.Routes.Get("feat-x.app.test") != "" {
		t.Error("route still present")
	}
	if _, ok := h.RT.Container(ws.Container); ok {
		t.Error("container still present")
	}
	if !slices.Contains(h.RT.RemovedImages(), deploy.ImageName("app", "feat-x", ws.Release)) {
		t.Errorf("removed images = %v", h.RT.RemovedImages())
	}
	if !slices.Contains(h.RT.RemovedVolumes(), "sagan_app_feat-x_node_modules") {
		t.Errorf("removed volumes = %v", h.RT.RemovedVolumes())
	}
	if _, err := os.Stat(filepath.Join(h.SrvDir, "app", "feat-x")); !os.IsNotExist(err) {
		t.Error("workspace directory still present")
	}
	if keys, _ := h.D.EnvKeys("app", "feat-x"); len(keys) != 0 {
		t.Errorf("env keys = %v", keys)
	}
	if err := h.D.Remove(context.Background(), "app", "feat-x"); errCode(err) != deploy.CodeNotFound {
		t.Fatalf("second remove: %v", err)
	}
}

func TestListReportsWorkspaces(t *testing.T) {
	h := testdeploy.New(t)
	_, _ = h.Deploy(t, testdeploy.Request("production"))
	_, _ = startDev(t, h, devReq("feat-x"))
	list := h.D.List(context.Background())
	if len(list) != 2 {
		t.Fatalf("list = %+v", list)
	}
	if list[0].Workspace != "feat-x" || list[0].Mode != "dev" || !list[0].Running || list[0].URL != "https://feat-x.app.test" {
		t.Errorf("list[0] = %+v", list[0])
	}
	if list[1].Workspace != "production" || list[1].Mode != "deploy" || list[1].HostPort == 0 {
		t.Errorf("list[1] = %+v", list[1])
	}
}

func TestLogs(t *testing.T) {
	h := testdeploy.New(t)
	_, _ = h.Deploy(t, testdeploy.Request("production"))
	var out bytes.Buffer
	if err := h.D.Logs(context.Background(), "app", "production", 100, false, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "log from sagan_app_production_") {
		t.Fatalf("logs = %q", out.String())
	}
	if err := h.D.Logs(context.Background(), "app", "ghost", 100, false, &out); errCode(err) != deploy.CodeNotFound {
		t.Fatalf("err = %v", err)
	}
}

func TestEnvIsInjectedOnNextDeploy(t *testing.T) {
	h := testdeploy.New(t)
	if err := h.D.EnvSet("app", "production", map[string]string{"API_KEY": "s3cr3t", "B": "2"}); err != nil {
		t.Fatal(err)
	}
	_, _ = h.Deploy(t, testdeploy.Request("production"))
	ws, _ := h.State.Get("app", "production")
	c, _ := h.RT.Container(ws.Container)
	if c.Spec.Env["API_KEY"] != "s3cr3t" {
		t.Fatalf("env = %v", c.Spec.Env)
	}
	if keys, _ := h.D.EnvKeys("app", "production"); !slices.Equal(keys, []string{"API_KEY", "B"}) {
		t.Fatalf("keys = %v", keys)
	}
	if err := h.D.EnvUnset("app", "production", []string{"B"}); err != nil {
		t.Fatal(err)
	}
	if keys, _ := h.D.EnvKeys("app", "production"); !slices.Equal(keys, []string{"API_KEY"}) {
		t.Fatalf("keys = %v", keys)
	}
}

func TestEnvValidation(t *testing.T) {
	h := testdeploy.New(t)
	if err := h.D.EnvSet("app", "production", map[string]string{"1BAD": "x"}); errCode(err) != deploy.CodeInvalid {
		t.Fatalf("err = %v", err)
	}
	if err := h.D.EnvSet("../etc", "production", map[string]string{"A": "x"}); errCode(err) != deploy.CodeInvalid {
		t.Fatalf("err = %v", err)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd agent && go test ./internal/deploy/`
Expected: FAIL com `undefined: deploy.DevRequest` (e demais símbolos).

- [ ] **Step 3: Implementar o dev e os arquivos**

`agent/internal/deploy/dev.go`:

```go
package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/borgim/sagansync/agent/internal/events"
	"github.com/borgim/sagansync/agent/internal/release"
	"github.com/borgim/sagansync/agent/internal/runtime"
	"github.com/borgim/sagansync/agent/internal/state"
	"github.com/borgim/sagansync/agent/internal/validate"
)

type DevRequest struct {
	Request
	Command []string
	Build   bool
	Force   bool
}

// Dev runs the workspace from a bind-mounted copy of the sources, so later
// PutFile/RemoveFile calls show up in the running container.
func (d *Deployer) Dev(ctx context.Context, req DevRequest, archive io.Reader, em events.Emitter) error {
	if err := req.Request.check(); err != nil {
		return err
	}
	if req.Workspace == "production" && !req.Force {
		return &OpError{Code: CodeProductionLocked, Msg: "dev mode on production requires --force"}
	}
	if len(req.Command) == 0 {
		return &OpError{Code: CodeInvalid, Msg: "dev requires a command"}
	}
	host, err := d.resolveHost(req.Request)
	if err != nil {
		return err
	}
	unlock, err := d.lock(req.Project, req.Workspace)
	if err != nil {
		return err
	}
	defer unlock()
	p, w := req.Project, req.Workspace

	em.Emit(events.Step("extract"))
	dir := d.devDir(p, w)
	if err := os.RemoveAll(dir); err != nil {
		return internalErr(err)
	}
	if err := release.Extract(archive, dir, release.DefaultLimits); err != nil {
		return &OpError{Code: CodeArchive, Msg: err.Error()}
	}
	image := ImageName(p, w, "dev")
	exists, err := d.rt.ImageExists(ctx, image)
	if err != nil {
		return internalErr(err)
	}
	if req.Build || !exists {
		em.Emit(events.Step("build"))
		if err := d.build(ctx, dir, image, em); err != nil {
			return err
		}
	}
	now := d.cfg.Now().UTC()
	id := fmt.Sprintf("dev-%s-%03d", now.Format("20060102-150405"), now.Nanosecond()/int(time.Millisecond))
	spec, err := d.devSpec(p, w, id, req.InternalPort, req.Command)
	if err != nil {
		return internalErr(err)
	}
	port, err := d.activate(ctx, activation{req: req.Request, host: host, id: id, spec: spec, mode: state.ModeDev,
		command: req.Command, healthTimeout: d.cfg.DevHealthTimeout, strict: false}, em)
	if err != nil {
		return err
	}
	em.Emit(events.Done(urlFor(host), id, port))
	return nil
}

func (d *Deployer) devSpec(p, w, id string, port int, command []string) (runtime.ContainerSpec, error) {
	env, err := d.env.Get(p, w)
	if err != nil {
		return runtime.ContainerSpec{}, err
	}
	if _, ok := env["NODE_ENV"]; !ok {
		env["NODE_ENV"] = "development"
	}
	return runtime.ContainerSpec{
		Name: ContainerName(p, w, id), Image: ImageName(p, w, "dev"), Command: command, Env: env,
		InternalPort: port, Labels: labels(p, w, id),
		Binds:   []runtime.Bind{{Source: d.devDir(p, w), Dest: "/app"}},
		Volumes: []runtime.Volume{{Name: volumeName(p, w), Dest: "/app/node_modules"}},
	}, nil
}

func (d *Deployer) devRoot(p, w string) (string, error) {
	if err := validate.Name("project", p); err != nil {
		return "", invalid(err)
	}
	if err := validate.Name("workspace", w); err != nil {
		return "", invalid(err)
	}
	ws, ok := d.st.Get(p, w)
	if !ok || ws.Mode != state.ModeDev {
		return "", &OpError{Code: CodeNotFound, Msg: fmt.Sprintf("%s/%s is not running in dev mode", p, w)}
	}
	return d.devDir(p, w), nil
}

func fileErr(err error) error {
	if errors.Is(err, validate.ErrInvalid) || errors.Is(err, release.ErrInvalidArchive) {
		return invalid(err)
	}
	return internalErr(err)
}

func (d *Deployer) PutFile(p, w, rel string, r io.Reader) error {
	root, err := d.devRoot(p, w)
	if err != nil {
		return err
	}
	if err := release.WriteFile(root, rel, r); err != nil {
		return fileErr(err)
	}
	return nil
}

func (d *Deployer) RemoveFile(p, w, rel string) error {
	root, err := d.devRoot(p, w)
	if err != nil {
		return err
	}
	if err := release.RemoveFile(root, rel); err != nil {
		return fileErr(err)
	}
	return nil
}
```

- [ ] **Step 4: Implementar remove, list, logs e env**

`agent/internal/deploy/manage.go`:

```go
package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/borgim/sagansync/agent/internal/envstore"
	"github.com/borgim/sagansync/agent/internal/runtime"
	"github.com/borgim/sagansync/agent/internal/validate"
)

func checkNames(p, w string) error {
	if err := validate.Name("project", p); err != nil {
		return invalid(err)
	}
	if err := validate.Name("workspace", w); err != nil {
		return invalid(err)
	}
	return nil
}

func notFound(p, w string) error {
	return &OpError{Code: CodeNotFound, Msg: fmt.Sprintf("workspace %s/%s does not exist", p, w)}
}

// Remove deletes a workspace: route, container, images, volume, files, env
// and state.
func (d *Deployer) Remove(ctx context.Context, p, w string) error {
	if err := checkNames(p, w); err != nil {
		return err
	}
	unlock, err := d.lock(p, w)
	if err != nil {
		return err
	}
	defer unlock()
	ws, ok := d.st.Get(p, w)
	if !ok {
		return notFound(p, w)
	}
	if ws.Host != "" {
		d.routes.Delete(ws.Host)
	}
	_ = d.rt.Stop(ctx, ws.Container, d.cfg.StopTimeout)
	if err := d.rt.Remove(ctx, ws.Container); err != nil && !errors.Is(err, runtime.ErrNotFound) {
		return internalErr(err)
	}
	entries, _ := os.ReadDir(d.releasesDir(p, w))
	for _, e := range entries {
		_ = d.rt.RemoveImage(ctx, ImageName(p, w, e.Name()))
	}
	_ = d.rt.RemoveImage(ctx, ImageName(p, w, "dev"))
	_ = d.rt.RemoveVolume(ctx, volumeName(p, w))
	if err := os.RemoveAll(d.workspaceDir(p, w)); err != nil {
		return internalErr(err)
	}
	if err := d.env.Delete(p, w); err != nil {
		return internalErr(err)
	}
	if err := d.st.Delete(p, w); err != nil {
		return internalErr(err)
	}
	return nil
}

type Status struct {
	Project   string    `json:"project"`
	Workspace string    `json:"workspace"`
	Mode      string    `json:"mode"`
	Release   string    `json:"release"`
	URL       string    `json:"url,omitempty"`
	HostPort  int       `json:"hostPort"`
	Running   bool      `json:"running"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (d *Deployer) List(ctx context.Context) []Status {
	out := []Status{}
	for _, e := range d.st.All() {
		info, err := d.rt.Inspect(ctx, e.WS.Container)
		out = append(out, Status{Project: e.Project, Workspace: e.Workspace, Mode: e.WS.Mode,
			Release: e.WS.Release, URL: urlFor(e.WS.Host), HostPort: e.WS.HostPort,
			Running: err == nil && info.Running, UpdatedAt: e.WS.UpdatedAt})
	}
	return out
}

func (d *Deployer) Logs(ctx context.Context, p, w string, tail int, follow bool, out io.Writer) error {
	if err := checkNames(p, w); err != nil {
		return err
	}
	ws, ok := d.st.Get(p, w)
	if !ok {
		return notFound(p, w)
	}
	return d.rt.Logs(ctx, ws.Container, tail, follow, out)
}

func envErr(err error) error {
	if errors.Is(err, validate.ErrInvalid) {
		return invalid(err)
	}
	return internalErr(err)
}

func (d *Deployer) EnvKeys(p, w string) ([]string, error) {
	if err := checkNames(p, w); err != nil {
		return nil, err
	}
	m, err := d.env.Get(p, w)
	if err != nil {
		return nil, internalErr(err)
	}
	return envstore.Keys(m), nil
}

func (d *Deployer) EnvSet(p, w string, kv map[string]string) error {
	if err := checkNames(p, w); err != nil {
		return err
	}
	if err := d.env.Set(p, w, kv); err != nil {
		return envErr(err)
	}
	return nil
}

func (d *Deployer) EnvUnset(p, w string, keys []string) error {
	if err := checkNames(p, w); err != nil {
		return err
	}
	if err := d.env.Unset(p, w, keys); err != nil {
		return envErr(err)
	}
	return nil
}
```

- [ ] **Step 5: Rodar e ver passar**

Run: `cd agent && go vet ./internal/... && go test -race ./internal/deploy/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add agent/internal/deploy
git commit -m "feat(agent): add dev mode, file sync, remove, list, logs and env"
```

---

### Task 14: `deploy` — reconciliação

**Files:**
- Create: `agent/internal/deploy/reconcile.go`
- Test: `agent/internal/deploy/reconcile_test.go`

**Interfaces:**
- Consumes: `deploySpec` (T12), `devSpec` (T13), `locks`, `st`, `rt`, `routes`.
- Produces: `func (d *Deployer) Reconcile(ctx context.Context) error` — para cada workspace do estado (pulando os que estão travados): recria o container se sumiu, inicia se parou, atualiza `HostPort` se mudou e registra a rota. Depois remove containers `sagan.managed=true` que não são o container atual do seu workspace, pulando workspaces travados. Devolve os erros agregados (`errors.Join`), sem parar no primeiro.

- [ ] **Step 1: Escrever o teste que falha**

`agent/internal/deploy/reconcile_test.go`:

```go
package deploy_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/borgim/sagansync/agent/internal/deploy"
	"github.com/borgim/sagansync/agent/internal/events"
	"github.com/borgim/sagansync/agent/internal/runtime"
	"github.com/borgim/sagansync/agent/internal/testutil"
	"github.com/borgim/sagansync/agent/internal/testutil/testdeploy"
)

func TestReconcileRestartsStoppedContainer(t *testing.T) {
	h := testdeploy.New(t)
	ctx := context.Background()
	_, _ = h.Deploy(t, testdeploy.Request("production"))
	ws, _ := h.State.Get("app", "production")
	if err := h.RT.Stop(ctx, ws.Container, 0); err != nil {
		t.Fatal(err)
	}
	h.Routes.Reset() // a freshly started daemon has an empty route table
	if err := h.D.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	c, ok := h.RT.Container(ws.Container)
	if !ok || !c.Running {
		t.Fatalf("container = %+v, %v", c, ok)
	}
	after, _ := h.State.Get("app", "production")
	if after.HostPort != c.HostPort {
		t.Errorf("state port %d, container port %d", after.HostPort, c.HostPort)
	}
	if got, want := h.Routes.Get("app.test"), fmt.Sprintf("127.0.0.1:%d", c.HostPort); got != want {
		t.Fatalf("route = %q, want %q", got, want)
	}
}

func TestReconcileRecreatesMissingContainer(t *testing.T) {
	h := testdeploy.New(t)
	ctx := context.Background()
	_, _ = h.Deploy(t, testdeploy.Request("production"))
	ws, _ := h.State.Get("app", "production")
	_ = h.RT.Remove(ctx, ws.Container)
	if err := h.D.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	c, ok := h.RT.Container(ws.Container)
	if !ok || !c.Running || c.Spec.Image != deploy.ImageName("app", "production", ws.Release) {
		t.Fatalf("container = %+v, %v", c, ok)
	}
}

func TestReconcileRecreatesDevContainer(t *testing.T) {
	h := testdeploy.New(t)
	ctx := context.Background()
	if _, err := startDev(t, h, devReq("feat-x")); err != nil {
		t.Fatal(err)
	}
	ws, _ := h.State.Get("app", "feat-x")
	_ = h.RT.Remove(ctx, ws.Container)
	if err := h.D.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	c, ok := h.RT.Container(ws.Container)
	if !ok || len(c.Spec.Binds) != 1 || strings.Join(c.Spec.Command, " ") != "npm run dev" {
		t.Fatalf("container = %+v, %v", c, ok)
	}
}

func orphan(t *testing.T, h *testdeploy.Harness, name, workspace, image string) {
	t.Helper()
	ctx := context.Background()
	spec := runtime.ContainerSpec{Name: name, Image: image, InternalPort: 3000, Labels: map[string]string{
		runtime.LabelManaged: "true", runtime.LabelProject: "app", runtime.LabelWorkspace: workspace}}
	if err := h.RT.Create(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := h.RT.Start(ctx, name); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileRemovesOrphans(t *testing.T) {
	h := testdeploy.New(t)
	_, _ = h.Deploy(t, testdeploy.Request("production"))
	ws, _ := h.State.Get("app", "production")
	orphan(t, h, "sagan_app_ghost_1", "ghost", deploy.ImageName("app", "production", ws.Release))
	orphan(t, h, "sagan_app_production_stale", "production", deploy.ImageName("app", "production", ws.Release))
	if err := h.D.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	names := h.RT.Names()
	if len(names) != 1 || names[0] != ws.Container {
		t.Fatalf("containers = %v, want only %s", names, ws.Container)
	}
}

func TestReconcileSkipsWorkspaceWithOperationInProgress(t *testing.T) {
	h := testdeploy.New(t)
	ctx := context.Background()
	if err := h.RT.Build(ctx, strings.NewReader(""), "localhost/seed:1", func(string) {}); err != nil {
		t.Fatal(err)
	}
	orphan(t, h, "sagan_app_production_inflight", "production", "localhost/seed:1")

	entered, unblock := make(chan struct{}), make(chan struct{})
	var once sync.Once
	h.RT.BuildHook = func(string) { once.Do(func() { close(entered); <-unblock }) }
	archive := testutil.App(t)
	errc := make(chan error, 1)
	go func() { errc <- h.D.Deploy(ctx, testdeploy.Request("production"), archive, &events.Recorder{}) }()
	<-entered

	if err := h.D.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.RT.Container("sagan_app_production_inflight"); !ok {
		t.Fatal("reconcile removed a container of a workspace with a deploy in progress")
	}
	close(unblock)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if err := h.D.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.RT.Container("sagan_app_production_inflight"); ok {
		t.Fatal("orphan survived once the workspace was free")
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd agent && go test ./internal/deploy/ -run Reconcile`
Expected: FAIL com `h.D.Reconcile undefined`

- [ ] **Step 3: Implementar**

`agent/internal/deploy/reconcile.go`:

```go
package deploy

import (
	"context"
	"errors"
	"fmt"

	"github.com/borgim/sagansync/agent/internal/runtime"
	"github.com/borgim/sagansync/agent/internal/state"
)

// Reconcile makes the runtime match the state: it runs at startup (after a
// reboot nothing is running) and periodically. Workspaces with an operation in
// progress are skipped.
func (d *Deployer) Reconcile(ctx context.Context) error {
	var errs []error
	for _, e := range d.st.All() {
		unlock, ok := d.locks.TryLock(e.Project, e.Workspace)
		if !ok {
			continue
		}
		if err := d.reconcileOne(ctx, e); err != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %w", e.Project, e.Workspace, err))
		}
		unlock()
	}
	list, err := d.rt.List(ctx)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, c := range list {
		p, w := c.Labels[runtime.LabelProject], c.Labels[runtime.LabelWorkspace]
		unlock, ok := d.locks.TryLock(p, w)
		if !ok {
			continue
		}
		// Re-read the state under the lock: a deploy may have finished after
		// the list was taken and made this container the current one.
		if ws, ok := d.st.Get(p, w); !ok || ws.Container != c.Name {
			_ = d.rt.Stop(ctx, c.Name, d.cfg.StopTimeout)
			if err := d.rt.Remove(ctx, c.Name); err != nil && !errors.Is(err, runtime.ErrNotFound) {
				errs = append(errs, err)
			}
		}
		unlock()
	}
	return errors.Join(errs...)
}

func (d *Deployer) reconcileOne(ctx context.Context, e state.Entry) error {
	ws := e.WS
	info, err := d.rt.Inspect(ctx, ws.Container)
	switch {
	case errors.Is(err, runtime.ErrNotFound):
		var spec runtime.ContainerSpec
		if ws.Mode == state.ModeDev {
			spec, err = d.devSpec(e.Project, e.Workspace, ws.Release, ws.InternalPort, ws.Command)
		} else {
			spec, err = d.deploySpec(e.Project, e.Workspace, ws.Release, ws.InternalPort)
		}
		if err != nil {
			return err
		}
		if err := d.rt.Create(ctx, spec); err != nil {
			return fmt.Errorf("recreate container: %w", err)
		}
	case err != nil:
		return err
	}
	if !info.Running {
		if err := d.rt.Start(ctx, ws.Container); err != nil {
			return fmt.Errorf("start container: %w", err)
		}
		if info, err = d.rt.Inspect(ctx, ws.Container); err != nil {
			return err
		}
	}
	if info.HostPort != 0 && info.HostPort != ws.HostPort {
		ws.HostPort = info.HostPort
		if err := d.st.Put(e.Project, e.Workspace, ws); err != nil {
			return err
		}
	}
	if ws.Host != "" && ws.HostPort != 0 {
		d.routes.Set(ws.Host, fmt.Sprintf("127.0.0.1:%d", ws.HostPort))
	}
	return nil
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd agent && go test -race ./internal/deploy/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add agent/internal/deploy
git commit -m "feat(agent): reconcile containers and routes with the saved state"
```

---

### Task 15: `api` — servidor HTTP no socket Unix e cliente

**Files:**
- Create: `agent/internal/api/server.go`
- Create: `agent/internal/api/client.go`
- Test: `agent/internal/api/server_test.go`

**Interfaces:**
- Consumes: `deploy.*` (T12–14), `events.*` (T3), `testdeploy` (T12, nos testes).
- Produces:
  - `type VersionInfo struct { Version string; Protocol int }` (JSON `version`, `protocol`)
  - `func NewServer(d *deploy.Deployer, version string) *Server`, `(*Server).Handler() http.Handler`
  - `func Listen(socket string) (net.Listener, error)` — remove socket antigo, escuta, `chmod 0600`.
  - `func NewClient(socket string) *Client`, `(*Client).Do(ctx context.Context, method, path string, q url.Values, body io.Reader) (*http.Response, error)`

**Rotas:**

| Rota | Entrada | Saída |
| --- | --- | --- |
| `GET /v1/version` | — | `VersionInfo` |
| `POST /v1/deploy` | query `project, workspace, domain, previewDomain, port, healthPath, healthTimeout` (segundos, padrão 60), `sha`; corpo tar.gz | eventos NDJSON, sempre HTTP 200; falha = evento `error` |
| `POST /v1/dev` | a query do deploy + `command` (array JSON), `build`, `force`; corpo tar.gz | eventos NDJSON |
| `POST /v1/remove` | `project, workspace` | eventos NDJSON (`done` ou `error`) |
| `GET /v1/list` | — | `[]deploy.Status` |
| `GET /v1/logs` | `project, workspace, tail` (padrão 100), `follow` | texto em stream |
| `PUT /v1/files` / `DELETE /v1/files` | `project, workspace, path`; corpo = conteúdo (PUT) | 204 |
| `GET /v1/env` | `project, workspace` | `{"keys": [...]}` |
| `POST /v1/env` | `project, workspace`; corpo = objeto JSON `{"KEY":"value"}` (até 1 MB) | 204 |
| `DELETE /v1/env` | `project, workspace, key` (repetível) | 204 |

Erros fora de stream: corpo `{"code","message"}` com status `invalid`→400, `production_locked`→403, `not_found`→404, `busy`/`host_conflict`→409, resto→500.

**Detalhe importante:** nas rotas de stream, a resposta começa antes do corpo terminar de chegar. Em HTTP/1.1 o Go pode parar de ler o corpo depois que a resposta começa, a menos que o handler chame `http.NewResponseController(w).EnableFullDuplex()` (Go 1.21+). O `stream` faz isso. Ele também usa `context.WithoutCancel`, para que a queda do SSH não interrompa um deploy no meio (o deploy termina ou é revertido sozinho).

- [ ] **Step 1: Escrever o teste que falha**

`agent/internal/api/server_test.go`:

```go
package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/borgim/sagansync/agent/internal/api"
	"github.com/borgim/sagansync/agent/internal/deploy"
	"github.com/borgim/sagansync/agent/internal/events"
	"github.com/borgim/sagansync/agent/internal/testutil"
	"github.com/borgim/sagansync/agent/internal/testutil/testdeploy"
)

func newServer(t *testing.T) (*httptest.Server, *testdeploy.Harness) {
	t.Helper()
	h := testdeploy.New(t)
	srv := httptest.NewServer(api.NewServer(h.D, "1.2.3").Handler())
	t.Cleanup(srv.Close)
	return srv, h
}

func deployQuery() url.Values {
	return url.Values{"project": {"app"}, "workspace": {"production"}, "domain": {"app.test"}, "port": {"3000"},
		"healthPath": {"/health"}, "healthTimeout": {"5"}, "sha": {"abc1234"}}
}

func do(t *testing.T, srv *httptest.Server, method, path string, q url.Values, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path+"?"+q.Encode(), body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func readEvents(t *testing.T, r io.Reader) []events.Event {
	t.Helper()
	var out []events.Event
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		var e events.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("bad event line %q: %v", sc.Text(), err)
		}
		out = append(out, e)
	}
	return out
}

func TestVersion(t *testing.T) {
	srv, _ := newServer(t)
	resp := do(t, srv, "GET", "/v1/version", nil, nil)
	var v api.VersionInfo
	json.NewDecoder(resp.Body).Decode(&v)
	if v.Version != "1.2.3" || v.Protocol != events.Protocol {
		t.Fatalf("version = %+v", v)
	}
}

func TestDeployStreamsEventsAndDone(t *testing.T) {
	srv, h := newServer(t)
	resp := do(t, srv, "POST", "/v1/deploy", deployQuery(), testutil.App(t))
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("status %d, content-type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	evs := readEvents(t, resp.Body)
	last := evs[len(evs)-1]
	if last.Type != "done" || last.URL != "https://app.test" {
		t.Fatalf("last = %+v", last)
	}
	if _, ok := h.State.Get("app", "production"); !ok {
		t.Fatal("deploy not recorded")
	}
}

func TestDeployErrorsAreEvents(t *testing.T) {
	srv, _ := newServer(t)
	q := deployQuery()
	q.Set("port", "abc")
	evs := readEvents(t, do(t, srv, "POST", "/v1/deploy", q, testutil.App(t)).Body)
	if last := evs[len(evs)-1]; last.Type != "error" || last.Code != deploy.CodeInvalid {
		t.Fatalf("last = %+v", last)
	}
	q = deployQuery()
	q.Set("project", "Bad!")
	evs = readEvents(t, do(t, srv, "POST", "/v1/deploy", q, testutil.App(t)).Body)
	if last := evs[len(evs)-1]; last.Code != deploy.CodeInvalid || events.ExitCode(last) != 2 {
		t.Fatalf("last = %+v", last)
	}
}

func TestDevRequiresJSONCommand(t *testing.T) {
	srv, _ := newServer(t)
	q := deployQuery()
	q.Set("workspace", "feat-x")
	q.Set("command", "npm run dev")
	evs := readEvents(t, do(t, srv, "POST", "/v1/dev", q, testutil.App(t)).Body)
	if last := evs[len(evs)-1]; last.Code != deploy.CodeInvalid {
		t.Fatalf("last = %+v", last)
	}
	q.Set("command", `["npm","run","dev"]`)
	evs = readEvents(t, do(t, srv, "POST", "/v1/dev", q, testutil.App(t)).Body)
	if last := evs[len(evs)-1]; last.Type != "done" {
		t.Fatalf("last = %+v", last)
	}
}

func TestListAndLogsAndRemove(t *testing.T) {
	srv, _ := newServer(t)
	readEvents(t, do(t, srv, "POST", "/v1/deploy", deployQuery(), testutil.App(t)).Body)

	var list []deploy.Status
	json.NewDecoder(do(t, srv, "GET", "/v1/list", nil, nil).Body).Decode(&list)
	if len(list) != 1 || list[0].Workspace != "production" || !list[0].Running {
		t.Fatalf("list = %+v", list)
	}
	ws := url.Values{"project": {"app"}, "workspace": {"production"}}
	b, _ := io.ReadAll(do(t, srv, "GET", "/v1/logs", ws, nil).Body)
	if !strings.HasPrefix(string(b), "log from ") {
		t.Fatalf("logs = %q", b)
	}
	evs := readEvents(t, do(t, srv, "POST", "/v1/remove", ws, nil).Body)
	if last := evs[len(evs)-1]; last.Type != "done" {
		t.Fatalf("remove = %+v", last)
	}
	resp := do(t, srv, "GET", "/v1/logs", ws, nil)
	var e struct{ Code, Message string }
	json.NewDecoder(resp.Body).Decode(&e)
	if resp.StatusCode != 404 || e.Code != deploy.CodeNotFound {
		t.Fatalf("logs after remove: %d %+v", resp.StatusCode, e)
	}
}

func TestFilesRequireDevMode(t *testing.T) {
	srv, _ := newServer(t)
	q := url.Values{"project": {"app"}, "workspace": {"production"}, "path": {"a.js"}}
	resp := do(t, srv, "PUT", "/v1/files", q, strings.NewReader("x"))
	var e struct{ Code string }
	json.NewDecoder(resp.Body).Decode(&e)
	if resp.StatusCode != 404 || e.Code != deploy.CodeNotFound {
		t.Fatalf("got %d %+v", resp.StatusCode, e)
	}
}

func TestEnvRoundTrip(t *testing.T) {
	srv, _ := newServer(t)
	ws := url.Values{"project": {"app"}, "workspace": {"production"}}
	if resp := do(t, srv, "POST", "/v1/env", ws, strings.NewReader(`{"B":"2","A":"line1\nline2"}`)); resp.StatusCode != 204 {
		t.Fatalf("set: %d", resp.StatusCode)
	}
	var keys struct{ Keys []string }
	json.NewDecoder(do(t, srv, "GET", "/v1/env", ws, nil).Body).Decode(&keys)
	if strings.Join(keys.Keys, ",") != "A,B" {
		t.Fatalf("keys = %v", keys.Keys)
	}
	unset := url.Values{"project": {"app"}, "workspace": {"production"}, "key": {"A"}}
	if resp := do(t, srv, "DELETE", "/v1/env", unset, nil); resp.StatusCode != 204 {
		t.Fatalf("unset: %d", resp.StatusCode)
	}
	if resp := do(t, srv, "POST", "/v1/env", ws, strings.NewReader(`not json`)); resp.StatusCode != 400 {
		t.Fatalf("bad body: %d", resp.StatusCode)
	}
	if resp := do(t, srv, "POST", "/v1/env", ws, strings.NewReader(`{"1BAD":"x"}`)); resp.StatusCode != 400 {
		t.Fatalf("bad key: %d", resp.StatusCode)
	}
}

func TestListenOverUnixSocket(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "sgd") // short path: macOS limits socket paths to 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s.sock")
	if err := os.WriteFile(sock, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := api.Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	fi, _ := os.Stat(sock)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("socket mode = %v", fi.Mode().Perm())
	}
	h := testdeploy.New(t)
	srv := &http.Server{Handler: api.NewServer(h.D, "1.2.3").Handler()}
	go srv.Serve(l)
	defer srv.Close()
	resp, err := api.NewClient(sock).Do(context.Background(), "GET", "/v1/version", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd agent && go test ./internal/api/`
Expected: FAIL com `undefined: api.NewServer`

- [ ] **Step 3: Implementar o servidor**

`agent/internal/api/server.go`:

```go
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
```

- [ ] **Step 4: Implementar o cliente**

`agent/internal/api/client.go`:

```go
package api

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
)

// Client talks to the daemon's Unix socket.
type Client struct{ hc *http.Client }

func NewClient(socket string) *Client {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}}
	return &Client{hc: &http.Client{Transport: tr}}
}

func (c *Client) Do(ctx context.Context, method, path string, q url.Values, body io.Reader) (*http.Response, error) {
	u := "http://sagand" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	return c.hc.Do(req)
}
```

- [ ] **Step 5: Rodar e ver passar**

Run: `cd agent && go vet ./internal/api/ && go test -race ./internal/api/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add agent/internal/api
git commit -m "feat(agent): serve the deployer over a Unix socket API"
```

---

### Task 16: `gateway` — forced command do SSH

**Files:**
- Create: `agent/internal/gateway/gateway.go`
- Test: `agent/internal/gateway/gateway_test.go`

**Interfaces:**
- Produces:
  - `var ErrDenied error`
  - `func Split(s string) ([]string, error)` — divide como um shell POSIX simples: espaços separam; `'...'` é literal; `"..."` aceita `\"` e `\\`; `\` fora de aspas escapa o próximo caractere; aspas não fechadas são erro. Nenhum outro caractere é especial (`;`, `$()`, `|` viram texto).
  - `func Parse(original string) ([]string, error)` — aplica `Split`, remove um `sagand` ou `/usr/local/bin/sagand` inicial e aceita só `version`, `host`, `deploy`, `list`, `logs`, `remove`, `dev`, `put`, `rm`, `env`. Comando vazio (tentativa de shell), `daemon` e `gateway` são negados. Erros envolvem `ErrDenied`.
- O `Split` é compatível com o `shQuote` da CLI (`'it'"'"'s'` → `it's`).

- [ ] **Step 1: Escrever o teste que falha**

`agent/internal/gateway/gateway_test.go`:

```go
package gateway

import (
	"errors"
	"slices"
	"testing"
)

func TestSplit(t *testing.T) {
	cases := map[string][]string{
		`a b`:                       {"a", "b"},
		`  a   b  `:                 {"a", "b"},
		`'a b' c`:                   {"a b", "c"},
		`"a \"b\" \\c"`:             {`a "b" \c`},
		`'it'"'"'s'`:                {"it's"},
		`''`:                        {""},
		`a\ b`:                      {"a b"},
		`x;rm -rf /`:                {"x;rm", "-rf", "/"},
		`$(id) | cat`:               {"$(id)", "|", "cat"},
		`--command '["npm","run"]'`: {"--command", `["npm","run"]`},
	}
	for in, want := range cases {
		got, err := Split(in)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("Split(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{`'open`, `"open`, `trailing\`} {
		if _, err := Split(in); err == nil {
			t.Errorf("Split(%q) accepted malformed input", in)
		}
	}
}

func TestParse(t *testing.T) {
	allowed := map[string][]string{
		"sagand deploy --project app":        {"deploy", "--project", "app"},
		"/usr/local/bin/sagand list":         {"list"},
		"version":                            {"version"},
		"sagand put app feat-x 'src/a b.ts'": {"put", "app", "feat-x", "src/a b.ts"},
	}
	for in, want := range allowed {
		got, err := Parse(in)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	denied := []string{"", "   ", "sagand", "bash", "bash -i", "sh -c id", "sagand daemon", "sagand gateway",
		"scp -t /tmp", "rsync --server . /tmp", "internal-sftp", "'unterminated"}
	for _, in := range denied {
		if _, err := Parse(in); !errors.Is(err, ErrDenied) {
			t.Errorf("Parse(%q) = %v, want ErrDenied", in, err)
		}
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd agent && go test ./internal/gateway/`
Expected: FAIL com `undefined: Split`

- [ ] **Step 3: Implementar**

`agent/internal/gateway/gateway.go`:

```go
// Package gateway turns the command a deploy key asked for (SSH's
// SSH_ORIGINAL_COMMAND) into an allowed sagand subcommand. The key's
// authorized_keys entry forces this gateway, so this list is everything the
// key can do: no shell, no scp, no tunnels.
package gateway

import (
	"errors"
	"fmt"
	"strings"
)

var ErrDenied = errors.New("command not allowed")

var allowed = map[string]bool{
	"version": true, "host": true, "deploy": true, "list": true, "logs": true,
	"remove": true, "dev": true, "put": true, "rm": true, "env": true,
}

func Parse(original string) ([]string, error) {
	args, err := Split(original)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDenied, err)
	}
	if len(args) > 0 && (args[0] == "sagand" || args[0] == "/usr/local/bin/sagand") {
		args = args[1:]
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("%w: interactive sessions are disabled for this key", ErrDenied)
	}
	if !allowed[args[0]] {
		return nil, fmt.Errorf("%w: %q", ErrDenied, args[0])
	}
	return args, nil
}

// Split breaks s into words like a minimal POSIX shell, without expansions.
func Split(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		switch ch := s[i]; ch {
		case ' ', '\t', '\n':
			if inWord {
				args = append(args, cur.String())
				cur.Reset()
				inWord = false
			}
		case '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return nil, errors.New("unterminated single quote")
			}
			cur.WriteString(s[i+1 : i+1+end])
			i += end + 1
			inWord = true
		case '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\') {
					i++
				}
				cur.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, errors.New("unterminated double quote")
			}
			inWord = true
		case '\\':
			if i+1 >= len(s) {
				return nil, errors.New("trailing backslash")
			}
			i++
			cur.WriteByte(s[i])
			inWord = true
		default:
			cur.WriteByte(ch)
			inWord = true
		}
	}
	if inWord {
		args = append(args, cur.String())
	}
	return args, nil
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd agent && go test -race ./internal/gateway/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add agent/internal/gateway
git commit -m "feat(agent): restrict deploy keys to an allowlist of sagand commands"
```

---

### Task 17: `cmd/sagand` — cliente, gateway e daemon

**Files:**
- Create: `agent/cmd/sagand/main.go`
- Create: `agent/cmd/sagand/client.go`
- Create: `agent/cmd/sagand/daemon.go`
- Test: `agent/cmd/sagand/main_test.go`

**Interfaces:**
- Consumes: todos os pacotes anteriores.
- Produces (o contrato que a CLI do plano 2 usa):
  - `sagand daemon [-config /var/lib/sagand/config.json]`
  - `sagand gateway` — lê `SSH_ORIGINAL_COMMAND`; negado → stderr + saída 126.
  - `sagand version` → `{"version":"...","protocol":1}`
  - `sagand host --project p --workspace w [--domain d] [--preview-domain pd]` → `{"host":"..."}` (cálculo local, sem daemon)
  - `sagand deploy --project p --workspace w --port N [--domain d] [--preview-domain pd] [--health-path /h] [--health-timeout S] [--sha X]` com tar.gz no stdin → eventos NDJSON no stdout
  - `sagand dev <flags do deploy> --command '<array JSON>' [--build] [--force]` com tar.gz no stdin → eventos
  - `sagand remove --project p --workspace w` → eventos
  - `sagand list` → JSON; `sagand logs --project p --workspace w [--tail N] [-f]` → texto
  - `sagand put <p> <w> <caminho>` (conteúdo no stdin) e `sagand rm <p> <w> <caminho>` → sem saída em caso de sucesso
  - `sagand env list|set|unset --project p --workspace w [KEY...]` — `set` lê um objeto JSON do stdin
  - Qualquer falha é escrita no stdout como um evento `error` (`{"v":1,"type":"error","code":...}`) e define o código de saída via `events.ExitCode`. Daemon inacessível → código `daemon_unavailable`, saída 1.
  - Variável `version` preenchida via `-ldflags "-X main.version=..."` (padrão `dev`).

- [ ] **Step 1: Escrever o teste que falha**

`agent/cmd/sagand/main_test.go`:

```go
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/borgim/sagansync/agent/internal/api"
	"github.com/borgim/sagansync/agent/internal/events"
	"github.com/borgim/sagansync/agent/internal/testutil"
	"github.com/borgim/sagansync/agent/internal/testutil/testdeploy"
)

// startDaemon serves a fake-backed API on a Unix socket and points
// SAGAND_SOCKET at it.
func startDaemon(t *testing.T) *testdeploy.Harness {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "sgd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	h := testdeploy.New(t)
	l, err := api.Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: api.NewServer(h.D, "test").Handler()}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	t.Setenv("SAGAND_SOCKET", sock)
	return h
}

func lastEvent(t *testing.T, out *bytes.Buffer) events.Event {
	t.Helper()
	var last events.Event
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		if err := json.Unmarshal(sc.Bytes(), &last); err != nil {
			t.Fatalf("stdout line %q is not an event: %v", sc.Text(), err)
		}
	}
	return last
}

func TestGatewayDeniesShellAndDaemon(t *testing.T) {
	for _, cmd := range []string{"", "bash -i", "sagand daemon", "scp -t /tmp"} {
		t.Setenv("SSH_ORIGINAL_COMMAND", cmd)
		var out, errb bytes.Buffer
		if code := run([]string{"gateway"}, strings.NewReader(""), &out, &errb); code != 126 {
			t.Errorf("%q: exit %d, want 126", cmd, code)
		}
		if !strings.Contains(errb.String(), "not allowed") {
			t.Errorf("%q: stderr = %q", cmd, errb.String())
		}
	}
}

func TestVersionAndHost(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"version"}, nil, &out, &out); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var v api.VersionInfo
	json.Unmarshal(out.Bytes(), &v)
	if v.Protocol != events.Protocol || v.Version == "" {
		t.Fatalf("version = %+v", v)
	}
	out.Reset()
	code := run([]string{"host", "--project", "barbervip", "--workspace", "feat-login", "--domain", "api.pedroborgim.com.br",
		"--preview-domain", "pedroborgim.com.br"}, nil, &out, &out)
	var h struct{ Host string }
	json.Unmarshal(out.Bytes(), &h)
	if code != 0 || h.Host != "feat-login-barbervip.pedroborgim.com.br" {
		t.Fatalf("host = %q (exit %d)", h.Host, code)
	}
}

func TestDeployThroughGateway(t *testing.T) {
	h := startDaemon(t)
	t.Setenv("SSH_ORIGINAL_COMMAND", "sagand deploy --project app --workspace production --domain app.test --port 3000 --health-path /health --health-timeout 5 --sha abc1234")
	var out, errb bytes.Buffer
	code := run([]string{"gateway"}, testutil.App(t), &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
	if last := lastEvent(t, &out); last.Type != "done" || last.URL != "https://app.test" {
		t.Fatalf("last = %+v", last)
	}
	if _, ok := h.State.Get("app", "production"); !ok {
		t.Fatal("deploy not recorded")
	}

	t.Setenv("SSH_ORIGINAL_COMMAND", "sagand list")
	out.Reset()
	if code := run([]string{"gateway"}, nil, &out, &errb); code != 0 || !strings.Contains(out.String(), `"workspace":"production"`) {
		t.Fatalf("list: exit %d, %q", code, out.String())
	}
}

func TestValidationErrorExitsWith2(t *testing.T) {
	startDaemon(t)
	var out bytes.Buffer
	code := run([]string{"deploy", "--project", "Bad!", "--workspace", "production", "--port", "3000"}, testutil.App(t), &out, &out)
	if code != 2 || lastEvent(t, &out).Code != "invalid" {
		t.Fatalf("exit %d, out %q", code, out.String())
	}
	out.Reset()
	if code := run([]string{"deploy", "--no-such-flag"}, nil, &out, &out); code != 2 {
		t.Fatalf("unknown flag: exit %d", code)
	}
}

func TestEnvSetFromStdin(t *testing.T) {
	startDaemon(t)
	var out bytes.Buffer
	if code := run([]string{"env", "set", "--project", "app", "--workspace", "production"}, strings.NewReader(`{"A":"1"}`), &out, &out); code != 0 {
		t.Fatalf("set: exit %d, %q", code, out.String())
	}
	out.Reset()
	if code := run([]string{"env", "list", "--project", "app", "--workspace", "production"}, nil, &out, &out); code != 0 || !strings.Contains(out.String(), `"A"`) {
		t.Fatalf("list: exit %d, %q", code, out.String())
	}
}

func TestDaemonUnavailable(t *testing.T) {
	t.Setenv("SAGAND_SOCKET", "/tmp/sagand-does-not-exist.sock")
	var out bytes.Buffer
	if code := run([]string{"list"}, nil, &out, &out); code != 1 || lastEvent(t, &out).Code != "daemon_unavailable" {
		t.Fatalf("exit %d, out %q", code, out.String())
	}
}

func TestLoadDaemonConfig(t *testing.T) {
	cfg, err := loadDaemonConfig(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil || cfg.Socket != "/run/sagand/sagand.sock" || cfg.HTTPSAddr != ":443" || cfg.SrvDir != "/srv/sagan" {
		t.Fatalf("defaults = %+v, %v", cfg, err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"acmeCA":"https://pebble:14000/dir","acmeEmail":"ops@example.com"}`), 0o600)
	cfg, err = loadDaemonConfig(path)
	if err != nil || cfg.AcmeCA != "https://pebble:14000/dir" || cfg.AcmeEmail != "ops@example.com" || cfg.HTTPAddr != ":80" {
		t.Fatalf("overlay = %+v, %v", cfg, err)
	}
	os.WriteFile(path, []byte(`{broken`), 0o600)
	if _, err := loadDaemonConfig(path); err == nil {
		t.Fatal("broken config accepted")
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd agent && go test ./cmd/sagand/`
Expected: FAIL com `undefined: run`

- [ ] **Step 3: Implementar o `main.go`**

`agent/cmd/sagand/main.go`:

```go
// Command sagand is the SaganSync agent: a daemon on the VPS and the client
// that deploy keys run through SSH.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/borgim/sagansync/agent/internal/gateway"
)

var version = "dev" // set with -ldflags "-X main.version=v0.1.0"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: sagand daemon | sagand gateway | sagand <command> [flags]")
		return 2
	}
	switch args[0] {
	case "daemon":
		return runDaemon(args[1:], stderr)
	case "gateway":
		cmd, err := gateway.Parse(os.Getenv("SSH_ORIGINAL_COMMAND"))
		if err != nil {
			fmt.Fprintf(stderr, "sagand: %v\n", err)
			return 126
		}
		return runClient(cmd, stdin, stdout)
	}
	return runClient(args, stdin, stdout)
}
```

- [ ] **Step 4: Implementar o cliente**

`agent/cmd/sagand/client.go`:

```go
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"github.com/borgim/sagansync/agent/internal/api"
	"github.com/borgim/sagansync/agent/internal/deploy"
	"github.com/borgim/sagansync/agent/internal/domains"
	"github.com/borgim/sagansync/agent/internal/events"
)

const defaultSocket = "/run/sagand/sagand.sock"

type requestFlags struct {
	project, workspace, domain, previewDomain, healthPath, sha string
	port, healthTimeout                                        int
}

func (f *requestFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.project, "project", "", "project name")
	fs.StringVar(&f.workspace, "workspace", "", "workspace name")
	fs.StringVar(&f.domain, "domain", "", "production domain")
	fs.StringVar(&f.previewDomain, "preview-domain", "", "base domain for branch workspaces")
	fs.StringVar(&f.healthPath, "health-path", "", "HTTP health check path")
	fs.StringVar(&f.sha, "sha", "", "git commit sha")
	fs.IntVar(&f.port, "port", 0, "port the app listens on inside the container")
	fs.IntVar(&f.healthTimeout, "health-timeout", 60, "health check timeout in seconds")
}

func (f *requestFlags) query() url.Values {
	return url.Values{"project": {f.project}, "workspace": {f.workspace}, "domain": {f.domain},
		"previewDomain": {f.previewDomain}, "healthPath": {f.healthPath}, "sha": {f.sha},
		"port": {strconv.Itoa(f.port)}, "healthTimeout": {strconv.Itoa(f.healthTimeout)}}
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func workspaceFlags(fs *flag.FlagSet) (*string, *string) {
	return fs.String("project", "", "project name"), fs.String("workspace", "", "workspace name")
}

// fail writes an error event to stdout and returns its exit code.
func fail(stdout io.Writer, code, msg string) int {
	e := events.Fail(code, msg, nil)
	events.NewWriter(stdout).Emit(e)
	return events.ExitCode(e)
}

func runClient(args []string, stdin io.Reader, stdout io.Writer) int {
	sock := os.Getenv("SAGAND_SOCKET")
	if sock == "" {
		sock = defaultSocket
	}
	c := api.NewClient(sock)
	ctx := context.Background()
	cmd, rest := args[0], args[1:]
	fs := newFlagSet(cmd)

	switch cmd {
	case "version":
		_ = json.NewEncoder(stdout).Encode(api.VersionInfo{Version: version, Protocol: events.Protocol})
		return 0

	case "host":
		var f requestFlags
		f.register(fs)
		if err := fs.Parse(rest); err != nil {
			return fail(stdout, deploy.CodeInvalid, err.Error())
		}
		_ = json.NewEncoder(stdout).Encode(map[string]string{"host": domains.Host(f.project, f.workspace, f.domain, f.previewDomain)})
		return 0

	case "deploy":
		var f requestFlags
		f.register(fs)
		if err := fs.Parse(rest); err != nil {
			return fail(stdout, deploy.CodeInvalid, err.Error())
		}
		return stream(ctx, c, http.MethodPost, "/v1/deploy", f.query(), stdin, stdout)

	case "dev":
		var f requestFlags
		f.register(fs)
		command := fs.String("command", "", "dev command as a JSON array")
		build := fs.Bool("build", false, "rebuild the dev image")
		force := fs.Bool("force", false, "allow dev mode on production")
		if err := fs.Parse(rest); err != nil {
			return fail(stdout, deploy.CodeInvalid, err.Error())
		}
		q := f.query()
		q.Set("command", *command)
		q.Set("build", strconv.FormatBool(*build))
		q.Set("force", strconv.FormatBool(*force))
		return stream(ctx, c, http.MethodPost, "/v1/dev", q, stdin, stdout)

	case "remove":
		p, w := workspaceFlags(fs)
		if err := fs.Parse(rest); err != nil {
			return fail(stdout, deploy.CodeInvalid, err.Error())
		}
		return stream(ctx, c, http.MethodPost, "/v1/remove", url.Values{"project": {*p}, "workspace": {*w}}, nil, stdout)

	case "list":
		return simple(ctx, c, http.MethodGet, "/v1/list", nil, nil, stdout)

	case "logs":
		p, w := workspaceFlags(fs)
		tail := fs.Int("tail", 100, "lines from the end")
		follow := fs.Bool("f", false, "follow")
		if err := fs.Parse(rest); err != nil {
			return fail(stdout, deploy.CodeInvalid, err.Error())
		}
		q := url.Values{"project": {*p}, "workspace": {*w}, "tail": {strconv.Itoa(*tail)}, "follow": {strconv.FormatBool(*follow)}}
		return simple(ctx, c, http.MethodGet, "/v1/logs", q, nil, stdout)

	case "put", "rm":
		if len(rest) != 3 {
			return fail(stdout, deploy.CodeInvalid, "usage: sagand "+cmd+" <project> <workspace> <path>")
		}
		q := url.Values{"project": {rest[0]}, "workspace": {rest[1]}, "path": {rest[2]}}
		if cmd == "put" {
			return simple(ctx, c, http.MethodPut, "/v1/files", q, stdin, stdout)
		}
		return simple(ctx, c, http.MethodDelete, "/v1/files", q, nil, stdout)

	case "env":
		return envCmd(ctx, c, rest, stdin, stdout)
	}
	return fail(stdout, deploy.CodeInvalid, "unknown command "+cmd)
}

func envCmd(ctx context.Context, c *api.Client, args []string, stdin io.Reader, stdout io.Writer) int {
	if len(args) == 0 {
		return fail(stdout, deploy.CodeInvalid, "usage: sagand env list|set|unset --project p --workspace w [KEY...]")
	}
	sub := args[0]
	fs := newFlagSet("env")
	p, w := workspaceFlags(fs)
	if err := fs.Parse(args[1:]); err != nil {
		return fail(stdout, deploy.CodeInvalid, err.Error())
	}
	q := url.Values{"project": {*p}, "workspace": {*w}}
	switch sub {
	case "list":
		return simple(ctx, c, http.MethodGet, "/v1/env", q, nil, stdout)
	case "set":
		return simple(ctx, c, http.MethodPost, "/v1/env", q, stdin, stdout)
	case "unset":
		q["key"] = fs.Args()
		return simple(ctx, c, http.MethodDelete, "/v1/env", q, nil, stdout)
	}
	return fail(stdout, deploy.CodeInvalid, "unknown env subcommand "+sub)
}

func unavailable(stdout io.Writer, err error) int {
	return fail(stdout, "daemon_unavailable",
		fmt.Sprintf("cannot reach the sagand daemon (%v); an admin can check it with: systemctl status sagand", err))
}

func errorResponse(resp *http.Response, stdout io.Writer) int {
	var e struct{ Code, Message string }
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&e)
	if e.Code == "" {
		e.Code, e.Message = deploy.CodeInternal, "daemon returned HTTP "+strconv.Itoa(resp.StatusCode)
	}
	return fail(stdout, e.Code, e.Message)
}

// stream relays NDJSON events and exits according to the last terminal event.
func stream(ctx context.Context, c *api.Client, method, path string, q url.Values, body io.Reader, stdout io.Writer) int {
	resp, err := c.Do(ctx, method, path, q, body)
	if err != nil {
		return unavailable(stdout, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errorResponse(resp, stdout)
	}
	code := 1 // a stream that ends without done/error is a failure
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		stdout.Write(line)
		stdout.Write([]byte("\n"))
		var e events.Event
		if json.Unmarshal(line, &e) == nil && (e.Type == "done" || e.Type == "error") {
			code = events.ExitCode(e)
		}
	}
	return code
}

// simple copies a successful response body to stdout.
func simple(ctx context.Context, c *api.Client, method, path string, q url.Values, body io.Reader, stdout io.Writer) int {
	resp, err := c.Do(ctx, method, path, q, body)
	if err != nil {
		return unavailable(stdout, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return errorResponse(resp, stdout)
	}
	if _, err := io.Copy(stdout, resp.Body); err != nil {
		return 1
	}
	return 0
}
```

- [ ] **Step 5: Implementar o daemon**

`agent/cmd/sagand/daemon.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/borgim/sagansync/agent/internal/api"
	"github.com/borgim/sagansync/agent/internal/deploy"
	"github.com/borgim/sagansync/agent/internal/envstore"
	"github.com/borgim/sagansync/agent/internal/podman"
	"github.com/borgim/sagansync/agent/internal/proxy"
	"github.com/borgim/sagansync/agent/internal/state"
)

type daemonConfig struct {
	Socket       string `json:"socket"`
	SrvDir       string `json:"srvDir"`
	StateDir     string `json:"stateDir"`
	PodmanSocket string `json:"podmanSocket"`
	HTTPAddr     string `json:"httpAddr"`
	HTTPSAddr    string `json:"httpsAddr"`
	AcmeEmail    string `json:"acmeEmail"`
	AcmeCA       string `json:"acmeCA"`
	AcmeRootCA   string `json:"acmeRootCA"`
}

// loadDaemonConfig returns the defaults overlaid with the JSON file, if any.
func loadDaemonConfig(path string) (daemonConfig, error) {
	cfg := daemonConfig{
		Socket: defaultSocket, SrvDir: "/srv/sagan", StateDir: "/var/lib/sagand",
		PodmanSocket: fmt.Sprintf("/run/user/%d/podman/podman.sock", os.Getuid()),
		HTTPAddr:     ":80", HTTPSAddr: ":443",
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func runDaemon(args []string, stderr io.Writer) int {
	fset := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fset.SetOutput(stderr)
	cfgPath := fset.String("config", "/var/lib/sagand/config.json", "config file")
	if err := fset.Parse(args); err != nil {
		return 2
	}
	logger := log.New(stderr, "sagand: ", log.LstdFlags)
	cfg, err := loadDaemonConfig(*cfgPath)
	if err != nil {
		logger.Print(err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := serve(ctx, cfg, logger); err != nil {
		logger.Print(err)
		return 1
	}
	return 0
}

func serve(ctx context.Context, cfg daemonConfig, logger *log.Logger) error {
	st, err := state.Open(filepath.Join(cfg.StateDir, "state.json"))
	if err != nil {
		return err
	}
	router := proxy.NewRouter()
	tlsm, err := proxy.NewTLS(proxy.TLSOptions{StorageDir: filepath.Join(cfg.StateDir, "certs"),
		Email: cfg.AcmeEmail, CA: cfg.AcmeCA, RootCAPath: cfg.AcmeRootCA},
		func(host string) bool { _, ok := st.HostOwner(host); return ok })
	if err != nil {
		return err
	}
	defer tlsm.Close()
	dep := deploy.New(podman.New(cfg.PodmanSocket), st, envstore.New(filepath.Join(cfg.StateDir, "env")),
		router, tlsm, deploy.DefaultConfig(cfg.SrvDir))

	if err := dep.Reconcile(ctx); err != nil {
		logger.Printf("reconcile: %v", err)
	}
	go func() {
		t := time.NewTicker(60 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := dep.Reconcile(ctx); err != nil {
					logger.Printf("reconcile: %v", err)
				}
			}
		}
	}()

	httpSrv := &http.Server{Addr: cfg.HTTPAddr, Handler: tlsm.HTTPHandler(proxy.RedirectHandler()), ReadHeaderTimeout: 10 * time.Second}
	httpsSrv := &http.Server{Addr: cfg.HTTPSAddr, Handler: router, TLSConfig: tlsm.TLSConfig(), ReadHeaderTimeout: 10 * time.Second}
	apiSrv := &http.Server{Handler: api.NewServer(dep, version).Handler(), ReadHeaderTimeout: 10 * time.Second}
	l, err := api.Listen(cfg.Socket)
	if err != nil {
		return err
	}

	errc := make(chan error, 3)
	go func() { errc <- httpSrv.ListenAndServe() }()
	go func() { errc <- httpsSrv.ListenAndServeTLS("", "") }()
	go func() { errc <- apiSrv.Serve(l) }()
	logger.Printf("sagand %s started (api %s, http %s, https %s)", version, cfg.Socket, cfg.HTTPAddr, cfg.HTTPSAddr)

	var runErr error
	select {
	case <-ctx.Done():
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = err
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, s := range []*http.Server{httpSrv, httpsSrv, apiSrv} {
		_ = s.Shutdown(shutdownCtx)
	}
	return runErr
}
```

- [ ] **Step 6: Rodar e ver passar**

Run: `cd agent && go vet ./... && go test -race ./...`
Expected: PASS em todos os pacotes.

- [ ] **Step 7: Conferir que o binário compila para o VPS**

Run: `cd agent && GOOS=linux GOARCH=amd64 go build -o /tmp/sagand-linux-amd64 ./cmd/sagand && GOOS=linux GOARCH=arm64 go build -o /tmp/sagand-linux-arm64 ./cmd/sagand && echo ok`
Expected: `ok`

- [ ] **Step 8: Commit**

```bash
git add agent/cmd
git commit -m "feat(agent): add sagand command with daemon, gateway and client"
```

---

### Task 18: CI do agente

**Files:**
- Create: `.github/workflows/ci.yml`

**Interfaces:**
- Produces: job `agent` que roda `go vet ./...` e `go test -race ./...` em `agent/` a cada push e pull request. O plano 2 adiciona o job da CLI no mesmo arquivo.

- [ ] **Step 1: Criar o workflow**

`.github/workflows/ci.yml`:

```yaml
name: ci

on:
  push:
  pull_request:

jobs:
  agent:
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: agent
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: agent/go.mod
      - name: gofmt
        run: test -z "$(gofmt -l .)" || (gofmt -l . && exit 1)
      - run: go vet ./...
      - run: go test -race ./...
```

- [ ] **Step 2: Validar localmente**

Run: `cd agent && test -z "$(gofmt -l .)" && go vet ./... && go test -race ./... && echo ok`
Expected: `ok`. Se o `gofmt` listar arquivos, rode `gofmt -w .` e faça o commit junto.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: run agent vet and race tests"
```

---

## Fora deste plano (próximos)

- **Plano 2 (CLI):** reescrever a CLI em cima do contrato da Task 17; `pack` respeitando ignores; SSH com `accept-new` e ControlMaster; `shQuote` compatível com `gateway.Split`; verificação `dns_mismatch` usando `sagand host`; renderização de eventos; truncar nomes de branch para caber em 40 caracteres.
- **Plano 3 (provision + e2e + release):** `provision.sh` (usuário `sagan`, Podman rootless, unit systemd da spec 4.3, `authorized_keys` com forced command), VM Lima Ubuntu 24.04 com Pebble, teste de integração do Podman (`-tags integration`), os cenários da spec 12.4, GoReleaser e publicação no npm.
