# C.O.R.S.I Backend

Monolito modular. Go 1.25, arquitetura hexagonal, packed by feature.

> Este README explica **o que é o backend, como rodar e como testar**.
> A visão geral do projeto está em [`../README.md`](../README.md), e o código tem precedência
> quando houver conflito.

---

## O que é

Um binário HTTP (`cmd/corsi`) que monta **dois bounded contexts** e uma camada de plataforma
compartilhada:

| Contexto | Prefixo | Rotas | Schema | Migrations |
|---|---|---|---|---|
| **finance** | `/finance` | 28 | `finance` | 8, tabela `schema_migrations` |
| **chat** (domínio de produto: **Agents**) | `/chat` | 23 | `chat` | 6, tabela `schema_migrations_chat` |

O nome `chat` é histórico e **não deve ser renomeado**: o domínio de produto chama-se Agents,
e o namespace de implementação continua `chat`.

Mais três binários auxiliares: `cmd/migrate` (runner golang-migrate) e `cmd/swagger`
(validador do spec).

---

## Autenticação é externa

Este repositório **não possui autenticação**. Não há Keycloak, verificação de JWT/JWKS nem
cliente OIDC. Não existe configuração `AUTH_*`.

O backend roda sem autenticação. O contexto de workspace vem do header `X-Workspace-Id`
(UUID), governado por `WORKSPACE_REQUIRE_HEADER`:

| Modo | Env | Header ausente | Uso |
|---|---|---|---|
| Lax (default) | `WORKSPACE_REQUIRE_HEADER=false` | Cai no sentinela `00000000-0000-0000-0000-000000000001`, com `X-Workspace-Source: dev-sentinel` na resposta | desenvolvimento |
| Estrito | `WORKSPACE_REQUIRE_HEADER=true` | Rejeita com `401 workspace_required` | **produção** |

Header malformado devolve 400 em qualquer modo.

> **Limitação real, registrada.** Um header UUID sintaticamente válido é aceito **sem
> qualquer validação**: não existe registro de workspaces contra o qual validar. A premissa
> de que produção fica atrás de um gateway que injeta o header verificado **não está
> satisfeita: esse gateway não existe.** Detalhe em
> o catálogo de capacidades da Platform.

---

## Layout

```text
backend/
  cmd/
    corsi/            # entrypoint do servidor HTTP (composition root)
    migrate/          # runner de migrations (golang-migrate)
    swagger/          # validador do spec OpenAPI
  internal/
    platform/         # kernel compartilhado, apenas infra transversal
      config/         # config por env, twelve-factor
      logger/         # slog
      postgres/       # pgxpool + TxManager
      httpserver/     # router chi + middlewares
      workspace/      # X-Workspace-Id para contexto
      secrets/        # AES-256-GCM (Seal / Open)
      apierror/       # resposta de erro uniforme
      render/         # decode com limite de corpo, encode JSON
      health/         # probes /live e /ready
      metrics/        # registro Prometheus + collector do pool
      observability/  # tracing OpenTelemetry (off por default)
    finance/          # bounded context
      domain/ ports/ app/
      adapters/httpapi/  adapters/repo/
      module.go       # único símbolo público
    chat/             # bounded context (domínio: Agents)
      domain/ ports/ app/
      adapters/httpapi/  adapters/repo/  adapters/llm/
      module.go
  api/openapi/        # contrato schema-first, embutido, servido em /openapi.yaml
  migrations/
    finance/          # uma pasta por bounded context
    chat/
  deploy/
    entrypoint.sh     # aplica as migrations e sobe o servidor
  docs/
    totals-contract.md  # invariante FROZEN de agregação financeira
  Dockerfile
```

---

## Contrato de fronteira entre módulos

Um bounded context pode importar apenas:

- o próprio pacote;
- `internal/platform/*`;
- a stdlib e libs externas.

**Importar outro bounded context é erro de build**, por construção. `finance` e `chat` não
se conhecem, e nenhum pacote de `internal/platform` importa qualquer um dos dois.

A composição acontece em `cmd/corsi/main.go`, que é a única exceção legítima.

---

## Como rodar

```bash
make -C .. dev          # sobe postgres, aplica migrations e roda o backend
make run                # roda a API com .env
make up / down / logs   # controla o postgres de desenvolvimento
```

Configuração é **somente por ambiente**. Sem arquivo de config. Ver `.env.example`.

Variáveis que mais importam: `POSTGRES_DSN`, `HTTP_ADDR`, `WORKSPACE_REQUIRE_HEADER`,
`SECRETS_KEY`, `LOG_FORMAT`, `OTEL_ENABLED`.

