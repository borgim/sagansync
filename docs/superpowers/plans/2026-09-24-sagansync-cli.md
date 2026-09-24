# SaganSync CLI — Plano de Implementação (plano 2 de 3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reescrever a CLI `sagansync` em TypeScript sobre o agente `sagand` (plano 1): `init`, `deploy`, `dev`, `list`, `logs`, `remove` e `env`, falando com o VPS só por SSH com a chave de deploy.

**Architecture:** Pacote npm em `cli/` (ESM, Node 20.19+). `lib/` tem uma peça por responsabilidade: configuração, git, SSH, empacotamento, eventos, protocolo com o agente, DNS. Cada comando recebe um `Ctx` (`cwd`, `config`, `remote`, `out`, `lookup`), então os testes usam um `FakeRemote`. Um teste de contrato roda a CLI real contra o `sagand gateway` real e a API real (com runtime falso), através de um `ssh` falso que imita o forced command.

**Tech Stack:** TypeScript 7, vitest 5, tsup, commander, @inquirer/prompts, tar-stream, ignore, chokidar; `node:util.styleText` para cores (sem chalk).

**Spec:** `docs/superpowers/specs/2026-09-24-sagansync-v0.1-agent-design.md` (seções 4.6, 5.2, 5.3, 8). Contrato do agente: `agent/cmd/sagand/client.go` e `agent/internal/gateway/gateway.go`.

**Planos:** plano 1 (agente) está na `main`. Este é o plano 2. O plano 3 traz `provision` (instalação do agente e do usuário `sagan`), o teste ponta a ponta na VM e o release. Até lá, a CLI deste plano só funciona num VPS já provisionado.

**Verificação prévia:** todo o código deste plano foi escrito e executado num módulo de rascunho antes: `tsc --noEmit` limpo, 101 testes passando em 3 execuções seguidas (incluindo o teste de contrato com o Go), `npm run build` gera `dist/main.js` executável.

## Global Constraints

- Pacote `sagansync` em `cli/`, versão `0.1.0`, `"type": "module"`, `engines.node >= 20.19`, binário `sagansync -> dist/main.js`.
- Dependências de produção, e só elas: `commander`, `@inquirer/prompts`, `tar-stream`, `ignore`, `chokidar`. Sem chalk: cores via `node:util.styleText`, desligadas fora de TTY e com `NO_COLOR`.
- `tsconfig`: `strict`, `noUncheckedIndexedAccess`, `module`/`moduleResolution` `NodeNext` (imports relativos terminam em `.js`).
- `VERSION = "0.1.0"` e `PROTOCOL = 1` em `src/version.ts`; o `PROTOCOL` precisa bater com `events.Protocol` do agente.
- SSH sempre com: `-T`, `-i <chave>`, `-l sagan`, `IdentitiesOnly=yes`, `BatchMode=yes`, `StrictHostKeyChecking=accept-new`, `UserKnownHostsFile=~/.config/sagansync/known_hosts`, `ControlMaster=auto`, `ControlPath=~/.config/sagansync/cm-%C`, `ControlPersist=60s`, e `--` antes do host. **`StrictHostKeyChecking=no` nunca aparece.**
- Comando remoto: `sagand <args>` com **cada argumento entre aspas simples** (`shQuote`), compatível com `gateway.Split` do agente.
- **Valores de env nunca vão na linha de comando remota**: só pelo stdin, como objeto JSON.
- Diretório da CLI: `~/.config/sagansync` (sobrescrito por `SAGANSYNC_HOME`); chave padrão `keys/<host>_ed25519`. `SAGANSYNC_SSH` troca o binário do ssh (testes).
- O upload **sempre** exclui `.git`, `node_modules`, `.sagansync`, `.env`, `.env.*`, `.DS_Store`, mesmo que `.gitignore`/`.dockerignore` tentem reincluí-los.
- Nomes de projeto e workspace: `^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$` (mesma regra do agente).
- Códigos de saída: 0 sucesso, 1 erro de operação, 2 entrada inválida (`invalid` do agente, `ConfigError`, argumentos), 130 Ctrl+C num prompt.

## Decisões deste plano (fora do texto da spec)

- **`provision` fica para o plano 3**, junto com o `provision.sh` que ele executa.
- **`dev --command` roda via `sh -c`**, para aceitar a linha de comando como o usuário a digitaria (`pnpm dev --port 3000`). Padrão: `npm run dev`.
- **`init` não cria `.sagansync/.gitignore`**: o `config.json` não tem mais segredos (a chave fica em `~/.config/sagansync/keys`), então pode ser commitado.
- **`env set --file .env.production`** além de `KEY=VALUE`: é o jeito comum de levar um `.env` que nunca é enviado no upload.
- **`list --all`** mostra todos os projetos do VPS; sem ele, só o projeto atual.
- **Só o `.gitignore` e o `.dockerignore` da raiz** são lidos, ambos com a regra do gitignore (o `.dockerignore` fica um pouco mais restritivo que no Docker, o que é o lado seguro).
- **Symlinks que o agente rejeitaria** (absolutos ou com `..`) são pulados com aviso, em vez de fazer o deploy inteiro falhar.
- **A trava de troca de branch no `dev` consulta o branch a cada segundo**: o `git checkout` troca `.git/HEAD` por um rename, que os watchers de arquivo não percebem.
- **Remove a CLI antiga** (`src/cli`, `package.json` da raiz, `provision.sh` com Caddy).

## Review Focus

1. **Nomes de branch longos, com barra ou com caracteres não ASCII** precisam virar workspaces válidos e distintos, ou pedir `-w`. Teste na Task 3 (`long names stay valid and distinct`, `branch with no usable characters requires -w`).
2. **Um `.gitignore` com `!.env`** não pode fazer o `.env` ser enviado. Teste na Task 5 (`a .gitignore cannot re-include .env`).
3. **Symlink apontando para fora do projeto** (monorepo com `../shared`) não pode derrubar o deploy inteiro. Teste na Task 5 (`skips symlinks the agent would reject`).
4. **Falha de SSH** (chave não autorizada, host key trocada, VPS fora do ar) precisa virar uma mensagem clara com o que fazer, não um stack trace. Teste na Task 7 (`explains an SSH failure`, `host key change gets a specific hint`).
5. **Valores de env com aspas, quebras de linha, `$` e crases** precisam chegar intactos ao agente, e um arquivo com aspas simples no nome precisa sincronizar no `dev`. Teste na Task 12 (teste de contrato).

## Mapa de arquivos

```
cli/
├── package.json, package-lock.json, tsconfig.json, tsup.config.ts
├── src/
│   ├── main.ts               # commander: liga os comandos
│   ├── version.ts            # VERSION, PROTOCOL
│   ├── lib/
│   │   ├── style.ts          # paint(): cores só em TTY
│   │   ├── config.ts         # Config, validação, caminhos (~/.config/sagansync)
│   │   ├── git.ts            # branch -> workspace
│   │   ├── ssh.ts            # argumentos, shQuote, Remote (run/stream)
│   │   ├── pack.ts           # lista de arquivos com ignores, tar.gz em stream
│   │   ├── events.ts         # eventos NDJSON do agente, Renderer, dicas por código
│   │   ├── agent.ts          # CliError, checkAgent, runStreaming, flags
│   │   ├── dns.ts            # aviso dns_mismatch, registros sugeridos
│   │   └── report.ts         # erro -> mensagem + código de saída
│   └── commands/
│       ├── context.ts        # Ctx, preflight, checkDns, collect
│       ├── deploy.ts, dev.ts, list.ts, logs.ts, remove.ts, env.ts, init.ts
└── test/
    ├── helpers/              # fakeSsh, FakeRemote, testCtx
    ├── *.test.ts
    └── contract.test.ts      # CLI real <-> sagand real (precisa de Go)
agent/internal/testutil/cmd/fakesagand/main.go   # API real + runtime falso, só para o teste de contrato
.github/workflows/ci.yml                         # job "cli"
```

Todos os comandos abaixo rodam em `cli/`, salvo indicação.

---

### Task 1: Pacote `cli/` e remoção da CLI antiga

**Files:**
- Create: `cli/package.json`, `cli/package-lock.json` (gerado), `cli/tsconfig.json`, `cli/tsup.config.ts`, `cli/src/version.ts`
- Test: `cli/test/version.test.ts`
- Delete: `src/` (CLI antiga), `package.json`, `package-lock.json`, `tsconfig.json`, `tsup.config.ts`, `sagansyncascii.txt` (todos na raiz)

**Interfaces:**
- Produces: `VERSION: string` (`"0.1.0"`) e `PROTOCOL: number` (`1`) em `src/version.ts`.

- [ ] **Step 1: Remover a CLI antiga**

A CLI antiga instala Caddy e fala direto com o Podman; nada dela é reaproveitado. `node_modules/` e `dist/` da raiz são ignorados pelo git e podem ser apagados à mão.

```bash
git rm -r -q src package.json package-lock.json tsconfig.json tsup.config.ts sagansyncascii.txt
rm -rf node_modules dist
```

- [ ] **Step 2: Criar o pacote e instalar as dependências**

Crie `cli/package.json` com este conteúdo:

`cli/package.json`:

```json
{
  "name": "sagansync",
  "version": "0.1.0",
  "description": "Deploy every git branch to its own HTTPS URL on your own VPS.",
  "type": "module",
  "bin": {
    "sagansync": "dist/main.js"
  },
  "files": [
    "dist"
  ],
  "engines": {
    "node": ">=20.19"
  },
  "scripts": {
    "build": "tsup",
    "typecheck": "tsc --noEmit",
    "test": "vitest run"
  },
  "repository": "github:borgim/sagansync",
  "author": "Pedro Borges",
  "license": "MIT",
  "dependencies": {
    "@inquirer/prompts": "^8.7.2",
    "chokidar": "^5.0.0",
    "commander": "^15.0.0",
    "ignore": "^7.0.10",
    "tar-stream": "^3.2.1"
  },
  "devDependencies": {
    "@types/node": "^26.6.2",
    "tsup": "^8.5.1",
    "typescript": "^7.0.2",
    "vitest": "^5.0.1"
  }
}
```

```bash
cd cli && npm install
```

Expected: `node_modules/` e `package-lock.json` criados, sem erros. As versões acima são as que foram testadas. Não adicione `@types/tar-stream`: o `tar-stream` 3.2 traz os próprios tipos e os dois conflitam.

- [ ] **Step 3: Configurar TypeScript e o build**

`cli/tsconfig.json`:

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "module": "NodeNext",
    "moduleResolution": "NodeNext",
    "strict": true,
    "noUncheckedIndexedAccess": true,
    "skipLibCheck": true,
    "types": ["node"],
    "noEmit": true
  },
  "include": ["src", "test", "tsup.config.ts"]
}
```

`cli/tsup.config.ts`:

```ts
import { defineConfig } from "tsup";

export default defineConfig({
  entry: ["src/main.ts"],
  format: ["esm"],
  platform: "node",
  target: "node20",
  clean: true,
  banner: { js: "#!/usr/bin/env node" },
});
```

- [ ] **Step 4: Escrever o teste que falha**

`cli/test/version.test.ts`:

```ts
import { readFileSync } from "node:fs";
import { expect, test } from "vitest";
import { VERSION } from "../src/version.js";

test("VERSION matches package.json", () => {
  const pkg = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8"));
  expect(VERSION).toBe(pkg.version);
});
```

- [ ] **Step 5: Rodar e ver falhar**

Run: `npx vitest run test/version.test.ts`
Expected: FAIL — o vitest não encontra `../src/version.js`.

- [ ] **Step 6: Implementar**

`cli/src/version.ts`:

```ts
// Kept in sync with package.json by test/version.test.ts.
export const VERSION = "0.1.0";

// Bumped together with sagand's events.Protocol on breaking changes.
export const PROTOCOL = 1;
```

- [ ] **Step 7: Rodar e ver passar**

Run: `npx tsc --noEmit && npx vitest run test/version.test.ts`
Expected: sem erros de tipo; PASS.

- [ ] **Step 8: Commit**

```bash
git add -A cli src package.json package-lock.json tsconfig.json tsup.config.ts sagansyncascii.txt
git commit -m "chore(cli): start the new CLI package and remove the old one"
```

---

### Task 2: Configuração do projeto (`lib/config.ts`)

**Files:**
- Create: `cli/src/lib/config.ts`
- Test: `cli/test/config.test.ts`

**Interfaces:**
- Produces:
  - `type Config = { host; sshPort; user; project; internalPort; domain?; previewDomain?; healthPath?; healthTimeout?; identityFile? }`
  - `class ConfigError extends Error`
  - `NAME_RE: RegExp`, `isDomain(s: string): boolean`
  - `validateConfig(raw: unknown): Config` — preenche `sshPort = 22` e `user = "sagan"`; lista todos os problemas numa `ConfigError`.
  - `configPath(cwd)`, `loadConfig(cwd): Config`, `saveConfig(cwd, cfg)`
  - `configDir()` (`SAGANSYNC_HOME` ou `~/.config/sagansync`), `keyPath(cfg)`, `knownHostsPath()`
- O `host` não aceita `user@` e não pode começar com `-` (seria lido pelo ssh como opção).

- [ ] **Step 1: Escrever o teste que falha**

`cli/test/config.test.ts`:

```ts
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { describe, expect, test } from "vitest";
import { ConfigError, keyPath, loadConfig, saveConfig, validateConfig } from "../src/lib/config.js";

const base = { host: "vps.example.com", project: "app", internalPort: 3000 };

describe("validateConfig", () => {
  test("fills defaults", () => {
    expect(validateConfig(base)).toEqual({ ...base, sshPort: 22, user: "sagan" });
  });

  test("keeps optional fields", () => {
    const full = { ...base, sshPort: 2222, user: "deploy", domain: "api.example.com", previewDomain: "example.com", healthPath: "/health", healthTimeout: 90, identityFile: "/k" };
    expect(validateConfig(full)).toEqual(full);
  });

  test.each([
    [{ ...base, host: "root@1.2.3.4" }, "host"],
    [{ ...base, host: "-oProxyCommand=evil" }, "host"],
    [{ ...base, project: "My App" }, "project"],
    [{ ...base, project: "a".repeat(41) }, "project"],
    [{ ...base, internalPort: 0 }, "internalPort"],
    [{ ...base, domain: "https://example.com" }, "domain"],
    [{ ...base, previewDomain: "*.example.com" }, "previewDomain"],
    [{ ...base, healthPath: "health" }, "healthPath"],
    [{ ...base, healthTimeout: 0 }, "healthTimeout"],
    [{ ...base, user: "Root" }, "user"],
  ])("rejects %j", (raw, field) => {
    expect(() => validateConfig(raw)).toThrow(ConfigError);
    expect(() => validateConfig(raw)).toThrow(field);
  });

  test("accepts IPv4 and IPv6 hosts", () => {
    expect(validateConfig({ ...base, host: "203.0.113.7" }).host).toBe("203.0.113.7");
    expect(validateConfig({ ...base, host: "2001:db8::1" }).host).toBe("2001:db8::1");
  });
});

