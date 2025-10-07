#!/usr/bin/env bash
set -euo pipefail

docker compose -f deployments/docker-compose.yml --env-file .env.example up --build
