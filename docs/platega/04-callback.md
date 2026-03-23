# Callback (Webhook) об изменении статуса

Platega отправляет POST-запрос на ваш endpoint при изменении статуса транзакции.

## Настройка

URL callback указывается в ЛК: **Настройки -> Callback URLs**.

## Требования к endpoint

- Только HTTPS (HTTP запрещен)
- Только публичные IP / доменные имена
- Корректный SSL-сертификат от доверенного CA
- Self-signed сертификаты НЕ допускаются
- Приватные IP-диапазоны запрещены (10.x, 172.16.x, 192.168.x, 127.x)

## Заголовки входящего запроса

| Заголовок | Описание |
|-----------|----------|
| `X-MerchantId` | UUID мерчанта (для верификации) |
| `X-Secret` | API-ключ (для верификации) |

**Важно:** верифицировать `X-MerchantId` и `X-Secret` из заголовков callback, чтобы убедиться, что запрос пришел от Platega.

## Request Body

```json
{
  "id": "00000000-0000-0000-0000-000000000000",
  "amount": 1000,
  "currency": "RUB",
  "status": "CONFIRMED",
  "paymentMethod": 2,
  "payload": "telegram_id:123456789"
}
```

### Поля

| Поле | Тип | Описание |
|------|-----|----------|
| `id` | UUID | ID транзакции |
| `amount` | float | Сумма |
| `currency` | string | Валюта |
| `status` | string | `CONFIRMED` / `CANCELED` / `CHARGEBACKED` |
| `paymentMethod` | integer | ID метода оплаты |
| `payload` | string | Данные, переданные при создании платежа |

## Статусы

| Статус | Значение |
|--------|----------|
| `CONFIRMED` | Оплата успешна |
| `CANCELED` | Оплата отменена / не прошла |
| `CHARGEBACKED` | Возврат денежных средств |

## Retry-логика

- Таймаут ответа: **60 секунд**
- При отсутствии успешного ответа: до **3 повторных попыток** с интервалом **5 минут**
- Ваш endpoint должен вернуть HTTP 200

## Пример обработки (Go)

```go
func handlePlategaCallback(w http.ResponseWriter, r *http.Request) {
    // Верификация заголовков
    merchantID := r.Header.Get("X-MerchantId")
    secret := r.Header.Get("X-Secret")
    if merchantID != cfg.PlategaMerchantID || secret != cfg.PlategaSecret {
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        return
    }

    var cb CallbackPayload
    json.NewDecoder(r.Body).Decode(&cb)

    switch cb.Status {
    case "CONFIRMED":
        // Активировать подписку
    case "CANCELED":
        // Уведомить пользователя об отмене
    case "CHARGEBACKED":
        // Деактивировать подписку
    }

    w.WriteHeader(http.StatusOK)
}
```
