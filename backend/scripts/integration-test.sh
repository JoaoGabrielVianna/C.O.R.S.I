#!/usr/bin/env bash
#
# Runs the integration suite against a Postgres that this script owns from
# start to finish.
#
# Why a dedicated database instead of the dev one: the suite calls
# `migrate.Down()` before each test to guarantee a clean slate (see
# freshDB in finance_integration_test.go). Pointed at the developer's dev
# database, that is a data-destroying operation. The gate must never be a
# reason to lose local work, so it brings its own database and throws it
# away afterwards.
#
# Usage:
#   ./scripts/integration-test.sh            # whole suite
#   ./scripts/integration-test.sh -run TestX # extra args go to `go test`
#
set -euo pipefail

IMAGE="${INTEGRATION_PG_IMAGE:-postgres:17-alpine}"
DB_USER="corsi"
DB_PASS="corsi"
DB_NAME="corsi_test"

# Unique per invocation so two runs (two shells, two branches, a watcher)
# never fight over a container name.
CONTAINER="corsi-integration-$$-${RANDOM}"

# Readiness bound. Postgres normally answers in ~2s; this only exists so a
# broken Docker daemon fails the gate instead of hanging it.
READY_TIMEOUT_SECONDS="${INTEGRATION_PG_TIMEOUT:-60}"

cleanup() {
  # Runs on success, on test failure, and on Ctrl-C. `docker rm -f` is
  # idempotent, and stderr is silenced so a container that never started
  # does not add noise to a real failure.
  docker rm -f "${CONTAINER}" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

if ! command -v docker >/dev/null 2>&1; then
  echo "integration-test: docker is required to provision the test database" >&2
  echo "integration-test: set TEST_POSTGRES_DSN and use 'make test-integration' to bring your own" >&2
  exit 1
fi

echo "integration-test: starting disposable postgres (${IMAGE})"
# Port 0 lets Docker pick a free one: no fixed host port to collide on.
# Bound to loopback so the throwaway database is never reachable off-box.
docker run -d --rm \
  --name "${CONTAINER}" \
  -e POSTGRES_USER="${DB_USER}" \
  -e POSTGRES_PASSWORD="${DB_PASS}" \
  -e POSTGRES_DB="${DB_NAME}" \
  -p 127.0.0.1::5432 \
  "${IMAGE}" >/dev/null

HOST_PORT="$(docker port "${CONTAINER}" 5432/tcp | head -1 | sed 's/.*://')"
if [ -z "${HOST_PORT}" ]; then
  echo "integration-test: could not resolve the mapped port" >&2
  exit 1
fi

printf 'integration-test: waiting for postgres on 127.0.0.1:%s' "${HOST_PORT}"
waited=0
until docker exec "${CONTAINER}" pg_isready -U "${DB_USER}" -d "${DB_NAME}" >/dev/null 2>&1; do
  if [ "${waited}" -ge "${READY_TIMEOUT_SECONDS}" ]; then
    echo
    echo "integration-test: postgres did not become ready in ${READY_TIMEOUT_SECONDS}s" >&2
    docker logs "${CONTAINER}" 2>&1 | tail -20 >&2
    exit 1
  fi
  printf '.'
  sleep 1
  waited=$((waited + 1))
done
echo " ready in ${waited}s"

# The suite discovers the database exclusively through TEST_POSTGRES_DSN and
# applies its own migrations, so no schema setup happens here. Finance and
# chat keep the migration contracts they already have.
DSN="postgres://${DB_USER}:${DB_PASS}@127.0.0.1:${HOST_PORT}/${DB_NAME}?sslmode=disable"

# The one piece of setup that IS this script's job: saying that the database
# it just created may be destroyed.
#
# The suites refuse to reset a database without this table — see
# internal/platform/testdb. It is written here, by the process that created
# the database seconds ago, because that is the only moment at which
# "disposable" is a fact rather than an assumption. A suite that could mark
# its own target could mark the development database, which is the accident
# the marker exists to prevent.
#
# Retried, because pg_isready answers during the initdb bootstrap server
# that Postgres then shuts down and replaces. The readiness loop above is
# satisfied by it; the first real statement after it can still land on a
# server on its way out. Every suite depends on this table existing, so it
# gets its own short retry rather than failing the whole gate on a race.
marked=""
for attempt in $(seq 1 20); do
  if docker exec "${CONTAINER}" psql --username="${DB_USER}" --dbname="${DB_NAME}" --quiet \
       --set ON_ERROR_STOP=1 -c \
       'CREATE TABLE IF NOT EXISTS public.destructible_database (marked_at TIMESTAMPTZ NOT NULL DEFAULT now())' \
       >/dev/null 2>&1; then
    marked="yes"
    break
  fi
  sleep 1
done
if [ -z "${marked}" ]; then
  echo "integration-test: could not mark ${DB_NAME} as destructible" >&2
  echo "integration-test: every suite will refuse to run; see internal/platform/testdb" >&2
  exit 1
fi
echo "integration-test: marked ${DB_NAME} as destructible"

echo "integration-test: running suite"
set +e
TEST_POSTGRES_DSN="${DSN}" go test -tags=integration -count=1 "$@" ./...
STATUS=$?
set -e

if [ "${STATUS}" -eq 0 ]; then
  echo "integration-test: PASS"
else
  echo "integration-test: FAIL (exit ${STATUS})" >&2
fi
exit "${STATUS}"