describe("loadConfig / saveConfig", () => {
  test("round trip", () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-"));
    saveConfig(dir, validateConfig(base));
    expect(loadConfig(dir).project).toBe("app");
  });

  test("missing config explains how to create it", () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-"));
    expect(() => loadConfig(dir)).toThrow("sagansync init");
  });

  test("broken JSON is a ConfigError", () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-"));
    fs.mkdirSync(path.join(dir, ".sagansync"));
    fs.writeFileSync(path.join(dir, ".sagansync", "config.json"), "{nope");
    expect(() => loadConfig(dir)).toThrow(ConfigError);
  });
});

test("keyPath defaults under SAGANSYNC_HOME and honors identityFile", () => {
  process.env.SAGANSYNC_HOME = "/tmp/sgs-home";
  expect(keyPath({ host: "vps.example.com" })).toBe("/tmp/sgs-home/keys/vps.example.com_ed25519");
  expect(keyPath({ host: "vps.example.com", identityFile: "/k" })).toBe("/k");
  delete process.env.SAGANSYNC_HOME;
});
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `npx vitest run test/config.test.ts`
Expected: FAIL — o vitest não encontra `../src/lib/config.js`.

- [ ] **Step 3: Implementar**

`cli/src/lib/config.ts`:

```ts
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

export type Config = {
  host: string;
  sshPort: number;
  user: string;
  project: string;
  internalPort: number;
  domain?: string;
  previewDomain?: string;
  healthPath?: string;
  healthTimeout?: number;
  identityFile?: string;
};

export class ConfigError extends Error {}

// Same rules sagand enforces (agent/internal/validate).
export const NAME_RE = /^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/;
const LABEL_RE = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;
// Hostnames, IPv4 and IPv6. Must not start with "-" so ssh never reads it as an option.
const HOST_RE = /^[A-Za-z0-9][A-Za-z0-9.:-]*$/;
const USER_RE = /^[a-z_][a-z0-9_-]{0,31}$/;

export function isDomain(s: string): boolean {
  if (s.length === 0 || s.length > 253) return false;
  const labels = s.split(".");
  return labels.length >= 2 && labels.every((l) => LABEL_RE.test(l));
}

function isPort(n: unknown): n is number {
  return Number.isInteger(n) && (n as number) >= 1 && (n as number) <= 65535;
}

// validateConfig checks a parsed config.json and fills in defaults.
export function validateConfig(raw: unknown): Config {
  if (typeof raw !== "object" || raw === null) throw new ConfigError("config must be a JSON object");
  const c = raw as Record<string, unknown>;
  const problems: string[] = [];
  const str = (k: string) => (typeof c[k] === "string" ? (c[k] as string) : undefined);

  const host = str("host");
  if (!host || !HOST_RE.test(host)) problems.push("host must be a hostname or IP address (without user@)");
  const sshPort = c.sshPort ?? 22;
  if (!isPort(sshPort)) problems.push("sshPort must be a port number");
  const user = str("user") ?? "sagan";
  if (!USER_RE.test(user)) problems.push("user must be a valid Unix user name");
  const project = str("project");
  if (!project || !NAME_RE.test(project)) problems.push("project must be 1-40 characters of a-z, 0-9 and '-'");
  if (!isPort(c.internalPort)) problems.push("internalPort must be a port number");
  for (const k of ["domain", "previewDomain"]) {
    if (c[k] !== undefined && !(typeof c[k] === "string" && isDomain(c[k] as string))) {
      problems.push(`${k} must be a hostname like example.com, without protocol or port`);
    }
  }
  const healthPath = str("healthPath");
  if (c.healthPath !== undefined && !(healthPath && /^\/\S{0,199}$/.test(healthPath))) {
    problems.push("healthPath must start with '/' and contain no spaces");
  }
  const t = c.healthTimeout;
  if (t !== undefined && !(Number.isInteger(t) && (t as number) >= 1 && (t as number) <= 600)) {
    problems.push("healthTimeout must be 1-600 seconds");
  }
  if (c.identityFile !== undefined && typeof c.identityFile !== "string") problems.push("identityFile must be a path");
  if (problems.length > 0) throw new ConfigError(`Invalid .sagansync/config.json:\n  - ${problems.join("\n  - ")}`);

  const cfg: Config = { host: host!, sshPort: sshPort as number, user, project: project!, internalPort: c.internalPort as number };
  if (c.domain !== undefined) cfg.domain = c.domain as string;
  if (c.previewDomain !== undefined) cfg.previewDomain = c.previewDomain as string;
  if (healthPath !== undefined) cfg.healthPath = healthPath;
  if (t !== undefined) cfg.healthTimeout = t as number;
  if (c.identityFile !== undefined) cfg.identityFile = c.identityFile as string;
  return cfg;
}

export function configPath(cwd: string): string {
  return path.join(cwd, ".sagansync", "config.json");
}

export function loadConfig(cwd: string): Config {
  let text: string;
  try {
    text = fs.readFileSync(configPath(cwd), "utf8");
  } catch {
    throw new ConfigError("No .sagansync/config.json in this directory. Run `sagansync init` first.");
  }
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch (err) {
    throw new ConfigError(`.sagansync/config.json is not valid JSON: ${(err as Error).message}`);
  }
  return validateConfig(raw);
}

export function saveConfig(cwd: string, cfg: Config): void {
  fs.mkdirSync(path.dirname(configPath(cwd)), { recursive: true });
  fs.writeFileSync(configPath(cwd), JSON.stringify(cfg, null, 2) + "\n");
}

// configDir holds keys, known_hosts and SSH control sockets.
// SAGANSYNC_HOME overrides it (used by tests).
export function configDir(): string {
  return process.env.SAGANSYNC_HOME ?? path.join(os.homedir(), ".config", "sagansync");
}

export function keyPath(cfg: Pick<Config, "host" | "identityFile">): string {
  return cfg.identityFile ?? path.join(configDir(), "keys", `${cfg.host}_ed25519`);
}

export function knownHostsPath(): string {
  return path.join(configDir(), "known_hosts");
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `npx tsc --noEmit && npx vitest run test/config.test.ts`
Expected: sem erros de tipo; PASS.

- [ ] **Step 5: Commit**

```bash
git add cli/src/lib/config.ts cli/test/config.test.ts
git commit -m "feat(cli): load and validate the project config"
```

---

### Task 3: Workspace a partir do git (`lib/git.ts`)

**Files:**
- Create: `cli/src/lib/git.ts`
- Test: `cli/test/git.test.ts`

**Interfaces:**
- Consumes: `ConfigError`, `NAME_RE` (Task 2).
- Produces:
  - `type BranchState = { kind: "branch"; name } | { kind: "detached" } | { kind: "none" }`, `branchState(cwd)`
  - `currentSha(cwd): string` (`""` fora do git)
  - `workspaceFromBranch(branch): string` — `main`/`master` → `production`, `develop`/`dev` → `staging`; nomes com mais de 40 caracteres viram 32 caracteres + `-` + 7 hex do SHA-1.
  - `resolveWorkspace(cwd, explicit?): string` — `-w` validado; fora do git → `production`; HEAD destacado ou branch sem caracteres utilizáveis → `ConfigError` pedindo `-w`.

- [ ] **Step 1: Escrever o teste que falha**

`cli/test/git.test.ts`:

```ts
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { describe, expect, test } from "vitest";
import { NAME_RE } from "../src/lib/config.js";
import { branchState, resolveWorkspace, workspaceFromBranch } from "../src/lib/git.js";

describe("workspaceFromBranch", () => {
  test.each([
    ["main", "production"],
    ["master", "production"],
    ["develop", "staging"],
    ["dev", "staging"],
    ["feature/Login-Page", "feature-login-page"],
    ["fix__weird..name//", "fix-weird-name"],
    ["release/2026.09", "release-2026-09"],
  ])("%s -> %s", (branch, ws) => {
    expect(workspaceFromBranch(branch)).toBe(ws);
  });

  test("long names stay valid and distinct", () => {
    const a = workspaceFromBranch("feature/" + "x".repeat(60) + "-a");
    const b = workspaceFromBranch("feature/" + "x".repeat(60) + "-b");
    expect(a).toMatch(NAME_RE);
    expect(b).toMatch(NAME_RE);
    expect(a).not.toBe(b);
    expect(a.length).toBeLessThanOrEqual(40);
  });
});

function repo(branch: string): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-git-"));
  const run = (...args: string[]) => execFileSync("git", args, { cwd: dir, stdio: "ignore" });
  run("init", "-q", "-b", branch);
  run("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init");
  return dir;
}

describe("resolveWorkspace", () => {
  test("uses the branch", () => {
    expect(resolveWorkspace(repo("feature/x"))).toBe("feature-x");
    expect(resolveWorkspace(repo("main"))).toBe("production");
  });

  test("outside git defaults to production", () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-nogit-"));
    expect(branchState(dir)).toEqual({ kind: "none" });
    expect(resolveWorkspace(dir)).toBe("production");
  });

  test("detached HEAD requires -w", () => {
    const dir = repo("main");
    execFileSync("git", ["checkout", "-q", "--detach"], { cwd: dir });
    expect(() => resolveWorkspace(dir)).toThrow("-w");
    expect(resolveWorkspace(dir, "hotfix")).toBe("hotfix");
  });

  test("branch with no usable characters requires -w", () => {
    expect(() => resolveWorkspace(repo("日本"))).toThrow("-w");
  });

  test("explicit workspace is validated", () => {
    expect(() => resolveWorkspace(os.tmpdir(), "Bad Name")).toThrow("Invalid workspace");
  });

});
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `npx vitest run test/git.test.ts`
Expected: FAIL — o vitest não encontra `../src/lib/git.js`.

- [ ] **Step 3: Implementar**

`cli/src/lib/git.ts`:

```ts
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { ConfigError, NAME_RE } from "./config.js";

function git(cwd: string, args: string[]): string | null {
  try {
    return execFileSync("git", args, { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }).trim();
  } catch {
    return null;
  }
}

export type BranchState = { kind: "branch"; name: string } | { kind: "detached" } | { kind: "none" };

export function branchState(cwd: string): BranchState {
  if (git(cwd, ["rev-parse", "--is-inside-work-tree"]) !== "true") return { kind: "none" };
  const name = git(cwd, ["symbolic-ref", "--short", "HEAD"]);
  return name ? { kind: "branch", name } : { kind: "detached" };
}

export function currentSha(cwd: string): string {
  return git(cwd, ["rev-parse", "HEAD"]) ?? "";
}

// workspaceFromBranch turns a branch name into a valid workspace name.
// main/master deploy to production and develop/dev to staging. Names longer
// than 40 characters are shortened with a hash so different branches never
// share a workspace.
export function workspaceFromBranch(branch: string): string {
  const name = branch.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
  if (name === "main" || name === "master") return "production";
  if (name === "develop" || name === "dev") return "staging";
  if (name.length <= 40) return name;
  const hash = createHash("sha1").update(name).digest("hex").slice(0, 7);
  return `${name.slice(0, 32).replace(/-+$/, "")}-${hash}`;
}

// resolveWorkspace picks the workspace from -w, or from the current branch.
export function resolveWorkspace(cwd: string, explicit?: string): string {
  if (explicit !== undefined) {
    if (!NAME_RE.test(explicit)) throw new ConfigError(`Invalid workspace "${explicit}": use 1-40 characters of a-z, 0-9 and '-'.`);
    return explicit;
  }
  const state = branchState(cwd);
  if (state.kind === "none") return "production";
  if (state.kind === "detached") throw new ConfigError("HEAD is detached, so there is no branch to name the workspace after. Pass -w <workspace>.");
  const ws = workspaceFromBranch(state.name);
  if (!NAME_RE.test(ws)) throw new ConfigError(`Cannot derive a workspace name from branch "${state.name}". Pass -w <workspace>.`);
  return ws;
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `npx tsc --noEmit && npx vitest run test/git.test.ts`
Expected: sem erros de tipo; PASS.

- [ ] **Step 5: Commit**

```bash
git add cli/src/lib/git.ts cli/test/git.test.ts
git commit -m "feat(cli): derive workspace names from git branches"
```

---

### Task 4: SSH e aspas (`lib/ssh.ts`)

**Files:**
- Create: `cli/src/lib/ssh.ts`
- Create: `cli/test/helpers/fakeSsh.ts`
- Test: `cli/test/ssh.test.ts`

**Interfaces:**
- Consumes: `Config`, `configDir`, `keyPath`, `knownHostsPath` (Task 2).
- Produces:
  - `type Target = { host; port; user; identityFile; knownHosts; controlDir }`, `targetFor(cfg): Target`
  - `shQuote(s)`, `remoteCommand(args): string` (`'sagand' 'arg1' ...`)
  - `sshArgs(t): string[]`
  - `type RunResult = { code; stdout; stderr }`, `type Input = Readable | string`
  - `interface Remote { run(args, stdin?): Promise<RunResult>; stream(args, onLine, stdin?): Promise<{ code; stderr }> }`
  - `sshRemote(t, sshBin = process.env.SAGANSYNC_SSH ?? "ssh"): Remote`
- Test helper: `fakeSsh({ stdout?, code?, stderr? })` cria um executável que grava argv e stdin num JSON.

- [ ] **Step 1: Escrever o teste que falha**

`cli/test/helpers/fakeSsh.ts`:

```ts
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

// fakeSsh writes an executable that records its argv and stdin to a JSON
// file, prints `stdout`, and exits with `code`.
export function fakeSsh(opts: { stdout?: string; code?: number; stderr?: string } = {}): { bin: string; record: () => { argv: string[]; stdin: string } } {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-ssh-"));
  const out = path.join(dir, "record.json");
  const bin = path.join(dir, "ssh");
  fs.writeFileSync(bin, `#!/usr/bin/env node
const fs = require("fs");
const chunks = [];
process.stdin.on("data", (c) => chunks.push(c));
process.stdin.on("end", () => {
  fs.writeFileSync(${JSON.stringify(out)}, JSON.stringify({ argv: process.argv.slice(2), stdin: Buffer.concat(chunks).toString("utf8") }));
  process.stdout.write(${JSON.stringify(opts.stdout ?? "")});
  process.stderr.write(${JSON.stringify(opts.stderr ?? "")});
  process.exit(${opts.code ?? 0});
});
`, { mode: 0o755 });
  return { bin, record: () => JSON.parse(fs.readFileSync(out, "utf8")) };
}
```

`cli/test/ssh.test.ts`:

```ts
import os from "node:os";
import { Readable } from "node:stream";
import { describe, expect, test } from "vitest";
import { remoteCommand, shQuote, sshArgs, sshRemote, type Target } from "../src/lib/ssh.js";
import { fakeSsh } from "./helpers/fakeSsh.js";

