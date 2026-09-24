# SaganSync provision, teste ponta a ponta e release — Plano de Implementação (plano 3 de 3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Tornar o SaganSync usável num VPS de verdade: `sagansync provision` instala o agente (usuário `sagan`, Podman rootless, systemd blindado, chave de deploy restrita), um teste ponta a ponta numa VM prova os critérios de sucesso da spec, e o release publica o agente no GitHub Releases e a CLI no npm.

**Architecture:** A CLI empacota `scripts/provision.sh`, o binário do `sagand` (baixado do release e conferido por sha256, ou `--agent-binary`), a chave pública de deploy e as configurações do daemon num tar, e roda o script como root por SSH com uma conta de administrador. O teste ponta a ponta (`test/e2e/run.sh`) cria uma VM Lima com Ubuntu 24.04, usa a CLI real e o Pebble como servidor ACME. O release usa GoReleaser e GitHub Actions disparados por tag.

**Tech Stack:** Bash, TypeScript (CLI do plano 2), Go (agente do plano 1), Lima, Podman 4.9, Pebble, GoReleaser v2, GitHub Actions v7.

**Spec:** `docs/superpowers/specs/2026-09-24-sagansync-v0.1-agent-design.md` (seções 4.1–4.3, 10, 12.4).

**Verificação prévia:** tudo neste plano foi escrito e executado antes, num rascunho, contra uma VM Lima real (Ubuntu 24.04 arm64, Podman 4.9.3):
- `provision` funcionou de primeira e em re-execuções;
- o teste ponta a ponta passou **26/26 cenários em VMs novas** (três execuções completas; numa outra, a VM não chegou a ligar e o script agora tenta de novo uma vez);
- `tsc` limpo e 132 testes da CLI; agente 13/13 pacotes;
- `actionlint` limpo nos workflows; `shellcheck` limpo nos scripts;
- build do GoReleaser em modo snapshot conferido, e o `downloadAgent` da CLI validado contra a saída real do GoReleaser.

## O que a VM revelou (e este plano corrige)

1. **Os logs dos containers ficavam vazios** (bug do plano 1). O Podman rootless no Ubuntu usa o driver `journald`, e o usuário `sagan` não lê o journal: o `sagansync logs` voltava vazio e um deploy quebrado não mostrava as últimas linhas. Correção: criar os containers com o driver `k8s-file` (Task 1).
2. **A porta SSH do Lima muda a cada reinício da VM:** o teste regrava a configuração depois do reboot.
3. **O `/tmp` da VM é apagado no reboot:** o teste guarda a raiz do Pebble em `/var/tmp`.
4. **O Pebble gera uma CA nova a cada reinício:** o reboot fica por último no teste.
5. **Com `Containerfile` e `Dockerfile` juntos, o Podman usa o `Containerfile`:** documentado no README.
6. **O teste de integração do Podman falhava às vezes (1 em ~15 execuções)** com o `sagand` rodando: a reconciliação do agente remove containers `sagan.managed=true` que não estão no estado, o que inclui o container do teste. O agente está certo; o teste ponta a ponta para o `sagand` enquanto esse teste roda.

## Global Constraints

- Servidor: Ubuntu 24.04+ ou Debian 12+, Podman 4.3+; o `provision.sh` recusa o resto com mensagem clara.
- O `provision` usa uma conta de administrador **uma vez**: root, ou sudo sem senha (`sudo -n`). Depois disso, só a chave de deploy é usada.
- A chave de deploy entra no `authorized_keys` do `sagan` como `command="/usr/local/bin/sagand gateway",restrict <chave>`.
- Unit do systemd exatamente como a spec 4.3 (`User=sagan`, `AmbientCapabilities=CAP_NET_BIND_SERVICE`, `NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome=tmpfs` + `BindPaths=/run/user/<uid>/podman`, `ReadWritePaths=/srv/sagan /var/lib/sagand`).
- Release: tag `v<versão>` igual à versão de `cli/package.json`; arquivos `sagand_<versão>_linux_<amd64|arm64>.tar.gz` (só o binário) e `checksums.txt` (sha256), em `https://github.com/borgim/sagansync/releases/download/v<versão>/`.
- O `provision` baixa **a mesma versão da CLI** e recusa o arquivo se o sha256 não bater (spec 10).
- Actions: `actions/checkout@v7`, `actions/setup-go@v7` (com `cache-dependency-path: agent/go.sum`), `actions/setup-node@v7`, `goreleaser/goreleaser-action@v7`.
- Scripts em Bash passam no `shellcheck`.

## Decisões deste plano (fora do texto da spec)

- **O binário é baixado na máquina do usuário**, conferido e enviado pelo SSH junto com o script, em vez de baixado no servidor. Não depende de `curl` no VPS e usa o mesmo caminho do `--agent-binary`.
- **`--admin-key`**: chave SSH da conta de administrador (a de um provedor de nuvem, ou a do Lima no teste).
- **`--remove-caddy` é a confirmação** da remoção do Caddy da versão antiga (spec 4.1, passo 7). Sem ela, o script para e explica. Qualquer outro serviço nas portas 80/443 faz o script parar com a lista do que as ocupa.
- **O firewall `ufw`, se estiver ativo, ganha as portas 80 e 443.**
- **`--upgrade` pula a instalação de pacotes e mantém o `config.json` do daemon** (e-mail ACME etc.); sem ele, o `provision` reescreve essa configuração com as flags atuais.
- **`--acme-ca` e `--acme-root-ca` ficam ocultas** na ajuda: existem para o teste com o Pebble.
- **O README e a LICENSE da raiz vão para o pacote npm** por um `prepack` que os copia para `cli/` (as cópias são ignoradas pelo git).
- **`LICENSE` MIT**, coerente com o `"license": "MIT"` que o `package.json` já declarava.
- **A publicação no npm usa o secret `NPM_TOKEN`.** O trusted publishing do npm exige um pacote já existente, então pode substituir o token depois da primeira versão.
- **O CI ganha um job `shell`** com `shellcheck` nos dois scripts.

## Review Focus

1. **Rodar o `provision` de novo** (para reparar, ou depois de mudar a chave) não pode quebrar nada nem duplicar linhas. Coberto pelo teste ponta a ponta (dois `provision` seguidos e um `--upgrade`).
2. **Uma conta de administrador com sudo que pede senha** precisa virar uma mensagem clara. Teste na Task 3 (`explains sudo that needs a password`).
3. **Um `--agent-binary` para a CPU errada** (compilado para amd64, servidor arm64) não pode ser instalado. Teste na Task 3 (`archFor and checkBinary`).
4. **Um release adulterado ou corrompido** não pode ser instalado. Teste na Task 3 (`refuses a tampered archive`).
5. **Depois de um reboot, a CLI e o app precisam voltar sozinhos.** Coberto pelo teste ponta a ponta (cenário Reboot).

---

### Task 1: Agente: logs legíveis com o driver `k8s-file`

**Files:**
- Modify: `agent/internal/podman/podman.go`
- Test: `agent/internal/podman/podman_test.go`

**Interfaces:**
- Produces: `(*podman.Client).Create` envia `"log_configuration": {"driver": "k8s-file"}` no corpo de `POST /containers/create`.
- Motivo: no Ubuntu, o Podman rootless grava os logs no `journald`, que o usuário `sagan` não consegue ler. Com isso, `podman logs` (e o `sagansync logs`, e as linhas de log de um deploy quebrado) voltavam vazios. Verificado na VM: com `k8s-file`, os logs aparecem pela API.
- Os arquivos abaixo são os arquivos inteiros. A mudança no teste é a linha `"k8s-file logs"` em `TestCreateSendsSpec`; no código, o tipo `logConfig`, o campo `LogConfig` do `specgen` e o valor em `Create`.

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
			"k8s-file logs":     body["log_configuration"].(map[string]any)["driver"] == "k8s-file",
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
Expected: FAIL em `TestCreateSendsSpec`: `create body: k8s-file logs is wrong` (o corpo ainda não tem `log_configuration`).

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

type logConfig struct {
	Driver string `json:"driver"`
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
	LogConfig       logConfig         `json:"log_configuration"`
}

