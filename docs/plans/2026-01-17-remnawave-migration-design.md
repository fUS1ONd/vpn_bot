# Миграция VPN-бота на Remnawave

## Обзор

Переход с 3X-UI (три сервера) на Remnawave (единая панель). Упрощение архитектуры: бот становится "пультом управления" для API Remnawave.

**Ключевые изменения:**
- Один источник правды — Remnawave API
- Закрытый доступ через инвайты
- Автоматический сброс трафика 1-го числа (30 GB/месяц)
- Возможность добавить трафик вручную

---

## Этап 1: Фундамент

### Удаляем

- `internal/threexui` — клиент 3X-UI
- `internal/vless` — генератор VLESS-ссылок
- `internal/subscription` — HTTP-сервер подписок
- Таблицы `promo_codes`, `payments`, `promo_uses`
- Все переменные `SERVER_A_*`, `SERVER_B_*`, `SERVER_C_*` из конфига

### Новая схема БД (`bot.db`)

```sql
-- Связка Telegram <-> Remnawave
CREATE TABLE users (
    telegram_id INTEGER PRIMARY KEY,
    username TEXT,
    remnawave_uuid TEXT UNIQUE NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Система инвайтов
CREATE TABLE invites (
    code TEXT PRIMARY KEY,
    created_by INTEGER NOT NULL,
    used_by INTEGER,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

### Конфигурация (.env)

```env
# Telegram
BOT_TOKEN=...
ADMIN_ID=...

# Remnawave
REMNAWAVE_URL=https://panel.example.com
REMNAWAVE_API_TOKEN=...
REMNAWAVE_DEFAULT_SQUAD_UUID=  # опционально, UUID внутреннего сквада

# База данных
DB_PATH=/app/data/bot.db

# Донат
DONATE_TEXT=Перевод по СБП: +7 999 000-00-00 (Т-Банк), Константин К.
```

### Клиент Remnawave API (`internal/remnawave`)

**Авторизация:** Bearer JWT токен в заголовке `Authorization`.

**Методы:**

| Метод | API Endpoint | Описание |
|-------|--------------|----------|
| `CreateUser(telegramID, username)` | `POST /api/users` | Создать пользователя, вернуть uuid и subscriptionUrl |
| `GetUser(uuid)` | `GET /api/users/{uuid}` | Получить статус и трафик |
| `GetAllUsers()` | `GET /api/users` | Список всех пользователей (для рассылки) |
| `UpdateUserTraffic(uuid, bytes)` | `PATCH /api/users` | Изменить лимит трафика |
| `DeleteUser(uuid)` | `DELETE /api/users/{uuid}` | Удалить пользователя |

**Параметры создания пользователя:**
- `username`: формат `tg_{telegram_id}` или Telegram username
- `telegramId`: для связки в панели
- `trafficLimitBytes`: 32212254720 (30 GB)
- `trafficLimitStrategy`: `MONTH`
- `expireAt`: `2099-01-01T00:00:00Z` (бессрочно)
- `activeInternalSquads`: [UUID] если задан в конфиге

---

## Этап 2: Мигратор

### Предварительный тест

**До миграции обязательно:**
1. Создать одного тестового пользователя через API без сквада
2. Проверить, работает ли подписка (появляются ли серверы)
3. Если не работает — создать internal squad в панели, добавить UUID в `REMNAWAVE_DEFAULT_SQUAD_UUID`

### CLI-утилита (`cmd/migrator`)

```bash
./migrator --dry-run    # показать что будет мигрировано (без изменений)
./migrator --live       # выполнить миграцию
```

### Логика работы

**1. Extract (чтение старой БД):**
- Подключение к `users.db` в режиме read-only
- Выборка: `telegram_id`, `username` (игнорируем статусы, трафик, UUID)

**2. Transform:**
- Генерация нового UUID (Remnawave сам создаст)
- Лимит: 30 GB, стратегия: MONTH
- Note: `Migrated from v1 | @username`

**3. Load:**
- `POST /api/users` — создать в Remnawave
- Записать `telegram_id <-> remnawave_uuid` в `bot.db`

### Обработка ошибок

- **Пользователь уже существует:** залогировать, попытаться найти по telegram_id, обновить локальную БД
- **Сбой сети:** остановить миграцию, при перезапуске пропустить уже мигрированных (проверка по telegram_id в новой БД)

### Отчёт

Лог-файл `migration_YYYY-MM-DD.log`:
```
[OK] telegram_id=123456, uuid=abc-def-...
[OK] telegram_id=789012, uuid=ghi-jkl-...
[SKIP] telegram_id=345678 — already migrated
[ERROR] telegram_id=901234 — API error: ...
```

---

## Этап 3: UI бота

### Точка входа (`main.go`)

1. Загрузка конфига
2. Инициализация `remnawave.Client`
3. Подключение к `bot.db`
4. Запуск встроенного scheduler
5. Запуск Telegram бота

### Система "привратника" (Onboarding)

```
Пользователь пишет /start или любое сообщение
         │
         ▼
   telegram_id в БД?
    /           \
  Да             Нет
   │              │
   ▼              ▼
Главное       "🔒 Введите код
 меню          приглашения"
                  │
                  ▼
            StateWaitInvite
```

### Обработка инвайта

1. Проверить код в таблице `invites` (существует, не использован)
2. Вызов `CreateUser(telegram_id, username)`
3. Сохранить `telegram_id <-> remnawave_uuid` в `users`
4. Обновить `invites`: `used_by = telegram_id`
5. Отправить приветствие + главное меню

### Кнопки пользователя

| Кнопка | Действие |
|--------|----------|
| 👤 Мой статус | `GetUser(uuid)` → "Трафик: 12.5 / 30 GB. Сброс: 1-го числа" |
| 🌐 Подключить | Отдать `subscriptionUrl` из Remnawave |
| 💸 Поддержать | Текст из `DONATE_TEXT` |
| 📚 Инструкции | Без изменений |

### Админ-кнопки

| Кнопка | Действие |
|--------|----------|
| 📋 Управление → Создать инвайт | Генерация 8-символьного кода, сохранение в `invites` |
| 📋 Управление → Добавить трафик | Ввод telegram_id/username + GB, `UpdateUserTraffic()` |
| 📢 Рассылка | `GetAllUsers()`, фильтр `status=ACTIVE`, отправка сообщения |

### Scheduler (сброс лимитов)

- Запуск: goroutine с тикером (проверка каждый день в 00:05)
- Условие: сегодня 1-е число месяца
- Действие: `PATCH /api/users` для всех пользователей с `trafficLimitBytes = 30GB`

---

## Порядок выполнения

1. **Этап 1:** Создать ветку, удалить старый код, реализовать клиент Remnawave и новую схему БД
2. **Тест:** Развернуть Remnawave, проверить API без сквадов
3. **Этап 2:** Реализовать мигратор, протестировать `--dry-run`, выполнить миграцию
4. **Этап 3:** Обновить хендлеры бота, добавить scheduler
5. **Деплой:** Заменить старого бота новым

