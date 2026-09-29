# C.O.R.S.I.

[![CI](https://github.com/JoaoGabrielVianna/C.O.R.S.I/actions/workflows/ci.yml/badge.svg)](https://github.com/JoaoGabrielVianna/C.O.R.S.I/actions/workflows/ci.yml)

A personal operations hub, built in public, for a single operator. Go backend, React
frontend, PostgreSQL. Agents, Finance, Threads, Job Radar, Palace and Closet are
implemented and in daily use; the rest of this file says exactly how far each one goes.

> Também disponível em português: [`README.pt-BR.md`](README.pt-BR.md).

This is the tool I use every day, not a demo. The limits documented here are real
limits, found by using it.

---

## Architecture

A modular monolith: one Go module, one binary, bounded contexts that do not know about
each other. Every context is laid out the same way.

```text
internal/<context>/
    domain/     entities, value objects, invariants. stdlib only.
    ports/      interfaces the context needs or offers
    app/        application services, use cases
    adapters/   http, postgres, external clients
```

Seven module contexts (`chat`, `finance`, `threads`, `jobradar`, `palace`, `closet`,
`releases`), three integration contexts (`github`, `metathreads`, `telegram`), and
`platform` for shared technical capability. The dependency rule is one-directional:

```text
Module      → Platform
Integration → Platform
Module      → Integration → External
```

`Integration → Module`, `Platform → anything`, and `Module → Module` are forbidden.
Wiring happens in `cmd/corsi`, which is the only place allowed to know about all of
them.

### The rule is enforced, not documented

That claim used to be a habit. Go's compiler only rejects import *cycles*; it does not
care about layering, and nothing else was looking. [`backend/internal/arch_test.go`](backend/internal/arch_test.go)
closes it: it walks `./internal` with `go/parser` in `ImportsOnly` mode, maps every file
and every internal import to its context, and fails on any cross-context edge that is
not in a declared allowlist. Standard library only, no `golang.org/x/tools`.

Three details that make it hold up:

- **Allowances name exact packages, never prefixes.** Permitting `chat/domain` does not
  permit `chat/app`.
- **Test files are scanned too.** Most integration suites here are behind
  `//go:build integration`, so any tool resolving packages under default build
  constraints reports a tidier graph than the one that exists. Parsing sees them.
- **An allowance that matches nothing fails the test.** When coupling is removed, the
  permission has to be removed with it, so the list cannot rot into a junk drawer.

It runs in CI as its own named check.

## Debt, named

The allowlist has two entries. Both are real violations of the rule above, written down
rather than hidden by loosening it.

**1. The tool contract lives in the wrong module.** Six contexts import `chat/domain`
and `chat/ports`. `internal/chat` is two things at once: the Agents module, and the
owner of the tool contract (`ports.Tool`, `ToolDefinition`, the receipt types) that
every other context implements to expose capabilities to an agent. Finance does not
depend on Agents because it wants to talk to Agents, but because that is where the
interface it implements happens to be declared. **These are the only cross-context
imports in production code.** Destination is `platform/toolkit`, which every context may
already import, so the move closes all six edges without a new allowance. It is deferred
because `chat/domain` mixes the contract with conversation state, and deciding which
types are which is a domain decision, not a file move.

**2. Integration suites compose other contexts.** Every other cross-context import is in
a `_test.go` file: a module's integration suite builds a real chat runtime and drives a
scripted model through an actual turn to prove its capabilities are reachable by an
agent end to end. These are the most valuable tests here and the coupling is the point
of them, but they are misfiled. A test that composes three contexts is not a test of any
one of them; it belongs to the composition root.

## Two ideas that hold up the rest

**No write claim without a write receipt.** A finance agent once answered *"8
transactions imported"* in a turn with **zero tool calls**, against an empty ledger.
Every layer behaved correctly and the operator was told the opposite of the truth. Now
every assistant turn carries a `WriteReceipt` derived from execution, never from prose:
`REQUESTED` / `EXECUTED` / `FAILED` / `NOT_EXECUTED`.

**No verified external claim without a current external read.** The same defect in the
read direction: an agent reported *"1,535 followers, 40,055 views"* with no tool call.
The real numbers were **163 and 224**. A capability now declares whether it talks to an
external system, and every turn carries a `ReadReceipt`: `VERIFIED_EXTERNAL_READ` /
`FAILED_EXTERNAL_READ` / `NO_EXTERNAL_READ`. For reads the dangerous state is
*absence*, so silence is what gets labelled.

In both cases **nothing reads the model's response.** There is no claim detection and no
heuristic hunting for numbers in prose. The receipt comes from execution or it does not
come. The guarantee is narrow and worth stating precisely: this does not stop the model
from producing a false sentence, it stops the product from presenting that sentence as
confirmed.

## Running it

```bash
make doctor      # check docker, go, node are reachable
make dev         # postgres + migrations + backend
make frontend    # vite on :5173
make test        # backend unit tests, no database
```

Config lives in [`backend/.env.example`](backend/.env.example) and
[`frontend/.env.example`](frontend/.env.example). `docker-compose.dev.yml` brings up
PostgreSQL 17 and Jaeger. `make reset` destroys the local volume and re-applies
everything; it asks first.

It also runs on a small VPS, which is where it is actually used day to day; local is
development. Deploys are manual and deliberate: `git push` publishes to GitHub and
triggers no deployment.

## Tests

**1,813 test functions** in the backend, split between unit tests that need nothing and
integration suites that run against a real PostgreSQL. The integration suites are behind
`//go:build integration` because they drop and recreate schemas.

```bash
make -C backend ci        # full gate: integration included, needs docker
make -C backend ci-fast   # no integration. NOT sufficient alone
```

CI ([`.github/workflows/ci.yml`](.github/workflows/ci.yml)) runs everything that does
not need a database: build, vet, **vet with `-tags=integration`** so the suites that only
run by hand cannot silently stop compiling, the architecture test as a named check, unit
tests, and `golangci-lint` pinned to v2.12.2. The linter config is small on purpose:
staticcheck's `SA` bug checks are on, its `QF`/`ST` style suggestions are off, and issue
truncation is disabled because a gate that hides findings reports a small stable number
while the rest surfaces one run later.

## Migrations

One timeline per bounded context, each with its own version table: eleven of them today
(`schema_migrations` for finance, plus `_chat`, `_releases`, `_github`, `_jobradar`,
`_threads`, `_metathreads`, `_palace`, `_telegram`, `_identity`, `_closet`). The
entrypoint applies all of them on start and aborts if any one fails.

**Forward-only on a populated database.** `.down.sql` files exist and are used against
throwaway databases in tests; real rollback is a restore from backup.

## Observability

`GET /metrics` with no auth, which is a deliberate decision: Prometheus needs
unrestricted access. Nine custom `corsi_*` families covering HTTP, workspace header
outcomes and chat turn terminal reasons, plus Go, process and a pgxpool stats collector
that reads `pool.Stat()` on scrape rather than running a background goroutine.

`corsi_chat_turn_terminal_total{reason}` uses a **closed** vocabulary and never the raw
`finish_reason` from the gateway, because an open set on a per-turn counter is unbounded
cardinality.

OpenTelemetry traces are opt-in, exported to the Jaeger in the dev compose file.
`/health/live` and `/health/ready` are served. `ready` only does `pool.Ping`, so an empty
database and a migrated one look identical to it. That is a known blind spot.

## Modules

| Module | What it does | State |
|---|---|---|
| **Agents** | agents with audited capabilities and execution receipts | `1.6.0` |
| **Finance** | cents-only ledger, frozen totals contract | `1.1.0` |
| **Threads** | ideas and drafts, operated through chat, no UI by decision | `1.0.0` |
| **Job Radar** | opportunity pipeline | `1.0.0` |
| **Palace** | operator's long-term memory, queried by agents across turns | implemented |
| **Closet** | real wardrobe and look composer, no agent | `C1` |
| **Releases** | version history, published snapshots immutable by trigger | implemented |
| **GitHub** | read-only integration, 7 tools | active |
| **Meta Threads** | read-only integration, 6 tools | frozen by decision |

Finance is the oldest and most exercised: **38 HTTP routes, 15 migrations**, and a
totals contract frozen since 2026-05-25 with three buckets (`REALIZED`, `PROJECTED`,
`EXCLUDED`) where classification is a pure function of the row plus the clock. Money is
`int64` cents everywhere, no float and no parseable string in any layer. Amounts are
unsigned; direction comes from the category.

Palace is the least obvious one. It is not a generic key-value store: it holds the
operator's long-term state with operator semantics, so an agent can reach for something
that outlives the current turn without that state being smuggled into a prompt as if it
were fresh.

## Roadmap

No dates. What is decided:

- move the tool contract to `platform/toolkit` and close debt 1;
- relocate the composing integration suites to the composition root and close debt 2;
- fix legacy release snapshot decoding, which renders some fields blank for six of the
  published releases; the fix is in the reader, because snapshots are immutable by
  trigger;
- unfreeze the Meta Threads integration, whose foundation is built and whose activation
  is waiting on a stable OAuth callback;
- converge the frontend totals consumers onto the contract instead of their own
  semantics.

Gym, Diet, Content and Market Intelligence appear in old notes. They were never built
and are not planned. Implemented integrations are exactly three: GitHub, Meta Threads
and Telegram.

## What this is not

- **Not a framework or a library.** Nothing here is meant to be imported by another
  project. There is no stable public API and no versioning promise.
- **Not multi-tenant.** Single operator, one credential. Authentication is real
  (Postgres-backed sessions, `__Host-` cookie, deny-by-default at the router root), but
  workspace isolation is organisation rather than security: the middleware accepts any
  syntactically valid UUID.
- **Not fully covered by remote CI.** The integration suites need docker and a
  disposable PostgreSQL, so they stay behind a manual gate.
- **Not browser-tested.** The frontend has component tests; visual verification is
  manual.
- **Not accepting contributions yet.**

## License

**All rights reserved.** This repository is public for reading, and that is **not** an
open source license: no permission is granted to use, copy, modify or distribute.

© João Corsi
