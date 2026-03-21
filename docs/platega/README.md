# Platega API - Документация для интеграции

Платежная система для приема платежей (СБП, карты, крипта, ЕРИП, международные).

**Базовый URL:** `https://app.platega.io/`

## Содержание

1. [Авторизация](./01-authorization.md) — заголовки X-MerchantId / X-Secret
2. [Создание платежа](./02-create-payment.md) — POST /transaction/process
3. [Проверка статуса](./03-check-status.md) — GET /transaction/{id}
4. [Callback (вебхук)](./04-callback.md) — прием уведомлений об изменении статуса
5. [Курсы валют](./05-rates.md) — GET /rates/payment_method_rate
6. [Конвертации](./06-conversions.md) — GET /transaction/balance-unlock-operations
7. [Схемы данных](./07-schemas.md) — PaymentStatus, PaymentMethodInt, Request/Response
8. [Интеграция с VPN-ботом](./08-bot-integration.md) — план интеграции с нашим ботом

## Способы оплаты

| ID | Название |
|----|----------|
| 2  | СБП (QR-код) |
| 3  | ЕРИП |
| 11 | Карточный эквайринг |
| 12 | Международная оплата |
| 13 | Криптовалюта |

## SDK

Официальные SDK: PHP, Python (скачиваются с сайта). Для Go SDK нет — используем HTTP-клиент напрямую.