func (c *Client) Create(ctx context.Context, spec runtime.ContainerSpec) error {
	body := specgen{
		Name: spec.Name, Image: spec.Image, Command: spec.Command, Env: spec.Env, Labels: spec.Labels,
		NoNewPrivileges: true,
		// Rootless Podman defaults to journald on systemd hosts, and the sagan
		// user cannot read the journal, so `podman logs` came back empty.
		LogConfig:    logConfig{Driver: "k8s-file"},
		PortMappings: []portMapping{{HostIP: "127.0.0.1", ContainerPort: uint16(spec.InternalPort), Protocol: "tcp"}},
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

Run: `cd agent && gofmt -l . && go vet ./... && go test -race ./... && go test ./internal/podman/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add agent/internal/podman/podman.go agent/internal/podman/podman_test.go
git commit -m "fix(agent): write container logs to files so sagan can read them"
```

---

### Task 2: CLI: conta de administrador no SSH

**Files:**
- Modify: `cli/src/lib/ssh.ts`
- Test: `cli/test/ssh.test.ts`

**Interfaces:**
- Produces (além do que o `ssh.ts` já exportava):
  - `interface Shell { run(command, stdin?): Promise<RunResult>; stream(command, onLine, stdin?): Promise<{ code; stderr }> }` — comandos crus (não `sagand`).
  - `type AdminTarget = { host; port; user; identityFile?; knownHosts }`, `adminSshArgs(t): string[]`, `adminShell(t, sshBin?): Shell`.
- A conta de administrador verifica a host key (o mesmo `known_hosts` da CLI), pode pedir senha ou passphrase (sem `BatchMode`), usa só a chave indicada quando há `identityFile` e nunca compartilha conexão (`ControlMaster=no`).
- `sshRemote` passa a ser construído sobre o mesmo runner interno (`sshShell`), sem mudar de comportamento: os testes existentes continuam passando.

- [ ] **Step 1: Escrever o teste que falha**

`cli/test/ssh.test.ts`:

```ts
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { Readable } from "node:stream";
import { describe, expect, test } from "vitest";
import { adminSshArgs, ensurePrivateDir, remoteCommand, shQuote, sshArgs, sshRemote, type Target } from "../src/lib/ssh.js";
import { fakeSsh } from "./helpers/fakeSsh.js";

// A private directory, like ~/.config/sagansync. Never the shared tmpdir:
// on Linux that is /tmp itself, which the CLI rightly refuses.
const target: Target = { host: "vps.example.com", port: 2222, user: "sagan", identityFile: "/keys/id",
  knownHosts: "/cfg/known_hosts", controlDir: fs.mkdtempSync(path.join(os.tmpdir(), "sgs-cfg-")) };

describe("shQuote", () => {
  test.each([
    ["plain", "'plain'"],
    ["it's", `'it'"'"'s'`],
    ["$(rm -rf /); `id`", "'$(rm -rf /); `id`'"],
    ["", "''"],
  ])("%s", (input, quoted) => {
    expect(shQuote(input)).toBe(quoted);
  });

  test("remoteCommand quotes every argument", () => {
    expect(remoteCommand(["deploy", "--project", "a b"])).toBe("'sagand' 'deploy' '--project' 'a b'");
  });
});

describe("sshArgs", () => {
  const args = sshArgs({ ...target, controlDir: "/cfg" });
  test("verifies host keys and never disables checking", () => {
    expect(args.join(" ")).toContain("StrictHostKeyChecking=accept-new");
    expect(args.join(" ")).toContain("UserKnownHostsFile=/cfg/known_hosts");
    expect(args.join(" ")).not.toContain("StrictHostKeyChecking=no");
  });
  test("reuses connections", () => {
    expect(args).toContain("ControlMaster=auto");
    expect(args).toContain("ControlPersist=60s");
    expect(args.some((a) => a.startsWith("ControlPath=") && a.endsWith("cm-%C"))).toBe(true);
  });
  test("uses the deploy key only and never prompts", () => {
    expect(args).toEqual(expect.arrayContaining(["-i", "/keys/id", "IdentitiesOnly=yes", "BatchMode=yes"]));
  });
  test("ends with -- and the host so the host can never be read as an option", () => {
    expect(args.slice(-2)).toEqual(["--", "vps.example.com"]);
    expect(args.slice(args.indexOf("-p"), args.indexOf("-p") + 2)).toEqual(["-p", "2222"]);
  });
});

describe("control socket path", () => {
  const pathOf = (args: string[]) => args.find((a) => a.startsWith("ControlPath="))!.slice("ControlPath=".length);

  test("uses the config dir when the socket path fits", () => {
    expect(pathOf(sshArgs({ ...target, controlDir: "/Users/pedro/.config/sagansync" }))).toBe("/Users/pedro/.config/sagansync/cm-%C");
  });

  test("falls back to a short private path for long home directories", () => {
    const long = `/Users/${"firstname.lastname"}/.config/sagansync`;
    const p = pathOf(sshArgs({ ...target, controlDir: long }));
    expect(p.startsWith(long)).toBe(false);
    // ssh listens on "<path>.<16 random chars>"; macOS allows 103 bytes.
    expect(p.length + 17).toBeLessThanOrEqual(103);
    expect(p).not.toBe(pathOf(sshArgs({ ...target, controlDir: long, host: "other.test" })));
  });

  test("refuses a directory others can write to, with a fix that keeps its contents", () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-cm-"));
    fs.chmodSync(dir, 0o777);
    expect(() => ensurePrivateDir(dir)).toThrow(`chmod 700 ${dir}`);
    expect(() => ensurePrivateDir(dir)).not.toThrow("remove");
    fs.chmodSync(dir, 0o700);
    expect(() => ensurePrivateDir(dir)).not.toThrow();
  });

  test("accepts a directory others can only read (the socket itself is 0600)", () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-cm-"));
    fs.chmodSync(dir, 0o755);
    expect(() => ensurePrivateDir(dir)).not.toThrow();
  });
});

describe("adminSshArgs", () => {
  const admin = { host: "203.0.113.7", port: 22, user: "ubuntu", knownHosts: "/cfg/known_hosts" };

  test("logs in as the admin, verifies the host key and never shares a connection", () => {
    const args = adminSshArgs(admin);
    expect(args).toEqual(expect.arrayContaining(["-l", "ubuntu", "StrictHostKeyChecking=accept-new", "UserKnownHostsFile=/cfg/known_hosts", "ControlMaster=no"]));
    expect(args.slice(-2)).toEqual(["--", "203.0.113.7"]);
  });

  test("may prompt for a password or passphrase, unlike the deploy key", () => {
    expect(adminSshArgs(admin)).not.toContain("BatchMode=yes");
  });

  test("uses only the given key when there is one", () => {
    expect(adminSshArgs({ ...admin, identityFile: "/k" })).toEqual(expect.arrayContaining(["-i", "/k", "IdentitiesOnly=yes"]));
    expect(adminSshArgs(admin)).not.toContain("-i");
  });
});

describe("sshRemote", () => {
  test("run passes the quoted command and stdin, and returns output", async () => {
    const ssh = fakeSsh({ stdout: '{"version":"0.1.0"}\n', code: 0 });
    const r = await sshRemote(target, ssh.bin).run(["env", "set", "--project", "app"], '{"A":"x"}');
    expect(r).toEqual({ code: 0, stdout: '{"version":"0.1.0"}\n', stderr: "" });
    const rec = ssh.record();
    expect(rec.argv.at(-1)).toBe("'sagand' 'env' 'set' '--project' 'app'");
    expect(rec.stdin).toBe('{"A":"x"}');
  });

  test("stream delivers lines and pipes a stream to stdin", async () => {
    const ssh = fakeSsh({ stdout: "one\ntwo\n", code: 3, stderr: "boom" });
    const lines: string[] = [];
    const r = await sshRemote(target, ssh.bin).stream(["deploy"], (l) => lines.push(l), Readable.from(["tar", "ball"]));
    expect(lines).toEqual(["one", "two"]);
    expect(r).toEqual({ code: 3, stderr: "boom" });
    expect(ssh.record().stdin).toBe("tarball");
  });

  test("a failing input stream rejects instead of sending a truncated body", async () => {
    const ssh = fakeSsh({ code: 0 });
    const broken = new Readable({ read() { this.destroy(new Error("EACCES: permission denied")); } });
    await expect(sshRemote(target, ssh.bin).run(["put", "app", "ws", "a.ts"], broken)).rejects.toThrow("EACCES");
  });

  test("a missing ssh binary rejects", async () => {
    await expect(sshRemote(target, "/nonexistent/ssh").run(["version"])).rejects.toThrow();
  });
});
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd cli && npx vitest run test/ssh.test.ts`
Expected: FAIL — `adminSshArgs` não é exportado por `../src/lib/ssh.js`.

- [ ] **Step 3: Implementar**

`cli/src/lib/ssh.ts`:

```ts
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import readline from "node:readline";
import type { Readable } from "node:stream";
import { type Config, configDir, keyPath, knownHostsPath } from "./config.js";

export type Target = {
  host: string;
  port: number;
  user: string;
  identityFile: string;
  knownHosts: string;
  controlDir: string;
};

export function targetFor(cfg: Config): Target {
  return { host: cfg.host, port: cfg.sshPort, user: cfg.user, identityFile: keyPath(cfg),
    knownHosts: knownHostsPath(), controlDir: configDir() };
}

// shQuote wraps s in single quotes for the remote shell. sagand's gateway
// splits SSH_ORIGINAL_COMMAND with the same rules, without any expansion.
export function shQuote(s: string): string {
  return `'${s.replaceAll("'", `'"'"'`)}'`;
}

export function remoteCommand(args: string[]): string {
  return ["sagand", ...args].map(shQuote).join(" ");
}

// ssh listens on "<ControlPath>.<16 random chars>" and %C expands to 40 hex
// characters. Unix socket paths are limited to 103 bytes on macOS, so long
// home directories fall back to a short per-user directory in /tmp.
const SOCKET_LIMIT = 103;

export function controlPath(t: Target): string {
  const preferred = path.join(t.controlDir, "cm-%C");
  if (preferred.length - 2 + 40 + 17 <= SOCKET_LIMIT) return preferred;
  const id = createHash("sha256").update(`${t.user}@${t.host}:${t.port}`).digest("hex").slice(0, 16);
  return path.join(`/tmp/sagansync-${process.getuid?.() ?? "user"}`, id);
}

// ensurePrivateDir creates dir (mode 0700) for the ssh control socket and
// refuses one that is a symlink, belongs to someone else, or that other users
// can write to (they could swap the socket). Read access is harmless: ssh
// creates the socket itself with mode 0600.
export function ensurePrivateDir(dir: string): void {
  fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
  const st = fs.lstatSync(dir);
  const mine = process.getuid === undefined || st.uid === process.getuid();
  if (!st.isDirectory() || !mine) {
    throw new Error(`${dir} must be a directory owned by you, since it holds the ssh connection socket. Move it aside and try again.`);
  }
  if ((st.mode & 0o022) !== 0) {
    throw new Error(`${dir} can be modified by other users, so it cannot hold the ssh connection socket. Fix it with: chmod 700 ${dir}`);
  }
}

export function sshArgs(t: Target): string[] {
  return [
    "-T",
    "-p", String(t.port),
    "-i", t.identityFile,
    "-l", t.user,
    "-o", "IdentitiesOnly=yes",
    "-o", "BatchMode=yes",
    "-o", "StrictHostKeyChecking=accept-new",
    "-o", `UserKnownHostsFile=${t.knownHosts}`,
    "-o", "ControlMaster=auto",
    "-o", `ControlPath=${controlPath(t)}`,
    "-o", "ControlPersist=60s",
    "-o", "LogLevel=ERROR",
    "--", t.host,
  ];
}

export type RunResult = { code: number; stdout: string; stderr: string };
export type Input = Readable | string | Buffer;

// Remote runs sagand subcommands on the VPS.
export interface Remote {
  run(args: string[], stdin?: Input): Promise<RunResult>;
  // stream calls onLine for every stdout line as it arrives.
  stream(args: string[], onLine: (line: string) => void, stdin?: Input): Promise<{ code: number; stderr: string }>;
}

// A Shell runs raw commands over ssh (used with the admin account).
export interface Shell {
  run(command: string, stdin?: Input): Promise<RunResult>;
  stream(command: string, onLine: (line: string) => void, stdin?: Input): Promise<{ code: number; stderr: string }>;
}

// sshShell spawns ssh with the given arguments followed by the command.
function sshShell(args: string[], sshBin: string, before: () => void = () => {}): Shell {
  const start = (command: string, stdin?: Input) => {
    before();
    const child = spawn(sshBin, [...args, command], { stdio: ["pipe", "pipe", "pipe"] });
    child.stdin.on("error", () => {}); // the remote may exit before reading all input
    // If the local input fails (unreadable file, packing error), ssh must not
    // see a clean EOF: the remote would accept the truncated body as complete.
    let inputError: Error | undefined;
    if (typeof stdin === "string" || Buffer.isBuffer(stdin)) child.stdin.end(stdin);
    else if (stdin) {
      stdin.on("error", (err) => {
        inputError = err;
        stdin.unpipe(child.stdin);
        child.kill("SIGTERM");
      });
      stdin.pipe(child.stdin);
    } else child.stdin.end();
    let stderr = "";
    child.stderr.setEncoding("utf8").on("data", (d: string) => (stderr += d));
    const done = new Promise<{ code: number; stderr: string }>((resolve, reject) => {
      child.on("error", reject);
      child.on("close", (code) => (inputError ? reject(inputError) : resolve({ code: code ?? 1, stderr })));
    });
    return { child, done };
  };
  return {
    async run(command, stdin) {
      const { child, done } = start(command, stdin);
      let stdout = "";
      child.stdout.setEncoding("utf8").on("data", (d: string) => (stdout += d));
      const { code, stderr } = await done;
      return { code, stdout, stderr };
    },
    async stream(command, onLine, stdin) {
      const { child, done } = start(command, stdin);
      const rl = readline.createInterface({ input: child.stdout, crlfDelay: Infinity });
      rl.on("line", onLine);
      const result = await done;
      rl.close();
      return result;
    },
  };
}

// sshRemote talks to sagand through ssh. SAGANSYNC_SSH replaces the ssh
// binary (tests use a fake one).
export function sshRemote(t: Target, sshBin = process.env.SAGANSYNC_SSH ?? "ssh"): Remote {
  const shell = sshShell(sshArgs(t), sshBin, () => ensurePrivateDir(path.dirname(controlPath(t))));
  return {
    run: (args, stdin) => shell.run(remoteCommand(args), stdin),
    stream: (args, onLine, stdin) => shell.stream(remoteCommand(args), onLine, stdin),
  };
}

// AdminTarget is the account `sagansync provision` installs with (root or a
// sudoer). Unlike the deploy key it may prompt for a passphrase or password,
// and it never reuses the deploy key's control socket.
export type AdminTarget = { host: string; port: number; user: string; identityFile?: string; knownHosts: string };

export function adminSshArgs(t: AdminTarget): string[] {
  return [
    "-T",
    "-p", String(t.port),
    ...(t.identityFile ? ["-i", t.identityFile, "-o", "IdentitiesOnly=yes"] : []),
    "-l", t.user,
    "-o", "StrictHostKeyChecking=accept-new",
    "-o", `UserKnownHostsFile=${t.knownHosts}`,
    "-o", "ControlMaster=no",
    "-o", "ControlPath=none",
    "-o", "LogLevel=ERROR",
    "--", t.host,
  ];
}

export function adminShell(t: AdminTarget, sshBin = process.env.SAGANSYNC_SSH ?? "ssh"): Shell {
  return sshShell(adminSshArgs(t), sshBin, () => fs.mkdirSync(path.dirname(t.knownHosts), { recursive: true, mode: 0o700 }));
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd cli && npx tsc --noEmit && npx vitest run test/ssh.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cli/src/lib/ssh.ts cli/test/ssh.test.ts
git commit -m "feat(cli): add an admin ssh shell for provisioning"
```

---

### Task 3: CLI: comando `provision` e o `provision.sh`

**Files:**
- Create: `cli/scripts/provision.sh`
- Create: `cli/src/lib/paths.ts`
- Create: `cli/src/commands/provision.ts`
- Test: `cli/test/provision.test.ts`

**Interfaces:**
- Consumes: `Shell`, `AdminTarget`, `shQuote` (T2); `checkAgent`, `CliError`, `sshError` (plano 2); `keyPath`, `knownHostsPath` (plano 2); `VERSION`.
- Produces:
  - `packageFile(rel)` — arquivo do pacote npm (funciona a partir de `src/` e de `dist/`).
  - `adminTarget(cfg, { admin?, adminKey? }): AdminTarget` — `--admin user@host`, `user` ou padrão `root`; host padrão = `config.host`.
  - `archFor(unameOutput)`, `checkBinary(buf, arch)` — valida ELF e CPU de um `--agent-binary`.
  - `downloadAgent(arch, fetch?, version?)` — baixa `checksums.txt` e o tar.gz do release, confere o sha256 e extrai `sagand`.
  - `bundle(files)`, `daemonSettings(opts)`, `remoteScript(flags)`.
  - `provision(ctx, opts, { shell, fetch? })`.
- O `provision.sh` segue a spec 4.1/4.3 e as decisões acima. Ele não tem teste unitário: é validado pelo teste ponta a ponta (Task 5) e pelo `shellcheck` (Task 6).

- [ ] **Step 1: Escrever o teste que falha**

`cli/test/provision.test.ts`:

```ts
import { createHash } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { gzipSync } from "node:zlib";
import { extract as tarExtract } from "tar-stream";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { adminTarget, archFor, bundle, checkBinary, daemonSettings, downloadAgent, provision, remoteScript } from "../src/commands/provision.js";
import type { Input, RunResult, Shell } from "../src/lib/ssh.js";
import { testCtx } from "./helpers/ctx.js";
import { FakeRemote, VERSION_OK } from "./helpers/fakeRemote.js";

async function untar(buf: Buffer): Promise<Record<string, { data: Buffer; mode: number }>> {
  const ex = tarExtract();
  const out: Record<string, { data: Buffer; mode: number }> = {};
  const done = new Promise<void>((resolve, reject) => {
    ex.on("entry", (h, body, next) => {
      const chunks: Buffer[] = [];
      body.on("data", (c) => chunks.push(c as Buffer));
      body.on("end", () => {
        out[h.name] = { data: Buffer.concat(chunks), mode: h.mode ?? 0 };
        next();
      });
    });
    ex.on("finish", resolve);
    ex.on("error", reject);
  });
  ex.end(buf);
  await done;
  return out;
}

// elf returns a minimal ELF header for the given machine (0x3e amd64, 0xb7 arm64).
function elf(machine: number): Buffer {
  const b = Buffer.alloc(64);
  b.writeUInt32BE(0x7f454c46, 0);
  b.writeUInt16LE(machine, 18);
  return b;
}

class FakeShell implements Shell {
  calls: { command: string; stdin?: Buffer }[] = [];
  constructor(private readonly reply: (command: string) => Partial<RunResult> & { lines?: string[] }) {}
  async run(command: string, stdin?: Input) {
    this.calls.push({ command, stdin: stdin as Buffer | undefined });
    const r = this.reply(command);
    return { code: r.code ?? 0, stdout: r.stdout ?? "", stderr: r.stderr ?? "" };
  }
  async stream(command: string, onLine: (l: string) => void, stdin?: Input) {
    const r = await this.run(command, stdin);
    for (const l of this.reply(command).lines ?? []) onLine(l);
    return { code: r.code, stderr: r.stderr };
  }
}

let home: string;
beforeEach(() => {
  home = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-home-"));
  process.env.SAGANSYNC_HOME = home;
  fs.mkdirSync(path.join(home, "keys"));
  fs.writeFileSync(path.join(home, "keys", "vps.test_ed25519.pub"), "ssh-ed25519 AAAAC3Nza deploy\n");
});
afterEach(() => {
  delete process.env.SAGANSYNC_HOME;
});

describe("adminTarget", () => {
  const cfg = testCtx(new FakeRemote()).config;
  test("defaults to root on the configured host", () => {
    expect(adminTarget(cfg, {})).toMatchObject({ user: "root", host: "vps.test", port: 22 });
  });
  test("accepts user@host and a key", () => {
    expect(adminTarget(cfg, { admin: "ubuntu@203.0.113.7", adminKey: "/k" })).toMatchObject({ user: "ubuntu", host: "203.0.113.7", identityFile: "/k" });
  });
  test("rejects garbage", () => {
    expect(() => adminTarget(cfg, { admin: "root@-oProxyCommand=x" })).toThrow("Invalid --admin");
  });
});

test("archFor and checkBinary", () => {
  expect(archFor("x86_64\n").name).toBe("amd64");
  expect(archFor("aarch64").name).toBe("arm64");
  expect(() => archFor("riscv64")).toThrow("not supported");
  expect(() => checkBinary(elf(0xb7), archFor("aarch64"))).not.toThrow();
  expect(() => checkBinary(elf(0x3e), archFor("aarch64"))).toThrow("GOARCH=arm64");
  expect(() => checkBinary(Buffer.from("#!/bin/sh\necho hi\n"), archFor("x86_64"))).toThrow("GOOS=linux");
});

describe("downloadAgent", () => {
  async function release(binary: Buffer, tamper = false) {
    const tgz = gzipSync(await bundle([{ name: "sagand", data: binary, mode: 0o755 }]));
    const sum = createHash("sha256").update(tamper ? Buffer.from("other") : tgz).digest("hex");
    const files: Record<string, Buffer> = {
      "v0.1.0/checksums.txt": Buffer.from(`${sum}  sagand_0.1.0_linux_arm64.tar.gz\nabc  sagand_0.1.0_linux_amd64.tar.gz\n`),
      "v0.1.0/sagand_0.1.0_linux_arm64.tar.gz": tgz,
    };
    const urls: string[] = [];
    const fetchFn = (async (url: string) => {
      urls.push(url);
      const f = files[url.split("/").slice(-2).join("/")];
      return f ? new Response(new Uint8Array(f)) : new Response("nope", { status: 404 });
    }) as typeof fetch;
    return { fetchFn, urls };
  }

  test("downloads, verifies and extracts the binary", async () => {
    const { fetchFn, urls } = await release(elf(0xb7));
    expect(await downloadAgent("arm64", fetchFn, "0.1.0")).toEqual(elf(0xb7));
    expect(urls[0]).toBe("https://github.com/borgim/sagansync/releases/download/v0.1.0/checksums.txt");
  });

  test("refuses a tampered archive", async () => {
    const { fetchFn } = await release(elf(0xb7), true);
    await expect(downloadAgent("arm64", fetchFn, "0.1.0")).rejects.toThrow("Checksum mismatch");
  });

  test("explains a missing release", async () => {
    const { fetchFn } = await release(elf(0xb7));
    await expect(downloadAgent("arm64", fetchFn, "9.9.9")).rejects.toThrow("HTTP 404");
  });
});

test("remoteScript runs provision.sh as root or through passwordless sudo, under sh", () => {
  const s = remoteScript(["--upgrade"]);
  expect(s.startsWith("sh -c '")).toBe(true);
  expect(s).toContain(`sudo -n bash "$d/provision.sh" "$d" '"'"'--upgrade'"'"'`);
});

test("daemonSettings", () => {
  expect(JSON.parse(daemonSettings({}))).toEqual({});
  expect(JSON.parse(daemonSettings({ acmeEmail: "ops@example.com", acmeCa: "https://localhost:14000/dir", acmeRootCa: "/x.pem" }))).toEqual({
    acmeEmail: "ops@example.com", acmeCA: "https://localhost:14000/dir", acmeRootCA: "/var/lib/sagand/acme-root-ca.pem",
  });
});

describe("provision", () => {
  test("uploads the bundle, streams progress and checks the deploy key", async () => {
    const bin = path.join(home, "sagand");
    fs.writeFileSync(bin, elf(0xb7));
    const shell = new FakeShell((cmd) => (cmd === "uname -m" ? { stdout: "aarch64\n" } : { lines: ["▸ Installing Podman", "✔ sagand is running"] }));
    const remote = new FakeRemote({ version: VERSION_OK });
    const ctx = testCtx(remote);
    await provision(ctx, { agentBinary: bin, upgrade: true, acmeEmail: "ops@example.com" }, { shell });
    const files = await untar(shell.calls[1]!.stdin!);
    expect(Object.keys(files).sort()).toEqual(["config.json", "deploy.pub", "provision.sh", "sagand"]);
    expect(files["sagand"]!.mode).toBe(0o755);
    expect(files["deploy.pub"]!.data.toString()).toContain("ssh-ed25519");
    expect(files["provision.sh"]!.data.toString()).toContain("sagand gateway");
    expect(JSON.parse(files["config.json"]!.data.toString())).toEqual({ acmeEmail: "ops@example.com" });
    expect(shell.calls[1]!.command).toContain("--upgrade");
    expect(ctx.lines).toContain("▸ Installing Podman");
    expect(remote.argsOf("version")).toEqual(["version"]);
    expect(ctx.lines.at(-1)).toContain("is ready");
  });

  test("needs the deploy key", async () => {
    fs.rmSync(path.join(home, "keys", "vps.test_ed25519.pub"));
    await expect(provision(testCtx(new FakeRemote()), {}, { shell: new FakeShell(() => ({})) })).rejects.toMatchObject({ hint: expect.stringContaining("sagansync init") });
  });

  test("explains sudo that needs a password", async () => {
    const bin = path.join(home, "sagand");
    fs.writeFileSync(bin, elf(0xb7));
    const shell = new FakeShell((cmd) => (cmd === "uname -m" ? { stdout: "aarch64" } : { code: 1, stderr: "sudo: a password is required\n" }));
    await expect(provision(testCtx(new FakeRemote()), { agentBinary: bin }, { shell })).rejects.toMatchObject({ hint: expect.stringContaining("--admin root@") });
  });

  test("shows provision.sh's own error message", async () => {
    const bin = path.join(home, "sagand");
    fs.writeFileSync(bin, elf(0xb7));
    const shell = new FakeShell((cmd) => (cmd === "uname -m" ? { stdout: "aarch64" } : { code: 1, stderr: "✖ Ubuntu 22.04 is too old: sagand needs Ubuntu 24.04 or newer (for Podman 4.3+).\n" }));
    await expect(provision(testCtx(new FakeRemote()), { agentBinary: bin }, { shell })).rejects.toThrow("Ubuntu 22.04 is too old");
  });

  test("an SSH failure is explained", async () => {
    const shell = new FakeShell(() => ({ code: 255, stderr: "Permission denied (publickey)." }));
    await expect(provision(testCtx(new FakeRemote()), {}, { shell })).rejects.toThrow("Could not connect over SSH");
  });
});
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd cli && npx vitest run test/provision.test.ts`
Expected: FAIL — o vitest não encontra `../src/commands/provision.js`.

- [ ] **Step 3: Implementar**

`cli/scripts/provision.sh`:

```bash
#!/bin/bash
# provision.sh installs sagand on a Debian/Ubuntu server. `sagansync provision`
# uploads it with the files it needs and runs it as root:
#
#   provision.sh <bundle-dir> [--upgrade] [--remove-caddy]
#
# The bundle holds: sagand (the agent binary), deploy.pub (the project's deploy
# key), config.json (daemon settings) and optionally acme-root-ca.pem.
# Running it again is safe; --upgrade skips package installation and keeps the
# existing daemon settings.
set -euo pipefail

BUNDLE=$1
shift
UPGRADE=0
REMOVE_CADDY=0
for arg in "$@"; do
  case $arg in
    --upgrade) UPGRADE=1 ;;
    --remove-caddy) REMOVE_CADDY=1 ;;
    *) echo "provision.sh: unknown option $arg" >&2; exit 2 ;;
  esac
