# SaganSync v0.1 — agente `sagand` — Design

- **Data:** 2026-09-24
- **Status:** em revisão
- **Roadmap geral:** `~/Downloads/sagansync-roadmap.md`

## 1. Contexto e objetivo

O SaganSync é uma CLI que faz deploy de um projeto local para um VPS próprio, com um workspace (e subdomínio) por branch de git. Hoje ele cola ferramentas existentes (tar + `podman build/run` + Caddy via SSH), sem lógica própria, e tem falhas de segurança (host key ignorada, injeção de shell, `.env` enviado ao servidor).

A v0.1 introduz o **`sagand`**, um agente em Go que roda no VPS e substitui o Caddy, e reescreve a CLI em cima dele.

**Objetivos:**
- Ferramenta usável de verdade e peça de portfólio.
- Deploy sem downtime.
- Superfície de ataque mínima: nada roda como root depois do provisionamento.
- Menos dependências no servidor: apenas Podman + um binário.

**Critérios de sucesso** (verificados na VM local, seção 12):
1. Numa VM nova: `sagansync init` → `provision` → `deploy` resulta numa URL HTTPS funcionando.
2. Um segundo `deploy` com tráfego contínuo (1 requisição a cada 100 ms) não tem nenhuma requisição falha.
3. Um deploy quebrado mantém a versão anterior no ar e mostra os logs do erro.
4. Depois do reboot da VM, os apps voltam sozinhos.
5. `list`, `logs`, `remove` e `dev` funcionam.
6. A chave de deploy não abre shell; nenhum comando fora da lista permitida é executado.
7. CLI publicada no npm; binários do agente publicados no GitHub Releases.

## 2. Escopo

**Dentro:**
- Agente `sagand`: TLS automático, reverse proxy, deploy sem downtime, estado dos workspaces, reconciliação no boot.
- Usuário dedicado `sagan`, Podman rootless, chave de deploy com forced command.
- Comandos da CLI: `init`, `provision`, `deploy`, `list`, `logs`, `remove`, `dev`.
- Variáveis de ambiente por workspace (seção 8.3).
- Correções de segurança da CLI.
- CI (testes unitários) e pipeline de release.

**Fora (versões futuras):**
- `dev` com sync por stream e manifesto de hashes (v0.2).
- Scale to zero e limpeza automática de branches.
- Services / banco de dados.
- Comando `rollback` (a retenção de releases já deixa isso preparado).
- Suporte a Docker como runtime.
- API HTTP pública, webhooks, painel web.
- Teste ponta a ponta no CI e com domínio público.

**Requisitos do servidor:** Ubuntu 24.04+ ou Debian 12+ (Podman 4.3+ com API `v4.0.0`, cgroups v2), `amd64` ou `arm64`, acesso root ou sudo para o provisionamento. O Ubuntu 22.04 fica de fora porque vem com Podman 3.4.

**Requisitos locais:** Node 20+, `ssh` do OpenSSH.

## 3. Arquitetura

```
local                                   VPS
┌────────────────────┐   ssh sagan@vps  ┌─────────────────────────────────────────────┐
│ sagansync CLI (TS) │─────────────────▶│ sshd → forced command: sagand gateway       │
│  - empacota        │  (ControlMaster) │          │ (cliente, roda como `sagan`)     │
│  - mostra eventos  │◀─ eventos JSON ──│          ▼ HTTP sobre /run/sagand/sagand.sock│
└────────────────────┘                  │ sagand daemon (systemd, User=sagan)         │
                                        │  ├─ proxy :80/:443 + certmagic              │
                                        │  ├─ deploy / estado / reconciliação         │
                                        │  └─ API REST do Podman (socket do usuário)  │
                                        │          ▼                                  │
                                        │ podman rootless (usuário sagan)             │
                                        │  sagan_app_production_<id>, sagan_app_feat-x│
                                        └─────────────────────────────────────────────┘
```

