# Создание ссылки на оплату

**POST** `/transaction/process`

Создает транзакцию и возвращает ссылку для оплаты. ID транзакции генерируется автоматически — не передавать `id` в запросе.

## Request Body

```json
{
  "paymentMethod": 2,
  "paymentDetails": {
    "amount": 500,
    "currency": "RUB"
  },
  "description": "Оплата VPN подписки",
  "return": "https://t.me/your_bot",
  "failedUrl": "https://t.me/your_bot",
  "payload": "telegram_id:123456789"
}
```

### Поля

| Поле | Тип | Обязательное | Описание |
|------|-----|:---:|----------|
| `paymentMethod` | integer | да | ID способа оплаты (2=СБП, 3=ЕРИП, 11=Карта, 12=Международная, 13=Крипта) |
| `paymentDetails.amount` | float | да | Сумма платежа |
| `paymentDetails.currency` | string | да | Валюта (например, `RUB`) |
| `description` | string | да | Назначение платежа |
| `return` | string (URI) | нет | Редирект при успешном платеже |
| `failedUrl` | string (URI) | нет | Редирект при неуспешном платеже |
| `payload` | string | нет | Дополнительная информация (передается обратно в callback) |

## Response (200)

```json
{
  "paymentMethod": "SBPQR",
  "transactionId": "3fa85f64-5717-4562-b3fc-2c463f66afa6",
  "redirect": "https://pay.platega.io?qrsbp",
  "return": "https://t.me/your_bot",
  "paymentDetails": "500 RUB",
  "status": "PENDING",
  "expiresIn": "00:15:00",
  "merchantId": "1a021d91-9b26-4762-b303-5d4aac74e921",
  "usdtRate": 93.45
}
```

### Поля ответа

| Поле | Тип | Описание |
|------|-----|----------|
| `transactionId` | UUID | ID созданной транзакции (сохранять!) |
| `redirect` | URI | Ссылка для оплаты — отправить пользователю |
| `status` | string | Всегда `PENDING` при создании |
| `expiresIn` | string | Время жизни платежа (HH:MM:SS), обычно 15 минут |
| `usdtRate` | float | Текущий курс USDT |

## Ошибки

| Код | Описание |
|-----|----------|
| 400 | Ошибка валидации запроса |
| 401 | Неверный X-MerchantId или X-Secret |