done

step() { echo "▸ $*"; }
fail() { echo "✖ $*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || fail "provision.sh must run as root"

# --- System -------------------------------------------------------------------
# shellcheck source=/dev/null # only exists on the server
. /etc/os-release
case "$ID" in
  ubuntu) dpkg --compare-versions "$VERSION_ID" ge 24.04 || fail "Ubuntu $VERSION_ID is too old: sagand needs Ubuntu 24.04 or newer (for Podman 4.3+)." ;;
  debian) dpkg --compare-versions "$VERSION_ID" ge 12 || fail "Debian $VERSION_ID is too old: sagand needs Debian 12 or newer (for Podman 4.3+)." ;;
  *) fail "$PRETTY_NAME is not supported: sagand needs Ubuntu 24.04+ or Debian 12+." ;;
esac

# --- Ports 80 and 443 -----------------------------------------------------------
busy=$(ss -Hltnp '( sport = :80 or sport = :443 )' | grep -v '"sagand"' || true)
if [ -n "$busy" ]; then
  if echo "$busy" | grep -q '"caddy"' && [ -d /etc/caddy/conf.d ]; then
    [ "$REMOVE_CADDY" -eq 1 ] || fail "Caddy from an older SaganSync is using ports 80/443. Run again with --remove-caddy to remove it."
    step "Removing Caddy"
    systemctl disable --now caddy >/dev/null 2>&1 || true
    DEBIAN_FRONTEND=noninteractive apt-get purge -y -qq caddy >/dev/null
    rm -rf /etc/caddy
  else
    echo "$busy" >&2
    fail "Ports 80/443 are already in use (see above). sagand needs them for HTTPS: stop that service and run again."
  fi
