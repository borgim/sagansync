# Pendências da v0.1 (agente `sagand` e CLI)

Pontos levantados na revisão final do plano 1 (`plans/2026-09-24-sagand-agent.md`) que ficaram para depois. Nenhum bloqueia o plano 2 (CLI).

## Correções pequenas

- [x] **Conflito de domínio sem trava.** Corrigido na v0.1.1 com `Store.PutIfHostFree`, antes de mover a rota. A checagem `host_conflict` roda antes da trava do workspace e não se repete na hora de salvar. Dois projetos com o mesmo `domain` fazendo deploy ao mesmo tempo podem ambos reivindicar o host, e remover um depois derruba a rota do outro. Correção: um `Store.PutIfHostFree` atômico (`internal/deploy/deploy.go`, `internal/state`).
- [ ] **`dev` reextrai no lugar.** Um upload interrompido deixa a árvore de dev pela metade, e uma reconciliação ou reboot recria o container em cima dela. Correção: extrair em `dev.new` e trocar com `rename` só em caso de sucesso (`internal/deploy/dev.go`).
- [ ] **`put`/`rm` e `env set/unset` sem trava de workspace.** Um `put` durante o `remove` pode recriar `/srv/sagan/<p>/<w>/dev`; o `env` usa um arquivo `.tmp` compartilhado. Correção: trava curta por workspace, ou documentar (`internal/deploy/dev.go`, `internal/envstore`).
- [x] **`Remove` apaga a rota antes de remover o container.** Corrigido na v0.1.1. Se a remoção do container falhar, o app fica fora do ar até a próxima reconciliação, com o estado mantido. Correção: apagar a rota só depois do sucesso (`internal/deploy/manage.go`).
- [ ] **Reconciliação segura a trava enquanto para órfãos** (até 10 s). Um deploy nesse momento recebe `busy` sem motivo aparente. Correção: aceitar, ou citar a reconciliação na mensagem de `busy` (`internal/deploy/reconcile.go`).
- [ ] **`sagand version` informa o binário, não o daemon.** Depois de um upgrade em que o reinício falhou, a CLI conversaria com o daemon antigo sem perceber. Correção: consultar `GET /v1/version` e usar a versão local só como fallback (`cmd/sagand/client.go`).
- [x] **`pax_global_header` rejeitado.** Corrigido na v0.1.1. Tar gerado com `git archive` falha com `invalid_archive`. Correção: ignorar `tar.TypeXGlobalHeader` (`internal/release/extract.go`).
- [ ] **Build interrompido tratado como sucesso.** Se o serviço do Podman cair no meio do build, o erro aparece depois como `internal` ("image not found") em vez de `build_failed`. Correção: exigir a mensagem final do build (`internal/podman/podman.go`).

## Validar no plano 3 (VM com Podman real)

- [x] **`Containerfile` vs `Dockerfile`:** verificado na VM (Podman 4.9.3): com os dois presentes, o `Containerfile` é usado, como no Podman. Documentado no README.
- [ ] **Containers de dev com `USER` não-root:** arquivos criados no bind mount ficam com um subuid, e o `RemoveAll` do `dev`/`remove` pode falhar com EACCES.
- [ ] **Reinício do daemon no meio de um deploy** (`provision --upgrade`): o deploy é cortado após 15 s; confirmar que a reconciliação limpa tudo.

## Ajustes na spec

- [ ] **Seção 6:** um domínio desconhecido acessado por HTTPS recebe falha de handshake TLS, não 404. Sem certificado não há como responder 404. Corrigir o texto.

## CLI (revisão final do plano 2)

Pontos da revisão de `plans/2026-09-24-sagansync-cli.md` que ficaram para depois.