**Sem `SECRETS_KEY`** a API sobe com warning e recusa gravar ou ler credencial de provider
com `503 not_configured`. Rotacionar a chave **invalida toda credencial guardada**: não há
re-wrap de envelope.

---

## Migrations

Uma timeline por bounded context, com **tabela de versão própria**.

```bash
make migrate-up            # aplica tudo (finance + chat)
make migrate-up-finance
make migrate-up-chat
make migrate-version       # imprime a versão de cada timeline
make migrate-create name=add_x [MODULE=chat]
```

`finance` mantém a tabela default `schema_migrations` por precedência histórica: apontá-la
para outro lugar faria o golang-migrate ler um banco populado como versão 0.

> **Armadilha que já custou um incidente:** todo módulo novo precisa nomear a própria tabela
> com `-table`. Omitir faz o runner ler a tabela do `finance`, encontrar uma versão alta e
> concluir que não há nada a aplicar.

**Execução em produção:** `deploy/entrypoint.sh` aplica **as duas timelines** no start, uma
invocação por contexto. `set -e` está ativo e cada invocação é guardada, então uma migração
que falha **mata o contêiner** em vez de servir um schema que ele não tem.

Política: **forward-only** em banco populado. Os `down.sql` existem para testes e CI em banco
vazio. Rollback real é restore de backup, e **`make migrate-down` nunca deve rodar em banco
com dados reais**.

---

## Testes

```bash
make test                        # unit, sem banco
make test-integration-isolated   # integração contra postgres descartável (exige docker)
make test-integration            # integração contra um banco QUE VOCÊ fornece (TEST_POSTGRES_DSN)
```

`make test-integration` **não tem default**: ele exige `TEST_POSTGRES_DSN` explícito. O
default foi removido de propósito, porque a suíte roda `migrate.Down()` e apontava para o
banco de desenvolvimento.

`make test-integration-isolated` provisiona um Postgres descartável com nome e porta únicos,
e faz cleanup por `trap` mesmo quando a suíte falha.

Cobertura atual: 24 de 24 testes de integração do Finance passam contra Postgres real, via
handler HTTP; mais testes unitários de domínio, workspace, secrets, métricas e do parser SSE;
mais 3 testes que executam o `entrypoint.sh` real.

**Sem cobertura:** testes de repositório e de rota do módulo `chat`.

---

## Gate

```bash
make ci        # tidy · fmt · vet · test · test-integration-isolated · build · swagger
make ci-fast   # o mesmo, sem a integração. NÃO é suficiente sozinho
```

**`make ci` é o gate oficial.** A suíte de integração é obrigatória, sem fallback e sem modo
warning-only, e por isso o gate **exige docker**.

O ciclo foi comprovado: PASS → mutação → FAIL → restore → PASS.

**Não existe CI remoto.** O gate protege quem o roda.

---

## Observabilidade

| Endpoint | O que é |
|---|---|
| `/health/live` | Liveness, sempre 200 |
| `/health/ready` | Readiness. Executa `pool.Ping` e nada além disso |
| `/metrics` | Prometheus, **sem autenticação** por decisão explícita |
| `/openapi.yaml`, `/swagger` | Contrato publicado e sua UI |

**12 famílias `corsi_*`:** quatro de HTTP, quatro de workspace e quatro do pool de conexões.

**Dois pontos cegos conhecidos:** `ready` não distingue banco vazio de banco completo, e
`corsi_workspace_sentinel_total` não incrementa quando o sentinela é enviado explicitamente.
Ambos registrados no catálogo de capacidades da Platform.

**Traces desligados por default** (`OTEL_ENABLED=false`). Jaeger disponível no compose sob
profile opt-in.

---

## Backup e restore

```bash
make backup                                  # backups/YYYYMMDD-HHMMSS.sql.gz
make restore FILE=backups/20260520-143000.sql.gz
```

---

## Contratos que não se mudam sem decisão

| Contrato | Onde |
|---|---|
| **Totals**, FROZEN em 2026-05-25 | [`docs/totals-contract.md`](docs/totals-contract.md) |
| Erro uniforme `{"error":{"code","message"}}` | `internal/platform/apierror` |
| Envelope de lista `{"items":[],"limit":N,"offset":N}` | ambos os módulos |
| Sigilo da credencial: só `api_key_hint` sai | `internal/chat/domain/provider.go` |
| Migrations forward-only, com abort no start | `deploy/entrypoint.sh` |

---

## Documentação profunda

| Assunto | Onde |
|---|---|
| Visão geral do projeto | [`../README.md`](../README.md) |
| Contrato de totais, FROZEN | [`docs/totals-contract.md`](docs/totals-contract.md) |
| Contrato HTTP do Finance | [`api/openapi/finance.yaml`](api/openapi/finance.yaml) |