fi

# --- Packages --------------------------------------------------------------------
if [ "$UPGRADE" -eq 0 ]; then
  step "Installing Podman"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq podman uidmap passt slirp4netns dbus-user-session >/dev/null
fi
podman_version=$(podman version --format '{{.Client.Version}}')
dpkg --compare-versions "$podman_version" ge 4.3 || fail "Podman $podman_version is too old: sagand needs 4.3 or newer."

# --- The sagan user ----------------------------------------------------------------
if ! id sagan >/dev/null 2>&1; then
  step "Creating the sagan user"
  useradd --system --create-home --home-dir /home/sagan --shell /bin/sh sagan
fi
passwd -l sagan >/dev/null
# useradd --system does not allocate subordinate ids, which rootless Podman needs.
for f in /etc/subuid /etc/subgid; do
  touch "$f"
  if ! grep -q '^sagan:' "$f"; then
    start=$(awk -F: 'BEGIN { m = 100000 } { e = $2 + $3; if (e > m) m = e } END { print m }' "$f")
    echo "sagan:$start:65536" >> "$f"
  fi
done
uid=$(id -u sagan)

step "Starting rootless Podman for sagan"
loginctl enable-linger sagan
systemctl start "user@$uid.service"
systemctl --user --machine=sagan@ enable --now podman.socket >/dev/null 2>&1

# --- Files ---------------------------------------------------------------------------
step "Installing sagand"
install -d -o sagan -g sagan -m 0700 /srv/sagan /var/lib/sagand
install -o root -g root -m 0755 "$BUNDLE/sagand" /usr/local/bin/sagand.new
mv /usr/local/bin/sagand.new /usr/local/bin/sagand
if [ "$UPGRADE" -eq 0 ] || [ ! -f /var/lib/sagand/config.json ]; then
  install -o sagan -g sagan -m 0600 "$BUNDLE/config.json" /var/lib/sagand/config.json
fi
if [ -f "$BUNDLE/acme-root-ca.pem" ]; then
  install -o sagan -g sagan -m 0600 "$BUNDLE/acme-root-ca.pem" /var/lib/sagand/acme-root-ca.pem
fi

# --- Deploy key: it may only run the sagand gateway --------------------------------
ssh-keygen -l -f "$BUNDLE/deploy.pub" >/dev/null 2>&1 || fail "deploy.pub is not a valid SSH public key."
key=$(head -n 1 "$BUNDLE/deploy.pub")
line="command=\"/usr/local/bin/sagand gateway\",restrict $key"
install -d -o sagan -g sagan -m 0700 /home/sagan/.ssh
auth=/home/sagan/.ssh/authorized_keys
touch "$auth"
grep -qxF "$line" "$auth" || echo "$line" >> "$auth"
chown sagan:sagan "$auth"
chmod 0600 "$auth"

# --- Service -------------------------------------------------------------------------
cat > /etc/systemd/system/sagand.service <<EOF
[Unit]
Description=SaganSync agent
After=network-online.target user@$uid.service
Wants=network-online.target
Requires=user@$uid.service

[Service]
User=sagan
Group=sagan
ExecStart=/usr/local/bin/sagand daemon
RuntimeDirectory=sagand
RuntimeDirectoryMode=0700
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=tmpfs
BindPaths=/run/user/$uid/podman
PrivateTmp=yes
ReadWritePaths=/srv/sagan /var/lib/sagand
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable sagand >/dev/null 2>&1
systemctl restart sagand

if command -v ufw >/dev/null 2>&1 && ufw status | grep -q "Status: active"; then
  step "Opening ports 80 and 443 in ufw"
  ufw allow 80/tcp >/dev/null
  ufw allow 443/tcp >/dev/null
fi

for _ in $(seq 1 30); do
  if systemctl is-active --quiet sagand && [ -S /run/sagand/sagand.sock ]; then
    echo "✔ sagand is running"
    exit 0
  fi
  sleep 1
done
journalctl -u sagand -n 30 --no-pager >&2
fail "sagand did not start (its last log lines are above)."
```

`cli/src/lib/paths.ts`:

```ts
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

// packageFile resolves a file shipped with the npm package (such as
// scripts/provision.sh), both from src/ in tests and from dist/ when installed.
export function packageFile(rel: string): string {
  let dir = path.dirname(fileURLToPath(import.meta.url));
  while (!fs.existsSync(path.join(dir, "package.json"))) {
    const parent = path.dirname(dir);
    if (parent === dir) throw new Error(`cannot find the sagansync package root for ${rel}`);
    dir = parent;
  }
  return path.join(dir, rel);
}
```

`cli/src/commands/provision.ts`:

```ts
import { createHash } from "node:crypto";
import fs from "node:fs";
import { gunzipSync } from "node:zlib";
import { extract as tarExtract, pack as tarPack } from "tar-stream";
import { checkAgent, CliError, sshError } from "../lib/agent.js";
import { type Config, keyPath, knownHostsPath } from "../lib/config.js";
import { packageFile } from "../lib/paths.js";
import { type AdminTarget, type Shell, shQuote } from "../lib/ssh.js";
import { paint } from "../lib/style.js";
import { VERSION } from "../version.js";
import { type Ctx, warn } from "./context.js";

export type ProvisionOptions = {
  admin?: string;
  adminKey?: string;
  upgrade?: boolean;
  agentBinary?: string;
  acmeEmail?: string;
  acmeCa?: string;
  acmeRootCa?: string;
  removeCaddy?: boolean;
};

export const RELEASES = "https://github.com/borgim/sagansync/releases/download";

