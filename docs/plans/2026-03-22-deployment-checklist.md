# Чеклист развёртывания платёжной системы Platega

**Дата:** 2026-03-22
**Связан с:** [План реализации](./2026-03-22-payment-implementation-plan.md)

---

## 1. Переменные окружения (.env)

### Обязательные (новые)

```env
# Platega API (получить в ЛК Platega)
PLATEGA_MERCHANT_ID=ваш_merchant_id
PLATEGA_SECRET=ваш_secret_key

# Полный URL для callback (HTTPS обязательно!)
PLATEGA_CALLBACK_URL=https://vpn.fus1ond.ru/platega/callback
```

### Опциональные (с дефолтами)

```env
# Порт callback-сервера внутри контейнера (nginx проксирует на него)
CALLBACK_PORT=8080

# Минимальная цена подписки (руб/мес)
MIN_SUBSCRIPTION_PRICE=400

# Лимит трафика триала (ГБ)
TRIAL_TRAFFIC_LIMIT_GB=1

# Комиссии Platega (%). Менять только если Platega изменит тарифы
PLATEGA_FEE_SBP=11
PLATEGA_FEE_CARD=12
PLATEGA_FEE_CRYPTO=5
PLATEGA_FEE_WITHDRAWAL=2
```

### Полный пример .env после обновления

```env
# Telegram
BOT_TOKEN=123456:ABC-DEF
ADMIN_ID=123456789

# Remnawave
REMNAWAVE_URL=https://panel.example.com
REMNAWAVE_API_TOKEN=your-token
REMNAWAVE_DEFAULT_SQUAD_UUIDS=uuid1,uuid2

# База данных
DB_PATH=/app/data/bot.db

# Мониторинг
SD_CONFIGS_PATH=/app/sd_configs
VICTORIA_METRICS_URL=http://victoriametrics:8428

# Platega (НОВОЕ)
PLATEGA_MERCHANT_ID=your-merchant-id
PLATEGA_SECRET=your-secret-key
PLATEGA_CALLBACK_URL=https://vpn.fus1ond.ru/platega/callback
CALLBACK_PORT=8080
MIN_SUBSCRIPTION_PRICE=400
TRIAL_TRAFFIC_LIMIT_GB=1
```

---

## 2. Настройка nginx

### Текущая ситуация на сервере

- **VPS:** `5.53.125.146` (Selectel)
- **nginx** работает как Docker-контейнер `mycvwebsite-nginx-1` (image: `nginx:alpine`)
- **Конфиг nginx:** `/root/MyCVWEBsite/nginx.prod.conf` (монтируется в контейнер как `/etc/nginx/nginx.conf`)
- **Docker-compose nginx:** `/root/MyCVWEBsite/docker-compose.prod.yml`
- **vpn-bot:** `/root/vpn_bot/docker-compose.yml`, контейнер `vpn-bot`
- **Сети:** nginx в `mycvwebsite_pwp-network`, vpn-bot в `vpn_bot_vpn-network` — **разные сети!**
- **Домены:** `fus1ond.ru` (портфолио), `moto-23.ru` (магазин). Для VPN-бота домена пока нет
- **SSL:** Let's Encrypt через certbot (контейнер `mycvwebsite-certbot-1`)

### Что нужно сделать

#### 2.1. Выделить домен/субдомен для callback

Platega требует HTTPS. Варианты:

- **Вариант A (рекомендуется):** Субдомен `vpn.fus1ond.ru` — добавить A-запись в DNS, указывающую на `5.53.125.146`
- **Вариант B:** Отдельный домен
- **Вариант C:** Использовать `fus1ond.ru` с отдельным location — проще, но мешает основному сайту

#### 2.2. Подключить vpn-bot к сети nginx

nginx и vpn-bot в разных Docker-сетях. Нужно подключить vpn-bot к сети nginx, чтобы nginx мог проксировать на него по имени контейнера.

**Способ: добавить external network в docker-compose vpn-bot**

