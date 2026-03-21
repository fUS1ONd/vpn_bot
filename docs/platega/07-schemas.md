# Схемы данных

## PaymentStatus

```
PENDING       — платеж создан, ожидает оплаты
CONFIRMED     — оплата успешна
CANCELED      — оплата отменена
CHARGEBACKED  — возврат средств
```

## PaymentMethodInt

| ID | Название |
|----|----------|
| 2  | СБП (QR-код) |
| 3  | ЕРИП |
| 11 | Карточный эквайринг |
| 12 | Международная оплата |
| 13 | Криптовалюта |

## CreateTransactionRequest

```json
{
  "paymentMethod": 2,          // PaymentMethodInt, обязательное
  "paymentDetails": {
    "amount": 500.0,           // float, обязательное
    "currency": "RUB"          // string, обязательное
  },
  "description": "...",        // string, обязательное
  "return": "https://...",     // URI, опционально
  "failedUrl": "https://...", // URI, опционально
  "payload": "..."             // string, опционально
}
```

## CreateTransactionResponse

```json
{
  "paymentMethod": "SBPQR",           // string
  "transactionId": "uuid",            // UUID, обязательное
  "redirect": "https://pay...",       // URI — ссылка для оплаты
  "return": "https://...",            // URI
  "paymentDetails": "500 RUB",        // string или объект {amount, currency}
  "status": "PENDING",                // PaymentStatus, обязательное
  "expiresIn": "00:15:00",            // string HH:MM:SS
  "merchantId": "uuid",               // UUID
  "usdtRate": 93.45                   // float
}
```

## TransactionStatusResponse

```json
{
  "id": "uuid",
  "status": "PENDING",
  "paymentDetails": {"amount": 2000, "currency": "RUB"},
  "merchantName": "...",
  "mechantId": "uuid",
  "comission": 0,
  "paymentMethod": "SBPQR",
  "expiresIn": "00:15:00",
  "return": "https://...",
  "comissionUsdt": 1.64,
  "amountUsdt": 10.90,
  "qr": "base64-or-url",
  "payformSuccessUrl": "https://...",
  "payload": "...",
  "comissionType": 1,
  "externalId": "uuid",
  "description": "..."
}
```

## CallbackPayload

```json
{
  "id": "uuid",            // UUID транзакции
  "amount": 1000,          // float
  "currency": "RUB",       // string
  "status": "CONFIRMED",   // "CONFIRMED" | "CANCELED" | "CHARGEBACKED"
  "paymentMethod": 2,      // integer
  "payload": "..."         // string — наши данные
}
```
