# VPN Bot (Remnawave v2)

Telegram-бот управления VPN на базе [Remnawave](https://remnawave.com). Пользователи регистрируются по инвайт-кодам, управляют подключениями и отслеживают трафик. Админы создают инвайты, добавляют трафик и банят пользователей.

## Возможности

- **Инвайт-система** — регистрация по кодам приглашения
- **Управление пользователями** — учёт аккаунтов в Remnawave
- **Трафик** — базовый лимит 30 GB/месяц с автоматическим сбросом
- **Админ-панель** — создание/просмотр/удаление инвайтов, добавление трафика, бан, рассылки
- **Уведомления** — админ получает уведомление при активации нового пользователя
- **Актуализация данных** — автоматическая синхронизация username с Remnawave
- **Планировщик** — автоматический сброс лимитов 1-го числа месяца
- **Миграция** — перенос пользователей из старой БД

## Быстрый старт

### Docker (рекомендуется)

```bash
git clone https://github.com/fus1ond/vpn_bot.git
cd vpn_bot
cp .env.example .env
# Отредактируй .env с твоими ключами
make up
```

Просмотр логов:
```bash
make logs
```

### Локальная разработка

```bash
go mod download
go run cmd/bot/main.go
```

Или собрать бинарник:
```bash
go build -o vpn-bot cmd/bot/main.go
./vpn-bot
```

## Конфигурация

Переменные окружения в `.env`:

```env
# Telegram
BOT_TOKEN=токен_от_@BotFather
ADMIN_ID=твой_telegram_id  # Получи у @userinfobot

# Remnawave
REMNAWAVE_URL=https://panel.example.com
REMNAWAVE_API_TOKEN=jwt_токен_из_панели
REMNAWAVE_DEFAULT_SQUAD_UUID=  # опционально

# База данных
DB_PATH=/app/data/bot.db

# Информация для донатов
DONATE_TEXT=Переводи по СБП: +7 999 000-00-00
```

## Как работает

**Пользователь:**
1. Пишет `/start` боту
2. Вводит инвайт-код
3. Бот создаёт аккаунт в Remnawave (30 GB/месяц)
4. Пользователь может управлять подключениями

**Админ (кнопка "📋 Управление"):**
- **🎟 Создать инвайт** — генерирует коды
- **📋 Коды** — список всех кодов (статус, кто активировал, дата)
- **🗑 Удалить код** — удаляет неиспользованные коды
- **📊 Добавить трафик** — формат: `telegram_id GB`
- **🚫 Забанить** — удаляет из БД и Remnawave
- **📢 Рассылка** — только активным пользователям

При активации кода админ получает уведомление с информацией о новом пользователе (Telegram ID, username, имя).

## База данных

SQLite с двумя таблицами:

**users** — связь Telegram ID и Remnawave UUID:
```sql
CREATE TABLE users (
    telegram_id INTEGER PRIMARY KEY,
    username TEXT,
    first_name TEXT,                    -- имя из Telegram
    remnawave_uuid TEXT UNIQUE NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

**invites** — управление инвайт-кодами:
```sql
CREATE TABLE invites (
    code TEXT PRIMARY KEY,
    created_by INTEGER NOT NULL,
    used_by INTEGER,
    used_at TIMESTAMP,                  -- время активации
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

## Docker команды

```bash
make up              # Запустить бота
make down            # Остановить
make restart         # Перезагрузить
make logs            # Логи в реал-тайме
make clean           # Остановить и удалить контейнеры
make shell           # Оболочка в контейнере
```

Или прямо Docker Compose:
```bash
docker compose up -d --build
docker compose logs -f vpn-bot
docker compose down
```

## Разработка

```bash
# Тесты
go test ./...

# Анализ кода
go vet ./...

# Собрать миграцию
go build -o migrator cmd/migrator/main.go
```

**Структура:**
```
vpn_bot/
├── cmd/bot/           → Основное приложение
├── cmd/migrator/      → Инструмент миграции
├── internal/
│   ├── bot/           → Обработчики команд
│   ├── database/      → SQLite слой
│   ├── remnawave/     → API клиент
│   └── config/        → Конфигурация
├── Makefile          → Команды
└── docker-compose.yml
```

## Миграция

Перенос пользователей из старой БД:

```bash
go build -o migrator cmd/migrator/main.go

# Предпросмотр (без изменений)
./migrator --dry-run --old-db /path/to/old.db

# Выполнить
./migrator --live --old-db /path/to/old.db
```

Переносит только активных пользователей, логирует в `migration_YYYY-MM-DD.log`.

## Проблемы

| Проблема | Решение |
|----------|---------|
| Бот не отвечает | Проверь `BOT_TOKEN` в `.env`, перезагрузи `make restart` |
| Не работает регистрация | Проверь `REMNAWAVE_API_TOKEN` и `REMNAWAVE_URL` в логах |
| Трафик не сбрасывается | Планировщик работает 1-го числа, проверь логи |
| Проблемы БД | Резервная копия: `make backup`, логи: `make logs` |

## Безопасность

- Никогда не коммитируй `.env` в Git
- Используй надёжные API-токены
- Ограничь админ-доступ
- Делай резервные копии: `make backup`

## Дополнительно

- **CLAUDE.md** — детальная архитектура
- **Remnawave** — https://remnawave.com

---

**Версия:** 2.0 (Remnawave)
**Repository:** https://github.com/fus1ond/vpn_bot