const target: Target = { host: "vps.example.com", port: 2222, user: "sagan", identityFile: "/keys/id",
  knownHosts: "/cfg/known_hosts", controlDir: os.tmpdir() };

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
  const args = sshArgs(target);
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

  test("a missing ssh binary rejects", async () => {
    await expect(sshRemote(target, "/nonexistent/ssh").run(["version"])).rejects.toThrow();
  });
});
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `npx vitest run test/ssh.test.ts`
Expected: FAIL — o vitest não encontra `../src/lib/ssh.js`.

- [ ] **Step 3: Implementar**

`cli/src/lib/ssh.ts`:

```ts
import { spawn } from "node:child_process";
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
    "-o", `ControlPath=${path.join(t.controlDir, "cm-%C")}`,
    "-o", "ControlPersist=60s",
    "-o", "LogLevel=ERROR",
    "--", t.host,
  ];
}

export type RunResult = { code: number; stdout: string; stderr: string };
export type Input = Readable | string;

// Remote runs sagand subcommands on the VPS.
export interface Remote {
  run(args: string[], stdin?: Input): Promise<RunResult>;
  // stream calls onLine for every stdout line as it arrives.
  stream(args: string[], onLine: (line: string) => void, stdin?: Input): Promise<{ code: number; stderr: string }>;
}

// sshRemote talks to sagand through ssh. SAGANSYNC_SSH replaces the ssh
// binary (tests use a fake one).
export function sshRemote(t: Target, sshBin = process.env.SAGANSYNC_SSH ?? "ssh"): Remote {
  const start = (args: string[], stdin?: Input) => {
    fs.mkdirSync(t.controlDir, { recursive: true, mode: 0o700 });
    const child = spawn(sshBin, [...sshArgs(t), remoteCommand(args)], { stdio: ["pipe", "pipe", "pipe"] });
    child.stdin.on("error", () => {}); // the remote may exit before reading all input
    if (typeof stdin === "string") child.stdin.end(stdin);
    else if (stdin) {
      stdin.on("error", (err) => child.stdin.destroy(err));
      stdin.pipe(child.stdin);
    } else child.stdin.end();
    let stderr = "";
    child.stderr.setEncoding("utf8").on("data", (d: string) => (stderr += d));
    const done = new Promise<{ code: number; stderr: string }>((resolve, reject) => {
      child.on("error", reject);
      child.on("close", (code) => resolve({ code: code ?? 1, stderr }));
    });
    return { child, done };
  };
  return {
    async run(args, stdin) {
      const { child, done } = start(args, stdin);
      let stdout = "";
      child.stdout.setEncoding("utf8").on("data", (d: string) => (stdout += d));
      const { code, stderr } = await done;
      return { code, stdout, stderr };
    },
    async stream(args, onLine, stdin) {
      const { child, done } = start(args, stdin);
      const rl = readline.createInterface({ input: child.stdout, crlfDelay: Infinity });
      rl.on("line", onLine);
      const result = await done;
      rl.close();
      return result;
    },
  };
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `npx tsc --noEmit && npx vitest run test/ssh.test.ts`
Expected: sem erros de tipo; PASS.

- [ ] **Step 5: Commit**

```bash
git add cli/src/lib/ssh.ts cli/test/helpers/fakeSsh.ts cli/test/ssh.test.ts
git commit -m "feat(cli): run sagand over ssh with host key checks and connection reuse"
```

---

### Task 5: Empacotamento do projeto (`lib/pack.ts`)

**Files:**
- Create: `cli/src/lib/pack.ts`
- Test: `cli/test/pack.test.ts`

**Interfaces:**
- Produces:
  - `type Matcher = (rel, isDir) => boolean`, `ignoreMatcher(root): Matcher`
  - `type Entry = { rel; abs; type: "file" | "dir" | "symlink"; mode; size; mtime; link? }`, `type FileList = { entries: Entry[]; skipped: string[] }`
  - `listFiles(root, match?): FileList` — ordem estável; symlinks absolutos ou com `..` vão para `skipped` (`"rel -> alvo"`).
  - `packProject(entries): Readable` — tar.gz em stream; erro de leitura vira `packing the project failed: ...`.
- Usa os tipos do próprio `tar-stream` (`import { pack as tarPack, type Pack } from "tar-stream"`).

- [ ] **Step 1: Escrever o teste que falha**

`cli/test/pack.test.ts`:

```ts
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { Readable } from "node:stream";
import zlib from "node:zlib";
import { extract as tarExtract } from "tar-stream";
import { describe, expect, test } from "vitest";
import { listFiles, packProject } from "../src/lib/pack.js";

function project(files: Record<string, string>): string {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-pack-"));
  for (const [rel, body] of Object.entries(files)) {
    fs.mkdirSync(path.dirname(path.join(root, rel)), { recursive: true });
    fs.writeFileSync(path.join(root, rel), body);
  }
  return root;
}

const names = (root: string) => listFiles(root).entries.map((e) => e.rel);

describe("listFiles", () => {
  test("always excludes .git, node_modules, .sagansync and .env files", () => {
    const root = project({
      "src/index.ts": "", ".git/HEAD": "", "node_modules/x/i.js": "", "pkg/node_modules/y.js": "",
      ".sagansync/config.json": "", ".env": "SECRET=1", ".env.production": "SECRET=2", "apps/api/.env.local": "S",
      ".DS_Store": "", "Dockerfile": "",
    });
    expect(names(root)).toEqual(["Dockerfile", "apps", "apps/api", "pkg", "src", "src/index.ts"]);
  });

  test("respects .gitignore and .dockerignore", () => {
    const root = project({ ".gitignore": "dist/\n*.log\n", ".dockerignore": "coverage\n", "dist/a.js": "", "debug.log": "", "coverage/x": "", "keep.ts": "" });
    expect(names(root)).toEqual([".dockerignore", ".gitignore", "keep.ts"]);
  });

  test("a .gitignore cannot re-include .env", () => {
    const root = project({ ".gitignore": "!.env\n!.env.production\n", ".env": "SECRET=1", ".env.production": "S", "a.ts": "" });
    expect(names(root)).toEqual([".gitignore", "a.ts"]);
  });

  test("skips symlinks the agent would reject", () => {
    const root = project({ "src/a.ts": "" });
    fs.symlinkSync("src", path.join(root, "inside"));
    fs.symlinkSync("../shared", path.join(root, "up"));
    fs.symlinkSync("/etc/passwd", path.join(root, "abs"));
    const { entries, skipped } = listFiles(root);
    expect(entries.map((e) => e.rel)).toEqual(["inside", "src", "src/a.ts"]);
    expect(skipped).toEqual(["abs -> /etc/passwd", "up -> ../shared"]);
  });
});

async function readArchive(stream: Readable): Promise<Record<string, { type: string; body: string; link?: string; mode?: number }>> {
  const extract = tarExtract();
  const out: Record<string, { type: string; body: string; link?: string; mode?: number }> = {};
  const done = new Promise<void>((resolve, reject) => {
    extract.on("entry", (header, body, next) => {
      const chunks: Buffer[] = [];
      body.on("data", (c) => chunks.push(c as Buffer));
      body.on("end", () => {
        out[header.name] = { type: header.type ?? "file", body: Buffer.concat(chunks).toString(), link: header.linkname ?? undefined, mode: header.mode };
        next();
      });
      body.resume();
    });
    extract.on("finish", resolve);
    extract.on("error", reject);
  });
  stream.pipe(zlib.createGunzip()).pipe(extract);
  await done;
  return out;
}

describe("packProject", () => {
  test("produces a tar.gz with files, dirs, symlinks and modes", async () => {
    const root = project({ "Dockerfile": "FROM node", "src/index.ts": "console.log(1)" });
    fs.chmodSync(path.join(root, "Dockerfile"), 0o755);
    fs.symlinkSync("src", path.join(root, "lib"));
    const archive = await readArchive(packProject(listFiles(root).entries));
    expect(archive["Dockerfile"]).toMatchObject({ type: "file", body: "FROM node", mode: 0o755 });
    expect(archive["src"]?.type).toBe("directory");
    expect(archive["src/index.ts"]?.body).toBe("console.log(1)");
    expect(archive["lib"]).toMatchObject({ type: "symlink", link: "src" });
  });

  test("a file removed while packing fails the stream", async () => {
    const root = project({ "a.txt": "hello" });
    const { entries } = listFiles(root);
    fs.rmSync(path.join(root, "a.txt"));
    const stream = packProject(entries);
    await expect(new Promise((resolve, reject) => { stream.on("error", reject); stream.on("end", resolve); stream.resume(); }))
      .rejects.toThrow("packing the project failed");
  });
});
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `npx vitest run test/pack.test.ts`
Expected: FAIL — o vitest não encontra `../src/lib/pack.js`.

- [ ] **Step 3: Implementar**

`cli/src/lib/pack.ts`:

```ts
import fs from "node:fs";
import path from "node:path";
import type { Readable } from "node:stream";
import zlib from "node:zlib";
import ignore from "ignore";
import { pack as tarPack, type Pack } from "tar-stream";

// Never uploaded, whatever .gitignore or .dockerignore say (they cannot
// re-include these with "!" rules). Secrets in .env files are set with
// `sagansync env` instead.
const ALWAYS = [".git", "node_modules", ".sagansync", ".env", ".env.*", ".DS_Store"];

export type Matcher = (rel: string, isDir: boolean) => boolean;

// ignoreMatcher applies the fixed exclusions plus the project's root
// .gitignore and .dockerignore (both read with gitignore rules).
export function ignoreMatcher(root: string): Matcher {
  const always = ignore().add(ALWAYS);
  const project = ignore();
  for (const file of [".gitignore", ".dockerignore"]) {
    const p = path.join(root, file);
    if (fs.existsSync(p)) project.add(fs.readFileSync(p, "utf8"));
  }
  return (rel, isDir) => {
    const p = isDir ? `${rel}/` : rel;
    return always.ignores(p) || project.ignores(p);
  };
}

export type Entry = {
  rel: string;
  abs: string;
  type: "file" | "dir" | "symlink";
  mode: number;
  size: number;
  mtime: Date;
  link?: string;
};

export type FileList = { entries: Entry[]; skipped: string[] };

// sagand rejects archives with symlinks that could point outside the release.
function unsafeLink(target: string): boolean {
  return target.startsWith("/") || target.split("/").includes("..");
}

// listFiles walks root and returns what will be uploaded, in a stable order.
// Symlinks the agent would refuse are skipped and reported instead of failing
// the whole deploy; sockets and other special files are ignored.
export function listFiles(root: string, match: Matcher = ignoreMatcher(root)): FileList {
  const entries: Entry[] = [];
  const skipped: string[] = [];
  const walk = (dirRel: string) => {
    const names = fs.readdirSync(path.join(root, dirRel)).sort();
    for (const name of names) {
      const rel = dirRel ? `${dirRel}/${name}` : name;
      const abs = path.join(root, rel);
      const st = fs.lstatSync(abs);
      const base = { rel, abs, mode: st.mode & 0o777, mtime: st.mtime, size: 0 };
      if (st.isDirectory()) {
        if (match(rel, true)) continue;
        entries.push({ ...base, type: "dir" });
        walk(rel);
      } else if (st.isSymbolicLink()) {
        if (match(rel, false)) continue;
        const link = fs.readlinkSync(abs);
        if (unsafeLink(link)) skipped.push(`${rel} -> ${link}`);
        else entries.push({ ...base, type: "symlink", link });
      } else if (st.isFile()) {
        if (match(rel, false)) continue;
        entries.push({ ...base, type: "file", size: st.size });
      }
    }
  };
  walk("");
  return { entries, skipped };
}

type EntryHeader = Parameters<Pack["entry"]>[0];

function addEntry(pack: Pack, header: EntryHeader, file?: string): Promise<void> {
  return new Promise((resolve, reject) => {
    const done = (err?: Error | null) => (err ? reject(err) : resolve());
    if (!file) {
      pack.entry(header, Buffer.alloc(0), done);
      return;
    }
    const sink = pack.entry(header, done);
    sink.on("error", () => {}); // reported through done or the read stream
    const src = fs.createReadStream(file);
    src.on("error", reject);
    src.pipe(sink);
  });
}

// packProject streams the entries as a gzip-compressed tar archive.
export function packProject(entries: Entry[]): Readable {
  const pack = tarPack();
  const gz = zlib.createGzip();
  const fail = (err: Error) => {
    const msg = err.message === "Size mismatch" ? "a file changed while it was being packed; try again" : err.message;
    gz.destroy(new Error(`packing the project failed: ${msg}`));
  };
  pack.on("error", fail);
  pack.pipe(gz);
  (async () => {
    for (const e of entries) {
      const header = { name: e.rel, mode: e.mode, mtime: e.mtime };
      if (e.type === "dir") await addEntry(pack, { ...header, type: "directory" });
      else if (e.type === "symlink") await addEntry(pack, { ...header, type: "symlink", linkname: e.link });
      else await addEntry(pack, { ...header, type: "file", size: e.size }, e.abs);
    }
    pack.finalize();
  })().catch((err: Error) => {
    fail(err);
    pack.destroy();
  });
  return gz;
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `npx tsc --noEmit && npx vitest run test/pack.test.ts`
Expected: sem erros de tipo; PASS.

- [ ] **Step 5: Commit**

```bash
git add cli/src/lib/pack.ts cli/test/pack.test.ts
git commit -m "feat(cli): pack the project honoring ignores and never sending .env"
```

---

### Task 6: Eventos do agente e cores (`lib/events.ts`, `lib/style.ts`)

**Files:**
- Create: `cli/src/lib/style.ts`
- Create: `cli/src/lib/events.ts`
- Create: `cli/test/helpers/fakeRemote.ts`
- Test: `cli/test/events.test.ts`

**Interfaces:**
- Consumes: `Remote`, `RunResult`, `Input` (Task 4) no helper de teste.
- Produces:
  - `paint(format, text)` — cor só em TTY e sem `NO_COLOR`.
  - `type AgentEvent` (espelho de `agent/internal/events.Event`), `parseEvent(line): AgentEvent | null` (exige `v` numérico e um dos 5 tipos)
  - `class Renderer { last; constructor(out, verbose?); line(raw) }` — mostra steps e avisos; logs só com `verbose`; guarda o `done`/`error` final.
  - `HINTS: Record<string, string>` (dica por código de erro), `exitCodeFor(e)`.
- Test helper: `FakeRemote` (respostas por subcomando, ou por par como `"env list"`; registra as chamadas e o stdin), `ev(fields)`, `VERSION_OK`.

- [ ] **Step 1: Escrever o teste que falha**

`cli/test/helpers/fakeRemote.ts`:

```ts
import type { Readable } from "node:stream";
import type { Input, Remote, RunResult } from "../../src/lib/ssh.js";

export type Call = { args: string[]; stdin: string };
type Reply = Partial<RunResult> | ((call: Call) => Partial<RunResult>);

async function read(input?: Input): Promise<string> {
  if (input === undefined) return "";
  if (typeof input === "string") return input;
  const chunks: Buffer[] = [];
  for await (const c of input as Readable) chunks.push(Buffer.from(c));
  return Buffer.concat(chunks).toString("latin1");
}

