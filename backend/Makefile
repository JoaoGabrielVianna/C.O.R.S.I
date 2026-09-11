SHELL := /bin/bash
ENV ?= .env
POSTGRES_DSN ?= postgres://corsi:corsi@localhost:55432/corsi?sslmode=disable
# Dev compose lives at the repo root now — see ../docker-compose.dev.yml.
COMPOSE := docker-compose -f ../docker-compose.dev.yml

# Local backup/restore wires through the running postgres container so
# pg_dump matches the server version exactly. Override DB_CONTAINER if
# the compose project name was changed.
DB_CONTAINER ?= corsi-dev-postgres-1
DB_USER      ?= corsi
DB_NAME      ?= corsi
BACKUPS_DIR  ?= backups

MODULE ?= finance

.PHONY: help up down logs ps build run test test-integration test-integration-isolated tidy fmt vet lint migrate-up migrate-up-finance migrate-up-chat migrate-up-releases migrate-up-github migrate-up-jobradar migrate-down migrate-down-chat migrate-down-releases migrate-down-github migrate-down-jobradar migrate-version migrate-create openapi-gen swagger ci ci-fast backup restore

help: ## list targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-22s\033[0m %s\n", $$1, $$2}'

up: ## start dev postgres (root compose)
	$(COMPOSE) up -d postgres

down: ## stop stack
	$(COMPOSE) down

logs: ## tail stack logs
	$(COMPOSE) logs -f

ps: ## show stack status
	$(COMPOSE) ps

build: ## compile binaries
	go build -o bin/corsi ./cmd/corsi
	go build -o bin/migrate ./cmd/migrate

run: ## run corsi api with .env
	set -a; [ -f $(ENV) ] && source $(ENV); set +a; go run ./cmd/corsi

test: ## run unit tests (no database)
	go test ./...

# The integration suite wipes and re-applies migrations before every test
# (freshDB), so whatever database it points at loses its contents. That is
# why there is no default here any more: falling back to $(POSTGRES_DSN)
# meant `make test-integration` silently destroyed the dev database.
test-integration: ## run integration tests against a database YOU supply (TEST_POSTGRES_DSN)
	@test -n "$$TEST_POSTGRES_DSN" || { \
	  echo "test-integration: refusing to run without TEST_POSTGRES_DSN."; \
	  echo "  This suite DROPS AND RECREATES the schema of whatever database it is given."; \
	  echo "  Use 'make test-integration-isolated' to have one provisioned and thrown away for you,"; \
	  echo "  or export TEST_POSTGRES_DSN to point at a database you are willing to lose."; \
	  exit 1; }
	TEST_POSTGRES_DSN="$$TEST_POSTGRES_DSN" go test -tags=integration -count=1 ./...

test-integration-isolated: ## run integration tests against a throwaway postgres (needs docker)
	./scripts/integration-test.sh

tidy: ## tidy modules
	go mod tidy

fmt: ## format
	go fmt ./...

vet: ## vet
	go vet ./...

lint: vet ## alias for vet (swap for golangci-lint when adopted)

# Each bounded context has its own migration directory and its own version
# table, so the two timelines advance independently.
#
# finance keeps the driver's default `schema_migrations` table because it
# predates the split — pointing it somewhere new would make golang-migrate
# read a fully populated production database as version 0. Every module
# added after this one gets an explicit table.
MIGRATE := go run ./cmd/migrate -dsn "$(POSTGRES_DSN)"
CHAT_MIGRATE_TABLE := schema_migrations_chat
RELEASES_MIGRATE_TABLE := schema_migrations_releases
GITHUB_MIGRATE_TABLE := schema_migrations_github
JOBRADAR_MIGRATE_TABLE := schema_migrations_jobradar
THREADS_MIGRATE_TABLE := schema_migrations_threads
METATHREADS_MIGRATE_TABLE := schema_migrations_metathreads

migrate-up: migrate-up-finance migrate-up-chat migrate-up-releases migrate-up-github migrate-up-jobradar migrate-up-threads migrate-up-metathreads ## apply pending migrations (all modules)

migrate-up-finance: ## apply pending finance migrations
	$(MIGRATE) -dir migrations/finance up

migrate-up-chat: ## apply pending chat migrations
	$(MIGRATE) -dir migrations/chat -table $(CHAT_MIGRATE_TABLE) up

migrate-up-releases: ## apply pending releases migrations
	$(MIGRATE) -dir migrations/releases -table $(RELEASES_MIGRATE_TABLE) up

migrate-up-github: ## apply pending github integration migrations
	$(MIGRATE) -dir migrations/github -table $(GITHUB_MIGRATE_TABLE) up

migrate-up-jobradar: ## apply pending job radar migrations
	$(MIGRATE) -dir migrations/jobradar -table $(JOBRADAR_MIGRATE_TABLE) up

migrate-up-threads: ## apply pending threads migrations
	$(MIGRATE) -dir migrations/threads -table $(THREADS_MIGRATE_TABLE) up

# The Meta Threads INTEGRATION. Not the Threads module above — different
# schema, different timeline, different thing entirely. See the header of
# migrations/metathreads/0001_init.up.sql.
migrate-up-metathreads: ## apply pending meta threads integration migrations
	$(MIGRATE) -dir migrations/metathreads -table $(METATHREADS_MIGRATE_TABLE) up

