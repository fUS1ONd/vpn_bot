# Быстрый старт VPN Bot

## 1️⃣ Первоначальная настройка

```bash
# Клонировать проект (если еще не сделано)
cd vpn_bot

# Создать .env из шаблона
cp .env.example .env

# Отредактировать .env и заполнить все параметры
nano .env  # или vi, или любой другой редактор
```

## 2️⃣ Запуск

```bash
# Простой способ
make up

# Или через docker compose
docker compose up -d
```

## 3️⃣ Проверка

```bash
# Просмотр логов
make logs

# Проверка статуса
make status
```

## 4️⃣ Основные команды

### Управление ботом
```bash
make up          # Запустить
make down        # Остановить
make restart     # Перезапустить
make logs        # Логи
```

### Работа с базой данных
```bash
make db-ui       # Открыть веб-интерфейс БД (http://localhost:8080)
make backup      # Создать бэкап
```

### Обслуживание
```bash
make rebuild     # Пересобрать после изменений в коде
make shell       # Войти в контейнер
```

## 5️⃣ Использование бота

В Telegram отправьте боту:
- `/start` - проверить работу
- `/create username` - создать VPN клиента

Бот вернет ссылку для подписки вида:
```
http://YOUR_IP:8000/sub/UUID
```

Эту ссылку нужно добавить в VPN клиент (v2rayNG, Shadowrocket и т.д.)

## ⚠️ Важно

1. Порт 8000 должен быть открыт в firewall
2. В .env заполните ВСЕ параметры
3. Бэкапы сохраняются в `backups/`
4. База данных хранится в Docker volume `vpn_data`

## 🔧 Troubleshooting

### Бот не запускается
```bash
# Проверить логи
make logs

# Проверить .env
cat .env
```

### База данных не сохраняется
```bash
# Проверить volume
make volume-info

# Сделать бэкап
make backup
```

### Нужно сбросить всё
```bash
# ОСТОРОЖНО: удалит все данные!
make clean
make up
```

## 📋 Чек-лист развертывания

- [ ] Создан файл .env
- [ ] Заполнены BOT_TOKEN и ADMIN_ID
- [ ] Настроены параметры SERVER_A
- [ ] Настроены параметры SERVER_B
- [ ] Открыт порт 8000 в firewall
- [ ] Запущен бот: `make up`
- [ ] Проверены логи: `make logs`
- [ ] Протестирована команда /start в боте
- [ ] Создан тестовый клиент: /create test

## 🎯 Полезные ссылки

- SQLite Web UI: http://localhost:8080 (после `make db-ui`)
- Логи: `make logs`
- Помощь: `make help`