O binário `sagand` tem dois papéis:
- **Daemon** (`sagand daemon`): processo de longa duração no systemd.
- **Cliente** (`sagand gateway` e subcomandos): processo curto executado via SSH, que traduz o comando em uma chamada HTTP ao socket do daemon e repassa o stream de eventos ao stdout.

## 4. Modelo de segurança

### 4.1 Provisionamento (único momento com root)

`sagansync provision --admin <user@host>` conecta com o usuário admin (root, ou um usuário com sudo) e executa `provision.sh`, que é idempotente:

1. Instala `podman`, `uidmap`, `passt`/`slirp4netns`, `dbus-user-session`.
2. Cria o usuário `sagan`:
   - `useradd --system --create-home --home-dir /home/sagan --shell /bin/sh sagan`.
   - O shell precisa ser `/bin/sh`, e não `nologin`, porque o sshd executa o forced command através do shell do usuário. O bloqueio de acesso interativo vem do forced command com `restrict` (4.2).
   - Sem senha (`passwd -l`), sem sudo e fora de qualquer grupo privilegiado.
   - Faixas de subuid/subgid adicionadas explicitamente (`usermod --add-subuids/--add-subgids`), porque o `useradd --system` não cria.
3. `loginctl enable-linger sagan` e `systemctl --user enable --now podman.socket` como `sagan`.
4. Instala `/usr/local/bin/sagand` (seção 10) e a unit `sagand.service`.
5. Grava a chave pública de deploy em `/home/sagan/.ssh/authorized_keys`, no formato de 4.2.
6. Cria `/srv/sagan` e `/var/lib/sagand`, ambos pertencentes a `sagan`, com modo `0700`.
7. Se o Caddy estiver instalado (versão anterior do SaganSync), para o serviço e remove, com confirmação.

Depois disso, a CLI **nunca mais** usa o usuário admin. Ele não é salvo na configuração.

### 4.2 Chave de deploy com forced command

```
command="/usr/local/bin/sagand gateway",restrict ssh-ed25519 AAAA... sagansync-<projeto>
```

- O `restrict` desliga pty, port forwarding, agent forwarding e X11.
- O `sagand gateway` lê `SSH_ORIGINAL_COMMAND`, divide em argumentos **sem usar shell** e só aceita os subcomandos da lista permitida: `version`, `host`, `deploy`, `list`, `logs`, `remove`, `dev`, `put`, `rm`, `env`.
- Qualquer outro comando (incluindo vazio, ou seja, uma tentativa de shell) é rejeitado com código de saída 126 e uma mensagem.
- Resultado: uma chave vazada permite fazer deploy, mas não abre shell nem túnel.

### 4.3 Unit do daemon

```ini
[Unit]
After=network-online.target user@<uid-sagan>.service
Requires=user@<uid-sagan>.service

[Service]
User=sagan
ExecStart=/usr/local/bin/sagand daemon
RuntimeDirectory=sagand
RuntimeDirectoryMode=0700
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=tmpfs
BindPaths=/run/user/<uid-sagan>/podman
PrivateTmp=yes
ReadWritePaths=/srv/sagan /var/lib/sagand
Restart=always
```

- O socket da API é `/run/sagand/sagand.sock`, com modo `0600`.
- `ProtectHome=tmpfs` esconde `/home`, `/root` e `/run/user`. O `BindPaths` devolve só a pasta do socket do Podman. O teste ponta a ponta valida essa combinação.
- O daemon **não executa o binário `podman`**. Ele fala com a API REST do Podman em `/run/user/<uid>/podman/podman.sock`. Isso é obrigatório: o Podman rootless depende do `newuidmap` (setuid), que não funciona com `NoNewPrivileges`. O serviço do Podman roda como unit de usuário, fora da blindagem.

### 4.4 Containers

- Rootless, no user namespace de `sagan`.
- Criados com `no-new-privileges`.
- A porta interna é publicada apenas em `127.0.0.1`, numa porta aleatória do host.

