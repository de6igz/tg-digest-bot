.PHONY: dev migrate tidy test lint mtproto-bundle mtproto-import mtproto-import-from-telethon

PYTHON ?= python3
GO ?= go

TELETHON_EXPORT ?= $(PYTHON) scripts/export_telethon_session.py
MTPROTO_IMPORT ?= $(GO) run ./cmd/mtproto-session-importer

define require_var
$(if $($1),,$(error $1 is required))
endef

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

mtproto-bundle:
	$(call require_var,METADATA)
	$(call require_var,SESSION)
	$(call require_var,OUTPUT)
	$(TELETHON_EXPORT) --metadata "$(METADATA)" --session "$(SESSION)" --output "$(OUTPUT)"$(if $(NAME), --name "$(NAME)",)$(if $(POOL), --pool "$(POOL)",)$(if $(API_ID), --api-id $(API_ID),)$(if $(API_HASH), --api-hash "$(API_HASH)",)

mtproto-import:
	$(call require_var,BUNDLE)
	$(MTPROTO_IMPORT) -bundle "$(BUNDLE)"$(if $(NAME), -name "$(NAME)",)$(if $(POOL), -pool "$(POOL)",)

mtproto-import-from-telethon:
	$(call require_var,METADATA)
	$(call require_var,SESSION)
	@set -e; \
		session_basename=$$(basename "$(SESSION)"); \
		session_stem=$${session_basename%.*}; \
		bundle_path=$${BUNDLE:-build/mtproto/$${session_stem}.bundle.json}; \
		$(MAKE) mtproto-bundle METADATA="$(METADATA)" SESSION="$(SESSION)" OUTPUT="$$bundle_path"$(if $(NAME), NAME="$(NAME)",)$(if $(POOL), POOL="$(POOL)",)$(if $(API_ID), API_ID=$(API_ID),)$(if $(API_HASH), API_HASH="$(API_HASH)",); \
		$(MAKE) mtproto-import BUNDLE="$$bundle_path"$(if $(NAME), NAME="$(NAME)",)$(if $(POOL), POOL="$(POOL)",)