// FakeRemote answers sagand subcommands from a table keyed by the first
// argument (or "env set" style pairs) and records every call.
export class FakeRemote implements Remote {
  calls: Call[] = [];
  constructor(private readonly replies: Record<string, Reply> = {}) {}

  private reply(call: Call): RunResult {
    const key2 = call.args.slice(0, 2).join(" ");
    const r = this.replies[key2] ?? this.replies[call.args[0] ?? ""] ?? {};
    const v = typeof r === "function" ? r(call) : r;
    return { code: v.code ?? 0, stdout: v.stdout ?? "", stderr: v.stderr ?? "" };
  }

  async run(args: string[], stdin?: Input): Promise<RunResult> {
    const call = { args, stdin: await read(stdin) };
    this.calls.push(call);
    return this.reply(call);
  }

  async stream(args: string[], onLine: (line: string) => void, stdin?: Input) {
    const r = await this.run(args, stdin);
    for (const line of r.stdout.split("\n")) if (line) onLine(line);
    return { code: r.code, stderr: r.stderr };
  }

  argsOf(cmd: string): string[] | undefined {
    return this.calls.find((c) => c.args[0] === cmd)?.args;
  }
}

export const ev = (e: Record<string, unknown>) => JSON.stringify({ v: 1, ...e });
export const VERSION_OK = { stdout: '{"version":"0.1.0","protocol":1}\n' };
```

`cli/test/events.test.ts`:

```ts
import { expect, test } from "vitest";
import { exitCodeFor, parseEvent, Renderer } from "../src/lib/events.js";
import { ev } from "./helpers/fakeRemote.js";

test("parseEvent accepts sagand events only", () => {
  expect(parseEvent(ev({ type: "step", name: "build" }))).toMatchObject({ type: "step", name: "build" });
  expect(parseEvent('{"level":"info","msg":"app log"}')).toBeNull();
  expect(parseEvent('{"v":1,"type":"other"}')).toBeNull();
  expect(parseEvent("plain text")).toBeNull();
  expect(parseEvent("{broken")).toBeNull();
});

test("Renderer shows steps and warnings, hides logs unless verbose", () => {
  const lines: string[] = [];
  const r = new Renderer((s) => lines.push(s));
  r.line(ev({ type: "step", name: "build" }));
  r.line(ev({ type: "log", stream: "build", line: "STEP 1/3" }));
  r.line(ev({ type: "warn", code: "tls_pending", message: "certificate not ready" }));
  r.line(ev({ type: "done", url: "https://app.test" }));
  expect(lines).toEqual(["▸ Building image", "! certificate not ready"]);
  expect(r.last).toMatchObject({ type: "done", url: "https://app.test" });

  const verbose: string[] = [];
  new Renderer((s) => verbose.push(s), true).line(ev({ type: "log", line: "STEP 1/3" }));
  expect(verbose).toEqual(["  STEP 1/3"]);
});

test("exitCodeFor mirrors sagand", () => {
  expect(exitCodeFor({ v: 1, type: "done" })).toBe(0);
  expect(exitCodeFor({ v: 1, type: "error", code: "invalid" })).toBe(2);
  expect(exitCodeFor({ v: 1, type: "error", code: "busy" })).toBe(1);
});
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `npx vitest run test/events.test.ts`
Expected: FAIL — o vitest não encontra `../src/lib/events.js`.

- [ ] **Step 3: Implementar**

`cli/src/lib/style.ts`:

```ts
import { styleText } from "node:util";

type Format = Parameters<typeof styleText>[0];

// paint colors text only when stdout is a terminal and NO_COLOR is unset.
export function paint(format: Format, text: string): string {
  if (!process.stdout.isTTY || process.env.NO_COLOR) return text;
  return styleText(format, text);
}
```

`cli/src/lib/events.ts`:

```ts
import { paint } from "./style.js";

// AgentEvent is one line of sagand's NDJSON output (agent/internal/events).
export type AgentEvent = {
  v: number;
  type: "step" | "log" | "warn" | "done" | "error";
  name?: string;
  stream?: string;
  line?: string;
  url?: string;
  release?: string;
  hostPort?: number;
  code?: string;
  message?: string;
  logs?: string[];
};

const TYPES = new Set(["step", "log", "warn", "done", "error"]);

// parseEvent returns the event on a line, or null for anything else
// (application logs may be JSON too, but they never carry "v" and our types).
export function parseEvent(line: string): AgentEvent | null {
  if (!line.startsWith("{")) return null;
  try {
    const e = JSON.parse(line) as Partial<AgentEvent>;
    if (typeof e.v === "number" && typeof e.type === "string" && TYPES.has(e.type)) return e as AgentEvent;
  } catch {
    // not an event
  }
  return null;
}

const STEPS: Record<string, string> = {
  extract: "Unpacking upload",
  build: "Building image",
  start: "Starting container",
  health: "Waiting for the health check",
  tls: "Getting the TLS certificate",
  drain: "Retiring the previous release",
};

// Renderer prints progress for a stream of events and remembers the final
// done or error event.
export class Renderer {
  last: AgentEvent | null = null;

  constructor(
    private readonly out: (s: string) => void,
    private readonly verbose = false,
  ) {}

  line(raw: string): void {
    const e = parseEvent(raw);
    if (!e) {
      if (raw.trim()) this.out(raw);
      return;
    }
    switch (e.type) {
      case "step":
        this.out(`${paint("cyan", "▸")} ${STEPS[e.name ?? ""] ?? e.name}`);
        break;
      case "log":
        if (this.verbose) this.out(paint("gray", `  ${e.line ?? ""}`));
        break;
      case "warn":
        this.out(paint("yellow", `! ${e.message ?? e.code}`));
        break;
      case "done":
      case "error":
        this.last = e;
        break;
    }
  }
}

export const HINTS: Record<string, string> = {
  busy: "Another operation is running on this workspace. Wait for it to finish and try again.",
  build_failed: "The image build failed. Run again with --verbose to see the full build output.",
  health_failed: "The new release never became healthy, so the previous one is still serving traffic.",
  invalid_archive: "The upload was rejected. If the connection dropped, just run the command again.",
  host_conflict: "Another project already uses this hostname. Change domain or previewDomain in .sagansync/config.json.",
  production_locked: "Use a feature branch, or pass --force if you really mean to run dev mode on production.",
  not_found: "Run `sagansync list` to see the workspaces on this VPS.",
  daemon_unavailable: "The sagand service is not running on the VPS. Run `sagansync provision --upgrade` to repair it.",
};

// exitCodeFor mirrors sagand's events.ExitCode.
export function exitCodeFor(e: AgentEvent): number {
  if (e.type === "done") return 0;
  return e.code === "invalid" ? 2 : 1;
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `npx tsc --noEmit && npx vitest run test/events.test.ts`
Expected: sem erros de tipo; PASS.

- [ ] **Step 5: Commit**

```bash
git add cli/src/lib/events.ts cli/src/lib/style.ts cli/test/events.test.ts cli/test/helpers/fakeRemote.ts
git commit -m "feat(cli): parse and render sagand events"
```

---

### Task 7: Protocolo com o agente (`lib/agent.ts`)

**Files:**
- Create: `cli/src/lib/agent.ts`
- Test: `cli/test/agent.test.ts`

**Interfaces:**
- Consumes: `Config` (T2), `Remote`/`RunResult`/`Input` (T4), `AgentEvent`/`Renderer`/`HINTS`/`exitCodeFor`/`parseEvent` (T6), `VERSION`/`PROTOCOL` (T1).
- Produces:
  - `class CliError extends Error { exitCode; hint?; details: string[] }`
  - `eventError(e)`, `sshError(stderr)` (dicas para chave não autorizada e host key trocada), `failure(r: RunResult)` (255 → SSH, evento `error` no stdout, 126 → comando negado)
  - `checkAgent(remote): Promise<string | undefined>` — erro se o protocolo difere; aviso se só a versão difere.
  - `runStreaming(remote, args, { out, verbose?, stdin? }): Promise<AgentEvent>` — devolve o `done` ou lança.
  - `workspaceFlags(cfg, ws)`, `domainFlags(cfg)`, `requestFlags(cfg, ws, sha)`

- [ ] **Step 1: Escrever o teste que falha**

`cli/test/agent.test.ts`:

```ts
import { describe, expect, test } from "vitest";
import { checkAgent, CliError, failure, requestFlags, runStreaming, sshError } from "../src/lib/agent.js";
import { validateConfig } from "../src/lib/config.js";
import { ev, FakeRemote, VERSION_OK } from "./helpers/fakeRemote.js";

const cfg = validateConfig({ host: "vps.test", project: "app", internalPort: 3000, domain: "app.test", previewDomain: "preview.test", healthPath: "/health", healthTimeout: 90 });

describe("checkAgent", () => {
  test("passes on a matching agent", async () => {
    expect(await checkAgent(new FakeRemote({ version: VERSION_OK }))).toBeUndefined();
  });
  test("warns on a different compatible version", async () => {
    const w = await checkAgent(new FakeRemote({ version: { stdout: '{"version":"0.2.0","protocol":1}' } }));
    expect(w).toContain("0.2.0");
  });
  test("refuses another protocol", async () => {
    await expect(checkAgent(new FakeRemote({ version: { stdout: '{"version":"1.0.0","protocol":2}' } }))).rejects.toThrow("protocol 2");
  });
  test("explains an SSH failure", async () => {
    const remote = new FakeRemote({ version: { code: 255, stderr: "Permission denied (publickey)." } });
    await expect(checkAgent(remote)).rejects.toMatchObject({ hint: expect.stringContaining("provision") });
  });
});

describe("failure", () => {
  test("uses the error event on stdout", () => {
    const e = failure({ code: 1, stdout: ev({ type: "error", code: "daemon_unavailable", message: "cannot reach" }) + "\n", stderr: "" });
    expect(e.message).toBe("cannot reach");
    expect(e.hint).toContain("provision");
  });
  test("explains a denied command", () => {
    expect(failure({ code: 126, stdout: "", stderr: 'sagand: command not allowed: "foo"' }).message).toContain("refused");
  });
  test("host key change gets a specific hint", () => {
    expect(sshError("@@@ WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED! @@@").hint).toContain("known_hosts");
  });
});

describe("runStreaming", () => {
  test("returns the done event and renders progress", async () => {
    const out: string[] = [];
    const remote = new FakeRemote({ deploy: { stdout: [ev({ type: "step", name: "build" }), ev({ type: "done", url: "https://app.test" })].join("\n") } });
    const done = await runStreaming(remote, ["deploy"], { out: (s) => out.push(s) });
    expect(done.url).toBe("https://app.test");
    expect(out).toEqual(["▸ Building image"]);
  });
  test("throws the error event with logs and exit code", async () => {
    const remote = new FakeRemote({ deploy: { code: 1, stdout: ev({ type: "error", code: "health_failed", message: "exited", logs: ["boom"] }) } });
    const err = (await runStreaming(remote, ["deploy"], { out: () => {} }).catch((e) => e)) as CliError;
    expect(err).toBeInstanceOf(CliError);
    expect(err.exitCode).toBe(1);
    expect(err.details).toEqual(["Last container logs:", "  boom"]);
  });
  test("validation errors exit with 2", async () => {
    const remote = new FakeRemote({ deploy: { code: 2, stdout: ev({ type: "error", code: "invalid", message: "bad" }) } });
    await expect(runStreaming(remote, ["deploy"], { out: () => {} })).rejects.toMatchObject({ exitCode: 2 });
  });
  test("a stream without a result is a failure", async () => {
    await expect(runStreaming(new FakeRemote({ deploy: { code: 255, stderr: "Connection refused" } }), ["deploy"], { out: () => {} }))
      .rejects.toThrow("Could not connect over SSH");
  });
});

test("requestFlags carries the whole config", () => {
  expect(requestFlags(cfg, "feat-x", "abc1234")).toEqual([
    "--project", "app", "--workspace", "feat-x", "--port", "3000", "--domain", "app.test", "--preview-domain", "preview.test",
    "--health-path", "/health", "--health-timeout", "90", "--sha", "abc1234",
  ]);
});
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `npx vitest run test/agent.test.ts`
Expected: FAIL — o vitest não encontra `../src/lib/agent.js`.

- [ ] **Step 3: Implementar**

`cli/src/lib/agent.ts`:

```ts
import type { Config } from "./config.js";
import { type AgentEvent, exitCodeFor, HINTS, parseEvent, Renderer } from "./events.js";
import type { Input, Remote, RunResult } from "./ssh.js";
import { PROTOCOL, VERSION } from "../version.js";

// CliError is a failure to show the user without a stack trace.
export class CliError extends Error {
  constructor(
    message: string,
    readonly exitCode = 1,
    readonly hint?: string,
    readonly details: string[] = [],
  ) {
    super(message);
  }
}

export function eventError(e: AgentEvent): CliError {
  const details = e.logs && e.logs.length > 0 ? ["Last container logs:", ...e.logs.map((l) => `  ${l}`)] : [];
  return new CliError(e.message ?? e.code ?? "unknown error", exitCodeFor(e), HINTS[e.code ?? ""], details);
}

// ssh exits with 255 when the connection itself fails.
export function sshError(stderr: string): CliError {
  const text = stderr.trim();
  let hint = "Check that the VPS is reachable and that `sagansync provision` has been run.";
  if (text.includes("REMOTE HOST IDENTIFICATION HAS CHANGED")) {
    hint = "The VPS host key changed. If you reinstalled the server, delete its line from ~/.config/sagansync/known_hosts.";
  } else if (text.includes("Permission denied")) {
    hint = "The deploy key is not authorized on the VPS. Run `sagansync provision`.";
  }
  return new CliError(`Could not connect over SSH: ${text || "unknown error"}`, 1, hint);
}

// failure converts a non-zero RunResult into a CliError.
export function failure(r: RunResult): CliError {
  if (r.code === 255) return sshError(r.stderr);
  const lines = r.stdout.trim().split("\n");
  const e = parseEvent(lines[lines.length - 1] ?? "");
  if (e?.type === "error") return eventError(e);
  if (r.code === 126) return new CliError(`The VPS refused the command: ${r.stderr.trim()}`, 1, "The agent on the VPS may be older than this CLI. Run `sagansync provision --upgrade`.");
  return new CliError(`sagand failed (exit ${r.code}): ${(r.stderr || r.stdout).trim()}`);
}

// checkAgent makes sure sagand answers and speaks our protocol. It returns a
// warning when the versions differ but are compatible.
export async function checkAgent(remote: Remote): Promise<string | undefined> {
  const r = await remote.run(["version"]);
  if (r.code !== 0) throw failure(r);
  let info: { version?: string; protocol?: number };
  try {
    info = JSON.parse(r.stdout);
  } catch {
    throw new CliError("sagand gave an unexpected answer to `version`.", 1, "Run `sagansync provision --upgrade`.");
  }
  if (info.protocol !== PROTOCOL) {
    throw new CliError(`sagand on the VPS speaks protocol ${info.protocol}, this CLI speaks ${PROTOCOL}.`, 1,
      "Run `sagansync provision --upgrade` to install the matching agent.");
  }
  if (info.version !== VERSION) {
    return `sagand ${info.version} is running on the VPS and this CLI is ${VERSION}. Run \`sagansync provision --upgrade\` to align them.`;
  }
  return undefined;
}