// adminTarget turns --admin (user@host, or just user; default root) into the
// account provision logs in with. The host defaults to the config's.
export function adminTarget(cfg: Config, opts: Pick<ProvisionOptions, "admin" | "adminKey">): AdminTarget {
  const value = opts.admin ?? "root";
  const at = value.lastIndexOf("@");
  const user = at >= 0 ? value.slice(0, at) : value;
  const host = at >= 0 ? value.slice(at + 1) : cfg.host;
  if (!/^[A-Za-z_][A-Za-z0-9_.-]{0,31}$/.test(user) || !/^[A-Za-z0-9][A-Za-z0-9.:-]*$/.test(host)) {
    throw new CliError(`Invalid --admin "${value}": use user@host, for example root@203.0.113.7.`, 2);
  }
  return { host, port: cfg.sshPort, user, identityFile: opts.adminKey, knownHosts: knownHostsPath() };
}

const ARCHES: Record<string, { name: "amd64" | "arm64"; elfMachine: number }> = {
  x86_64: { name: "amd64", elfMachine: 0x3e },
  aarch64: { name: "arm64", elfMachine: 0xb7 },
  arm64: { name: "arm64", elfMachine: 0xb7 },
};

export function archFor(uname: string): { name: "amd64" | "arm64"; elfMachine: number } {
  const arch = ARCHES[uname.trim()];
  if (!arch) throw new CliError(`The server's architecture "${uname.trim()}" is not supported: sagand is built for x86_64 and arm64.`);
  return arch;
}

// checkBinary makes sure a local --agent-binary is a Linux executable for the
// server's CPU before anything is installed.
export function checkBinary(bin: Buffer, arch: { name: string; elfMachine: number }): void {
  const isElf = bin.length > 20 && bin.readUInt32BE(0) === 0x7f454c46;
  if (!isElf) throw new CliError("--agent-binary is not a Linux executable. Build it with GOOS=linux.", 2);
  if (bin.readUInt16LE(18) !== arch.elfMachine) {
    throw new CliError(`--agent-binary was built for another CPU; the server is ${arch.name}. Build it with GOARCH=${arch.name}.`, 2);
  }
}

