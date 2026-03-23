# Прогресс: Этап 2 — Platega HTTP-клиент

**План:** [docs/plans/2026-03-22-payment-implementation-plan.md](../plans/2026-03-22-payment-implementation-plan.md) (строки 501–806)

**Дата:** 2026-03-23

## Что сделано

### Шаг 1: Конфигурация Platega в `config.go`

Добавлены поля в структуру `Config`:
- `PlategaMerchantID`, `PlategaSecret`, `PlategaCallbackURL` — строки
- `MinSubscriptionPrice` (default: 400), `TrialTrafficLimitGB` (default: 1)
- `PlategaFeeSBP` (default: 11), `PlategaFeeCard` (default: 12), `PlategaFeeCrypto` (default: 5), `PlategaFeeWithdrawal` (default: 2)

Добавлен хелпер `getEnvOrDefaultInt` для чтения int из env с default-значением.

### Шаг 2: `internal/platega/client.go`

Создан HTTP-клиент с:
- `NewClient(merchantID, secret)` — production клиент
- `NewClientWithBaseURL(merchantID, secret, baseURL)` — для тестов с httptest.Server
- `CreatePayment(req)` — POST /transaction/process
- `GetTransactionStatus(id)` — GET /transaction/{id}
- `MerchantID()`, `Secret()` — геттеры для верификации callback
- Константы `PaymentMethodSBP/Card/Crypto`, `StatusPending/Confirmed/Canceled/Chargebacked`
- Хелперы `PaymentMethodName`, `PaymentMethodString`, `PaymentMethodFromString`
- Типы `CreateTransactionRequest`, `CreateTransactionResponse`, `TransactionStatus`, `CallbackPayload`

### Шаг 3: `internal/platega/client_test.go`

Написаны тесты (TDD: сначала тест, потом реализация):
- `TestPaymentMethodConversion` — конвертация int ↔ string для всех 3 способов
- `TestPaymentMethodConversionUnknown` — обработка неизвестных значений
- `TestClientHeaders` — проверка X-MerchantId и X-Secret заголовков через httptest
- `TestCreatePayment` — мок-сервер, проверка метода/пути/тела/ответа
- `TestCreatePaymentError` — обработка 401 ошибки
- `TestGetTransactionStatus` — мок GET запроса и парсинга ответа
- `TestGetTransactionStatusNotFound` — обработка 404
- `TestClientMerchantAndSecretAccessors` — геттеры

## Критерии приёмки

- [x] Platega-клиент компилируется и тесты проходят (`make tests` — ok)
- [x] Конфигурация расширена новыми переменными (все опциональные)
- [x] Бот запускается без PLATEGA_* переменных (клиент не создаётся)
- [x] `make fmt` без ошибок

## Изменённые файлы

- `internal/config/config.go` — поля Platega + хелпер getEnvOrDefaultInt
- `internal/config/config_test.go` — тесты TestLoadPlategaConfig
- `internal/platega/client.go` — создан
- `internal/platega/client_test.go` — создан