// runStreaming runs an event-producing sagand command, renders its progress
// and returns the final done event, or throws on error.
export async function runStreaming(remote: Remote, args: string[], opts: { out: (s: string) => void; verbose?: boolean; stdin?: Input }): Promise<AgentEvent> {
  const renderer = new Renderer(opts.out, opts.verbose);
  const { code, stderr } = await remote.stream(args, (l) => renderer.line(l), opts.stdin);
  const last = renderer.last;
  if (last?.type === "error") throw eventError(last);
  if (last?.type === "done") return last;
  throw failure({ code, stdout: "", stderr });
}

export function workspaceFlags(cfg: Config, workspace: string): string[] {
  return ["--project", cfg.project, "--workspace", workspace];
}

export function domainFlags(cfg: Config): string[] {
  const f: string[] = [];
  if (cfg.domain) f.push("--domain", cfg.domain);
  if (cfg.previewDomain) f.push("--preview-domain", cfg.previewDomain);
  return f;
}

// requestFlags are the flags `sagand deploy` and `sagand dev` share.
export function requestFlags(cfg: Config, workspace: string, sha: string): string[] {
  const f = [...workspaceFlags(cfg, workspace), "--port", String(cfg.internalPort), ...domainFlags(cfg)];
  if (cfg.healthPath) f.push("--health-path", cfg.healthPath);
  if (cfg.healthTimeout) f.push("--health-timeout", String(cfg.healthTimeout));
  if (sha) f.push("--sha", sha);
  return f;
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `npx tsc --noEmit && npx vitest run test/agent.test.ts`
Expected: sem erros de tipo; PASS.

- [ ] **Step 5: Commit**

```bash
git add cli/src/lib/agent.ts cli/test/agent.test.ts
git commit -m "feat(cli): check the agent version and turn failures into clear errors"
```

---

### Task 8: Verificação de DNS (`lib/dns.ts`)

**Files:**
- Create: `cli/src/lib/dns.ts`
- Test: `cli/test/dns.test.ts`

**Interfaces:**
- Consumes: `Config` (T2).
- Produces: `type Lookup = (host) => Promise<string[]>`, `systemLookup`, `dnsWarning(publicHost, vpsHost, lookup?): Promise<string | null>`, `dnsRecords(cfg, address): string[]`.
- Spec 5.3: o aviso nunca interrompe o deploy.

- [ ] **Step 1: Escrever o teste que falha**

`cli/test/dns.test.ts`:

```ts
import { expect, test } from "vitest";
import { dnsRecords, dnsWarning, type Lookup } from "../src/lib/dns.js";

const table = (m: Record<string, string[]>): Lookup => async (h) => {
  const r = m[h];
  if (!r) throw new Error("ENOTFOUND");
  return r;
};

test("no warning when the host points to the VPS", async () => {
  expect(await dnsWarning("app.test", "vps.test", table({ "vps.test": ["203.0.113.7"], "app.test": ["203.0.113.7"] }))).toBeNull();
});

test("warns when the host does not resolve", async () => {
  expect(await dnsWarning("app.test", "vps.test", table({ "vps.test": ["203.0.113.7"] }))).toContain("does not resolve");
});

test("warns when the host points elsewhere", async () => {
  const w = await dnsWarning("app.test", "vps.test", table({ "vps.test": ["203.0.113.7"], "app.test": ["198.51.100.1"] }));
  expect(w).toContain("198.51.100.1");
  expect(w).toContain("203.0.113.7");
});

test("stays quiet when the VPS itself cannot be resolved", async () => {
  expect(await dnsWarning("app.test", "vps.test", table({}))).toBeNull();
});

test("dnsRecords suggests one wildcard for previews", () => {
  expect(dnsRecords({ domain: "api.example.com", previewDomain: "example.com" }, "203.0.113.7")).toEqual([
    "A  api.example.com  ->  203.0.113.7",
    "A  *.example.com  ->  203.0.113.7   (one wildcard for every project's branches)",
  ]);
  expect(dnsRecords({}, "x")).toEqual([]);
});
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `npx vitest run test/dns.test.ts`
Expected: FAIL — o vitest não encontra `../src/lib/dns.js`.

- [ ] **Step 3: Implementar**

`cli/src/lib/dns.ts`:

```ts
import { promises as dns } from "node:dns";
import type { Config } from "./config.js";

export type Lookup = (host: string) => Promise<string[]>;

export const systemLookup: Lookup = async (host) => (await dns.lookup(host, { all: true })).map((a) => a.address);

// dnsWarning explains what to fix when publicHost does not resolve to the VPS.
// It returns null when everything matches or when the VPS itself cannot be
// resolved (nothing useful to compare against).
export async function dnsWarning(publicHost: string, vpsHost: string, lookup: Lookup = systemLookup): Promise<string | null> {
  let vps: string[];
  try {
    vps = await lookup(vpsHost);
  } catch {
    return null;
  }
  let pub: string[];
  try {
    pub = await lookup(publicHost);
  } catch {
    return `${publicHost} does not resolve yet. Create a DNS record pointing it to ${vps[0]}, or HTTPS will not work.`;
  }
  if (pub.some((a) => vps.includes(a))) return null;
  return `${publicHost} resolves to ${pub.join(", ")}, but the VPS is ${vps.join(", ")}. Point it to the VPS, or HTTPS will not work.`;
}

// dnsRecords lists the records a project needs, for `sagansync init`.
export function dnsRecords(cfg: Pick<Config, "domain" | "previewDomain">, address: string): string[] {
  const records: string[] = [];
  if (cfg.domain) records.push(`A  ${cfg.domain}  ->  ${address}`);
  if (cfg.previewDomain) records.push(`A  *.${cfg.previewDomain}  ->  ${address}   (one wildcard for every project's branches)`);
  else if (cfg.domain) records.push(`A  *.${cfg.domain}  ->  ${address}   (only needed for branch workspaces)`);
  return records;
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `npx tsc --noEmit && npx vitest run test/dns.test.ts`
Expected: sem erros de tipo; PASS.

- [ ] **Step 5: Commit**

```bash
git add cli/src/lib/dns.ts cli/test/dns.test.ts
git commit -m "feat(cli): warn when the workspace hostname does not point to the VPS"
```

---

### Task 9: Comandos `deploy`, `list`, `logs`, `remove` e `env`

**Files:**
- Create: `cli/src/commands/context.ts`
- Create: `cli/src/commands/deploy.ts`
- Create: `cli/src/commands/list.ts`
- Create: `cli/src/commands/logs.ts`
- Create: `cli/src/commands/remove.ts`
- Create: `cli/src/commands/env.ts`
- Create: `cli/test/helpers/ctx.ts`
- Test: `cli/test/commands.test.ts`

**Interfaces:**
- Consumes: T2–T8.
- Produces:
  - `type Ctx = { cwd; config; remote; out; lookup? }`, `warn(ctx, msg)`, `preflight(ctx)`, `checkDns(ctx, ws)` (pergunta o host ao `sagand host`), `collect(ctx): FileList`
  - `deploy(ctx, { workspace?, verbose? })`
  - `list(ctx, { all? })`, `formatTable(headers, rows)`
  - `logs(ctx, { workspace?, tail?, follow? })`
  - `remove(ctx, { workspace?, yes?, confirm })`
  - `envSet(ctx, pairs, { workspace?, file? })`, `envUnset(ctx, keys, { workspace? })`, `envList(ctx, { workspace? })`, `parseDotenv(text)`, `parsePairs(pairs)`
- Test helper: `projectDir(files?)`, `pointsToVps`, `testCtx(remote, cwd?, extraConfig?)` (com `lines` para inspecionar a saída).

- [ ] **Step 1: Escrever o teste que falha**

`cli/test/helpers/ctx.ts`:

```ts
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import type { Ctx } from "../../src/commands/context.js";
import { validateConfig } from "../../src/lib/config.js";
import type { Lookup } from "../../src/lib/dns.js";
import { FakeRemote } from "./fakeRemote.js";

// projectDir creates a throwaway project (outside git, so the workspace
// defaults to "production").
export function projectDir(files: Record<string, string> = { "Dockerfile": "FROM node\n", "server.js": "1\n" }): string {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-proj-"));
  for (const [rel, body] of Object.entries(files)) {
    fs.mkdirSync(path.dirname(path.join(root, rel)), { recursive: true });
    fs.writeFileSync(path.join(root, rel), body);
  }
  return root;
}

export const pointsToVps: Lookup = async () => ["203.0.113.7"];

export function testCtx(remote: FakeRemote, cwd = projectDir(), extra: Record<string, unknown> = {}): Ctx & { lines: string[] } {
  const lines: string[] = [];
  const config = validateConfig({ host: "vps.test", project: "app", internalPort: 3000, domain: "app.test", ...extra });
  return { cwd, config, remote, lookup: pointsToVps, out: (s) => lines.push(s), lines };
}
```

`cli/test/commands.test.ts`:

```ts
import fs from "node:fs";
import path from "node:path";
import { gunzipSync } from "node:zlib";
import { describe, expect, test } from "vitest";
import { deploy } from "../src/commands/deploy.js";
import { envList, envSet, envUnset, parseDotenv, parsePairs } from "../src/commands/env.js";
import { list } from "../src/commands/list.js";
import { logs } from "../src/commands/logs.js";
import { remove } from "../src/commands/remove.js";
import { projectDir, testCtx } from "./helpers/ctx.js";
import { ev, FakeRemote, VERSION_OK } from "./helpers/fakeRemote.js";

const doneDeploy = { stdout: [ev({ type: "step", name: "build" }), ev({ type: "done", url: "https://app.test", release: "r1", hostPort: 41000 })].join("\n") };

describe("deploy", () => {
  test("uploads the project and reports the URL", async () => {
    const remote = new FakeRemote({ version: VERSION_OK, host: { stdout: '{"host":"app.test"}' }, deploy: doneDeploy });
    const ctx = testCtx(remote, projectDir({ "Dockerfile": "FROM node\n", ".env": "SECRET=1" }));
    await deploy(ctx, {});
    expect(remote.argsOf("deploy")).toEqual(["deploy", "--project", "app", "--workspace", "production", "--port", "3000", "--domain", "app.test"]);
    const upload = gunzipSync(Buffer.from(remote.calls.find((c) => c.args[0] === "deploy")!.stdin, "latin1")).toString("latin1");
    expect(upload).toContain("Dockerfile");
    expect(upload).not.toContain("SECRET=1");
    expect(ctx.lines.at(-1)).toBe("✔ Live at https://app.test");
  });

  test("warns about DNS without failing", async () => {
    const remote = new FakeRemote({ version: VERSION_OK, host: { stdout: '{"host":"app.test"}' }, deploy: doneDeploy });
    const ctx = testCtx(remote);
    ctx.lookup = async (h) => (h === "vps.test" ? ["203.0.113.7"] : ["198.51.100.1"]);
    await deploy(ctx, {});
    expect(ctx.lines.some((l) => l.includes("198.51.100.1"))).toBe(true);
    expect(ctx.lines.at(-1)).toContain("Live at");
  });

  test("skips the DNS check without domains and shows the port", async () => {
    const remote = new FakeRemote({ version: VERSION_OK, deploy: { stdout: ev({ type: "done", hostPort: 41000 }) } });
    const ctx = testCtx(remote);
    delete ctx.config.domain;
    await deploy(ctx, {});
    expect(remote.argsOf("host")).toBeUndefined();
    expect(ctx.lines.at(-1)).toContain("port 41000");
  });

  test("an explicit workspace is used", async () => {
    const remote = new FakeRemote({ version: VERSION_OK, deploy: doneDeploy });
    await deploy(testCtx(remote), { workspace: "feat-x" });
    expect(remote.argsOf("deploy")).toContain("feat-x");
  });
});

describe("list", () => {
  const statuses = JSON.stringify([
    { project: "app", workspace: "production", mode: "deploy", release: "r1", url: "https://app.test", hostPort: 1, running: true },
    { project: "other", workspace: "production", mode: "deploy", release: "r9", hostPort: 2, running: false },
  ]);
  test("shows this project's workspaces", async () => {
    const ctx = testCtx(new FakeRemote({ version: VERSION_OK, list: { stdout: statuses } }));
    await list(ctx, {});
    expect(ctx.lines).toEqual(["WORKSPACE   MODE    STATUS   RELEASE  URL", "production  deploy  running  r1       https://app.test"]);
  });
  test("--all shows every project", async () => {
    const ctx = testCtx(new FakeRemote({ version: VERSION_OK, list: { stdout: statuses } }));
    await list(ctx, { all: true });
    expect(ctx.lines).toHaveLength(3);
    expect(ctx.lines[2]).toContain("127.0.0.1:2");
  });
});

describe("logs", () => {
  test("prints lines and passes flags", async () => {
    const remote = new FakeRemote({ version: VERSION_OK, logs: { stdout: "hello\n{\"level\":\"info\"}\n" } });
    const ctx = testCtx(remote);
    await logs(ctx, { tail: 20, follow: true });
    expect(remote.argsOf("logs")).toEqual(["logs", "--project", "app", "--workspace", "production", "--tail", "20", "-f"]);
    expect(ctx.lines).toEqual(["hello", '{"level":"info"}']);
  });
  test("turns an error event into an error", async () => {
    const remote = new FakeRemote({ version: VERSION_OK, logs: { code: 1, stdout: ev({ type: "error", code: "not_found", message: "workspace app/production does not exist" }) } });
    await expect(logs(testCtx(remote), {})).rejects.toThrow("does not exist");
  });
});

describe("remove", () => {
  test("asks first and can be cancelled", async () => {
    const remote = new FakeRemote({ version: VERSION_OK });
    const ctx = testCtx(remote);
    await remove(ctx, { confirm: async () => false });
    expect(remote.calls).toHaveLength(0);
    expect(ctx.lines).toEqual(["Cancelled."]);
  });
  test("--yes removes without asking", async () => {
    const remote = new FakeRemote({ version: VERSION_OK, remove: { stdout: ev({ type: "done" }) } });
    const ctx = testCtx(remote);
    await remove(ctx, { yes: true, confirm: async () => { throw new Error("should not ask"); } });
    expect(remote.argsOf("remove")).toEqual(["remove", "--project", "app", "--workspace", "production"]);
    expect(ctx.lines.at(-1)).toBe("✔ Removed app/production");
  });
});

describe("env", () => {
  test("parseDotenv", () => {
    expect(parseDotenv('# c\n\nexport A=1\nB = "two words"\nC=\'x=y\'\nD="l1\\nl2"\nE=\n')).toEqual({ A: "1", B: "two words", C: "x=y", D: "l1\nl2", E: "" });
    expect(() => parseDotenv("not a pair")).toThrow("Line 1");
  });

  test("parsePairs keeps everything after the first =", () => {
    expect(parsePairs(["URL=postgres://u:p@h/db?a=b"])).toEqual({ URL: "postgres://u:p@h/db?a=b" });
    expect(() => parsePairs(["=x"])).toThrow("KEY=VALUE");
    expect(() => parsePairs(["1A=x"])).toThrow("KEY=VALUE");
  });

  test("set sends values on stdin, merging a file and pairs", async () => {
    const remote = new FakeRemote({ version: VERSION_OK });
    const ctx = testCtx(remote);
    const file = path.join(ctx.cwd, ".env.production");
    fs.writeFileSync(file, "A=from-file\nB=file\n");
    await envSet(ctx, ["B=pair", "C=it's \"quoted\""], { file });
    const call = remote.calls.find((c) => c.args[0] === "env")!;
    expect(call.args).toEqual(["env", "set", "--project", "app", "--workspace", "production"]);
    expect(JSON.parse(call.stdin)).toEqual({ A: "from-file", B: "pair", C: `it's "quoted"` });
    expect(call.args.join(" ")).not.toContain("from-file");
  });

  test("set needs something to set", async () => {
    await expect(envSet(testCtx(new FakeRemote()), [], {})).rejects.toMatchObject({ exitCode: 2 });
  });

  test("unset and list", async () => {
    const remote = new FakeRemote({ version: VERSION_OK, "env list": { stdout: '{"keys":["A","B"]}' } });
    const ctx = testCtx(remote);
    await envUnset(ctx, ["A"], {});
    await envList(ctx, {});
    expect(remote.calls.find((c) => c.args[1] === "unset")!.args.slice(-1)).toEqual(["A"]);
    expect(ctx.lines.slice(-2)).toEqual(["A=********", "B=********"]);
  });
});
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `npx vitest run test/commands.test.ts`
Expected: FAIL — o vitest não encontra `../src/commands/deploy.js` (e os demais).