async function extractFile(tgz: Buffer, name: string): Promise<Buffer> {
  const ex = tarExtract();
  let found: Buffer | undefined;
  const done = new Promise<void>((resolve, reject) => {
    ex.on("entry", (header, body, next) => {
      const chunks: Buffer[] = [];
      body.on("data", (c) => chunks.push(c as Buffer));
      body.on("end", () => {
        if (header.name.replace(/^\.\//, "") === name) found = Buffer.concat(chunks);
        next();
      });
    });
    ex.on("finish", resolve);
    ex.on("error", reject);
  });
  ex.end(gunzipSync(tgz));
  await done;
  if (!found) throw new CliError(`The release archive does not contain ${name}.`);
  return found;
}

// downloadAgent fetches the sagand release matching this CLI and checks it
// against the release's checksums.txt before it is used.
export async function downloadAgent(arch: string, fetchFn: typeof fetch = fetch, version = VERSION): Promise<Buffer> {
  const base = `${RELEASES}/v${version}`;
  const name = `sagand_${version}_linux_${arch}.tar.gz`;
  const get = async (file: string) => {
    let res: Response;
    try {
      res = await fetchFn(`${base}/${file}`);
    } catch (err) {
      throw new CliError(`Could not download ${base}/${file}: ${(err as Error).message}`, 1, "Check your connection, or pass --agent-binary with a sagand built for the server.");
    }
    if (!res.ok) throw new CliError(`Could not download ${base}/${file} (HTTP ${res.status}).`, 1, "Pass --agent-binary with a sagand built for the server.");
    return Buffer.from(await res.arrayBuffer());
  };
  const sums = (await get("checksums.txt")).toString("utf8");
  const expected = sums.split("\n").map((l) => l.trim().split(/\s+/)).find((parts) => parts[1] === name)?.[0];
  if (!expected) throw new CliError(`checksums.txt of v${version} has no entry for ${name}.`);
  const archive = await get(name);
  const actual = createHash("sha256").update(archive).digest("hex");
  if (actual !== expected) {
    throw new CliError(`Checksum mismatch for ${name}: expected ${expected}, got ${actual}. Nothing was installed.`, 1, "The download is corrupt or was tampered with. Try again later.");
  }
  return extractFile(archive, "sagand");
}

export type BundleFile = { name: string; data: Buffer | string; mode?: number };

// bundle packs the files provision.sh needs into an uncompressed tar.
export async function bundle(files: BundleFile[]): Promise<Buffer> {
  const pack = tarPack();
  const chunks: Buffer[] = [];
  pack.on("data", (c) => chunks.push(c as Buffer));
  const done = new Promise<void>((resolve, reject) => {
    pack.on("end", resolve);
    pack.on("error", reject);
  });
  for (const f of files) pack.entry({ name: f.name, mode: f.mode ?? 0o644 }, f.data);
  pack.finalize();
  await done;
  return Buffer.concat(chunks);
}

export function daemonSettings(opts: ProvisionOptions): string {
  const cfg: Record<string, string> = {};
  if (opts.acmeEmail) cfg.acmeEmail = opts.acmeEmail;
  if (opts.acmeCa) cfg.acmeCA = opts.acmeCa;
  if (opts.acmeRootCa) cfg.acmeRootCA = "/var/lib/sagand/acme-root-ca.pem";
  return JSON.stringify(cfg, null, 2) + "\n";
}

// remoteScript unpacks the bundle into a temporary directory and runs
// provision.sh as root (directly, or through passwordless sudo). It runs under
// sh whatever the admin's login shell is.
export function remoteScript(flags: string[]): string {
  const args = flags.map(shQuote).join(" ");
  const script = `set -e; d=$(mktemp -d); trap 'rm -rf "$d"' EXIT; tar -xf - -C "$d"; ` +
    `if [ "$(id -u)" -eq 0 ]; then bash "$d/provision.sh" "$d" ${args}; else sudo -n bash "$d/provision.sh" "$d" ${args}; fi`;
  return `sh -c ${shQuote(script)}`;
}

function provisionError(stderr: string): CliError {
  const text = stderr.trim();
  if (/sudo: (a password is required|a terminal is required)/.test(text)) {
    return new CliError("The admin account needs root or passwordless sudo.", 1, "Use --admin root@<host>, or allow passwordless sudo for that user.");
  }
  const last = text.split("\n").filter((l) => l.startsWith("✖")).pop();
  return new CliError(last ? last.replace(/^✖\s*/, "") : `Provisioning failed: ${text || "unknown error"}`, 1, undefined,
    last ? text.split("\n").filter((l) => l !== last) : []);
}

// provision installs or upgrades sagand on the VPS with an admin account, then
// checks that the deploy key reaches it.
export async function provision(ctx: Ctx, opts: ProvisionOptions, deps: { shell: Shell; fetch?: typeof fetch }): Promise<void> {
  const pub = `${keyPath(ctx.config)}.pub`;
  if (!fs.existsSync(pub)) throw new CliError(`The deploy key ${pub} does not exist.`, 1, "Run `sagansync init` to create it.");
  if (opts.acmeRootCa && !fs.existsSync(opts.acmeRootCa)) throw new CliError(`${opts.acmeRootCa} does not exist.`, 2);

  const uname = await deps.shell.run("uname -m");
  if (uname.code === 255) throw sshError(uname.stderr);
  if (uname.code !== 0) throw new CliError(`Could not inspect the server: ${uname.stderr.trim()}`);
  const arch = archFor(uname.stdout);

  let agent: Buffer;
  if (opts.agentBinary) {
    agent = fs.readFileSync(opts.agentBinary);
    checkBinary(agent, arch);
  } else {
    ctx.out(`Downloading sagand ${VERSION} for linux/${arch.name}...`);
    agent = await downloadAgent(arch.name, deps.fetch);
  }

  const files: BundleFile[] = [
    { name: "provision.sh", data: fs.readFileSync(packageFile("scripts/provision.sh")), mode: 0o755 },
    { name: "sagand", data: agent, mode: 0o755 },
    { name: "deploy.pub", data: fs.readFileSync(pub) },
    { name: "config.json", data: daemonSettings(opts) },
  ];
  if (opts.acmeRootCa) files.push({ name: "acme-root-ca.pem", data: fs.readFileSync(opts.acmeRootCa) });
  const flags = [...(opts.upgrade ? ["--upgrade"] : []), ...(opts.removeCaddy ? ["--remove-caddy"] : [])];

  const r = await deps.shell.stream(remoteScript(flags), (line) => ctx.out(line), await bundle(files));
  if (r.code === 255) throw sshError(r.stderr);
  if (r.code !== 0) throw provisionError(r.stderr);

  const warning = await checkAgent(ctx.remote);
  if (warning) warn(ctx, warning);
  ctx.out(paint("green", `✔ ${ctx.config.host} is ready. Next: sagansync deploy`));
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd cli && npx tsc --noEmit && npx vitest run test/provision.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cli/scripts/provision.sh cli/src/commands/provision.ts cli/src/lib/paths.ts cli/test/provision.test.ts
git commit -m "feat(cli): install the agent with sagansync provision"
```

---

### Task 4: CLI: ligar o `provision` e empacotar para o npm

**Files:**
- Modify: `cli/src/main.ts`, `cli/package.json`, `.gitignore`

**Interfaces:**
- Consumes: `provision`, `adminTarget` (T3), `adminShell` (T2).
- Produces:
  - `sagansync provision [--admin] [--admin-key] [--upgrade] [--acme-email] [--remove-caddy] [--agent-binary]`, mais as ocultas `--acme-ca` e `--acme-root-ca`.
  - `package.json`: `"files": ["dist", "scripts"]`, `"prepublishOnly": "npm run build"`, `"prepack": "cp ../README.md ../LICENSE ."`.
  - `.gitignore`: ignora as cópias `cli/README.md` e `cli/LICENSE`.

- [ ] **Step 1: Ligar o comando**

`cli/src/main.ts`:

```ts
import { confirm } from "@inquirer/prompts";
import { Command, Option } from "commander";
import type { Ctx } from "./commands/context.js";
import { deploy } from "./commands/deploy.js";
import { dev } from "./commands/dev.js";
import { envList, envSet, envUnset } from "./commands/env.js";
import { askInteractively, init, sshKeygen } from "./commands/init.js";
import { list } from "./commands/list.js";
import { logs } from "./commands/logs.js";
import { adminTarget, provision } from "./commands/provision.js";
import { remove } from "./commands/remove.js";
import { loadConfig } from "./lib/config.js";
import { exitOnBrokenPipe, report } from "./lib/report.js";
import { adminShell, sshRemote, targetFor } from "./lib/ssh.js";
import { VERSION } from "./version.js";

function ctx(): Ctx {
  const cwd = process.cwd();
  const config = loadConfig(cwd);
  return { cwd, config, remote: sshRemote(targetFor(config)), out: (s) => console.log(s) };
}

exitOnBrokenPipe(process.stdout);

const ask = (message: string) => confirm({ message, default: false });
const int = (v: string) => Number.parseInt(v, 10);

const program = new Command()
  .name("sagansync")
  .description("Deploy every git branch to its own HTTPS URL on your own VPS.")
  .version(VERSION);

program.command("init").description("configure this project for a VPS")
  .action(async () => {
    await init(process.cwd(), { ask: askInteractively, confirmOverwrite: () => ask("Overwrite the existing .sagansync/config.json?"), keygen: sshKeygen, out: (s) => console.log(s) });
  });

program.command("provision").description("install or upgrade sagand on the VPS (uses an admin account once)")
  .option("--admin <user@host>", "account with root or passwordless sudo (default: root@<host>)")
  .option("--admin-key <path>", "SSH key for the admin account")
  .option("--upgrade", "only replace the agent and restart it, keeping its settings")
  .option("--acme-email <email>", "email for Let's Encrypt expiry notices")
  .option("--remove-caddy", "remove Caddy left behind by an older SaganSync")
  .option("--agent-binary <path>", "install this sagand binary instead of downloading the release")
  .addOption(new Option("--acme-ca <url>", "ACME directory (tests)").hideHelp())
  .addOption(new Option("--acme-root-ca <path>", "extra CA for the ACME server (tests)").hideHelp())
  .action(async (o) => {
    const c = ctx();
    await provision(c, o, { shell: adminShell(adminTarget(c.config, o)) });
  });

program.command("deploy").description("build and release the current branch with zero downtime")
  .option("-w, --workspace <name>", "workspace to deploy (default: from the git branch)")
  .option("-v, --verbose", "show the full build output")
  .action((o) => deploy(ctx(), o));

program.command("dev").description("run the branch in dev mode and sync local edits live")
  .option("-w, --workspace <name>", "workspace to use (default: from the git branch)")
  .option("-c, --command <cmd>", "dev command run inside the container", "npm run dev")
  .option("--build", "rebuild the dev image")
  .option("--force", "allow dev mode on production")
  .option("-v, --verbose", "show the full build output")
  .action(async (o) => {
    const session = await dev(ctx(), { ...o, onStop: () => process.exit(1) });
    process.once("SIGINT", () => void session.close().then(() => process.exit(0)));
  });

program.command("list").description("show the workspaces of this project")
  .option("-a, --all", "show every project on the VPS")
  .action((o) => list(ctx(), o));

program.command("logs").description("show the logs of a workspace")
  .option("-w, --workspace <name>", "workspace (default: from the git branch)")
  .option("-n, --tail <lines>", "lines from the end", int, 100)
  .option("-f, --follow", "keep streaming new lines")
  .action((o) => logs(ctx(), o));

program.command("remove").description("delete a workspace and everything it uses")
  .option("-w, --workspace <name>", "workspace (default: from the git branch)")
  .option("-y, --yes", "do not ask for confirmation")
  .action((o) => remove(ctx(), { ...o, confirm: ask }));

const env = program.command("env").description("manage environment variables of a workspace");
env.command("set").description("set variables (applied on the next deploy)")
  .argument("[pairs...]", "KEY=VALUE pairs")
  .option("-f, --file <path>", "read KEY=VALUE lines from a file such as .env.production")
  .option("-w, --workspace <name>", "workspace (default: from the git branch)")
  .action((pairs: string[], o) => envSet(ctx(), pairs, o));
env.command("unset").description("remove variables (applied on the next deploy)")
  .argument("<keys...>", "variable names")
  .option("-w, --workspace <name>", "workspace (default: from the git branch)")
  .action((keys: string[], o) => envUnset(ctx(), keys, o));
env.command("list").description("list variable names (values are never shown)")
  .option("-w, --workspace <name>", "workspace (default: from the git branch)")
  .action((o) => envList(ctx(), o));

program.parseAsync(process.argv).catch((err: unknown) => {
  process.exitCode = report(err);
});
```

- [ ] **Step 2: Ajustar o pacote**

```bash
cd cli
npm pkg set 'files[1]=scripts' 'scripts.prepublishOnly=npm run build' 'scripts.prepack=cp ../README.md ../LICENSE .'
```

E o `.gitignore` da raiz fica assim:

`.gitignore`:

```text
node_modules
.sagansync
dist
# copied from the repository root by "npm pack"
cli/README.md
cli/LICENSE
```

- [ ] **Step 3: Verificar**

Run: `cd cli && npx tsc --noEmit && npx vitest run && npm run build && node dist/main.js provision --help`
Expected:
- sem erros de tipo, todos os testes passando;
- a ajuda do `provision` lista `--admin`, `--admin-key`, `--upgrade`, `--acme-email`, `--remove-caddy`, `--agent-binary`, sem `--acme-ca`/`--acme-root-ca`.

O conteúdo do pacote npm (`npm pack`) é conferido na Task 7, quando o README e a LICENSE da raiz já existem.

- [ ] **Step 4: Commit**

```bash
git add cli/src/main.ts cli/package.json .gitignore
git commit -m "feat(cli): add the provision command and prepare the npm package"
```

---

### Task 5: Teste ponta a ponta na VM

**Files:**
- Create: `test/e2e/app/Dockerfile`, `test/e2e/app/server.js`, `test/e2e/run.sh`

**Interfaces:**
- Consumes: toda a CLI (plano 2 e Tasks 2–4) e todo o agente (plano 1 e Task 1).
- Produces: `test/e2e/run.sh [--keep]` — sai com 0 só se todos os cenários passarem. Imprime `N passed, M failed`.
- Pré-requisitos: `limactl` (Lima), `go`, `node` 22.12+, `npm`. O script cria e apaga a VM `sagansync-e2e` (use `--keep` para inspecionar). Leva uns 6 minutos; a primeira vez baixa a imagem do Ubuntu (≈600 MB).

- [ ] **Step 1: Criar o app de exemplo**

`test/e2e/app/Dockerfile`:

```dockerfile
FROM docker.io/library/node:22-alpine
WORKDIR /app
COPY . .
CMD ["node", "server.js"]
```

`test/e2e/app/server.js`:

```js
// Sample app for the end-to-end test: answers its version and one env var,
// logs every request, and has a /health endpoint.
const http = require("http");
const version = process.env.APP_VERSION || "v1";
http.createServer((req, res) => {
  console.log(`request ${req.url}`);
  if (req.url === "/health") return res.end("ok");
  res.end(`${version} ${process.env.GREETING || ""}`.trim() + "\n");
}).listen(3000);
```

- [ ] **Step 2: Criar o script**

`test/e2e/run.sh`:

```bash
#!/usr/bin/env bash
# End-to-end test for SaganSync (spec section 12.4).
#
# Creates a fresh Lima VM (Ubuntu 24.04), installs the agent with the real
# `sagansync provision`, runs Pebble as the ACME server, and checks the spec's
# success criteria with the real CLI: HTTPS deploy, zero downtime, rollback of
# a broken release, the deploy key's restrictions, env vars, dev mode on a
# preview domain, remove, the Podman integration test, upgrade and reboot.
#
# Usage: test/e2e/run.sh [--keep]
#   --keep  leave the VM running afterwards (delete it with: limactl delete -f sagansync-e2e)
#
# Needs: limactl, go, node >= 22.12, npm. Takes about 5 minutes.
set -euo pipefail

VM=sagansync-e2e
KEEP=0
[ "${1:-}" = "--keep" ] && KEEP=1
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
W=$(mktemp -d /tmp/sgs-e2e.XXXXXX) # short path: macOS limits socket paths to 104 bytes
CLI="$ROOT/cli/dist/main.js"
PASSED=0
FAILED=0

cleanup() {
  [ "$KEEP" -eq 1 ] || limactl delete -f "$VM" >/dev/null 2>&1 || true
  rm -rf "$W"
}
trap cleanup EXIT

say() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
pass() { PASSED=$((PASSED + 1)); printf '  \033[32m✔\033[0m %s\n' "$*"; }
fail() { FAILED=$((FAILED + 1)); printf '  \033[31m✖\033[0m %s\n' "$*"; }
# expect <description> <expected substring> <actual>
expect() { if [[ "$3" == *"$2"* ]]; then pass "$1"; else fail "$1 (expected \"$2\", got \"$3\")"; fi; }

vm() { limactl shell "$VM" -- bash -s; }
ssh_port() { limactl list --format '{{.SSHLocalPort}}' "$VM"; }
sagansync() { (cd "$W/app" && SAGANSYNC_HOME="$W/home" node "$CLI" "$@"); }
# get <host> [curl args...]: fetch https://<host>/ inside the VM, trusting Pebble's root
get() {
  local host=$1
  shift
  vm <<EOF
curl -s -m 5 --cacert /var/tmp/pebble-root.pem --resolve $host:443:127.0.0.1 $* https://$host/ || echo "curl failed (\$?)"
EOF
}

for bin in limactl go node npm; do command -v "$bin" >/dev/null || { echo "missing $bin"; exit 1; }; done

say "Building"
(cd "$ROOT/cli" && { [ -d node_modules ] || npm ci --silent; } && npm run build >/dev/null)
start_vm() {
  limactl delete -f "$VM" >/dev/null 2>&1 || true
  limactl start --name "$VM" --tty=false --cpus 4 --memory 4 --set '.mounts=[]' template://ubuntu-24.04 >"$W/lima.log" 2>&1
}
# A fresh VM occasionally fails to boot in time; one retry tells that apart
# from a real test failure.
start_vm || { echo "The VM did not boot; retrying once."; start_vm; } || { cat "$W/lima.log"; exit 1; }
case $(limactl shell "$VM" -- uname -m) in
  x86_64) GOARCH=amd64 ;;
  aarch64) GOARCH=arm64 ;;
  *) echo "unsupported VM architecture"; exit 1 ;;
esac
(cd "$ROOT/agent" && GOOS=linux GOARCH=$GOARCH go build -ldflags "-X main.version=$(node -p "require('$ROOT/cli/package.json').version")" -o "$W/sagand" ./cmd/sagand)
(cd "$ROOT/agent" && GOOS=linux GOARCH=$GOARCH go test -c -tags integration -o "$W/podman.test" ./internal/podman/)

mkdir -p "$W/home/keys" && chmod 700 "$W/home"
ssh-keygen -q -t ed25519 -N "" -C sagansync-e2e -f "$W/home/keys/127.0.0.1_ed25519"
cp -R "$ROOT/test/e2e/app" "$W/app"
(cd "$W/app" && git init -q -b main && git add . && git -c user.email=e2e@test -c user.name=e2e commit -qm v1)
mkdir -p "$W/app/.sagansync"
write_config() {
  cat >"$W/app/.sagansync/config.json" <<EOF
{ "host": "127.0.0.1", "sshPort": $(ssh_port), "project": "app", "internalPort": 3000,
  "domain": "app.test", "previewDomain": "preview.test", "healthPath": "/health" }
EOF
}
write_config
ADMIN=(--admin "$USER@127.0.0.1" --admin-key "$HOME/.lima/_config/user" --agent-binary "$W/sagand")