- [ ] **Edições durante o upload inicial do `dev`** não são sincronizadas até serem salvas de novo (o watcher só começa depois do build). Correção: iniciar o watcher antes de empacotar e enviar a fila depois do `done` (`cli/src/commands/dev.ts`).
- [ ] **Branches que diferem só por pontuação ou maiúsculas** (`feature/user_auth` e `feature/user-auth`) caem no mesmo workspace; o hash de nomes longos é feito sobre o nome já sanitizado. Correção: calcular o hash sobre o nome original do branch e corrigir o comentário (`cli/src/lib/git.ts`).
- [x] **`env unset` não valida as chaves:** corrigido na v0.1.1 (valida com `KEY_RE` e envia `--`). `sagansync env unset -- --workspace=production` é lido como flag pelo `sagand` e age em `production`. Correção: validar com `KEY_RE` e/ou enviar `--` antes das chaves (`cli/src/commands/env.ts`).
- [x] **`logs -n abc`** corrigido na v0.1.1. envia `NaN` para o agente. Correção: validar o número localmente (`cli/src/main.ts`).
- [ ] **Dicas de SSH imprecisas:** arquivo de chave ausente recebe "chave não autorizada"; queda com código 255 no meio de um deploy diz "não foi possível conectar" em vez de "a conexão caiu, confira com `sagansync list`" (spec 11) (`cli/src/lib/agent.ts`).
- [ ] **O `dev` segue symlinks** (`followSymlinks` padrão do chokidar): um symlink seguro enviado vira arquivo comum no servidor, e alvos como `../shared` são observados e sincronizados. Avaliar `followSymlinks: false` (`cli/src/commands/dev.ts`).
- [ ] **Comandos só funcionam na raiz do projeto.** Procurar `.sagansync/` subindo os diretórios (`cli/src/lib/config.ts`).
- [ ] **Monorepo:** as regras do `.gitignore` da raiz do git e dos `.gitignore` aninhados não são aplicadas quando o projeto está num subdiretório. Considerar `git ls-files -co --exclude-standard` com a lista fixa por cima (`cli/src/lib/pack.ts`).
- [ ] **`put` falhando para sempre** quando o workspace sai do modo dev (alguém fez deploy): cada save mostra um erro vermelho. Encerrar a sessão em `not_found` (`cli/src/commands/dev.ts`).

### Limitações do contrato com o agente

- [ ] **`put` sem tamanho:** uma transferência interrompida (Ctrl+C, SSH caindo) grava o arquivo truncado. Correção: o `sagand put` receber o tamanho esperado e rejeitar corpos incompletos (agente + CLI).
- [ ] **`put` não leva o modo do arquivo:** um script novo chega como 0644 no dev (arquivos existentes já mantêm o modo).

## Provision, teste ponta a ponta e release (revisão final do plano 3)

Pontos da revisão de `plans/2026-09-24-sagansync-release.md` que ficaram para depois.

- [ ] **`provision` sem `--upgrade` reescreve o `config.json` do daemon:** um reparo sem `--acme-email` apaga o e-mail configurado antes. Mesclar as flags na configuração existente, ou documentar (`cli/scripts/provision.sh`).
- [ ] **Todo `provision` reinicia o `sagand`**, então os sites ficam fora do ar por alguns segundos durante reparo ou upgrade (o agente reconcilia antes de abrir as portas). Documentar no README, ou abrir as portas antes de reconciliar (`agent/cmd/sagand/daemon.go`).
- [ ] **A checagem de prontidão pode passar com um daemon que ainda vai falhar** ao abrir as portas 80/443, porque o socket da API é criado antes. Exigir duas checagens seguidas, ou abrir as portas antes do socket (`provision.sh`, `daemon.go`).
- [ ] **Erros crus no `provision.sh`:** `--upgrade` num servidor sem Podman (`podman: command not found`); `VERSION_ID` ausente (Debian sid) com `set -u`; o erro do `systemctl --user` é descartado e vira "unknown error".
- [ ] **O sha256 só protege contra corrupção:** o `checksums.txt` vem do mesmo release que o binário, então não detecta um release substituído, e a dica fala em "adulterado". Embutir os hashes no pacote npm (o job da CLI roda depois do release) ou usar attestations. O `fetch` também não tem timeout, e o binário baixado não passa pelo `checkBinary` (`cli/src/commands/provision.ts`).
- [x] **Release:** na v0.1.1, o `goreleaser-action` foi fixado por SHA, pré-releases saem no npm com a tag `next` (e como pré-release no GitHub), e o job `cli` pula uma versão já publicada, então pode ser rodado de novo sozinho. O npm passou a publicar por trusted publishing (OIDC), sem token.
- [ ] **README:** firewalls do provedor (security groups) precisam liberar 80/443; `AllowUsers`/`AllowGroups` no sshd bloqueia o `sagan`; um admin que entra com senha digita duas vezes.
- [ ] **Teste ponta a ponta:** a checagem do deploy quebrado aceita `exit=1` como substring (também casaria `exit=10`/`126`); a do Podman procura a substring `PASS`; a imagem do Pebble está em `:latest`; o processo do `dev` não é encerrado se o script abortar no meio; os casos "tar com `../`" e "comando vazio" da spec 12.4.5 não estão no teste (o do tar é coberto pelos testes do Go).
- [ ] **Contas com `USER` não-root no `dev` e reinício do daemon no meio de um deploy** continuam em aberto (da seção do agente).