- [ ] **Step 3: Implementar**

`cli/src/commands/context.ts`:

```ts
import { checkAgent, domainFlags, workspaceFlags } from "../lib/agent.js";
import type { Config } from "../lib/config.js";
import { dnsWarning, type Lookup } from "../lib/dns.js";
import { listFiles, type FileList } from "../lib/pack.js";
import type { Remote } from "../lib/ssh.js";
import { paint } from "../lib/style.js";

// Ctx is everything a command needs; tests build it with fakes.
export type Ctx = {
  cwd: string;
  config: Config;
  remote: Remote;
  out: (line: string) => void;
  lookup?: Lookup;
};

export function warn(ctx: Ctx, message: string): void {
  ctx.out(paint("yellow", `! ${message}`));
}

// preflight checks that the agent answers and speaks our protocol.
export async function preflight(ctx: Ctx): Promise<void> {
  const warning = await checkAgent(ctx.remote);
  if (warning) warn(ctx, warning);
}

// checkDns warns (without failing) when the workspace hostname does not point
// to the VPS. The hostname comes from sagand so the rule lives in one place.
export async function checkDns(ctx: Ctx, workspace: string): Promise<void> {
  if (!ctx.config.domain && !ctx.config.previewDomain) return;
  const r = await ctx.remote.run(["host", ...workspaceFlags(ctx.config, workspace), ...domainFlags(ctx.config)]);
  if (r.code !== 0) return;
  let host = "";
  try {
    host = (JSON.parse(r.stdout) as { host?: string }).host ?? "";
  } catch {
    return;
  }
  if (!host) return;
  const w = await dnsWarning(host, ctx.config.host, ctx.lookup);
  if (w) warn(ctx, w);
}

// collect lists the project files and reports skipped symlinks.
export function collect(ctx: Ctx): FileList {
  const files = listFiles(ctx.cwd);
  for (const s of files.skipped) warn(ctx, `Skipping symlink that points outside the project: ${s}`);
  return files;
}
```

`cli/src/commands/deploy.ts`:

```ts
import { requestFlags, runStreaming } from "../lib/agent.js";
import { currentSha, resolveWorkspace } from "../lib/git.js";
import { packProject } from "../lib/pack.js";
import { paint } from "../lib/style.js";
import { checkDns, collect, type Ctx, preflight } from "./context.js";

export type DeployOptions = { workspace?: string; verbose?: boolean };

export async function deploy(ctx: Ctx, opts: DeployOptions): Promise<void> {
  const workspace = resolveWorkspace(ctx.cwd, opts.workspace);
  await preflight(ctx);
  await checkDns(ctx, workspace);
  const { entries } = collect(ctx);
  ctx.out(`Deploying ${ctx.config.project}/${workspace} (${entries.filter((e) => e.type === "file").length} files)`);
  const done = await runStreaming(ctx.remote, ["deploy", ...requestFlags(ctx.config, workspace, currentSha(ctx.cwd))], {
    out: ctx.out, verbose: opts.verbose, stdin: packProject(entries),
  });
  ctx.out(paint("green", done.url ? `✔ Live at ${done.url}` : `✔ Running on port ${done.hostPort} of the VPS (no domain configured)`));
}
```

`cli/src/commands/list.ts`:

```ts
import { failure } from "../lib/agent.js";
import { type Ctx, preflight } from "./context.js";

type Status = { project: string; workspace: string; mode: string; release: string; url?: string; hostPort: number; running: boolean };

export function formatTable(headers: string[], rows: string[][]): string[] {
  const widths = headers.map((h, i) => Math.max(h.length, ...rows.map((r) => (r[i] ?? "").length)));
  const fmt = (cells: string[]) => cells.map((c, i) => c.padEnd(widths[i] ?? 0)).join("  ").trimEnd();
  return [fmt(headers), ...rows.map(fmt)];
}

export async function list(ctx: Ctx, opts: { all?: boolean }): Promise<void> {
  await preflight(ctx);
  const r = await ctx.remote.run(["list"]);
  if (r.code !== 0) throw failure(r);
  const all = JSON.parse(r.stdout) as Status[];
  const rows = opts.all ? all : all.filter((s) => s.project === ctx.config.project);
  if (rows.length === 0) {
    ctx.out(opts.all ? "Nothing is deployed on this VPS yet." : `Nothing deployed for ${ctx.config.project} yet. Run \`sagansync deploy\`.`);
    return;
  }
  const headers = opts.all ? ["PROJECT", "WORKSPACE", "MODE", "STATUS", "RELEASE", "URL"] : ["WORKSPACE", "MODE", "STATUS", "RELEASE", "URL"];
  const table = rows.map((s) => {
    const cells = [s.workspace, s.mode, s.running ? "running" : "stopped", s.release, s.url ?? `127.0.0.1:${s.hostPort}`];
    return opts.all ? [s.project, ...cells] : cells;
  });
  for (const line of formatTable(headers, table)) ctx.out(line);
}
```

`cli/src/commands/logs.ts`:

```ts
import { eventError, failure, workspaceFlags } from "../lib/agent.js";
import { type AgentEvent, parseEvent } from "../lib/events.js";
import { resolveWorkspace } from "../lib/git.js";
import { type Ctx, preflight } from "./context.js";

export type LogsOptions = { workspace?: string; tail?: number; follow?: boolean };

export async function logs(ctx: Ctx, opts: LogsOptions): Promise<void> {
  const workspace = resolveWorkspace(ctx.cwd, opts.workspace);
  await preflight(ctx);
  const args = ["logs", ...workspaceFlags(ctx.config, workspace), "--tail", String(opts.tail ?? 100)];
  if (opts.follow) args.push("-f");
  let error: AgentEvent | null = null;
  const { code, stderr } = await ctx.remote.stream(args, (line) => {
    const e = parseEvent(line);
    if (e?.type === "error") error = e;
    else ctx.out(line);
  });
  if (error) throw eventError(error);
  if (code !== 0) throw failure({ code, stdout: "", stderr });
}
```

`cli/src/commands/remove.ts`:

```ts
import { runStreaming, workspaceFlags } from "../lib/agent.js";
import { resolveWorkspace } from "../lib/git.js";
import { paint } from "../lib/style.js";
import { type Ctx, preflight } from "./context.js";

export type RemoveOptions = { workspace?: string; yes?: boolean; confirm: (message: string) => Promise<boolean> };

export async function remove(ctx: Ctx, opts: RemoveOptions): Promise<void> {
  const workspace = resolveWorkspace(ctx.cwd, opts.workspace);
  const what = `${ctx.config.project}/${workspace}`;
  if (!opts.yes && !(await opts.confirm(`Remove ${what}? Its container, releases, files and env vars will be deleted.`))) {
    ctx.out("Cancelled.");
    return;
  }
  await preflight(ctx);
  await runStreaming(ctx.remote, ["remove", ...workspaceFlags(ctx.config, workspace)], { out: ctx.out });
  ctx.out(paint("green", `✔ Removed ${what}`));
}
```

`cli/src/commands/env.ts`:

```ts
import fs from "node:fs";
import { CliError, failure, workspaceFlags } from "../lib/agent.js";
import { resolveWorkspace } from "../lib/git.js";
import { paint } from "../lib/style.js";
import { type Ctx, preflight } from "./context.js";

const KEY_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;

export type EnvOptions = { workspace?: string };

// parseDotenv reads KEY=VALUE lines. Blank lines and # comments are skipped,
// an optional "export " prefix is allowed, and matching single or double
// quotes around the value are removed ("\n" inside double quotes is a newline).
export function parseDotenv(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  text.split(/\r?\n/).forEach((raw, i) => {
    const line = raw.trim();
    if (!line || line.startsWith("#")) return;
    const m = /^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$/.exec(line);
    if (!m) throw new CliError(`Line ${i + 1} is not KEY=VALUE: ${raw}`, 2);
    let value = m[2] ?? "";
    if (value.length >= 2 && value.startsWith('"') && value.endsWith('"')) value = value.slice(1, -1).replaceAll("\\n", "\n");
    else if (value.length >= 2 && value.startsWith("'") && value.endsWith("'")) value = value.slice(1, -1);
    out[m[1]!] = value;
  });
  return out;
}

export function parsePairs(pairs: string[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const p of pairs) {
    const i = p.indexOf("=");
    const key = i > 0 ? p.slice(0, i) : "";
    if (!KEY_RE.test(key)) throw new CliError(`Expected KEY=VALUE, got "${p}".`, 2);
    out[key] = p.slice(i + 1);
  }
  return out;
}

export async function envSet(ctx: Ctx, pairs: string[], opts: EnvOptions & { file?: string }): Promise<void> {
  const workspace = resolveWorkspace(ctx.cwd, opts.workspace);
  const kv = { ...(opts.file ? parseDotenv(fs.readFileSync(opts.file, "utf8")) : {}), ...parsePairs(pairs) };
  const keys = Object.keys(kv);
  if (keys.length === 0) throw new CliError("Nothing to set. Pass KEY=VALUE pairs or --file <path>.", 2);
  await preflight(ctx);
  // Values travel on stdin, never on the remote command line.
  const r = await ctx.remote.run(["env", "set", ...workspaceFlags(ctx.config, workspace)], JSON.stringify(kv));
  if (r.code !== 0) throw failure(r);
  ctx.out(paint("green", `✔ Set ${keys.sort().join(", ")} on ${workspace}.`) + " Redeploy to apply: sagansync deploy");
}

export async function envUnset(ctx: Ctx, keys: string[], opts: EnvOptions): Promise<void> {
  const workspace = resolveWorkspace(ctx.cwd, opts.workspace);
  if (keys.length === 0) throw new CliError("Pass the names of the variables to remove.", 2);
  await preflight(ctx);
  const r = await ctx.remote.run(["env", "unset", ...workspaceFlags(ctx.config, workspace), ...keys]);
  if (r.code !== 0) throw failure(r);
  ctx.out(paint("green", `✔ Removed ${keys.join(", ")} from ${workspace}.`) + " Redeploy to apply: sagansync deploy");
}

