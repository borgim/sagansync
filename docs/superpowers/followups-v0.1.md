# Pendências da v0.1 (agente `sagand` e CLI)

Pontos levantados na revisão final do plano 1 (`plans/2026-09-24-sagand-agent.md`) que ficaram para depois. Nenhum bloqueia o plano 2 (CLI).

## Correções pequenas

- [ ] **Conflito de domínio sem trava.** A checagem `host_conflict` roda antes da trava do workspace e não se repete na hora de salvar. Dois projetos com o mesmo `domain` fazendo deploy ao mesmo tempo podem ambos reivindicar o host, e remover um depois derruba a rota do outro. Correção: um `Store.PutIfHostFree` atômico (`internal/deploy/deploy.go`, `internal/state`).
- [ ] **`dev` reextrai no lugar.** Um upload interrompido deixa a árvore de dev pela metade, e uma reconciliação ou reboot recria o container em cima dela. Correção: extrair em `dev.new` e trocar com `rename` só em caso de sucesso (`internal/deploy/dev.go`).
- [ ] **`put`/`rm` e `env set/unset` sem trava de workspace.** Um `put` durante o `remove` pode recriar `/srv/sagan/<p>/<w>/dev`; o `env` usa um arquivo `.tmp` compartilhado. Correção: trava curta por workspace, ou documentar (`internal/deploy/dev.go`, `internal/envstore`).
- [ ] **`Remove` apaga a rota antes de remover o container.** Se a remoção do container falhar, o app fica fora do ar até a próxima reconciliação, com o estado mantido. Correção: apagar a rota só depois do sucesso (`internal/deploy/manage.go`).
- [ ] **Reconciliação segura a trava enquanto para órfãos** (até 10 s). Um deploy nesse momento recebe `busy` sem motivo aparente. Correção: aceitar, ou citar a reconciliação na mensagem de `busy` (`internal/deploy/reconcile.go`).
- [ ] **`sagand version` informa o binário, não o daemon.** Depois de um upgrade em que o reinício falhou, a CLI conversaria com o daemon antigo sem perceber. Correção: consultar `GET /v1/version` e usar a versão local só como fallback (`cmd/sagand/client.go`).
- [ ] **`pax_global_header` rejeitado.** Tar gerado com `git archive` falha com `invalid_archive`. Correção: ignorar `tar.TypeXGlobalHeader` (`internal/release/extract.go`).
- [ ] **Build interrompido tratado como sucesso.** Se o serviço do Podman cair no meio do build, o erro aparece depois como `internal` ("image not found") em vez de `build_failed`. Correção: exigir a mensagem final do build (`internal/podman/podman.go`).

## Validar no plano 3 (VM com Podman real)

- [ ] **`Containerfile` vs `Dockerfile`:** qual o endpoint `/build` do Podman 4.3+ escolhe quando o projeto tem os dois.
- [ ] **Containers de dev com `USER` não-root:** arquivos criados no bind mount ficam com um subuid, e o `RemoveAll` do `dev`/`remove` pode falhar com EACCES.
- [ ] **Reinício do daemon no meio de um deploy** (`provision --upgrade`): o deploy é cortado após 15 s; confirmar que a reconciliação limpa tudo.

## Ajustes na spec

- [ ] **Seção 6:** um domínio desconhecido acessado por HTTPS recebe falha de handshake TLS, não 404. Sem certificado não há como responder 404. Corrigir o texto.

## CLI (revisão final do plano 2)

Pontos da revisão de `plans/2026-09-24-sagansync-cli.md` que ficaram para depois.

- [ ] **Edições durante o upload inicial do `dev`** não são sincronizadas até serem salvas de novo (o watcher só começa depois do build). Correção: iniciar o watcher antes de empacotar e enviar a fila depois do `done` (`cli/src/commands/dev.ts`).
- [ ] **Branches que diferem só por pontuação ou maiúsculas** (`feature/user_auth` e `feature/user-auth`) caem no mesmo workspace; o hash de nomes longos é feito sobre o nome já sanitizado. Correção: calcular o hash sobre o nome original do branch e corrigir o comentário (`cli/src/lib/git.ts`).
- [ ] **`env unset` não valida as chaves:** `sagansync env unset -- --workspace=production` é lido como flag pelo `sagand` e age em `production`. Correção: validar com `KEY_RE` e/ou enviar `--` antes das chaves (`cli/src/commands/env.ts`).
- [ ] **`logs -n abc`** envia `NaN` para o agente. Correção: validar o número localmente (`cli/src/main.ts`).
- [ ] **Dicas de SSH imprecisas:** arquivo de chave ausente recebe "chave não autorizada"; queda com código 255 no meio de um deploy diz "não foi possível conectar" em vez de "a conexão caiu, confira com `sagansync list`" (spec 11) (`cli/src/lib/agent.ts`).
- [ ] **O `dev` segue symlinks** (`followSymlinks` padrão do chokidar): um symlink seguro enviado vira arquivo comum no servidor, e alvos como `../shared` são observados e sincronizados. Avaliar `followSymlinks: false` (`cli/src/commands/dev.ts`).
- [ ] **Comandos só funcionam na raiz do projeto.** Procurar `.sagansync/` subindo os diretórios (`cli/src/lib/config.ts`).
- [ ] **Monorepo:** as regras do `.gitignore` da raiz do git e dos `.gitignore` aninhados não são aplicadas quando o projeto está num subdiretório. Considerar `git ls-files -co --exclude-standard` com a lista fixa por cima (`cli/src/lib/pack.ts`).
- [ ] **`put` falhando para sempre** quando o workspace sai do modo dev (alguém fez deploy): cada save mostra um erro vermelho. Encerrar a sessão em `not_found` (`cli/src/commands/dev.ts`).

### Limitações do contrato com o agente

- [ ] **`put` sem tamanho:** uma transferência interrompida (Ctrl+C, SSH caindo) grava o arquivo truncado. Correção: o `sagand put` receber o tamanho esperado e rejeitar corpos incompletos (agente + CLI).
- [ ] **`put` não leva o modo do arquivo:** um script novo chega como 0644 no dev (arquivos existentes já mantêm o modo).

### Para o plano 3

- [ ] Adicionar `"prepublishOnly": "npm run build"` no `cli/package.json`, já que `dist/` não é versionado.
- [ ] README: Windows não é suportado por enquanto (o OpenSSH do Windows não tem ControlMaster); Node 22.12 ou mais novo.
