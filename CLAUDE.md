# CLAUDE.md

Telegram-бот управления VPN на базе [Remnawave](https://remnawave.com)

./docs/api-remnawave2.5.5.json содержит актуальную документацию для всего api панели, используй его, чтобы работать с панелью. Используй поиск по файлу, но не читай его целиком (~4k строк кода, быстро забьется контекст)

./docs/plans/ используется для хранения планов. После создания плана, необходимо документировать его выполнение в новом файле ./docs/progress/ (с соотстветсвующим названием и ссылкой на план для последующей верификации)

## Архитектура (v2)

Бот работает как **пульт управления** для Remnawave API. База данных хранит только связь `Telegram ID <-> Remnawave UUID`.

### Основные компоненты

- **`internal/remnawave/client.go`** — HTTP-клиент Remnawave API
- **`internal/database/users.go`** — таблица users (telegram_id, username, first_name, remnawave_uuid)
- **`internal/database/invites.go`** — таблица invites (система инвайтов с датой активации)
- **`internal/bot/handlers.go`** — обработчики сообщений, команд и синхронизация данных пользователей
- **`internal/bot/admin.go`** — админ-панель (инвайты, просмотр кодов, бан, уведомления)
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

## Разработка в докере

### Все команды

```bash
go mod download      # Установить зависимости
make down # Остановить бота
make up # Пересобрать докер с ботом
make tests        # Запустить тесты
make fmt         # Проверить код
make logs            # Показать логи
```

### Разработка и Проверка (ОБЯЗАТЕЛЬНО)

Доступные команды:
`make down` / `make up` - управление докером
`make logs` - логи

**Твои обязательные шаги перед завершением любой задачи:**

1. Написал код -> проверь форматирование: `make fmt`
2. Изменил логику -> запусти тесты: `make tests`
   Никогда не рапортуй о завершении задачи, если `make tests` или `make fmt` выдают ошибки. Исправь их сам.

В этом репозитории разрешается делать коммиты, чтобы фиксировать последовательные изменения чаще
## Правило названий коммитов

Используй формат: `<type>: <краткое описание>`

- `type` только в нижнем регистре: `feat`, `fix`, `refactor`, `chore`, `docs`, `plan`, `init`
- описание на русском, краткое, с глаголом действия
- без точки в конце

## Важные заметки

1. **Рассылка** отправляется только активным пользователям (status=ACTIVE в Remnawave)
2. **Бан** удаляет пользователя как из БД бота, так и из Remnawave (отключает доступ к серверам)
3. **Сброс трафика** — счётчик `usedTrafficBytes` автоматически сбрасывается Remnawave 1-го числа при стратегии `MONTH`
4. **Сквады** опциональны — если пользователи не видят серверы, создайте internal squad в панели и добавьте UUID в конфиг
5. **Трафик** — без лимита (`trafficLimitBytes=0`)
6. **Актуализация данных** — при каждом /start бот обновляет username и first_name в БД и синхронизирует username с Remnawave
7. **Удаление кодов** — можно удалять только неиспользованные коды (защита истории активаций)
8. **Субтитры** — опционально, требует запущенный render-сервис. Голосовое → видео с субтитрами, кружок → кружок с субтитрами

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

## Ссылки (Документация)

Если тебе нужен контекст по конкретной фиче, прочитай соответствующий файл перед написанием кода:

- **План миграции**: `docs/plans/2026-01-17-remnawave-migration-design.md`