В файле `/root/vpn_bot/docker-compose.yml` добавить:

```yaml
services:
  vpn-bot:
    # ... существующие настройки ...
    ports:
      - "127.0.0.1:8080:8080"  # Fallback доступ с хоста (порт можно поменять через CALLBACK_PORT в .env)
    networks:
      - vpn-network
      - mycvwebsite_pwp-network  # Подключение к сети nginx

# ... существующие сервисы ...

networks:
  vpn-network:
    driver: bridge
  mycvwebsite_pwp-network:
    external: true  # Сеть создана docker-compose из MyCVWEBsite
```

После изменения:
```bash
cd /root/vpn_bot
docker compose down && docker compose up -d
```

Теперь nginx сможет обращаться к `vpn-bot:8080` по имени контейнера.

#### 2.3. Получить SSL-сертификат для субдомена

```bash
# Добавить субдомен в certbot (из директории MyCVWEBsite)
cd /root/MyCVWEBsite
docker compose -f docker-compose.prod.yml exec certbot certbot certonly --webroot \
  --webroot-path=/var/www/certbot \
  -d vpn.fus1ond.ru \
  --agree-tos --no-eff-email
```

Или расширить существующий сертификат:
```bash
docker compose -f docker-compose.prod.yml exec certbot certbot certonly --webroot \
  --webroot-path=/var/www/certbot \
  -d fus1ond.ru -d vpn.fus1ond.ru \
  --agree-tos --no-eff-email --expand
```

#### 2.4. Добавить server block в nginx.prod.conf

В файле `/root/MyCVWEBsite/nginx.prod.conf` добавить **перед** закрывающей `}` блока `http`:

```nginx
    # VPN Bot Platega callback
    server {
        listen 80;
        server_name vpn.fus1ond.ru;
        location /.well-known/acme-challenge/ {
            root /var/www/certbot;
        }
        location / {
            return 301 https://$host$request_uri;
        }
    }

    server {
        listen 443 ssl;
        server_name vpn.fus1ond.ru;
        ssl_certificate /etc/letsencrypt/live/vpn.fus1ond.ru/fullchain.pem;
        ssl_certificate_key /etc/letsencrypt/live/vpn.fus1ond.ru/privkey.pem;
        include /etc/letsencrypt/options-ssl-nginx.conf;
        ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;

        # Platega callback
        location /platega/callback {
            proxy_pass http://vpn-bot:8080;
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
            proxy_set_header X-Forwarded-Proto $scheme;

            # Передаём кастомные заголовки Platega (X-MerchantId, X-Secret)
            proxy_pass_request_headers on;

            # Таймаут (Platega ждёт до 60 сек)
            proxy_read_timeout 60s;
            proxy_send_timeout 60s;
        }

        # Health check
        location /health {
            proxy_pass http://vpn-bot:8080;
        }

        # Всё остальное — 404
        location / {
            return 404;
        }
    }
```

**Примечание:** `proxy_pass http://vpn-bot:8080` работает потому что vpn-bot подключён к сети `mycvwebsite_pwp-network` (шаг 2.2). Если вместо этого используется проброс порта на хост, заменить на `proxy_pass http://host.docker.internal:8080` или `proxy_pass http://172.17.0.1:8080` (IP хоста из Docker).

#### 2.5. Перезагрузить nginx

```bash
cd /root/MyCVWEBsite
docker compose -f docker-compose.prod.yml exec nginx nginx -t
docker compose -f docker-compose.prod.yml exec nginx nginx -s reload
```

### Проверка

```bash
# Health check через HTTPS
curl -s https://vpn.fus1ond.ru/health
# Ответ: OK

# Callback без заголовков — 401
curl -s -o /dev/null -w "%{http_code}" -X POST https://vpn.fus1ond.ru/platega/callback
# Ответ: 401
```

---

## 3. Настройки в ЛК Platega