export async function envList(ctx: Ctx, opts: EnvOptions): Promise<void> {
  const workspace = resolveWorkspace(ctx.cwd, opts.workspace);
  await preflight(ctx);
  const r = await ctx.remote.run(["env", "list", ...workspaceFlags(ctx.config, workspace)]);
  if (r.code !== 0) throw failure(r);
  const keys = (JSON.parse(r.stdout) as { keys: string[] }).keys;
  if (keys.length === 0) ctx.out(`No variables set on ${workspace}.`);
  for (const k of keys) ctx.out(`${k}=********`);
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `npx tsc --noEmit && npx vitest run test/commands.test.ts`
Expected: sem erros de tipo; PASS.

- [ ] **Step 5: Commit**

```bash
git add cli/src/commands/context.ts cli/src/commands/deploy.ts cli/src/commands/env.ts cli/src/commands/list.ts cli/src/commands/logs.ts cli/src/commands/remove.ts cli/test/commands.test.ts cli/test/helpers/ctx.ts
git commit -m "feat(cli): add deploy, list, logs, remove and env commands"
```

---

### Task 10: Comando `dev`

**Files:**
- Create: `cli/src/commands/dev.ts`
- Test: `cli/test/dev.test.ts`

**Interfaces:**
- Consumes: T4–T9 (`Ctx`, `preflight`, `checkDns`, `collect`, `runStreaming`, `requestFlags`, `failure`, `CliError`, `HINTS`, `ignoreMatcher`, `packProject`, `branchState`, `currentSha`, `resolveWorkspace`).
- Produces:
  - `type DevOptions = { workspace?; command?; build?; force?; verbose?; onBranchChange? }`
  - `createSyncer(ctx, ws): { put(rel); rm(rel); idle() }` — fila serial; falha é mostrada e a fila segue.
  - `dev(ctx, opts): Promise<{ close(); idle() }>` — trava de produção (sem `--force`), `sagand dev` com `--command '["sh","-c",<cmd>]'`, watcher com os mesmos ignores do upload, parada automática se o branch mudar.

- [ ] **Step 1: Escrever o teste que falha**

`cli/test/dev.test.ts`:

```ts
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { afterEach, describe, expect, test, vi } from "vitest";
import { createSyncer, dev, type DevSession } from "../src/commands/dev.js";
import { projectDir, testCtx } from "./helpers/ctx.js";
import { ev, FakeRemote, VERSION_OK } from "./helpers/fakeRemote.js";

const devDone = { stdout: ev({ type: "done", url: "https://feat-x.app.test" }) };

function gitRepo(branch: string): string {
  const dir = projectDir();
  const run = (...a: string[]) => execFileSync("git", a, { cwd: dir, stdio: "ignore" });
  run("init", "-q", "-b", branch);
  run("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init");
  return dir;
}

describe("createSyncer", () => {
  test("sends puts and removes in order and reports failures without stopping", async () => {
    const remote = new FakeRemote({ put: (c) => ({ code: c.args[3] === "bad.ts" ? 1 : 0, stderr: "nope" }) });
    const ctx = testCtx(remote, projectDir({ "a.ts": "A", "bad.ts": "B" }));
    const s = createSyncer(ctx, "feat-x");
    void s.put("a.ts");
    void s.put("bad.ts");
    void s.rm("old.ts");
    await s.idle();
    expect(remote.calls.map((c) => c.args)).toEqual([
      ["put", "app", "feat-x", "a.ts"], ["put", "app", "feat-x", "bad.ts"], ["rm", "app", "feat-x", "old.ts"],
    ]);
    expect(remote.calls[0]!.stdin).toBe("A");
    expect(ctx.lines).toEqual(["  ↑ a.ts", expect.stringContaining("✖ ↑ bad.ts"), "  ✕ old.ts"]);
  });
});

describe("dev", () => {
  let session: DevSession | undefined;
  afterEach(async () => {
    await session?.close();
    session = undefined;
  });

  test("refuses production without --force", async () => {
    const remote = new FakeRemote({ version: VERSION_OK });
    await expect(dev(testCtx(remote), {})).rejects.toThrow("production");
    expect(remote.calls).toHaveLength(0);
  });

  test("starts dev mode with the command wrapped in sh -c", async () => {
    const remote = new FakeRemote({ version: VERSION_OK, host: { stdout: '{"host":"feat-x.app.test"}' }, dev: devDone });
    const ctx = testCtx(remote);
    session = await dev(ctx, { workspace: "feat-x", command: "pnpm dev --port 3000", build: true });
    const args = remote.argsOf("dev")!;
    expect(args.slice(args.indexOf("--command"))).toEqual(["--command", '["sh","-c","pnpm dev --port 3000"]', "--build"]);
    expect(ctx.lines).toContain("✔ Dev server at https://feat-x.app.test");
  });

  test("syncs edits, skips ignored files", async () => {
    const remote = new FakeRemote({ version: VERSION_OK, host: { stdout: "{}" }, dev: devDone });
    const ctx = testCtx(remote);
    session = await dev(ctx, { workspace: "feat-x" });
    fs.writeFileSync(path.join(ctx.cwd, ".env"), "SECRET=1");
    fs.writeFileSync(path.join(ctx.cwd, "new.ts"), "hello");
    await vi.waitFor(() => expect(remote.calls.some((c) => c.args[0] === "put")).toBe(true), { timeout: 5000 });
    await session.idle();
    const puts = remote.calls.filter((c) => c.args[0] === "put");
    expect(puts.map((c) => c.args[3])).toEqual(["new.ts"]);
    expect(puts[0]!.stdin).toBe("hello");
    fs.rmSync(path.join(ctx.cwd, "new.ts"));
    await vi.waitFor(() => expect(remote.calls.some((c) => c.args[0] === "rm" && c.args[3] === "new.ts")).toBe(true), { timeout: 5000 });
  });

  test("stops when the git branch changes", async () => {
    const cwd = gitRepo("feat-x");
    const remote = new FakeRemote({ version: VERSION_OK, host: { stdout: "{}" }, dev: devDone });
    const ctx = testCtx(remote, cwd);
    let stopped = false;
    session = await dev(ctx, { onBranchChange: () => (stopped = true) });
    execFileSync("git", ["checkout", "-q", "-b", "other"], { cwd });
    await vi.waitFor(() => expect(stopped).toBe(true), { timeout: 5000 });
    expect(ctx.lines.some((l) => l.includes("branch changed"))).toBe(true);
  });
});
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `npx vitest run test/dev.test.ts`
Expected: FAIL — o vitest não encontra `../src/commands/dev.js`.

- [ ] **Step 3: Implementar**

`cli/src/commands/dev.ts`:

```ts
import fs from "node:fs";
import path from "node:path";
import chokidar from "chokidar";
import { CliError, failure, requestFlags, runStreaming } from "../lib/agent.js";
import { HINTS } from "../lib/events.js";
import { branchState, currentSha, resolveWorkspace } from "../lib/git.js";
import { ignoreMatcher, packProject } from "../lib/pack.js";
import type { RunResult } from "../lib/ssh.js";
import { paint } from "../lib/style.js";
import { checkDns, collect, type Ctx, preflight } from "./context.js";

export type DevOptions = {
  workspace?: string;
  command?: string;
  build?: boolean;
  force?: boolean;
  verbose?: boolean;
  onBranchChange?: () => void;
};

export type Syncer = { put(rel: string): Promise<void>; rm(rel: string): Promise<void>; idle(): Promise<void> };

// createSyncer sends file changes to the dev workspace one at a time, in the
// order they happened. A failed sync is reported and does not stop the queue.
export function createSyncer(ctx: Ctx, workspace: string): Syncer {
  let chain: Promise<void> = Promise.resolve();
  const enqueue = (label: string, op: () => Promise<RunResult>) => {
    chain = chain.then(async () => {
      try {
        const r = await op();
        if (r.code !== 0) throw failure(r);
        ctx.out(paint("gray", `  ${label}`));
      } catch (err) {
        ctx.out(paint("red", `  ✖ ${label}: ${(err as Error).message}`));
      }
    });
    return chain;
  };
  const { project } = ctx.config;
  return {
    put: (rel) => enqueue(`↑ ${rel}`, () => ctx.remote.run(["put", project, workspace, rel], fs.createReadStream(path.join(ctx.cwd, rel)))),
    rm: (rel) => enqueue(`✕ ${rel}`, () => ctx.remote.run(["rm", project, workspace, rel])),
    idle: () => chain,
  };
}

export type DevSession = { close(): Promise<void>; idle(): Promise<void> };

// dev starts the workspace in dev mode and keeps it in sync with local edits.
// It stops if the git branch changes, so one branch never writes into
// another branch's workspace.
export async function dev(ctx: Ctx, opts: DevOptions): Promise<DevSession> {
  const workspace = resolveWorkspace(ctx.cwd, opts.workspace);
  if (workspace === "production" && !opts.force) {
    throw new CliError("Dev mode is disabled on the production workspace.", 1, HINTS.production_locked);
  }
  await preflight(ctx);
  await checkDns(ctx, workspace);
  const { entries } = collect(ctx);
  const args = ["dev", ...requestFlags(ctx.config, workspace, currentSha(ctx.cwd)),
    "--command", JSON.stringify(["sh", "-c", opts.command ?? "npm run dev"])];
  if (opts.build) args.push("--build");
  if (opts.force) args.push("--force");
  const done = await runStreaming(ctx.remote, args, { out: ctx.out, verbose: opts.verbose, stdin: packProject(entries) });
  ctx.out(paint("green", done.url ? `✔ Dev server at ${done.url}` : `✔ Dev server on port ${done.hostPort} of the VPS`));

  const syncer = createSyncer(ctx, workspace);
  const match = ignoreMatcher(ctx.cwd);
  const rel = (p: string) => path.relative(ctx.cwd, p).split(path.sep).join("/");
  const watcher = chokidar.watch(ctx.cwd, {
    ignoreInitial: true,
    ignored: (p, stats) => {
      const r = rel(p);
      return r !== "" && match(r, stats?.isDirectory() ?? false);
    },
    awaitWriteFinish: { stabilityThreshold: 100, pollInterval: 25 },
  });
  watcher
    .on("add", (p) => void syncer.put(rel(p)))
    .on("change", (p) => void syncer.put(rel(p)))
    .on("unlink", (p) => void syncer.rm(rel(p)))
    .on("unlinkDir", (p) => void syncer.rm(rel(p)));
  await new Promise<void>((resolve) => watcher.once("ready", () => resolve()));

  // git replaces .git/HEAD with a rename on checkout, which file watchers miss,
  // so the branch is polled instead.
  const start = JSON.stringify(branchState(ctx.cwd));
  let stopped = false;
  const close = async () => {
    stopped = true;
    clearInterval(timer);
    await watcher.close();
  };
  const timer = setInterval(() => {
    if (stopped || JSON.stringify(branchState(ctx.cwd)) === start) return;
    ctx.out(paint("red", `✖ The git branch changed. Stopped syncing to ${workspace} so it does not receive another branch's files.`));
    void close().then(() => opts.onBranchChange?.());
  }, 1000);
  ctx.out("Watching for changes (Ctrl+C to stop)...");
  return { close, idle: () => syncer.idle() };
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `npx tsc --noEmit && npx vitest run test/dev.test.ts`
Expected: sem erros de tipo; PASS.

- [ ] **Step 5: Commit**

```bash
git add cli/src/commands/dev.ts cli/test/dev.test.ts
git commit -m "feat(cli): add dev mode with live file sync"
```

---

### Task 11: `init`, relatório de erros e `main`

**Files:**
- Create: `cli/src/commands/init.ts`
- Create: `cli/src/lib/report.ts`
- Create: `cli/src/main.ts`
- Test: `cli/test/init.test.ts`
- Test: `cli/test/report.test.ts`

**Interfaces:**
- Consumes: todos os comandos (T9, T10), `loadConfig`/`validateConfig`/`saveConfig`/`keyPath` (T2), `dnsRecords` (T8), `sshRemote`/`targetFor` (T4).
- Produces:
  - `type InitAnswers`, `type InitDeps = { ask; confirmOverwrite; keygen; out }`, `init(cwd, deps): Promise<Config | null>`, `defaultProject(cwd)`, `sshKeygen(keyFile, comment)`, `askInteractively(defaults)`
  - `report(err, write?): number` — `CliError` (mensagem, detalhes, dica, código), `ConfigError` → 2, `ExitPromptError` → 130, resto → 1.
  - `src/main.ts`: `init`, `deploy`, `dev`, `list`, `logs`, `remove`, `env set|unset|list`.

- [ ] **Step 1: Escrever o teste que falha**

`cli/test/init.test.ts`:

```ts
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, expect, test } from "vitest";
import { defaultProject, init, type InitAnswers } from "../src/commands/init.js";
import { loadConfig } from "../src/lib/config.js";

const answers: InitAnswers = { host: "203.0.113.7", sshPort: 22, project: "app", internalPort: 3000, domain: "api.example.com", previewDomain: "example.com" };
let home: string;

beforeEach(() => {
  home = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-home-"));
  process.env.SAGANSYNC_HOME = home;
});
afterEach(() => {
  delete process.env.SAGANSYNC_HOME;
});

function deps(overrides: Partial<Parameters<typeof init>[1]> = {}) {
  const lines: string[] = [];
  const keys: string[] = [];
  return {
    lines, keys,
    d: { ask: async () => answers, confirmOverwrite: async () => false, keygen: (k: string) => { keys.push(k); fs.writeFileSync(k, "key"); }, out: (s: string) => lines.push(s), ...overrides },
  };
}

test("writes the config, creates the key and prints DNS records", async () => {
  const cwd = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-init-"));
  const { d, lines, keys } = deps();
  await init(cwd, d);
  expect(loadConfig(cwd)).toEqual({ ...answers, user: "sagan" });
  expect(keys).toEqual([path.join(home, "keys", "203.0.113.7_ed25519")]);
  expect(lines).toContain("  A  *.example.com  ->  203.0.113.7   (one wildcard for every project's branches)");
  expect(lines).toContain("  sagansync provision --admin root@203.0.113.7   # installs sagand on the VPS");
});

test("reuses an existing key", async () => {
  const cwd = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-init-"));
  fs.mkdirSync(path.join(home, "keys"), { recursive: true });
  fs.writeFileSync(path.join(home, "keys", "203.0.113.7_ed25519"), "old");
  const { d, keys } = deps();
  await init(cwd, d);
  expect(keys).toEqual([]);
});

test("keeps an existing config unless confirmed", async () => {
  const cwd = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-init-"));
  fs.mkdirSync(path.join(cwd, ".sagansync"));
  fs.writeFileSync(path.join(cwd, ".sagansync", "config.json"), "{}");
  const { d } = deps();
  expect(await init(cwd, d)).toBeNull();
  expect(fs.readFileSync(path.join(cwd, ".sagansync", "config.json"), "utf8")).toBe("{}");
});

test("rejects invalid answers", async () => {
  const cwd = fs.mkdtempSync(path.join(os.tmpdir(), "sgs-init-"));
  const { d } = deps({ ask: async () => ({ ...answers, project: "Bad Name" }) });
  await expect(init(cwd, d)).rejects.toThrow("project");
});

test("defaultProject sanitizes the folder name", () => {
  expect(defaultProject("/work/My Cool_App")).toBe("my-cool-app");
  expect(defaultProject("/work/日本")).toBe("app");
});
```

`cli/test/report.test.ts`:

```ts
import { expect, test } from "vitest";
import { CliError } from "../src/lib/agent.js";
import { ConfigError } from "../src/lib/config.js";
import { report } from "../src/lib/report.js";

test("CliError prints message, details and hint and keeps its exit code", () => {
  const lines: string[] = [];
  expect(report(new CliError("deploy failed", 1, "try again", ["Last container logs:", "  boom"]), (s) => lines.push(s))).toBe(1);
  expect(lines).toEqual(["✖ deploy failed", "Last container logs:", "  boom", "try again"]);
});

test("ConfigError exits with 2, Ctrl+C in a prompt with 130, anything else with 1", () => {
  expect(report(new ConfigError("bad config"), () => {})).toBe(2);
  const abort = Object.assign(new Error("User force closed the prompt"), { name: "ExitPromptError" });
  expect(report(abort, () => {})).toBe(130);
  expect(report(new Error("boom"), () => {})).toBe(1);
});
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `npx vitest run test/init.test.ts test/report.test.ts`
Expected: FAIL — o vitest não encontra `../src/commands/init.js` e `../src/lib/report.js`.

- [ ] **Step 3: Implementar**

`cli/src/commands/init.ts`:

```ts
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import { isIP } from "node:net";
import path from "node:path";
import { input } from "@inquirer/prompts";
import { type Config, configPath, isDomain, keyPath, NAME_RE, saveConfig, validateConfig } from "../lib/config.js";
import { dnsRecords } from "../lib/dns.js";
import { paint } from "../lib/style.js";

export type InitAnswers = {
  host: string;
  sshPort: number;
  project: string;
  internalPort: number;
  domain?: string;
  previewDomain?: string;
  healthPath?: string;
};

export type InitDeps = {
  ask: (defaults: { project: string }) => Promise<InitAnswers>;
  confirmOverwrite: () => Promise<boolean>;
  keygen: (keyFile: string, comment: string) => void;
  out: (line: string) => void;
};

export function defaultProject(cwd: string): string {
  const name = path.basename(cwd).toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 40).replace(/-+$/, "");
  return NAME_RE.test(name) ? name : "app";
}

// init writes .sagansync/config.json, creates the deploy key if needed and
// explains the DNS records and next steps.
export async function init(cwd: string, deps: InitDeps): Promise<Config | null> {
  if (fs.existsSync(configPath(cwd)) && !(await deps.confirmOverwrite())) {
    deps.out("Kept the existing .sagansync/config.json.");
    return null;
  }
  const cfg = validateConfig({ ...(await deps.ask({ project: defaultProject(cwd) })), user: "sagan" });
  saveConfig(cwd, cfg);
  deps.out(paint("green", "✔ Saved .sagansync/config.json") + " (no secrets inside, safe to commit)");

  const key = keyPath(cfg);
  if (fs.existsSync(key)) {
    deps.out(`Using the existing deploy key ${key}`);
  } else {
    fs.mkdirSync(path.dirname(key), { recursive: true, mode: 0o700 });
    deps.keygen(key, `sagansync-${cfg.project}`);
    deps.out(paint("green", `✔ Created deploy key ${key}`));
  }

  const records = dnsRecords(cfg, isIP(cfg.host) ? cfg.host : "<VPS IP>");
  if (records.length > 0) {
    deps.out("\nCreate these DNS records:");
    for (const r of records) deps.out(`  ${r}`);
  }
  deps.out("\nNext steps:");
  deps.out(`  sagansync provision --admin root@${cfg.host}   # installs sagand on the VPS`);
  deps.out("  sagansync deploy");
  return cfg;
}

export function sshKeygen(keyFile: string, comment: string): void {
  execFileSync("ssh-keygen", ["-q", "-t", "ed25519", "-N", "", "-C", comment, "-f", keyFile], { stdio: "inherit" });
}

const optional = (v: string) => (v.trim() === "" ? undefined : v.trim());
const port = (v: string) => (/^\d+$/.test(v) && +v >= 1 && +v <= 65535 ? true : "Enter a port number (1-65535)");
const domain = (v: string) => (v.trim() === "" || isDomain(v.trim()) ? true : "Enter a hostname like example.com, without https:// or a port");

export async function askInteractively(defaults: { project: string }): Promise<InitAnswers> {
  const host = await input({ message: "VPS address (hostname or IP)", validate: (v) => (/^[A-Za-z0-9][A-Za-z0-9.:-]*$/.test(v) ? true : "Enter just the hostname or IP, without user@") });
  const sshPort = await input({ message: "SSH port", default: "22", validate: port });
  const project = await input({ message: "Project name", default: defaults.project, validate: (v) => (NAME_RE.test(v) ? true : "Use 1-40 characters of a-z, 0-9 and '-'") });
  const internalPort = await input({ message: "Port your app listens on inside the container", default: "3000", validate: port });
  const dom = await input({ message: "Production domain (optional, e.g. api.example.com)", validate: domain });
  const preview = await input({ message: "Domain for branch previews (optional, e.g. example.com gives feat-x-app.example.com)", validate: domain });
  const health = await input({ message: "Health check path (optional, e.g. /health)", validate: (v) => (v === "" || /^\/\S*$/.test(v) ? true : "Start with / and use no spaces") });
  return { host, sshPort: +sshPort, project, internalPort: +internalPort, domain: optional(dom), previewDomain: optional(preview), healthPath: optional(health) };
}
```

`cli/src/lib/report.ts`:

```ts
import { CliError } from "./agent.js";
import { ConfigError } from "./config.js";
import { paint } from "./style.js";

// report prints an error for the user and returns the process exit code.
export function report(err: unknown, write: (line: string) => void = console.error): number {
  if (err instanceof Error && err.name === "ExitPromptError") return 130; // Ctrl+C in a prompt
  if (err instanceof CliError) {
    write(paint("red", `✖ ${err.message}`));
    for (const d of err.details) write(d);
    if (err.hint) write(paint("yellow", err.hint));
    return err.exitCode;
  }
  if (err instanceof ConfigError) {
    write(paint("red", `✖ ${err.message}`));
    return 2;
  }
  write(paint("red", `✖ ${err instanceof Error ? err.message : String(err)}`));
  return 1;
}
```

`cli/src/main.ts`:

```ts
import { confirm } from "@inquirer/prompts";
import { Command } from "commander";
import type { Ctx } from "./commands/context.js";
import { deploy } from "./commands/deploy.js";
import { dev } from "./commands/dev.js";
import { envList, envSet, envUnset } from "./commands/env.js";
import { askInteractively, init, sshKeygen } from "./commands/init.js";
import { list } from "./commands/list.js";
import { logs } from "./commands/logs.js";
import { remove } from "./commands/remove.js";
import { loadConfig } from "./lib/config.js";
import { report } from "./lib/report.js";
import { sshRemote, targetFor } from "./lib/ssh.js";
import { VERSION } from "./version.js";

function ctx(): Ctx {
  const cwd = process.cwd();
  const config = loadConfig(cwd);
  return { cwd, config, remote: sshRemote(targetFor(config)), out: (s) => console.log(s) };
}

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
    const session = await dev(ctx(), { ...o, onBranchChange: () => process.exit(1) });
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

- [ ] **Step 4: Rodar e ver passar**

Run: `npx tsc --noEmit && npx vitest run test/init.test.ts test/report.test.ts`
Expected: sem erros de tipo; PASS.

- [ ] **Step 5: Conferir o build e o executável**

Run: `npm run build && node dist/main.js --version && node dist/main.js --help && (cd /tmp && node "$OLDPWD/dist/main.js" list; echo "exit=$?")`
Expected: `0.1.0`; a ajuda lista `init, deploy, dev, list, logs, remove, env`; em `/tmp`, a mensagem "✖ No .sagansync/config.json in this directory. Run `sagansync init` first." e `exit=2`. A primeira linha de `dist/main.js` é `#!/usr/bin/env node`.

- [ ] **Step 6: Commit**

```bash
git add cli/src/commands/init.ts cli/src/lib/report.ts cli/src/main.ts cli/test/init.test.ts cli/test/report.test.ts
git commit -m "feat(cli): add init, error reporting and the command-line entry point"
```

---

### Task 12: Teste de contrato CLI ↔ sagand

**Files:**
- Create: `agent/internal/testutil/cmd/fakesagand/main.go`
- Test: `cli/test/contract.test.ts`

**Interfaces:**
- Consumes: `api`, `deploy`, `envstore`, `proxy`, `fakert`, `state` do agente (plano 1); todos os comandos da CLI.
- Produces: `fakesagand <socket> <data-dir>` — imprime `ready` e serve a API real com o runtime falso (dreno 0). Nunca é distribuído (fica em `internal/testutil`).
- O `ssh` falso do teste repassa o último argumento como `SSH_ORIGINAL_COMMAND` para o `sagand gateway` real, como o sshd faz com o forced command. O `sagand` é compilado com `-ldflags "-X main.version=0.1.0"` para não gerar aviso de versão.
- O teste é pulado se `go` não estiver instalado. O CI instala Go no job da CLI (Task 13).

- [ ] **Step 1: Escrever o teste que falha**

`cli/test/contract.test.ts`:

```ts
// Contract test: the real CLI talks to the real `sagand gateway` and API
// through a fake ssh, with a fake container runtime behind the API. It
// proves the quoting, flags, stdin payloads and event parsing agree on both
// sides. Needs Go; skipped when it is not installed.
import { type ChildProcess, execFileSync, spawn } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterAll, beforeAll, describe, expect, test, vi } from "vitest";
import type { Ctx } from "../src/commands/context.js";
import { deploy } from "../src/commands/deploy.js";
import { dev, type DevSession } from "../src/commands/dev.js";
import { envList, envSet } from "../src/commands/env.js";
import { list } from "../src/commands/list.js";
import { logs } from "../src/commands/logs.js";
import { remove } from "../src/commands/remove.js";
import { validateConfig } from "../src/lib/config.js";
import { sshRemote, type Target } from "../src/lib/ssh.js";
import { pointsToVps, projectDir } from "./helpers/ctx.js";

const agentDir = path.resolve(import.meta.dirname, "../../agent");
const hasGo = (() => {
  try {
    execFileSync("go", ["version"], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
})();

describe.skipIf(!hasGo)("CLI <-> sagand contract", () => {
  let tmp: string;
  let data: string;
  let daemon: ChildProcess;
  let target: Target;
  let sshBin: string;

  beforeAll(async () => {
    tmp = fs.mkdtempSync("/tmp/sgc-"); // short: macOS limits socket paths to 104 bytes
    data = path.join(tmp, "data");
    const bin = path.join(tmp, "bin");
    const build = (pkg: string, out: string, ...flags: string[]) =>
      execFileSync("go", ["build", ...flags, "-o", path.join(bin, out), pkg], { cwd: agentDir, stdio: "inherit" });
    build("./cmd/sagand", "sagand", "-ldflags", "-X main.version=0.1.0");
    build("./internal/testutil/cmd/fakesagand", "fakesagand");
    const sock = path.join(tmp, "s.sock");
    daemon = spawn(path.join(bin, "fakesagand"), [sock, data], { stdio: ["ignore", "pipe", "inherit"] });
    await new Promise<void>((resolve) => daemon.stdout!.once("data", () => resolve()));

    // The fake ssh behaves like sshd with the forced command: whatever the
    // client asked for becomes SSH_ORIGINAL_COMMAND for `sagand gateway`.
    sshBin = path.join(tmp, "ssh");
    fs.writeFileSync(sshBin, `#!/bin/sh
for last; do :; done
SSH_ORIGINAL_COMMAND="$last" SAGAND_SOCKET="${sock}" exec "${bin}/sagand" gateway
`, { mode: 0o755 });
    target = { host: "vps.test", port: 22, user: "sagan", identityFile: "/dev/null", knownHosts: "/dev/null", controlDir: tmp };
  }, 180_000);

  afterAll(() => {
    daemon?.kill();
    fs.rmSync(tmp, { recursive: true, force: true });
  });

  function ctx(cwd = projectDir()): Ctx & { lines: string[] } {
    const lines: string[] = [];
    const config = validateConfig({ host: "vps.test", project: "app", internalPort: 3000, domain: "app.test", previewDomain: "preview.test" });
    return { cwd, config, remote: sshRemote(target, sshBin), lookup: pointsToVps, out: (s) => lines.push(s), lines };
  }

  test("deploy, list and logs", async () => {
    const c = ctx();
    await deploy(c, {});
    expect(c.lines.at(-1)).toBe("✔ Live at https://app.test");
    await list(c, {});
    expect(c.lines.find((l) => l.startsWith("production"))).toContain("running");
    await logs(c, { tail: 5 });
    expect(c.lines.at(-1)).toMatch(/^log from sagan_app_production_/);
  });

  test("env values with quotes, newlines and $ arrive intact", async () => {
    const c = ctx();
    const tricky = `it's "quoted" $HOME \`id\` a=b\nsecond line`;
    await envSet(c, [`TRICKY=${tricky}`, "PLAIN=1"], {});
    await envList(c, {});
    expect(c.lines.slice(-2)).toEqual(["PLAIN=********", "TRICKY=********"]);
    const stored = JSON.parse(fs.readFileSync(path.join(data, "env", "app", "production.json"), "utf8"));
    expect(stored.TRICKY).toBe(tricky);
  });

  test("dev mode starts on a preview host and syncs files with awkward names", async () => {
    const c = ctx();
    let session: DevSession | undefined;
    try {
      session = await dev(c, { workspace: "feat-x", command: "npm run dev" });
      expect(c.lines).toContain("✔ Dev server at https://feat-x-app.preview.test");
      fs.writeFileSync(path.join(c.cwd, "it's a file.ts"), "content");
      const synced = path.join(data, "srv", "app", "feat-x", "dev", "it's a file.ts");
      await vi.waitFor(() => expect(fs.readFileSync(synced, "utf8")).toBe("content"), { timeout: 10_000 });
    } finally {
      await session?.close();
    }
  });

  test("errors from the agent become clear messages", async () => {
    const c = ctx();
    await expect(logs(c, { workspace: "ghost" })).rejects.toThrow("does not exist");
    const denied = await sshRemote(target, sshBin).run(["daemon"]);
    expect(denied.code).toBe(126);
  });

  test("remove", async () => {
    const c = ctx();
    await remove(c, { workspace: "feat-x", yes: true, confirm: async () => true });
    expect(c.lines.at(-1)).toBe("✔ Removed app/feat-x");
  });
});
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `npx vitest run test/contract.test.ts`
Expected: FAIL no `beforeAll` — `go build ./internal/testutil/cmd/fakesagand` falha porque o pacote não existe.