say "Provision"
out=$(sagansync provision "${ADMIN[@]}" 2>&1) || true
expect "provision installs sagand and reaches it with the deploy key" "is ready" "$out"
vm <<'EOF' >/dev/null 2>&1
sudo podman run -d --name pebble --net host -e PEBBLE_VA_ALWAYS_VALID=1 ghcr.io/letsencrypt/pebble:latest
sleep 3
sudo podman cp pebble:/test/certs/pebble.minica.pem /var/tmp/pebble.minica.pem
sudo chmod 644 /var/tmp/pebble.minica.pem
curl -s --cacert /var/tmp/pebble.minica.pem https://localhost:15000/roots/0 -o /var/tmp/pebble-root.pem
EOF
limactl copy "$VM:/var/tmp/pebble.minica.pem" "$W/pebble.minica.pem"
out=$(sagansync provision "${ADMIN[@]}" --acme-ca https://localhost:14000/dir --acme-root-ca "$W/pebble.minica.pem" 2>&1) || true
expect "provision can run again (now with Pebble as the ACME server)" "is ready" "$out"

say "Deploy key restrictions"
K=(-i "$W/home/keys/127.0.0.1_ed25519" -o IdentitiesOnly=yes -o BatchMode=yes -o "UserKnownHostsFile=$W/home/known_hosts" -p "$(ssh_port)" -l sagan)
out=$(ssh "${K[@]}" 127.0.0.1 bash -c id 2>&1; echo "exit=$?")
expect "a shell command is refused" "exit=126" "$out"
ssh "${K[@]}" -N -L 18080:127.0.0.1:443 127.0.0.1 >/dev/null 2>&1 &
FWD=$!
sleep 2
out=$(curl -s -m 2 -o /dev/null -w "%{http_code}" -k https://127.0.0.1:18080/ || true)
kill "$FWD" 2>/dev/null || true
expect "port forwarding is refused" "000" "$out"
out=$(vm <<<'sudo -u sagan sudo -n true 2>&1 || true')
expect "the sagan user has no sudo" "password is required" "$out"

say "Deploy"
out=$(sagansync deploy 2>&1) || true
expect "deploy reports the URL" "Live at https://app.test" "$out"
expect "the app answers over HTTPS with a valid certificate" "v1" "$(get app.test)"
out=$(vm <<<'curl -s -o /dev/null -w "%{http_code} %{redirect_url}" --resolve app.test:80:127.0.0.1 http://app.test/x')
expect "HTTP redirects to HTTPS" "308 https://app.test/x" "$out"
expect "an unknown host gets no certificate" "curl failed" "$(get nope.test)"

say "Zero downtime"
out=$(sagansync env set APP_VERSION=v2 "GREETING=it's \"quoted\" \$HOME" 2>&1) || true
expect "env set" "Set APP_VERSION, GREETING" "$out"
vm <<'EOF'
rm -f /var/tmp/loop.log /var/tmp/loop.stop
nohup bash -c 'while [ ! -f /var/tmp/loop.stop ]; do curl -s -m 2 -o /dev/null -w "%{http_code}\n" --cacert /var/tmp/pebble-root.pem --resolve app.test:443:127.0.0.1 https://app.test/ >> /var/tmp/loop.log; sleep 0.1; done' >/dev/null 2>&1 &
EOF
sleep 2
sagansync deploy >/dev/null 2>&1 || true
sleep 2
# shellcheck disable=SC2016 # expanded inside the VM
out=$(vm <<<'touch /var/tmp/loop.stop; sleep 1; echo "total=$(wc -l < /var/tmp/loop.log) failed=$(grep -vc "^200$" /var/tmp/loop.log)"')
expect "no request failed while v2 replaced v1 ($out)" "failed=0" "$out"
expect "v2 is live with env values intact" "v2 it's \"quoted\" \$HOME" "$(get app.test)"

say "Broken release"
cp "$W/app/server.js" "$W/server.js.good"
printf 'console.error("boom: missing DATABASE_URL"); process.exit(1);\n' >"$W/app/server.js"
out=$(sagansync deploy 2>&1; echo "exit=$?")
cp "$W/server.js.good" "$W/app/server.js"
expect "a crashing release fails the deploy" "exit=1" "$out"
expect "the crash logs are shown" "boom: missing DATABASE_URL" "$out"
expect "the previous release keeps serving" "v2" "$(get app.test)"

say "list and logs"
expect "list shows production running" "running" "$(sagansync list 2>&1)"
expect "logs show the app's output" "request /" "$(sagansync logs -n 20 2>&1)"

say "Dev mode on a preview domain"
(cd "$W/app" && git checkout -q -b feat-x)
(cd "$W/app" && SAGANSYNC_HOME="$W/home" exec node "$CLI" dev -c "node --watch server.js") >"$W/dev.log" 2>&1 &
DEV=$!
for _ in $(seq 1 120); do grep -q "Watching for changes" "$W/dev.log" && break; sleep 1; done
expect "dev starts on the preview host" "Dev server at https://feat-x-app.preview.test" "$(cat "$W/dev.log")"
sed -i.bak 's/const version = process.env.APP_VERSION || "v1";/const version = "dev edit";/' "$W/app/server.js"
out=""
for _ in $(seq 1 20); do
  out=$(get feat-x-app.preview.test)
  [[ "$out" == *"dev edit"* ]] && break
  sleep 1
done
expect "a local edit shows up on the preview URL" "dev edit" "$out"
kill "$DEV" 2>/dev/null || true
wait "$DEV" 2>/dev/null || true
mv "$W/app/server.js.bak" "$W/app/server.js"
out=$(sagansync remove -w feat-x --yes 2>&1) || true
expect "remove deletes the workspace" "Removed app/feat-x" "$out"
expect "the preview host is gone" "404" "$(get feat-x-app.preview.test -k -o /dev/null -w '%{http_code}')"

say "Podman integration test"
limactl copy "$W/podman.test" "$VM:/var/tmp/podman.test"
out=$(vm <<'EOF'
sudo install -o sagan -m 0755 /var/tmp/podman.test /home/sagan/podman.test
# sagand's reconciliation removes managed containers it does not know about,
# which includes the test's container, so it is stopped while the test runs.
sudo systemctl stop sagand
sudo -iu sagan bash -c 'cd /home/sagan && SAGAN_PODMAN_SOCKET=/run/user/$(id -u)/podman/podman.sock ./podman.test -test.v -test.run TestAgainstRealPodman 2>&1 | tail -20'
sudo systemctl start sagand
EOF
)
expect "the Podman client works against real rootless Podman" "PASS" "$out"

say "Upgrade"
out=$(sagansync provision --upgrade "${ADMIN[@]}" 2>&1) || true
expect "provision --upgrade" "is ready" "$out"
expect "the app still serves after the upgrade" "v2" "$(get app.test)"

say "Reboot"
limactl stop "$VM" >/dev/null 2>&1
limactl start "$VM" >/dev/null 2>&1
write_config # Lima picks a new SSH port on every start
out=""
for _ in $(seq 1 60); do
  out=$(get app.test)
  [[ "$out" == *"v2"* ]] && break
  sleep 1
done
expect "the app comes back after a reboot" "v2" "$out"
expect "the CLI reaches the agent after the reboot" "running" "$(sagansync list 2>&1)"

printf '\n%d passed, %d failed\n' "$PASSED" "$FAILED"
[ "$FAILED" -eq 0 ]
```

- [ ] **Step 3: Rodar**

Run: `chmod +x test/e2e/run.sh && test/e2e/run.sh`
Expected: termina com `26 passed, 0 failed` e código de saída 0, cobrindo os blocos Provision, Deploy key restrictions, Deploy, Zero downtime, Broken release, list and logs, Dev mode on a preview domain, Podman integration test, Upgrade e Reboot.

- [ ] **Step 4: Commit**

```bash
git add test/e2e
git commit -m "test: end-to-end test on a Lima VM with real Podman and Pebble"
```

---

### Task 6: Release e CI

**Files:**
- Create: `.goreleaser.yaml`, `.github/workflows/release.yml`
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- Produces:
  - GoReleaser: `sagand_<versão>_linux_{amd64,arm64}.tar.gz` com só o binário (`-X main.version=<versão>`) e `checksums.txt`.
  - `release.yml`: numa tag `v*`, confere a tag contra `cli/package.json`, publica o agente (GoReleaser) e **depois** a CLI no npm com `--provenance` (precisa do secret `NPM_TOKEN`).
  - `ci.yml`: actions v7 (Node 24), cache do Go apontando para `agent/go.sum`, e um job `shell` com `shellcheck`.

- [ ] **Step 1: Configurar o GoReleaser**

`.goreleaser.yaml`:

```yaml
# Builds the sagand release that `sagansync provision` downloads:
#   sagand_<version>_linux_<arch>.tar.gz (the binary only) and checksums.txt.
# Run by .github/workflows/release.yml when a v* tag is pushed.
version: 2
project_name: sagand

builds:
  - id: sagand
    dir: agent
    main: ./cmd/sagand
    binary: sagand
    env: [CGO_ENABLED=0]
    goos: [linux]
    goarch: [amd64, arm64]
    flags: [-trimpath]
    ldflags: ["-s -w -X main.version={{ .Version }}"]

archives:
  - id: sagand
    formats: [tar.gz]
    name_template: "sagand_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    files: [none*] # the binary only

checksum:
  name_template: checksums.txt
  algorithm: sha256

changelog:
  use: github-native

release:
  github:
    owner: borgim
    name: sagansync
```

- [ ] **Step 2: Workflow de release**

`.github/workflows/release.yml`:

```yaml
name: release

# Pushing a tag such as v0.1.0 publishes the sagand binaries to GitHub Releases
# and then the CLI to npm. The tag must match cli/package.json's version.
on:
  push:
    tags: ["v*"]

jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - name: Tag matches cli/package.json
        run: |
          want="$(node -p "require('./cli/package.json').version")"
          test "${GITHUB_REF_NAME#v}" = "$want" || { echo "tag $GITHUB_REF_NAME does not match version $want"; exit 1; }

  agent:
    needs: check
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@v7
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v7
        with:
          go-version-file: agent/go.mod
          cache-dependency-path: agent/go.sum
      - uses: goreleaser/goreleaser-action@v7
        with:
          version: "~> v2"
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}

  cli:
    # After the agent: `sagansync provision` downloads the release published above.
    needs: agent
    runs-on: ubuntu-latest
    permissions:
      contents: read
      id-token: write # npm provenance
    defaults:
      run:
        working-directory: cli
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-node@v7
        with:
          node-version: 22
          registry-url: https://registry.npmjs.org
      - run: npm ci
      - run: npm publish --provenance --access public
        env:
          NODE_AUTH_TOKEN: ${{ secrets.NPM_TOKEN }}
```

- [ ] **Step 3: Atualizar o CI**

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
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version-file: agent/go.mod
          cache-dependency-path: agent/go.sum
      - name: gofmt
        run: test -z "$(gofmt -l .)" || (gofmt -l . && exit 1)
      - run: go vet ./...
      - run: go test -race ./...

  cli:
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: cli
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-node@v7
        with:
          node-version: 22
          cache: npm
          cache-dependency-path: cli/package-lock.json
      - uses: actions/setup-go@v7 # the contract test builds sagand
        with:
          go-version-file: agent/go.mod
          cache-dependency-path: agent/go.sum
      - run: npm ci
      - run: npm run typecheck
      - run: npm test
      - run: npm run build

  shell:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - name: shellcheck
        run: shellcheck cli/scripts/provision.sh test/e2e/run.sh
```

- [ ] **Step 4: Validar**

Run:
```bash
go run github.com/rhysd/actionlint/cmd/actionlint@latest .github/workflows/*.yml
go run github.com/goreleaser/goreleaser/v2@v2.18.2 check
go run github.com/goreleaser/goreleaser/v2@v2.18.2 release --snapshot --clean && ls dist/ && tar -tzf dist/*linux_arm64.tar.gz; rm -rf dist
```
Expected: `actionlint` sem saída; `1 configuration file(s) validated`; `dist/` com `checksums.txt` e os dois `sagand_<versão>-SNAPSHOT-<sha>_linux_{amd64,arm64}.tar.gz`, cada um contendo só `sagand`. (`--snapshot` não publica nada e não gera changelog; os nomes de verdade não têm o sufixo `-SNAPSHOT`.) O `dist/` da raiz é apagado no fim; ele já está no `.gitignore`.

O `shellcheck` roda no CI (job `shell`). Para rodar antes do push, sem instalar nada no Mac: `test/e2e/run.sh --keep`, depois `limactl copy` dos dois scripts para a VM, `sudo apt-get install -y shellcheck` e `shellcheck` neles.

- [ ] **Step 5: Commit**

```bash
git add .goreleaser.yaml .github/workflows/release.yml .github/workflows/ci.yml
git commit -m "ci: release the agent and the CLI from tags; update actions and add shellcheck"
```

---

### Task 7: README, licença e pendências

**Files:**
- Modify: `README.md`, `docs/superpowers/followups-v0.1.md`
- Create: `LICENSE`

**Interfaces:**
- Produces: README em inglês (o repositório é público e o pacote vai para o npm). Ele cobre requisitos (inclusive "Windows não é suportado ainda"), quickstart, DNS (registro.br, Cloudflare DNS only, Certificate Transparency, limite do Let's Encrypt), comandos, configuração, funcionamento, solução de problemas e desenvolvimento. Mais a `LICENSE` MIT e o arquivo de pendências atualizado.

- [ ] **Step 1: README**

`README.md`:

````markdown
<p align="center">
  <img src="./sagansync.png" width="80" alt="" />
  <h1 align="center">SaganSync</h1>
</p>

**Every git branch gets its own HTTPS URL on your own VPS.** Deploy with zero downtime, preview branches on their own subdomains, and live-edit a branch on the server while you code. No CI pipeline, no Kubernetes, no platform account: one small agent on the server and a CLI on your machine.

```console
$ git switch -c feat-login
$ sagansync deploy
▸ Building image
▸ Waiting for the health check
▸ Getting the TLS certificate
✔ Live at https://feat-login-myapp.example.com
```

- **Zero-downtime deploys.** The new release must pass a health check before traffic moves to it. If it fails, the old one keeps serving and you see its last log lines.
- **A URL per branch.** `main` goes to your domain; every other branch gets `<branch>-<project>.<your-preview-domain>`, each with its own certificate.
- **Live dev mode.** `sagansync dev` runs the branch in dev mode on the server and syncs every file you save, so you can share a URL with a client while you work.
- **Secure by default.** After a one-time setup nothing runs as root, containers are rootless, and the deploy key can only run the agent's commands: no shell, no tunnels.

> Inspired by indie devs and Carl Sagan: clarity, connection and exploration.

## Requirements

- **Server:** Ubuntu 24.04+ or Debian 12+ (x86_64 or arm64), with root or passwordless sudo for the one-time setup, and ports 80 and 443 free.
- **Your machine:** macOS or Linux with Node.js 22.12+ and OpenSSH. Windows is not supported yet (its OpenSSH lacks connection sharing).
- **Your project:** a `Dockerfile` (or `Containerfile`; if both exist, `Containerfile` wins, as in Podman) that starts an HTTP server.

## Quick start

```sh
npm install -g sagansync

cd my-project
sagansync init                                  # server address, project name, domains
sagansync provision --admin root@203.0.113.7    # installs the agent (once per server)
sagansync deploy
```

`init` writes `.sagansync/config.json`, which holds no secrets and can be committed, and creates a deploy key in `~/.config/sagansync/keys/`.

## DNS

Point your production domain at the server, and add **one wildcard record** for branch previews:

| Record | Points to |
| --- | --- |
| `A  api.example.com` | your server's IP |
| `A  *.example.com` | your server's IP (covers the previews of every project) |

With `"previewDomain": "example.com"`, branch `feat-login` of project `myapp` is served at `feat-login-myapp.example.com`. Explicit records such as `www` or `mail` keep working.

- **Registro.br** may not accept `*` records. Keep the domain registered there and move its nameservers to Cloudflare (free), creating the wildcard as **DNS only** (grey cloud). Cloudflare's proxy (orange cloud) is not supported.
- Every hostname gets its own Let's Encrypt certificate, and certificates are public in Certificate Transparency logs (e.g. crt.sh). Avoid branch names you don't want published.
- Let's Encrypt allows 50 certificates per registered domain per week, which is plenty for branches.

`sagansync deploy` warns when a hostname does not point at the server yet.

## Commands

| Command | What it does |
| --- | --- |
| `sagansync init` | Configure the project and create its deploy key. |
| `sagansync provision [--admin user@host] [--admin-key path]` | Install or repair the agent. `--upgrade` only replaces it, `--acme-email` sets your Let's Encrypt contact, `--remove-caddy` removes the Caddy an older SaganSync installed. |
| `sagansync deploy [-w workspace] [-v]` | Build and release the current branch with zero downtime. |
| `sagansync dev [-c "npm run dev"] [--build]` | Run the branch in dev mode and sync local edits. Refuses `production` unless `--force`. Stops if you switch branches. |
| `sagansync list [--all]` | Workspaces of this project (or of the whole server). |
| `sagansync logs [-n 100] [-f]` | Container logs. |
| `sagansync env set KEY=VALUE … \| --file .env.production` | Set variables for the next deploy. Values travel over SSH stdin, never on a command line. |
| `sagansync env unset KEY … / env list` | Remove or list variables (values are never shown). |
| `sagansync remove [-y]` | Delete a workspace: container, releases, files, variables. |

Workspaces come from the git branch: `main`/`master` → `production`, `develop`/`dev` → `staging`, anything else → the branch name. Use `-w` to choose one.

Files in `.gitignore` and `.dockerignore` are not uploaded, and `.git`, `node_modules`, `.sagansync` and every `.env*` file never are.

## Configuration

`.sagansync/config.json`:

```jsonc
{
  "host": "203.0.113.7",          // server hostname or IP
  "sshPort": 22,
  "project": "myapp",
  "internalPort": 3000,           // port your app listens on inside the container
  "domain": "api.example.com",    // optional: production
  "previewDomain": "example.com", // optional: branches at <branch>-<project>.example.com
  "healthPath": "/health",        // optional: HTTP health check (default: TCP)
  "healthTimeout": 60             // optional, seconds
}
```

## How it works

```
your machine                           server
sagansync ──ssh (deploy key)──▶ sshd ─▶ sagand gateway ─▶ sagand daemon ─▶ rootless Podman
                                        (forced command)   ├ HTTPS proxy + Let's Encrypt
                                                           └ zero-downtime swaps, state
```

`provision` creates an unprivileged `sagan` user, installs Podman and the `sagand` agent as a hardened systemd service, and authorizes the deploy key **only** for `sagand gateway`. The daemon binds ports 80/443 through `CAP_NET_BIND_SERVICE` alone, talks to Podman through its API, and never runs a shell. After a reboot it brings every workspace back on its own.

The design is documented in [`docs/superpowers/specs`](docs/superpowers/specs/2026-09-24-sagansync-v0.1-agent-design.md).

## Troubleshooting

- **`REMOTE HOST IDENTIFICATION HAS CHANGED`**: if you reinstalled the server, delete its line from `~/.config/sagansync/known_hosts`.
- **`The admin account needs root or passwordless sudo`**: use `--admin root@<host>`, or allow passwordless sudo for that user.
- **`Ports 80/443 are already in use`**: stop the web server that holds them (nginx, apache…), then run `provision` again.
- **A deploy fails its health check**: the last lines of the container's log are printed; `sagansync logs` shows more. Your previous release is still live.

## Development

```
agent/   Go: the sagand daemon, gateway and client        cd agent && go test -race ./...
cli/     TypeScript: the sagansync CLI                      cd cli && npm test
test/e2e End-to-end test on a Lima VM with real Podman     test/e2e/run.sh
```

The end-to-end test needs [Lima](https://lima-vm.io), Go and Node. It creates an Ubuntu 24.04 VM, provisions it with the real CLI, uses [Pebble](https://github.com/letsencrypt/pebble) as the ACME server, and checks deploys, zero downtime, rollback, dev mode, the deploy key's restrictions, upgrade and reboot.

Releases are cut by pushing a `v*` tag matching `cli/package.json`'s version: GitHub Actions publishes the agent binaries to GitHub Releases and then the CLI to npm.

## License

MIT
````

- [ ] **Step 2: Licença**

`LICENSE`:

```text
MIT License

Copyright (c) 2026 Pedro Borges

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

- [ ] **Step 3: Pendências**

No `docs/superpowers/followups-v0.1.md`:
- marque como resolvido o item do `Containerfile`, com a linha: `- [x] **\`Containerfile\` vs \`Dockerfile\`:** verificado na VM (Podman 4.9.3): com os dois presentes, o \`Containerfile\` é usado, como no Podman. Documentado no README.`
- apague a seção `### Para o plano 3` inteira, que as Tasks 4 e 7 cobrem.

- [ ] **Step 4: Verificar o pacote**

Run: `cd cli && npm pack --dry-run 2>&1 | grep -E "README|LICENSE|total files"; rm -f README.md LICENSE`
Expected: `README.md` e `LICENSE` listados e `total files: 5`.

- [ ] **Step 5: Commit**

```bash
git add README.md LICENSE docs/superpowers/followups-v0.1.md
git commit -m "docs: rewrite the README for v0.1 and add the MIT license"
```

---

## Depois deste plano: publicar a v0.1.0 (decisão sua)

Nada é publicado pela execução deste plano. Quando quiser lançar:
1. No npmjs.com, crie um token de automação e salve como secret `NPM_TOKEN` no repositório do GitHub (Settings → Secrets and variables → Actions).
2. `git tag v0.1.0 && git push origin v0.1.0`. O workflow `release` publica os binários do agente e depois a CLI.
3. Num VPS de verdade: `npm i -g sagansync`, `sagansync init`, `sagansync provision --admin root@<ip>`, `sagansync deploy`.
