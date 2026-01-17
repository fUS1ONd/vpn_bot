# CLAUDE.md

Руководство для разработчиков при работе с VPN-ботом на Remnawave.

## Архитектура (v2)

Бот работает как **пульт управления** для Remnawave API. База данных хранит только связь `Telegram ID <-> Remnawave UUID`.

### Основные компоненты

- **`internal/remnawave/client.go`** — HTTP-клиент Remnawave API
- **`internal/database/users.go`** — таблица users (telegram_id, username, remnawave_uuid)
- **`internal/database/invites.go`** — таблица invites (система инвайтов)
- **`internal/bot/handlers.go`** — обработчики сообщений и команд
- **`internal/bot/admin.go`** — админ-панель (инвайты, трафик, банн)
- **`internal/bot/scheduler.go`** — сброс увеличенных лимитов 1-го числа
- **`cmd/migrator/main.go`** — миграция активных пользователей из старой БД

## Переменные окружения

```env
# Telegram
BOT_TOKEN=...
ADMIN_ID=...

# Remnawave
REMNAWAVE_URL=https://panel.example.com
REMNAWAVE_API_TOKEN=...
REMNAWAVE_DEFAULT_SQUAD_UUID=  # опционально, UUID сквада если нужен

# База данных
DB_PATH=/app/data/bot.db

# Донат
DONATE_TEXT=Перевод по СБП: +7 999 000-00-00 (Т-Банк), Константин К.
```

## Система инвайтов

1. Новый пользователь отправляет `/start`
2. Бот просит инвайт-код
3. При вводе кода:
   - Проверяется наличие и неиспользованность кода
   - Создаётся пользователь в Remnawave (30 GB/месяц)
   - Сохраняется связка в БД
   - Инвайт помечается как использованный

**Админ создаёт инвайты**: кнопка "📋 Управление" → "🎟 Создать инвайт"

## Управление пользователями

### Админ-панель

- **📋 Управление**
  - 🎟 Создать инвайт
  - 📊 Добавить трафик (формат: `telegram_id GB`)
  - 🚫 Забанить (удаляет из БД и Remnawave)
- **📢 Рассылка** — только активным пользователям (status=ACTIVE)

### Статусы в Remnawave

- `ACTIVE` — активный доступ
- `DISABLED` — заблокирован администратором
- `LIMITED` — превышен лимит трафика
- `EXPIRED` — истёк срок действия

## Мигратор

Перенос активных пользователей из старой БД в новую.

```bash
# Сборка
go build -o migrator ./cmd/migrator

# Предпросмотр (без изменений)
./migrator --dry-run --old-db /path/to/users.db

# Выполнить миграцию
./migrator --live --old-db /path/to/users.db
```

**Что переносит:**
- Только пользователей со статусом `active` (у кого есть доступ прямо сейчас)
- Создаёт в Remnawave с тем же telegram_id и username
- Сохраняет связку в новой БД
- Логирует результаты в `migration_YYYY-MM-DD.log`

## Команды разработки

### Локальная разработка

```bash
go mod download      # Установить зависимости
go run cmd/bot/main.go  # Запустить бота локально
go build -o vpn-bot cmd/bot/main.go  # Собрать бинарник
go build ./cmd/migrator  # Собрать мигратор
go test ./...        # Запустить тесты
go vet ./...         # Проверить код
```

### Docker (если есть docker-compose.yml)

```bash
make up              # Запустить бота в Docker
make down            # Остановить бота
make logs            # Показать логи
docker compose up -d --build           # Собрать и запустить
docker compose logs -f vpn-bot         # Логи в реал-тайме
```

## Важные заметки

1. **Рассылка** отправляется только активным пользователям (status=ACTIVE в Remnawave)
2. **Бан** удаляет пользователя как из БД бота, так и из Remnawave (отключает доступ к серверам)
3. **Scheduler** проверяет 1-го числа месяца — сбрасывает увеличенные лимиты к базовым 30 GB
4. **Сквады** опциональны — если пользователи не видят серверы, создайте internal squad в панели и добавьте UUID в конфиг
5. **Трафик** — базовый лимит 30 GB/месяц, сбрасывается автоматически Remnawave
6. **Добавление трафика** — увеличивает текущий лимит (например, 30 → 40 GB), не затрагивает использованный трафик

## Структура БД

### Таблица `users`

```sql
CREATE TABLE users (
    telegram_id INTEGER PRIMARY KEY,
    username TEXT,
    remnawave_uuid TEXT UNIQUE NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

### Таблица `invites`

```sql
CREATE TABLE invites (
    code TEXT PRIMARY KEY,
    created_by INTEGER NOT NULL,
    used_by INTEGER,  -- NULL если не использован
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

## Ссылки

- **План миграции**: `docs/plans/2026-01-17-remnawave-migration-design.md`
- **Прогресс**: `docs/plans/PROGRESS.md`