- [ ] **Step 3: Implementar**

`agent/internal/testutil/cmd/fakesagand/main.go`:

```go
// Command fakesagand serves the real sagand API on a Unix socket, backed by
// the fake container runtime. The CLI's contract tests run it together with
// the real `sagand gateway` client. It is never shipped.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/borgim/sagansync/agent/internal/api"
	"github.com/borgim/sagansync/agent/internal/deploy"
	"github.com/borgim/sagansync/agent/internal/envstore"
	"github.com/borgim/sagansync/agent/internal/proxy"
	"github.com/borgim/sagansync/agent/internal/runtime/fakert"
	"github.com/borgim/sagansync/agent/internal/state"
)

type noCerts struct{}

func (noCerts) Ensure(context.Context, string) error { return nil }

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: fakesagand <socket> <data-dir>")
		os.Exit(2)
	}
	sock, dir := os.Args[1], os.Args[2]
	st, err := state.Open(filepath.Join(dir, "state.json"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	rt := fakert.New()
	defer rt.Close()
	cfg := deploy.DefaultConfig(filepath.Join(dir, "srv"))
	cfg.Drain = 0
	cfg.StopTimeout = 0
	cfg.HealthInterval = 20 * time.Millisecond
	d := deploy.New(rt, st, envstore.New(filepath.Join(dir, "env")), proxy.NewRouter(), noCerts{}, cfg)
	l, err := api.Listen(sock)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("ready")
	if err := http.Serve(l, api.NewServer(d, "0.1.0").Handler()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `npx tsc --noEmit && npx vitest run test/contract.test.ts`
Expected: sem erros de tipo; PASS.

- [ ] **Step 5: Commit**

```bash
git add agent/internal/testutil/cmd/fakesagand/main.go cli/test/contract.test.ts
git commit -m "test(cli): check the CLI against the real sagand gateway and API"
```

---

### Task 13: CI da CLI

**Files:**
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- Produces: job `cli` (Node 22 + Go do `agent/go.mod`) que roda `npm ci`, `npm run typecheck`, `npm test` (inclui o teste de contrato) e `npm run build`. O job `agent` do plano 1 continua igual.

- [ ] **Step 1: Adicionar o job**

O arquivo inteiro fica assim:

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

  cli:
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: cli
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: 22
          cache: npm
          cache-dependency-path: cli/package-lock.json
      - uses: actions/setup-go@v5 # the contract test builds sagand
        with:
          go-version-file: agent/go.mod
      - run: npm ci
      - run: npm run typecheck
      - run: npm test
      - run: npm run build
```

- [ ] **Step 2: Validar localmente**

Run: `npm ci && npm run typecheck && npm test && npm run build`
Expected: sem erros de tipo; `Tests  101 passed (101)`; build OK.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: typecheck, test and build the CLI"
```

---

## Fora deste plano (plano 3)

- `sagansync provision [--admin user@host] [--upgrade] [--agent-binary <arquivo>] [--acme-email <e>]` e o `provision.sh` (spec 4.1 e 4.3).
- Teste ponta a ponta na VM Lima com Podman real e Pebble (spec 12.4); teste de integração do Podman (`-tags integration`).
- README, GoReleaser, publicação no npm.
- As pendências do agente em `docs/superpowers/followups-v0.1.md`.
