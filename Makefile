.PHONY: dev migrate tidy test lint

dev:
docker compose -f deployments/docker-compose.yml --env-file .env.example up --build

migrate:
bash scripts/migrate.sh

tidy:
go mod tidy

lint:
@echo "golangci-lint не настроен, добавьте по необходимости"

test:
go test ./...
