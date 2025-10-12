# TG Digest Bot

Минимальный стартовый репозиторий Telegram-сервиса "Суточные дайджесты каналов". Архитектура организована по принципам Clean Architecture: domain/usecase/adapters/infra.

## Быстрый старт

1. Скопируйте `.env.example` в `.env` и заполните значения токенов Telegram и доступа к БД.
2. Подготовьте MTProto-сессию (например, выполните `go run github.com/gotd/td/cmd/telegram-auth` и авторизуйтесь под сервисным
   аккаунтом). Полученный JSON можно сохранить в таблицу `mtproto_sessions` вручную или с помощью утилиты импорта. Импортёр
   поддерживает файлы `tg.session`, а также JSON-описания аккаунтов, которые продают вместе с файлом SQLite — из них будет
   взята строковая сессия Telethon (`extra_params`) и автоматически преобразована к формату gotd:

   ```bash
   go run ./cmd/mtproto-session-importer -file /путь/к/tg.session -name default
   ```

   Имя сессии должно совпадать со значением `MTPROTO_SESSION_NAME` (по умолчанию `default`).
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

