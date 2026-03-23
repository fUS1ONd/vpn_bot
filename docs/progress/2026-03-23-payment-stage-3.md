# Прогресс: Этап 3 — Callback HTTP-сервер

**План:** [docs/plans/2026-03-22-payment-implementation-plan.md](../plans/2026-03-22-payment-implementation-plan.md) (строки 807–1016)

## Что сделано

Реализован встроенный HTTP-сервер для приёма callback от Platega. Сервер стартует в горутине при наличии `PLATEGA_MERCHANT_ID` и `PLATEGA_SECRET`. Поддерживает graceful shutdown через context.

## Изменённые файлы

| Файл | Что изменено |
|------|-------------|
| `internal/config/config.go` | Добавлено поле `CallbackPort int` и чтение `CALLBACK_PORT` (default 8080) |
| `internal/callback/server.go` | Создан новый файл: `Server`, `NewServer`, `Start`, `Shutdown`, `handleCallback`, `handleHealth`, интерфейс `PaymentHandler` |
| `internal/callback/server_test.go` | Создан новый файл: 4 теста (TDD — написаны до реализации) |
| `cmd/bot/main.go` | Запуск callback-сервера в горутине, `noopPaymentHandler` заглушка |
| `docker-compose.yml` | Добавлен проброс порта `127.0.0.1:8080:8080` для сервиса `vpn-bot` |

## Подход

Использован TDD:
1. **RED** — написан `server_test.go`, тесты не компилировались (пакет отсутствовал)
2. **GREEN** — написан `server.go`, все 4 теста прошли
3. Обновлены конфиг и main.go

## Отклонения от плана

- В `main.go` вместо `telegramBot.PaymentCallbackHandler()` (не реализован) добавлена `noopPaymentHandler` — заглушка, которая логирует callback и возвращает nil. Это согласовано с заданием: "добавь заглушку-комментарий TODO или пропусти условие запуска сервера". Выбран вариант с рабочей заглушкой — сервер стартует и отвечает на запросы.
- Добавлен метод `Server.Handler() http.Handler` для удобства тестирования (без запуска реального TCP-сервера). В плане явно не описан, но необходим для чистых unit-тестов.

## Статус критериев приёмки

| Критерий | Статус |
|----------|--------|
| Callback-сервер стартует на порту 8080 при наличии PLATEGA_* | ✅ |
| `/health` возвращает 200 | ✅ TestCallbackHealth |
| `/platega/callback` отклоняет запросы без X-MerchantId/X-Secret | ✅ TestCallbackVerification (4 сценария) |
| Корректные callback-запросы логируются | ✅ handleCallback логирует через slog |
| Без PLATEGA_* бот работает как раньше | ✅ проверка `if cfg.PlategaMerchantID != ""` |
| `make fmt` без ошибок | ✅ |
| `make tests` без ошибок | ✅ все 8 пакетов OK |
