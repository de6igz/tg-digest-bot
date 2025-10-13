# TG Digest Bot

Минимальный стартовый репозиторий Telegram-сервиса "Суточные дайджесты каналов". Архитектура организована по принципам Clean Architecture: domain/usecase/adapters/infra.

## Быстрый старт

1. Скопируйте `.env.example` в `.env` и заполните значения токенов Telegram и доступа к БД.
2. Подготовьте MTProto-аккаунты. Импортёр `cmd/mtproto-session-importer` сохраняет API ID/Hash и данные сессии в БД, поддерживая
   Telethon SQLite-файлы (`*.session`), JSON-описания аккаунтов и строковые сессии. Для комплекта из `*.json` и `*.session`
   достаточно выполнить:

   ```bash
   go run ./cmd/mtproto-session-importer \
     -meta /путь/к/аккаунту.json \
     -file /путь/к/аккаунту.session \
     -pool default
   ```

   Импортёр возьмёт `app_id` и `app_hash` из JSON, преобразует сессию к формату gotd и сохранит аккаунт в пул `default`. В рантайме
   сервисы используют пул, указанный в переменной `MTPROTO_SESSION_NAME`, перебирая аккаунты при ошибках MTProto.
3. Запустите инфраструктуру и сервисы:

```bash
make dev
```

4. После запуска бота выставьте вебхук:

```bash
curl -X POST "https://api.telegram.org/bot<Токен>/setWebhook" \
  -d url="https://<ваш-домен>/bot/webhook"
```

5. Mini App доступно через URL, укажите initData Telegram в GET-параметре `init_data`.

## Миграции

Для применения миграций локально установите CLI `migrate` и выполните:

```bash
make migrate
```

## API

- `GET /api/v1/digest/today`
- `GET /api/v1/digest/history?days=7`
- `GET /api/v1/channels`
- `POST /api/v1/channels`
- `DELETE /api/v1/channels/{id}`
- `PUT /api/v1/settings/time`

Подробнее — в `cmd/api/openapi.yaml`.

## Ограничения MVP

- MTProto-сборщик пока возвращает заглушечные данные, но резолвер требует рабочую сессию MTProto.
- Ранжирование и суммаризация используют эвристики.
- Доставка дайджестов через Telegram отправляет лишь уведомление об успешном формировании.
- Mini App API возвращает статические данные для демонстрации схемы.

## Тесты

```bash
make test
```

## Скрипты

- `scripts/dev.sh` — запуск docker-compose стека.
- `scripts/migrate.sh` — применение миграций через golang-migrate.

