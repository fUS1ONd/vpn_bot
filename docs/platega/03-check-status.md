# Проверка статуса оплаты

**GET** `/transaction/{id}`

Возвращает статус и детали транзакции.

## Параметры

| Параметр | Место | Тип | Описание |
|----------|-------|-----|----------|
| `id` | path | UUID | ID транзакции |

## Response (200)

```json
{
  "id": "3fa85f64-5717-4562-b3fc-2c963f66afa6",
  "status": "PENDING",
  "paymentDetails": {
    "amount": 2000,
    "currency": "RUB"
  },
  "merchantName": "Demo Merchant",
  "mechantId": "3fa85f64-5717-4562-b3fc-2c963f66afa6",
  "comission": 0,
  "paymentMethod": "SBPQR",
  "expiresIn": "00:15:00",
  "return": "https://example.com/success",
  "comissionUsdt": 1.64,
  "amountUsdt": 10.90,
  "qr": "base64-qr-data-or-url",
  "payformSuccessUrl": "https://pay.platega.io/success",
  "payload": "telegram_id:123456789",
  "comissionType": 1,
  "externalId": "0000a4f3-0000-0000-b8ac-fcb675a0000a",
  "description": "Оплата VPN подписки"
}
```

### Ключевые поля

| Поле | Описание |
|------|----------|
| `status` | `PENDING` / `CONFIRMED` / `CANCELED` / `CHARGEBACKED` |
| `payload` | Данные, переданные при создании (наш telegram_id) |
| `qr` | QR-код для оплаты (base64 или URL) |
| `expiresIn` | Оставшееся время жизни платежа |

## Ошибки

| Код | Описание |
|-----|----------|
| 404 | Транзакция не найдена |
