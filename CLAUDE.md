# CLAUDE.md

Telegram-бот управления VPN на базе [Remnawave](https://remnawave.com)

./docs/platega/README.md содержит информацию по взаимодействию с платежной системой 

./docs/api-remnawave2.6.4.json содержит актуальную документацию для всего api панели, используй его, чтобы работать с панелью. Используй поиск по файлу, но не читай его целиком (~4k строк кода, быстро забьется контекст)

./docs/plans/ используется для хранения планов. После создания плана, необходимо документировать его выполнение в новом файле ./docs/progress/ (с соотстветсвующим названием и ссылкой на план для последующей верификации)

## Архитектура (v2)

Бот работает как **пульт управления** для Remnawave API. База данных хранит только связь `Telegram ID <-> Remnawave UUID`.

### Основные компоненты

- **`internal/remnawave/client.go`** — HTTP-клиент Remnawave API
- **`internal/platega/client.go`** — HTTP-клиент Platega
- **`internal/callback/server.go`** — встроенный HTTP-сервер для callback и health-check
- **`internal/database/users.go`** — таблица users (`telegram_id`, `username`, `first_name`, `remnawave_uuid`, `subscription_price`, `moderator_id`)
- **`internal/database/invites.go`** — таблица invites (`code`, `created_by`, `used_by`, `expire_days`, `subscription_price`, `kicked_at`)
- **`internal/database/payments.go`** — таблица payments и логика подтверждения/ретраев платежей
- **`internal/database/earnings.go`** — таблица `moderator_earnings` и расчёт долей модераторов
- **`internal/database/bans.go`** — таблица `banned_users` и проверки перманентных банов
- **`internal/database/notifications.go`** — таблица `notifications_sent` (защита от повторных уведомлений)
- **`internal/bot/handlers.go`** — обработчики сообщений, команд и синхронизация данных пользователей
- **`internal/bot/admin.go`** — админ-панель (инвайты, просмотр кодов, бан, уведомления, статистика, режим обслуживания)
- **`internal/bot/payment_handler.go`** — пользовательский flow оплаты и ручная проверка платежей
- **`internal/bot/payment.go`** — callback-активация, retry и расчёт earnings
- **`internal/bot/scheduler.go`** — scheduler подписок и платежей: каждые 30 минут + первый проход при старте
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
REMNAWAVE_DEFAULT_SQUAD_UUIDS=  # опционально, UUID сквадов через запятую; новые пользователи попадут во все перечисленные

# База данных
DB_PATH=/app/data/bot.db

# Мониторинг (опционально, включается автоматически если VM доступна)
SD_CONFIGS_PATH=/app/sd_configs
VICTORIA_METRICS_URL=http://victoriametrics:8428

# Платежи Platega (опционально; если не заданы — бот работает как раньше)
PLATEGA_MERCHANT_ID=...
PLATEGA_SECRET=...
PLATEGA_CALLBACK_URL=https://vpn.example.com/platega/callback
CALLBACK_PORT=8080
MIN_SUBSCRIPTION_PRICE=400
TRIAL_TRAFFIC_LIMIT_GB=1
PLATEGA_FEE_SBP=11
PLATEGA_FEE_CARD=12
PLATEGA_FEE_CRYPTO=5
PLATEGA_FEE_WITHDRAWAL=2

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
2. **Типы инвайтов:** админский — бессрочный (`expire_days=NULL`), модераторский — триал на 72 часа с ценой подписки из инвайта
3. **Платежи Platega опциональны:** без `PLATEGA_MERCHANT_ID` и `PLATEGA_SECRET` бот работает как раньше, callback-сервер не стартует, кнопки оплаты не показываются
4. **Legacy-пользователи:** при `subscription_price = NULL` кнопка оплаты скрыта; scheduler пропускает старые записи без инвайта и цены
5. **Платёжный flow:** callback обрабатывается быстро, долгие retry не держат HTTP-запрос открытым; при сбое активации платёж переходит в `confirmed_not_activated`, а scheduler повторяет активацию без перезаписи исходного `confirmed_at`
6. **Плановый scheduler:** стартует сразу при запуске и далее работает каждые 30 минут; обрабатывает pending/confirmed_not_activated, уведомления, disable и grace kick
7. **Maintenance mode:** скрывает оплату и блокирует disable/автокики, но остальная функциональность бота продолжает работать
8. **Бан и автокик различаются:** бан пишет в `banned_users` (перманентно), автокик бан не ставит (пользователь может вернуться по новому инвайту)
9. **Сброс трафика** — счётчик `usedTrafficBytes` автоматически сбрасывается Remnawave 1-го числа при стратегии `MONTH`
10. **Сквады** опциональны — если пользователи не видят серверы, создайте internal squads в панели и добавьте UUID в `REMNAWAVE_DEFAULT_SQUAD_UUIDS`
11. **Трафик** — без лимита для оплаченных и админских пользователей (`trafficLimitBytes=0`); для триала лимит задаётся через `TRIAL_TRAFFIC_LIMIT_GB`
12. **Актуализация данных** — при каждом /start бот обновляет username и first_name в БД и синхронизирует username с Remnawave
13. **Удаление кодов** — можно удалять только неиспользованные коды (защита истории активаций)
14. **Субтитры** — опционально, требует запущенный render-сервис. Голосовое → видео с субтитрами, кружок → кружок с субтитрами

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
