SHELL   := /bin/bash
COMPOSE := docker-compose -f docker-compose.dev.yml

.DEFAULT_GOAL := help
.PHONY: help dev backend frontend migrate test build logs ps down reset doctor

# ─── help ───────────────────────────────────────────────────────────────
help: ## list targets
	@printf "C.O.R.S.I — top-level commands\n\n"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
	  | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'
	@printf "\nFor finer-grained backend commands: make -C backend help\n"

# ─── daily loop ─────────────────────────────────────────────────────────
dev: ## bring up postgres, run migrations, then run backend natively
	@$(COMPOSE) up -d postgres
	@until $(COMPOSE) exec -T postgres pg_isready -U corsi -d corsi >/dev/null 2>&1; do sleep 1; done
	@$(MAKE) -C backend migrate-up
	@$(MAKE) -C backend run

backend: ## run backend only (assumes postgres already up)
	@$(MAKE) -C backend run

frontend: ## run vite dev server (http://localhost:5173)
	@cd frontend && npm run dev

migrate: ## apply pending migrations against local postgres
	@$(MAKE) -C backend migrate-up

test: ## run backend unit tests
	@$(MAKE) -C backend test

# ─── deploy artifacts ───────────────────────────────────────────────────
build: ## build production docker images (corsi-backend, corsi-frontend)
	@docker build -t corsi-backend  ./backend
	@docker build -t corsi-frontend ./frontend

# ─── ops ────────────────────────────────────────────────────────────────
logs: ## tail dev-stack logs
	@$(COMPOSE) logs -f

ps: ## show dev-stack status
	@$(COMPOSE) ps

down: ## stop dev stack (keeps data volume)
	@$(COMPOSE) down

reset: ## DESTROY db volume + re-apply migrations (asks confirmation)
	@read -r -p "→ this WIPES the local corsi database. type 'yes' to continue: " ack && \
	  [ "$$ack" = "yes" ] || (echo "aborted"; exit 1)
	@$(COMPOSE) down -v
	@$(COMPOSE) up -d postgres
	@until $(COMPOSE) exec -T postgres pg_isready -U corsi -d corsi >/dev/null 2>&1; do sleep 1; done
	@$(MAKE) -C backend migrate-up
	@echo "→ db reset complete"

doctor: ## check that docker, go, and node are reachable
	@command -v docker >/dev/null || (echo "missing: docker"  && exit 1)
	@command -v go     >/dev/null || (echo "missing: go"      && exit 1)
	@command -v node   >/dev/null || (echo "missing: node"    && exit 1)
	@echo "ok — docker=$$(docker --version | head -1)  go=$$(go version)  node=$$(node -v)"
