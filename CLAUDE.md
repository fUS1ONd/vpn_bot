# CLAUDE.md

Руководство для разработчиков при работе с VPN-ботом на Remnawave.

## Архитектура (v2)

Бот работает как **пульт управления** для Remnawave API. База данных хранит только связь `Telegram ID <-> Remnawave UUID`.

### Основные компоненты

- **`internal/remnawave/client.go`** — HTTP-клиент Remnawave API
- **`internal/database/users.go`** — таблица users (telegram_id, username, first_name, remnawave_uuid)
- **`internal/database/invites.go`** — таблица invites (система инвайтов с датой активации)
- **`internal/bot/handlers.go`** — обработчики сообщений, команд и синхронизация данных пользователей
- **`internal/bot/admin.go`** — админ-панель (инвайты, просмотр кодов, трафик, бан, уведомления)
- **`internal/bot/scheduler.go`** — сброс увеличенных лимитов 1-го числа
- **`internal/bot/dashboard.go`** — Session Manager и движок live-дашборда мониторинга
- **`internal/bot/dashboard_render.go`** — визуализация дашборда (прогресс-бары, флаги, метрики)
- **`internal/monitoring/`** — пакет мониторинга (MetricsClient, SyncNodes, LoadIndex, Alerter)
- **`internal/render/client.go`** — HTTP-клиент render-сервиса (субтитры на видео)
- **`internal/bot/render_handler.go`** — обработчики субтитров (голосовое → видео, кружок → кружок)
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

# Мониторинг (опционально, включается автоматически если VM доступна)
SD_CONFIGS_PATH=/app/sd_configs
VICTORIA_METRICS_URL=http://victoriametrics:8428

# Render-сервис субтитров (опционально, кнопка скрыта если не задан)
RENDER_URL=http://render:8080
RENDER_API_KEY=ключ_render_сервиса
```

## Система инвайтов

1. Новый пользователь отправляет `/start`
2. Бот просит инвайт-код
3. При вводе кода:
   - Проверяется наличие и неиспользованность кода
   - Создаётся пользователь в Remnawave (30 GB/месяц)
   - Сохраняется связка в БД (с first_name из Telegram)
   - Инвайт помечается как использованный (с датой активации)
   - Админу отправляется уведомление о новом пользователе

**Админ создаёт инвайты**: кнопка "📋 Управление" → "🎟 Создать инвайт"

## Управление пользователями

### Админ-панель

- **📋 Управление**
  - 🎟 Создать инвайт — генерирует новый код
  - 📋 Коды — список всех кодов (статус, кто активировал, дата)
  - 🗑 Удалить код — удаляет только неиспользованные коды
  - 📊 Добавить трафик (формат: `telegram_id GB`)
  - 🚫 Забанить (удаляет из БД и Remnawave)
- **📢 Рассылка** — только активным пользователям (status=ACTIVE)

### Уведомления админу

При активации инвайт-кода админ получает уведомление:

- Дата и время активации (формат: `23.01.26 15:30`)
- Telegram ID с кликабельной ссылкой
- Username (если есть)
- First name (если есть)

### Статусы в Remnawave

- `ACTIVE` — активный доступ
- `DISABLED` — заблокирован администратором
- `LIMITED` — превышен лимит трафика
- `EXPIRED` — истёк срок действия

## Команды разработки

## Разработка в докере

```bash
go mod download      # Установить зависимости
make down # Остановить бота
make up # Пересобрать докер с ботом
make tests        # Запустить тесты
make fmt         # Проверить код
make logs            # Показать логи
```

## Важные заметки

1. **Рассылка** отправляется только активным пользователям (status=ACTIVE в Remnawave)
2. **Бан** удаляет пользователя как из БД бота, так и из Remnawave (отключает доступ к серверам)
3. **Scheduler** проверяет 1-го числа месяца — сбрасывает увеличенные лимиты к базовым 30 GB
4. **Сквады** опциональны — если пользователи не видят серверы, создайте internal squad в панели и добавьте UUID в конфиг
5. **Трафик** — базовый лимит 30 GB/месяц, сбрасывается автоматически Remnawave
6. **Добавление трафика** — увеличивает текущий лимит (например, 30 → 40 GB), не затрагивает использованный трафик
7. **Актуализация данных** — при каждом /start бот обновляет username и first_name в БД и синхронизирует username с Remnawave
8. **Удаление кодов** — можно удалять только неиспользованные коды (защита истории активаций)
9. **Субтитры** — опционально, требует запущенный render-сервис. Голосовое → видео с субтитрами, кружок → кружок с субтитрами

## Структура БД

### Таблица `users`

```sql
CREATE TABLE users (
    telegram_id INTEGER PRIMARY KEY,
    username TEXT,
    first_name TEXT,                    -- имя из Telegram (автоматически обновляется)
    remnawave_uuid TEXT UNIQUE NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

### Таблица `invites`

```sql
CREATE TABLE invites (
    code TEXT PRIMARY KEY,
    created_by INTEGER NOT NULL,
    used_by INTEGER,                    -- NULL если не использован
    used_at TIMESTAMP,                  -- время активации кода
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

## Мониторинг нод

### Архитектура

- **VictoriaMetrics** — база метрик (порт 8428)
- **vmagent** — скрейпит Node Exporter на нодах
- **Бот** — генерирует `targets.json`, читает метрики через PromQL

### Конвенция тегов

На нодах в Remnawave задаётся тег `bw:<число>` для указания bandwidth в Mbps.
Пример: `bw:1000` = 1 Gbit. Дефолт: 1000 Mbps.

### Алерты

Бот отправляет админу алерты при:

- Нода OFFLINE (Node Exporter не отвечает)
- Load Index > 80% (перегрузка)

### Установка Node Exporter на ноду

```bash
bash scripts/install-node-exporter.sh <IP_СЕРВЕРА_БОТА>
```

## Ссылки

- **План миграции**: `docs/plans/2026-01-17-remnawave-migration-design.md`
- **Прогресс**: `docs/plans/PROGRESS.md`
- **Дизайн отслеживания кодов**: `docs/plans/2026-01-23-admin-invite-tracking-design.md`
- **Инфраструктура мониторинга**: `docs/plans/2026-02-07-monitoring-infrastructure-design.md`
- **Дашборд мониторинга**: `docs/plans/2026-02-07-bot-monitoring-dashboard-design.md`
- **Субтитры (render)**: `docs/plans/2026-02-10-render-subtitles-design.md`