1. **Зайти в ЛК:** https://app.platega.io
2. **Создать мерчанта** (если ещё нет)
3. **Получить credentials:**
   - `Merchant ID` → в `PLATEGA_MERCHANT_ID`
   - `Secret Key` → в `PLATEGA_SECRET`
4. **Callback URL в ЛК оставить пустым** — URL передаётся при создании каждого платежа через параметр `callbackUrl` в API-запросе
5. **Проверить доступные методы оплаты:**
   - СБП (method 2) — должен быть включён
   - Карточный эквайринг (method 11) — должен быть включён
   - Крипто (method 13) — опционально

---

## 4. Порядок действий при первом деплое

### Подготовка (ДО деплоя)

```bash
# 1. Бэкап базы данных
cd /root/vpn_bot
cp data/bot.db data/bot.db.backup-$(date +%Y%m%d)

# 2. Добавить DNS-запись vpn.fus1ond.ru → 5.53.125.146
#    (в панели управления DNS провайдера)

# 2.1. Проверить DNS-пропагацию (certbot упадёт если DNS ещё не готов)
dig +short vpn.fus1ond.ru
# Ожидаемый ответ: 5.53.125.146
# Если пусто — подождать (обычно 5-30 минут, иногда до 48 часов)

# 3. Бэкап nginx-конфига
cp /root/MyCVWEBsite/nginx.prod.conf /root/MyCVWEBsite/nginx.prod.conf.backup

# 4. Обновить .env файл vpn-bot (добавить PLATEGA_* переменные)
nano /root/vpn_bot/.env

# 5. Обновить docker-compose.yml vpn-bot (добавить сеть и порт, см. раздел 2.2)
nano /root/vpn_bot/docker-compose.yml

# 6. Получить SSL-сертификат для vpn.fus1ond.ru (см. раздел 2.3)
cd /root/MyCVWEBsite
docker compose -f docker-compose.prod.yml exec certbot certbot certonly --webroot \
  --webroot-path=/var/www/certbot -d vpn.fus1ond.ru --agree-tos --no-eff-email

# 7. Добавить server block для vpn.fus1ond.ru в nginx (см. раздел 2.4)
nano /root/MyCVWEBsite/nginx.prod.conf

# 8. Проверить и перезагрузить nginx
docker compose -f docker-compose.prod.yml exec nginx nginx -t
docker compose -f docker-compose.prod.yml exec nginx nginx -s reload
```

### Деплой

```bash
# 8. Перезапустить vpn-bot (с новым кодом, сетью и портом)
cd /root/vpn_bot
docker compose down && docker compose up -d

# 9. Проверить логи
docker compose logs -f vpn-bot
```

### Проверка (ПОСЛЕ деплоя)

```bash
# 10. Проверить health endpoint (с хоста)
curl -s http://127.0.0.1:8080/health
# Ожидаемый ответ: OK

# 11. Проверить через nginx (HTTPS)
curl -s https://vpn.fus1ond.ru/health
# Ожидаемый ответ: OK

# 12. Проверить что callback endpoint доступен
curl -s -o /dev/null -w "%{http_code}" -X POST https://vpn.fus1ond.ru/platega/callback
# Ожидаемый ответ: 401 (нет заголовков — это правильно)

# 13. Проверить логи на ошибки
docker compose logs vpn-bot | grep -i "callback\|platega"
# Ожидаем: "Callback server starting", "Platega client initialized"
```

---

## 5. Миграция существующих пользователей

### Автоматическая миграция

При первом запуске нового кода:
- Таблицы `payments` и `moderator_earnings` создаются автоматически
- Поля `subscription_price` и `moderator_id` добавляются в `users` (NULL для всех)
- Поле `subscription_price` добавляется в `invites` (NULL для существующих)

### Ручная настройка (админ через бот)

Существующие пользователи с `subscription_price = NULL`:
- Кнопка "Оплатить" **не показывается** (бот продолжает работать как раньше)
- Подписки работают по старой модели (модератор продлевает вручную)

