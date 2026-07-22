#!/usr/bin/env bash
# Prepare the container's mutable state, then hand off to supervisord.
#
# Everything here is idempotent: the container is restarted far more often than
# it is created, and a second run must find the database it left behind rather
# than refuse to start or, worse, quietly initialise a new empty one.
set -euo pipefail

PG_BIN="/usr/lib/postgresql/${PG_MAJOR}/bin"

mkdir -p /var/log/mosaic /var/run/postgresql
chown -R postgres:postgres /var/run/postgresql

# PGDATA is a volume, so it is empty exactly once — on first create.
if [ ! -s "${PGDATA}/PG_VERSION" ]; then
  echo "entrypoint: initialising PostgreSQL ${PG_MAJOR} cluster in ${PGDATA}"
  mkdir -p "${PGDATA}"
  chown -R postgres:postgres "${PGDATA}"
  chmod 0700 "${PGDATA}"
  su postgres -c "${PG_BIN}/initdb -D ${PGDATA} --username=mosaic --pwfile=<(echo mosaic) --encoding=UTF8"

  # Trust local connections. This is a throwaway development database that is
  # never reachable beyond the container's own port mapping; requiring a
  # password here would buy nothing and cost a support question.
  echo "host all all all trust" >> "${PGDATA}/pg_hba.conf"
  echo "listen_addresses = '*'" >> "${PGDATA}/postgresql.conf"

  # Create the database the Platform expects. Done against a temporary
  # single-user server so nothing races supervisord's postgres.
  su postgres -c "${PG_BIN}/pg_ctl -D ${PGDATA} -o '-c listen_addresses=' -w start"
  su postgres -c "${PG_BIN}/createdb --username=mosaic mosaic" || true
  su postgres -c "${PG_BIN}/pg_ctl -D ${PGDATA} -m fast -w stop"
  echo "entrypoint: cluster ready"
else
  echo "entrypoint: reusing existing cluster in ${PGDATA}"
  chown -R postgres:postgres "${PGDATA}"
fi

exec "$@"