### 4.5 Validação no agente

O agente não confia na CLI.

- Projeto e workspace: `^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`.
- `domain` e `previewDomain`: hostname válido (rótulos `[a-z0-9-]` de 1 a 63 caracteres, total até 253), sem protocolo, porta ou curinga, com pelo menos dois rótulos.
- Portas: 1–65535. `healthPath` começa com `/`, com até 200 caracteres.
- Nomes de variáveis de ambiente: `^[A-Za-z_][A-Za-z0-9_]*$`.
- Nenhuma string entra em comando de shell. O agente não usa shell em lugar nenhum.
- Extração de tar (`release`):
  - rejeita caminhos absolutos e componentes `..`;
  - symlinks: o alvo precisa ser relativo e **não pode conter `..`**. Uma checagem só do destino resolvido pode ser burlada com symlinks encadeados;
  - hardlinks: o alvo precisa ser um arquivo regular já extraído dentro da release;
  - antes de gravar qualquer arquivo, o diretório pai (com symlinks resolvidos) precisa estar dentro da release. Isso também protege o `put` do `dev` contra symlinks criados pelo próprio container no bind mount;
  - rejeita tipos de arquivo especiais (device, fifo);
  - limites: 500 MB descompactados e 100.000 entradas.

### 4.6 Lado da CLI

- `StrictHostKeyChecking=accept-new` com `UserKnownHostsFile=~/.config/sagansync/known_hosts`. O valor `StrictHostKeyChecking=no` é proibido no código e há um teste que garante isso.
- Chave de deploy em `~/.config/sagansync/keys/<host>_ed25519` (modo `0600`), fora do projeto.
- ControlMaster: `ControlPath=~/.config/sagansync/cm-%C`, `ControlPersist=60s`.
- O empacotamento respeita `.gitignore` e `.dockerignore` e **sempre** exclui `.git`, `node_modules`, `.sagansync` e `.env*`.

## 5. Fluxo de deploy

### 5.1 Sequência

```
CLI                                   sagand
empacota (tar.gz)
ssh sagan@vps sagand deploy <flags> ─▶ 1. valida; adquire trava do workspace (sem fila)
   tar.gz pelo stdin                  2. extrai em /srv/sagan/<p>/<w>/releases/<id>/
◀── eventos JSON ──────────────────── 3. build da imagem localhost/sagan_<p>_<w>:<id> (logs em stream)
                                      4. cria e inicia sagan_<p>_<w>_<id> (127.0.0.1:<aleatória>)
                                      5. health check
                                      6a. OK → troca a rota (atômico), persiste o estado,
                                          espera o dreno, para e remove o container antigo,
                                          aplica a retenção
                                      6b. falha → captura as últimas 50 linhas de log,
                                          remove o container novo; o antigo não é tocado
                                      7. libera a trava
```

- **Nomes:** container `sagan_<p>_<w>_<id>`, imagem `localhost/sagan_<p>_<w>:<id>`. O separador é `_`, que não é permitido em nomes de projeto nem de workspace. Com `-`, os pares (`a-b`, `c`) e (`a`, `b-c`) gerariam o mesmo nome.
- **Id da release:** `YYYYMMDD-HHMMSS-<sha7>` (o sha vem do commit atual, ou `nogit`).
- **Trava:** mutex por `(projeto, workspace)` no daemon. Se já houver um deploy em andamento, retorna erro `busy` na hora.
- **Health check:**
  - com `healthPath`, faz `GET http://127.0.0.1:<porta><healthPath>` a cada 500 ms e espera 2xx ou 3xx;
  - sem ele, espera a conexão TCP ser aceita;
  - prazo: `healthTimeout` (padrão 60 s);
  - falha imediata se o container sair antes.