**Для перевода на новую модель:**
1. Админ заходит в бот → "Управление" → "Сменить тариф" → "Изменить цену"
2. Вводит telegram_id пользователя
3. Устанавливает цену подписки
4. После установки цены кнопка "Оплатить" появляется у пользователя

**Важно:** переводить пользователей можно постепенно, в своём темпе. Старая модель продолжает работать параллельно.

### Модераторы

Модераторам при назначении выдаётся бессрочная подписка (как раньше). Новые инвайты модераторов уже будут с ценой.

---

## 6. Smoke test — проверка что всё работает

### Тест 1: Health check

```bash
curl https://vpn.fus1ond.ru/health
# Ответ: OK
```

### Тест 2: Callback верификация

```bash
# Без заголовков — должен быть 401
curl -s -o /dev/null -w "%{http_code}" -X POST https://vpn.fus1ond.ru/platega/callback
# Ответ: 401

# С неверными заголовками — должен быть 401
curl -s -o /dev/null -w "%{http_code}" -X POST \
  -H "X-MerchantId: wrong" \
  -H "X-Secret: wrong" \
  https://vpn.fus1ond.ru/platega/callback
# Ответ: 401
```

### Тест 3: Бот работает

1. Написать `/start` боту → должен показать меню
2. Нажать "Мой статус" → должен показать статус

### Тест 4: Оплата (рекомендуется тестовый платёж)

1. Создать тестовый инвайт (с ценой) через модератора
2. Активировать инвайт тестовым аккаунтом → триал 3 дня
3. Нажать "Оплатить подписку" → выбрать СБП → получить ссылку
4. Оплатить → подписка активирована на месяц

### Тест 5: Scheduler

```bash
# Проверить в логах что scheduler запустился
docker compose logs vpn-bot | grep -i scheduler
# Ожидаем: "Scheduler: running initial pass on startup"
```

---

## 7. Откат при проблемах

### Быстрый откат (< 5 минут)

```bash
# 1. Остановить бота
make down

# 2. Восстановить бэкап БД (если миграция повредила данные)
cp data/bot.db.backup-YYYYMMDD data/bot.db

# 3. Откатить код на предыдущую версию
git checkout main  # или предыдущий тег/коммит

# 4. Запустить старую версию
make up

# 5. Проверить
make logs
```

### Частичный откат (отключить только оплату)

Если бот работает, но оплата глючит:

1. Включить **режим обслуживания** через админ-панель → кнопка "🔧 Режим обслуживания"
2. Это скрывает кнопку оплаты и останавливает кики
3. Всё остальное работает

Альтернативно, удалить PLATEGA_* из .env и перезапустить:

```bash
# Убрать PLATEGA_MERCHANT_ID и PLATEGA_SECRET из .env
make down && make up
```

Бот запустится без Platega-клиента и callback-сервера — как раньше.

### Откат nginx

```bash
# Удалить server block для vpn.fus1ond.ru из конфига
nano /root/MyCVWEBsite/nginx.prod.conf
cd /root/MyCVWEBsite
docker compose -f docker-compose.prod.yml exec nginx nginx -t
docker compose -f docker-compose.prod.yml exec nginx nginx -s reload
```

---

## 8. Важные заметки

1. **БД не откатывается автоматически.** Новые таблицы и колонки останутся после отката кода — это безопасно, SQLite игнорирует неиспользуемые колонки
2. **PENDING платежи протухают сами** — Platega отменяет их через ~15 минут
3. **Callback может прийти после отката** — nginx вернёт 502 (бот не слушает порт), Platega сделает retry до 3 раз. Если платёж прошёл, но callback не дошёл — пользователь может нажать "Проверить оплату" после восстановления
4. **Логи** — все платёжные события логируются (callback received, confirmed, errors). Для диагностики: `docker compose logs vpn-bot | grep -i "callback\|payment\|platega"`
5. **Порт 8080** — должен быть открыт только для localhost (127.0.0.1). Внешний доступ только через nginx (HTTPS)
