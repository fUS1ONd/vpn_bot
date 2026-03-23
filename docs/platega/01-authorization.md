# Авторизация

Все запросы к Platega API требуют два заголовка:

| Заголовок | Значение |
|-----------|----------|
| `X-MerchantId` | UUID мерчанта |
| `X-Secret` | API-ключ |

Данные выдаются менеджером при подключении и доступны в ЛК (Настройки).

## Пример запроса

```http
POST /transaction/process HTTP/1.1
Host: app.platega.io
Content-Type: application/json
X-MerchantId: 1a021d91-9b26-4762-b303-5d4aac74e921
X-Secret: your-api-secret-key

{...}
```

## Для Go-клиента

```go
req.Header.Set("X-MerchantId", cfg.PlategaMerchantID)
req.Header.Set("X-Secret", cfg.PlategaSecret)
req.Header.Set("Content-Type", "application/json")
```

## Переменные окружения (для нашего бота)

```env
PLATEGA_MERCHANT_ID=uuid-мерчанта
PLATEGA_SECRET=api-ключ
```
