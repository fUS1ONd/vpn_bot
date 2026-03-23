# Этап 9: Финализация

**Дата:** 2026-03-23
**План:** [2026-03-22-payment-implementation-plan.md](../plans/2026-03-22-payment-implementation-plan.md), строки 2006–2059
**Предлагаемый коммит:** `feat: завершить интеграцию платежей`

## Что сделано

### `docker-compose.yml`
- Добавлены все переменные платёжной интеграции в `environment`:
  - `PLATEGA_MERCHANT_ID`
  - `PLATEGA_SECRET`
  - `PLATEGA_CALLBACK_URL`
  - `CALLBACK_PORT`
  - `MIN_SUBSCRIPTION_PRICE`
  - `TRIAL_TRAFFIC_LIMIT_GB`
  - `PLATEGA_FEE_SBP`
  - `PLATEGA_FEE_CARD`
  - `PLATEGA_FEE_CRYPTO`
  - `PLATEGA_FEE_WITHDRAWAL`
- Проброс callback-порта переведён на env-переменную:
  - `127.0.0.1:${CALLBACK_PORT:-8080}:${CALLBACK_PORT:-8080}`

### Backward compatibility
- Добавлен регрессионный тест `TestSchedulerSkipsLegacyUserWithoutInvite`
  - RED: scheduler ошибочно пытался `disable` legacy-пользователя без инвайта
  - GREEN: legacy-пользователь без инвайта и без `subscription_price` пропускается
- В `runSubscriptionSchedulerPass()` добавлен guard:
  - если у пользователя нет инвайта и `subscription_price = NULL`, payment-scheduler его не обрабатывает
- Подтверждён существующий fallback без Platega через `TestLoadPlategaConfig`
  - пустые `PLATEGA_*` не ломают запуск и используют дефолты
- Кейс с `NULL subscription_price` не менялся:
  - в `internal/bot/handlers.go` кнопка оплаты по-прежнему скрывается до назначения цены

### Документация
- `README.md`
  - добавлены `CALLBACK_PORT` и `MIN_SUBSCRIPTION_PRICE`
  - задокументирована обратная совместимость без `PLATEGA_*`
  - уточнено скрытие кнопки оплаты при `subscription_price = NULL`
  - обновлена структура модулей (`callback`, `platega`, платежные таблицы)
- `CLAUDE.md`
  - обновлено описание архитектуры платёжных компонентов
  - обновлён блок env-переменных
  - исправлено описание scheduler и legacy-совместимости
- `.env.example`
  - добавлены `CALLBACK_PORT` и `MIN_SUBSCRIPTION_PRICE`

## Проверка

- `GOCACHE=/tmp/go-build go test ./internal/bot -run TestSchedulerSkipsLegacyUserWithoutInvite -count=1`
- `GOCACHE=/tmp/go-build go test ./internal/config -run TestLoadPlategaConfig -count=1`
- Далее обязательная полная проверка:
  - `make fmt`
  - `make tests`