- **Dreno:** 10 s depois da troca, `stop` com SIGTERM e 10 s de tolerância antes do SIGKILL. Depois `remove`.
- **Retenção:** as 3 releases mais recentes (diretório + imagem). As mais antigas são apagadas.
- **Cancelamento:** se a conexão SSH cair durante o deploy, o daemon **conclui ou reverte** a operação sozinho. O deploy não depende do cliente continuar conectado.

### 5.2 Domínios por workspace

Nomes de workspace a partir da branch:
- `main` ou `master` → `production`.
- `develop` ou `dev` → `staging`.
- Outras → nome da branch sanitizado.

O hostname de cada workspace é **calculado pelo agente** (pacote `domains`), a partir de `domain` e do opcional `previewDomain`, que a CLI envia:

| Workspace | Sem `previewDomain` | Com `previewDomain` |
| --- | --- | --- |
| `production` | `<domain>` | `<domain>` |
| outros (inclusive `staging`) | `<workspace>.<domain>` | `<workspace>-<projeto>.<previewDomain>` |

Exemplo: `project = "barbervip"`, `domain = "api.pedroborgim.com.br"` e `previewDomain = "pedroborgim.com.br"`:
- `production` → `api.pedroborgim.com.br`
- `feat-login` → `feat-login-barbervip.pedroborgim.com.br`

Regras adicionais:
- **Limite de 63 caracteres por rótulo DNS:** se `<workspace>-<projeto>` passar de 63, o agente usa os primeiros 55 caracteres (sem `-` no final), mais `-` e os 7 primeiros hex do SHA-1 do rótulo completo. Com isso o nome continua determinístico e único.
- **Sem `domain`:** o workspace fica acessível só pela porta local, que o `list` mostra. `previewDomain` sem `domain` é permitido: a produção fica sem URL pública e as branches usam o `previewDomain`.
- **Colisão:** dois workspaces que resultem no mesmo hostname fazem o deploy do segundo falhar com `error{code:"host_conflict"}`.
- O `done.url` informa o hostname final, e a CLI apenas o exibe. A CLI não replica essa lógica.

### 5.3 DNS

O SaganSync **não gerencia DNS**. O usuário cria os registros uma vez, e o `sagand` roteia pelo cabeçalho `Host`.

**Registros necessários:**

| Configuração | Registros |
| --- | --- |
| Só `domain` | `A <domain> → IP` e, para branches, `A *.<domain> → IP` |
| `domain` + `previewDomain` (recomendado) | `A <domain> → IP` e `A *.<previewDomain> → IP` (**um curinga para todos os projetos**) |

Registros explícitos têm precedência sobre o curinga. `www`, `mail` e similares não são afetados.

**Certificados:** um por hostname, via HTTP-01. Isso só exige o hostname resolvendo para o VPS, sem API de DNS. Um certificado curinga via DNS-01 fica fora da v0.1.

**Verificação na CLI:** antes do `deploy` e do `dev`, a CLI resolve o hostname esperado e o `host` do VPS.
- Se os IPs não coincidirem, ou o hostname não resolver, mostra o aviso `dns_mismatch` com os registros a criar e **continua** o deploy.
- Como a CLI não replica o cálculo do agente, ela usa o `sagand host <projeto> <workspace>`, um comando só de leitura. Esse comando entra na lista permitida do gateway.

**O README documenta:**
- **Registro.br:** há relatos de que o editor de DNS do registro.br não aceita `*`. Se for o caso, a alternativa é manter o domínio no registro.br e apontar os nameservers para a Cloudflare (gratuita), criando o curinga em modo **DNS only**.
- **Cloudflare com proxy (nuvem laranja)** não é suportado na v0.1: usar DNS only.
- **Certificate Transparency:** hostnames de branch ficam visíveis em logs públicos (ex.: crt.sh). Evitar nomes sensíveis em branches, ou esperar o certificado curinga.
- **Limite do Let's Encrypt:** 50 certificados por domínio registrado por semana.

