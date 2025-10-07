#!/usr/bin/env bash
set -euo pipefail

if ! command -v migrate >/dev/null; then
  echo "Установите CLI migrate: https://github.com/golang-migrate/migrate" >&2
  exit 1
fi

PG_DSN=${PG_DSN:-postgres://postgres:postgres@localhost:5432/tgdigest?sslmode=disable}

migrate -path migrations -database "$PG_DSN" up
