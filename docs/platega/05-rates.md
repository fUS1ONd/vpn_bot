# Получение курсов валют

**GET** `/rates/payment_method_rate`

Возвращает текущий курс обмена для указанного платежного метода и валют.

## Query-параметры

| Параметр | Тип | Обязательный | Описание |
|----------|-----|:---:|----------|
| `merchantId` | UUID | да | ID мерчанта |
| `paymentMethod` | integer | да | ID метода оплаты |
| `currencyFrom` | string | да | Исходная валюта (напр. `RUB`) |
| `currencyTo` | string | да | Целевая валюта (напр. `USDT`) |

## Пример запроса

```
GET /rates/payment_method_rate?merchantId=xxx&paymentMethod=2&currencyFrom=RUB&currencyTo=USDT
```

## Response (200)

```json
{
  "paymentMethod": 2,
  "currencyFrom": "RUB",
  "currencyTo": "USDT",
  "rate": 0.0105,
  "updatedAt": "2025-08-11T10:15:00Z"
}
```

## Применение

Может использоваться для отображения стоимости подписки в разных валютах.