migrate-down: ## revert one finance migration
	$(MIGRATE) -dir migrations/finance -step 1 down

migrate-down-chat: ## revert one chat migration
	$(MIGRATE) -dir migrations/chat -table $(CHAT_MIGRATE_TABLE) -step 1 down

migrate-down-releases: ## revert one releases migration
	$(MIGRATE) -dir migrations/releases -table $(RELEASES_MIGRATE_TABLE) -step 1 down

migrate-down-github: ## revert one github integration migration
	$(MIGRATE) -dir migrations/github -table $(GITHUB_MIGRATE_TABLE) -step 1 down

migrate-down-jobradar: ## revert one job radar migration
	$(MIGRATE) -dir migrations/jobradar -table $(JOBRADAR_MIGRATE_TABLE) -step 1 down

migrate-down-threads: ## revert one threads migration
	$(MIGRATE) -dir migrations/threads -table $(THREADS_MIGRATE_TABLE) -step 1 down

migrate-down-metathreads: ## revert one meta threads integration migration
	$(MIGRATE) -dir migrations/metathreads -table $(METATHREADS_MIGRATE_TABLE) -step 1 down

migrate-version: ## show migration version for every module
	@printf "finance  "; $(MIGRATE) -dir migrations/finance version
	@printf "chat     "; $(MIGRATE) -dir migrations/chat -table $(CHAT_MIGRATE_TABLE) version
	@printf "releases "; $(MIGRATE) -dir migrations/releases -table $(RELEASES_MIGRATE_TABLE) version
	@printf "github   "; $(MIGRATE) -dir migrations/github -table $(GITHUB_MIGRATE_TABLE) version
	@printf "jobradar "; $(MIGRATE) -dir migrations/jobradar -table $(JOBRADAR_MIGRATE_TABLE) version
	@printf "threads  "; $(MIGRATE) -dir migrations/threads -table $(THREADS_MIGRATE_TABLE) version
	@printf "metathreads "; $(MIGRATE) -dir migrations/metathreads -table $(METATHREADS_MIGRATE_TABLE) version

migrate-create: ## create migration: make migrate-create name=add_x [MODULE=chat]
	@test -n "$(name)" || (echo "usage: make migrate-create name=<name> [MODULE=finance|chat|releases|github|jobradar|threads|metathreads]"; exit 1)
	@ts=$$(date -u +%Y%m%d%H%M%S); \
	  touch migrations/$(MODULE)/$${ts}_$(name).up.sql; \
	  touch migrations/$(MODULE)/$${ts}_$(name).down.sql; \
	  echo "created migrations/$(MODULE)/$${ts}_$(name).{up,down}.sql"

openapi-gen: ## generate server stubs (requires oapi-codegen)
	go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen -config api/openapi/finance.cfg.yaml api/openapi/finance.yaml

swagger: ## validate the OpenAPI spec; UI is served at /swagger when running
	go run ./cmd/swagger api/openapi/finance.yaml

# The official backend gate. Integration tests are part of it, not an
# optional extra: a real bug survived for weeks because the suite was red and
# `ci` never looked at it. There is no warning-only mode — a failing
# integration test fails the gate.
#
# This requires docker. `make ci-fast` is the same gate minus the database,
# for a quick loop; it is NOT sufficient to validate a change.
ci: tidy fmt vet test test-integration-isolated build swagger ## official backend gate (requires docker)

ci-fast: tidy fmt vet test build swagger ## gate without integration tests — NOT sufficient on its own

backup: ## dump live db → backups/YYYYMMDD-HHMMSS.sql.gz
	@mkdir -p $(BACKUPS_DIR)
	@ts=$$(date -u +%Y%m%d-%H%M%S); \
	 file=$(BACKUPS_DIR)/$${ts}.sql.gz; \
	 echo "→ pg_dump $(DB_NAME)@$(DB_CONTAINER)  →  $$file"; \
	 docker exec $(DB_CONTAINER) pg_dump \
	   --username=$(DB_USER) --dbname=$(DB_NAME) \
	   --format=plain --no-owner --no-privileges \
	   --clean --if-exists \
	 | gzip -9 > $$file; \
	 ls -lh $$file

restore: ## restore from a backup: make restore FILE=backups/20260520-143000.sql.gz
	@test -n "$(FILE)" || (echo "usage: make restore FILE=backups/YYYYMMDD-HHMMSS.sql.gz" && exit 1)
	@test -f "$(FILE)" || (echo "missing file: $(FILE)" && exit 1)
	@echo "→ restoring $(FILE) into $(DB_NAME)@$(DB_CONTAINER)"
	@gunzip -c "$(FILE)" | docker exec -i $(DB_CONTAINER) \
	   psql --username=$(DB_USER) --dbname=$(DB_NAME) --quiet \
	        --set ON_ERROR_STOP=1 > /dev/null
	@echo "→ restored. current rows:"
	@docker exec $(DB_CONTAINER) psql --username=$(DB_USER) --dbname=$(DB_NAME) -tA -c \
	   "select 'categories='   || count(*) from finance.categories where deleted_at is null \
	    union all \
	    select 'cards='        || count(*) from finance.cards         where deleted_at is null \
	    union all \
	    select 'transactions=' || count(*) from finance.transactions  where deleted_at is null;"