## 6. Proxy e TLS

- **:443:** TLS via certmagic. O certificado é escolhido por SNI, e o proxy encaminha para o upstream do domínio, com `X-Forwarded-For`, `X-Forwarded-Proto` e `X-Forwarded-Host`. Suporta WebSocket (padrão do `httputil.ReverseProxy`).
- **:80:** responde ao desafio ACME HTTP-01 e redireciona o resto para HTTPS com 308.
- **Tabela de rotas:** mapa `domínio → upstream` protegido por `sync.RWMutex`, trocado atomicamente no passo 6a. Requisições em andamento terminam no upstream antigo.
- **Emissão de certificados:** apenas para domínios presentes no estado (`DecisionFunc` do certmagic). Um domínio desconhecido recebe 404 e não dispara emissão.
- **Armazenamento dos certificados:** `/var/lib/sagand/certs` (`0700`).
- **E-mail ACME:** opcional, informado no `provision` e salvo em `/var/lib/sagand/config.json`.
- **Configuração do daemon** (`/var/lib/sagand/config.json`): `acmeEmail`, `acmeCA` (padrão: Let's Encrypt produção) e `acmeRootCA` (caminho para uma CA extra, usada nos testes com Pebble).

## 7. Estado e reconciliação

`/var/lib/sagand/state.json`:

```jsonc
{
  "version": 1,
  "projects": {
    "myapp": {
      "workspaces": {
        "production": {
          "host": "api.example.com",     // hostname final calculado (5.2)
          "domain": "api.example.com",
          "previewDomain": null,
          "internalPort": 3000,
          "healthPath": "/health",
          "mode": "deploy",              // "deploy" | "dev"
          "release": "20260924-101500-a1b2c3d",
          "container": "sagan_myapp_production_20260924-101500-a1b2c3d",
          "hostPort": 41873,
          "updatedAt": "2026-09-24T10:15:30Z"
        }
      }
    }
  }
}
```

- **Escrita atômica:** grava `state.json.tmp`, faz `fsync` e depois `rename`. Um `.tmp` que sobrar é ignorado e apagado no boot.
- O daemon é a **fonte da verdade**. No boot, e a cada 60 s:
  - para cada workspace, garante que o container exista e esteja rodando. Se não estiver, inicia e atualiza `hostPort`;
  - reconstrói a tabela de rotas;
  - containers com o label `sagan.managed=true` que não constam no estado são parados e removidos (órfãos de um deploy interrompido). Workspaces com operação em andamento são ignorados.
- Os containers **não** usam `--restart`, porque a reconciliação cuida disso.

## 8. Comandos

### 8.1 Protocolo de eventos

O cliente `sagand` escreve no stdout um JSON por linha:

```jsonc
{"v":1,"type":"step","name":"build"}
{"v":1,"type":"log","stream":"build","line":"STEP 1/6: FROM node:22-alpine"}
{"v":1,"type":"warn","code":"tls_pending","message":"..."}
{"v":1,"type":"done","url":"https://api.example.com","release":"20260924-...","hostPort":41873}
{"v":1,"type":"error","code":"health_failed","message":"...","logs":["...últimas 50 linhas..."]}
```

- Código de saída: 0 em caso de sucesso, 1 em erro de operação, 2 em erro de validação, 126 para comando não permitido.
- A CLI desenha o progresso a partir dos eventos. `--verbose` mostra todos os `log`.
- **Compatibilidade de versões:** a CLI chama `sagand version` (resposta: `{"version":"0.1.0","protocol":1}`). Se o protocolo for diferente, a CLI aborta e sugere `sagansync provision --upgrade`. Se só a versão for diferente, a CLI apenas avisa.

### 8.2 Comandos da CLI

| CLI | Remoto | Comportamento |
| --- | --- | --- |
| `init` | — | Pergunta host, projeto (padrão: nome da pasta sanitizado), porta interna, domínio, `previewDomain`, `healthPath`. Mostra os registros DNS a criar (5.3). Gera a chave de deploy. Grava `.sagansync/config.json` e `.sagansync/.gitignore`. |
| `provision [--admin u@h] [--upgrade] [--agent-binary <arquivo>] [--acme-email <e>]` | SSH como admin | Seção 4.1. `--upgrade` só troca o binário e reinicia o daemon. |
| `deploy [-w <workspace>] [--verbose]` | `sagand deploy` | Seção 5. O workspace vem da branch atual. |
| `list` | `sagand list` | Tabela: workspace, modo, release, status, porta, URL. |
| `logs [-w] [-f] [--tail N]` | `sagand logs` | Logs do container atual. `-f` segue até Ctrl+C. |
| `remove [-w] [--yes]` | `sagand remove` | Para e remove o container, a rota, as releases e as imagens. Remove o workspace do estado e apaga o arquivo de env. Pede confirmação. |
| `dev [-w] [--command <cmd>] [--build] [--force]` | `sagand dev`, `put`, `rm` | Seção 8.4. |
| `env set/unset/list [-w]` | `sagand env` | Seção 8.3. |

### 8.3 Variáveis de ambiente por workspace

Apps reais precisam de configuração (`DATABASE_URL`, chaves de API). Como o `.env` local nunca é enviado, a v0.1 tem um mecanismo explícito:

- `sagansync env set KEY=VALUE [-w ws]`, `env unset KEY`, `env list` (mostra as chaves, com os valores mascarados).
- O agente guarda em `/var/lib/sagand/env/<p>/<w>.json` (`0600`, objeto JSON, para preservar valores com quebras de linha e aspas) e injeta no container na criação.
- Uma mudança de env só vale no próximo `deploy` (ou `dev`). A CLI avisa isso.
- O valor chega ao agente pelo stdin, e não pela linha de comando, para não aparecer em `ps` nem em logs.

### 8.4 `dev` adaptado

- A trava de produção continua: workspace `production` exige `--force` (verificado na CLI **e** no agente).
- Também continua a trava de troca de branch: o `dev` para se `.git/HEAD` mudar.
- **Início:** `sagand dev --project p --workspace w --command "<cmd>" [--build]` com o tar.gz pelo stdin. O agente:
  1. extrai em `/srv/sagan/<p>/<w>/dev/`;
  2. faz o build da imagem se ela não existir ou se `--build` for passado;
  3. troca o container do workspace por um em modo dev: bind mount do diretório em `/app`, volume anônimo em `/app/node_modules`, `NODE_ENV=development` e o comando informado;
  4. registra a rota e marca `mode: "dev"` no estado.
- O health check é só TCP, com prazo de 30 s. Se falhar, a CLI apenas avisa e o `dev` continua.
- **Sincronização:** a cada mudança local, `sagand put <p> <w> <caminho-relativo>` com o conteúdo pelo stdin. Remoção: `sagand rm <p> <w> <caminho>`. O agente aplica as mesmas regras de caminho da extração de tar. Todas as chamadas reaproveitam a conexão ControlMaster.
- O `deploy` seguinte no mesmo workspace substitui o container de dev normalmente e volta para `mode: "deploy"`.

### 8.5 Configuração do projeto

`.sagansync/config.json`:

```jsonc
{
  "host": "vps.example.com",
  "sshPort": 22,
  "user": "sagan",
  "project": "myapp",
  "internalPort": 3000,
  "domain": "api.example.com",   // opcional: produção
  "previewDomain": "example.com", // opcional: branches em <workspace>-<projeto>.example.com
  "healthPath": "/health",       // opcional
  "healthTimeout": 60            // opcional
}
```

A chave de deploy é localizada por convenção (`~/.config/sagansync/keys/<host>_ed25519`) e pode ser sobrescrita com `identityFile`.

## 9. Organização do código

```
sagansync/
├── cli/                         # pacote npm "sagansync" (TypeScript, Node 20+)
│   ├── src/
│   │   ├── main.ts
│   │   ├── commands/            # init, provision, deploy, list, logs, remove, dev, env
│   │   └── lib/
│   │       ├── ssh.ts           # argumentos seguros, ControlMaster, spawn com stdin
│   │       ├── pack.ts          # tar.gz respeitando ignores
│   │       ├── events.ts        # parser e renderização dos eventos JSON
│   │       ├── config.ts        # leitura e validação
│   │       ├── git.ts           # branch → workspace
│   │       └── dns.ts           # verificação dns_mismatch (5.3)
│   └── test/                    # vitest
├── agent/                       # módulo Go, binário "sagand"
│   ├── cmd/sagand/main.go       # subcomandos com o pacote flag
│   └── internal/
│       ├── gateway/             # SSH_ORIGINAL_COMMAND → subcomando permitido
│       ├── api/                 # servidor HTTP no socket Unix e o cliente dele
│       ├── deploy/              # orquestração (depende da interface Runtime)
│       ├── podman/              # cliente REST enxuto; implementa Runtime
│       ├── proxy/               # tabela de rotas, ReverseProxy, certmagic
│       ├── state/               # state.json, escrita atômica, trava por workspace
│       ├── release/             # extração segura, retenção
│       ├── envstore/            # arquivos de env por workspace
│       ├── domains/             # workspace → hostname (5.2)
│       └── validate/            # regras da seção 4.5
├── scripts/provision.sh
├── test/e2e/                    # VM Lima + Pebble + app de exemplo
├── .goreleaser.yaml
└── .github/workflows/ci.yml
```

- O código atual em `src/cli/` é migrado para `cli/src/`. `list.ts`, `remove.ts` e `init.ts` são reescritos. `deploy.ts` e `dev.ts` viram clientes finos. `provision.sh` é reescrito.
- **Interface `Runtime`** (em `deploy`): `Build`, `Create`, `Start`, `Stop`, `Remove`, `Inspect`, `Logs`, `List`, `RemoveImage`. O cliente Podman é implementado direto sobre `net/http` com um transporte de socket Unix, **sem** as bindings oficiais do Podman.
- **Dependências Go:** stdlib + `github.com/caddyserver/certmagic`.
- **Dependências da CLI:** `commander`, `@inquirer/prompts`, `chalk`, `tar-fs`, `ignore`, `chokidar`. Sai o `cli-table3`, trocado por uma renderização simples de tabela.

## 10. Distribuição e versões

- CLI e agente têm **a mesma versão** (tag `v0.1.0`).
- O GoReleaser gera `sagand_<versão>_linux_{amd64,arm64}.tar.gz` e `checksums.txt` no GitHub Release.
- O `provision` baixa o binário correspondente à versão da CLI, **confere o sha256** com o `checksums.txt` e instala.
- `--agent-binary <arquivo>` envia um binário local, sem download. Esse é o caminho usado em desenvolvimento e no teste ponta a ponta.
- A CLI é publicada no npm como `sagansync`, com `bin: sagansync`.

## 11. Tratamento de erros

| Situação | Comportamento |
| --- | --- |
| Deploy com a trava ocupada | `error{code:"busy"}` imediato |
| Falha no build | `error{code:"build_failed"}` com o fim do log do build; nenhum container é criado |
| Container sai ou health check estoura o prazo | `error{code:"health_failed"}` com as últimas 50 linhas do container; o novo é removido; o antigo fica intacto |
| SSH cai no meio do deploy | O daemon conclui ou reverte sozinho; o próximo `list` mostra o resultado |
| Tar inválido ou malicioso | `error{code:"invalid_archive"}`; a release parcial é apagada |
| Daemon fora do ar | O cliente sai com uma mensagem apontando `systemctl status sagand` (a ser executado pelo admin) |
| Protocolo incompatível | A CLI aborta e sugere `provision --upgrade` |
| Falha na emissão do certificado | O deploy conclui (o app responde pela porta local); aviso `tls_pending`; o certmagic tenta de novo em segundo plano |
| Host key mudou | O SSH recusa a conexão; a CLI explica e mostra o caminho do `known_hosts` |

## 12. Testes

1. **Go, unitários** (`go test -race ./...`):
   - `validate` e `gateway`: nomes inválidos, injeção, comandos fora da lista;
   - `release`: tar com caminho absoluto, `..`, symlink e hardlink para fora, device, limites;
   - `state`: escrita atômica, recarga, `.tmp` que sobrou;
   - `deploy` com `Runtime` falso: sucesso, falha no build, health check falho, container que sai, trava ocupada, antigo sempre preservado em falhas, cancelamento do cliente;
   - `proxy`: troca de rota com requisições simultâneas (`httptest`), 404 para domínio desconhecido, `DecisionFunc`;
   - `domains`: tabela da 5.2, truncamento em 63 caracteres com hash, colisão (`host_conflict`).
2. **Go, integração** (build tag `integration`, rodando na VM): cliente Podman contra o Podman rootless real.
3. **CLI, unitários** (vitest):
   - `pack` exclui `.env*`, `.git`, `node_modules` e respeita `.gitignore` e `.dockerignore`;
   - os argumentos SSH contêm `accept-new` e ControlMaster e nunca `StrictHostKeyChecking=no`;
   - parser de eventos;
   - validação da configuração;
   - branch → workspace;
   - `dns`: IPs coincidentes, divergentes e hostname que não resolve (resolver falso).
4. **Ponta a ponta** (`test/e2e/run.sh`, VM Lima Ubuntu 24.04, Pebble com `PEBBLE_VA_ALWAYS_VALID=1`, `curl --resolve` com a CA do Pebble, app de exemplo Node com `/health` e versão), cobrindo os critérios de sucesso da seção 1:
   1. provision → deploy v1 → HTTPS responde `v1`;
   2. deploy v2 com loop de requisições a cada 100 ms → nenhuma falha;
   3. deploy quebrado → código de saída ≠ 0, logs exibidos, `v2` segue no ar;
   4. reboot da VM → o app volta;
   5. segurança: `ssh sagan@vm bash` recusado; comando fora da lista recusado; `sudo -n true` como `sagan` falha; tar com `../` rejeitado;
   6. `env set` + deploy → variável visível no app; `list`, `logs`, `remove`; `dev` propaga uma edição;
   7. com `previewDomain`: branch `feat-x` responde em `feat-x-<projeto>.<previewDomain>` com certificado próprio.
5. **CI** (GitHub Actions, a cada push): `go vet`, `go test -race`, `tsc --noEmit`, vitest. O teste ponta a ponta fica local nesta versão.

## 13. Decisões registradas

- **Transporte SSH + socket Unix** em vez de API HTTP pública: menor superfície de ataque e autenticação já resolvida pelo SSH.
- **Proxy próprio com certmagic** em vez do Caddy: uma dependência a menos no servidor, e é pré-requisito para scale to zero.
- **Cliente REST próprio para o Podman** em vez das bindings oficiais: evita uma árvore de dependências grande e cgo.
- **JSON com escrita atômica** em vez de SQLite para o estado: o volume é pequeno e não há consultas.
- **`previewDomain` com rótulo único** (`<workspace>-<projeto>`) em vez de subdomínio aninhado: um único registro curinga serve todos os projetos, e curingas não cobrem dois níveis.
- **SaganSync não gerencia DNS** na v0.1: o registro curinga é criado uma vez pelo usuário; integração com API de DNS só quando houver certificado curinga (DNS-01).
- **Reconciliação pelo daemon** em vez de `--restart always`: uma única fonte da verdade, sem depender de `podman-restart.service` no modo rootless.
