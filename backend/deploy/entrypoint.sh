#!/bin/sh
set -e

# Platforms pass PORT; Go config reads HTTP_ADDR.
if [ -n "${PORT}" ]; then
  export HTTP_ADDR=":${PORT}"
fi

# Paths default to the layout the Dockerfile produces, so production behavior
# is unchanged. They are overridable only so the migration stage can be
# exercised against a throwaway database without building an image — see
# deploy/entrypoint_integration_test.go.
MIGRATE_BIN="${MIGRATE_BIN:-/app/migrate}"
MIGRATIONS_DIR="${MIGRATIONS_DIR:-/app/migrations}"
SERVER_BIN="${SERVER_BIN:-/app/corsi}"

if [ "${RUN_MIGRATIONS:-true}" = "true" ]; then
  if [ -z "${POSTGRES_DSN}" ]; then
    echo "entrypoint: POSTGRES_DSN is required when RUN_MIGRATIONS=true" >&2
    exit 1
  fi

  # One invocation per bounded context, because each owns an independent
  # migration timeline and therefore its own version table.
  #
  # finance keeps the driver's default table (`schema_migrations`): it
  # predates the split, and pointing it somewhere new would make
  # golang-migrate read a fully populated production database as version 0.
  # Every module added since names its own table explicitly. Omitting
  # `-table` for chat would make it read finance's table — already at
  # version 8 — and conclude there is nothing to apply. See backend/Makefile.
  #
  # `set -e` is on and each invocation is guarded, so a failed migration
  # exits here with a non-zero status. The exec below is never reached: the
  # container dies rather than serving a schema it does not have.
  echo "entrypoint: applying migrations"

  "${MIGRATE_BIN}" -dsn "${POSTGRES_DSN}" -dir "${MIGRATIONS_DIR}/finance" up \
    || { echo "entrypoint: finance migrations failed" >&2; exit 1; }

  "${MIGRATE_BIN}" -dsn "${POSTGRES_DSN}" -dir "${MIGRATIONS_DIR}/chat" -table schema_migrations_chat up \
    || { echo "entrypoint: chat migrations failed" >&2; exit 1; }

  "${MIGRATE_BIN}" -dsn "${POSTGRES_DSN}" -dir "${MIGRATIONS_DIR}/releases" -table schema_migrations_releases up \
    || { echo "entrypoint: releases migrations failed" >&2; exit 1; }

  "${MIGRATE_BIN}" -dsn "${POSTGRES_DSN}" -dir "${MIGRATIONS_DIR}/github" -table schema_migrations_github up \
    || { echo "entrypoint: github migrations failed" >&2; exit 1; }

  "${MIGRATE_BIN}" -dsn "${POSTGRES_DSN}" -dir "${MIGRATIONS_DIR}/jobradar" -table schema_migrations_jobradar up \
    || { echo "entrypoint: jobradar migrations failed" >&2; exit 1; }

  "${MIGRATE_BIN}" -dsn "${POSTGRES_DSN}" -dir "${MIGRATIONS_DIR}/threads" -table schema_migrations_threads up \
    || { echo "entrypoint: threads migrations failed" >&2; exit 1; }

  # The Meta Threads INTEGRATION, which is not the Threads module above.
  "${MIGRATE_BIN}" -dsn "${POSTGRES_DSN}" -dir "${MIGRATIONS_DIR}/metathreads" -table schema_migrations_metathreads up \
    || { echo "entrypoint: meta threads migrations failed" >&2; exit 1; }

  # Palace, the operator's persistent memory. Its `memories` and `sources`
  # are NOT chat.memories and chat.agent_sources, which are the agent's.
  # See the header of migrations/palace/0001_init.up.sql.
  "${MIGRATE_BIN}" -dsn "${POSTGRES_DSN}" -dir "${MIGRATIONS_DIR}/palace" -table schema_migrations_palace up \
    || { echo "entrypoint: palace migrations failed" >&2; exit 1; }

  # The Telegram INTEGRATION. Routing state only: which Telegram chat may
  # speak to which workspace, and which conversation carries which agent.
  # No transcript lives in this schema — those are chat.messages.
  #
  # Applied unconditionally, including on a deployment with no
  # TELEGRAM_BOT_TOKEN. The schema costs four empty tables and its absence
  # would make enabling the integration a migration the operator has to
  # remember, which is the failure mode this whole stage exists to remove.
  "${MIGRATE_BIN}" -dsn "${POSTGRES_DSN}" -dir "${MIGRATIONS_DIR}/telegram" -table schema_migrations_telegram up \
    || { echo "entrypoint: telegram migrations failed" >&2; exit 1; }

  # Identity, the platform capability that authenticates the one operator.
  # Sessions only: there is no user row and no password column, because the
  # credential is environment (AUTH_EMAIL + AUTH_PASSWORD_HASH) and never
  # data. See migrations/identity/0001_init.up.sql.
  "${MIGRATE_BIN}" -dsn "${POSTGRES_DSN}" -dir "${MIGRATIONS_DIR}/identity" -table schema_migrations_identity up \
    || { echo "entrypoint: identity migrations failed" >&2; exit 1; }
fi

echo "entrypoint: starting corsi on ${HTTP_ADDR}"
exec "${SERVER_BIN}"
