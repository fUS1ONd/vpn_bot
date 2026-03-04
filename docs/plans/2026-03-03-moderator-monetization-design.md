# Монетизация через модераторов — План реализации

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Реализовать модель монетизации, где модераторы приглашают пользователей с месячной подпиской, управляют продлением, а админ видит аналитику по каждому модератору.

**Зависимость:** Выполняется ПОСЛЕ плана `2026-03-02-remove-traffic-limits-design.md` (удаление лимитов трафика). Scheduler удаляется в том плане и пересоздаётся здесь с новой логикой.

**Architecture:**
- Модераторские инвайты создают пользователей с `ExpireAt = now + 30 дней`
- Админские инвайты остаются бессрочными (`ExpireAt = 2099-01-01`)
- Модер может продлевать подписку своим подписчикам через кнопку
- Scheduler ежедневно проверяет истекающие подписки: уведомления + автокик
- Админ видит статистику по каждому модератору в подменю «Модераторы»
- Назначить модератором можно только пользователя с бессрочным (админским) инвайтом

**Tech Stack:** Go, Remnawave API, SQLite

**Бизнес-модель (контекст, не реализуется в боте):**
- Минимальная цена: 200₽/месяц
- Доля модератора: 25% (до 10 подписчиков), 30% (10-25), 35% (25+)
- Бот отслеживает только техническую часть: кто подключён, кто продлил, кто истёк

---

### Task 1: Добавить поле `expire_days` в таблицу invites

**Files:**
- Modify: `internal/database/db.go` (миграция — ALTER TABLE)
- Modify: `internal/database/invites.go` (структура Invite, CreateInvite)

**Step 1: Миграция — добавить колонку `expire_days`**

В `initDB()` добавить миграцию:

```sql
ALTER TABLE invites ADD COLUMN expire_days INTEGER;
```

Значение `NULL` = бессрочный (админский), `30` = месячный (модераторский).

**Step 2: Обновить структуру Invite**

```go
type Invite struct {
    Code       string
    CreatedBy  int64
    UsedBy     *int64
    UsedAt     *time.Time
    ExpireDays *int       // NULL = бессрочный, 30 = месячный
    CreatedAt  time.Time
}
```

**Step 3: Добавить функцию `CreateInviteWithExpiry(createdBy int64, expireDays *int)`**

Или модифицировать существующий `CreateInvite` с параметром `expireDays *int`.

**Step 4: Проверить компиляцию**

Run: `go build ./...`

---

### Task 2: Модераторские инвайты ставят ExpireAt на 30 дней

**Files:**
- Modify: `internal/bot/handlers.go` (processInviteCode)
- Modify: `internal/remnawave/client.go` (CreateUser или новый параметр)

**Step 1: Изменить processInviteCode**

При активации инвайта:
- Прочитать `expire_days` из инвайта
- Если `expire_days IS NULL` → `ExpireAt = "2099-01-01T00:00:00Z"` (как сейчас)
- Если `expire_days = 30` → `ExpireAt = time.Now().AddDate(0, 0, 30)`

**Step 2: Передать ExpireAt в CreateUser**

Сделать `ExpireAt` параметром `CreateUser()` вместо хардкода.

```go
// Было:
func (c *Client) CreateUser(telegramID int64, username string) (*User, error)

// Стало:
func (c *Client) CreateUser(telegramID int64, username string, expireAt time.Time) (*User, error)
```

**Step 3: Обновить вызовы CreateUser**

В `processInviteCode`:
```go
var expireAt time.Time
if invite.ExpireDays != nil {
    expireAt = time.Now().AddDate(0, 0, *invite.ExpireDays)
} else {
    expireAt = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
}
user, err := b.remnawave.CreateUser(telegramID, username, expireAt)
```

**Step 4: В создании инвайтов — модер всегда expire_days=30, админ всегда NULL**

В `handleModCreateInvite`: `CreateInviteWithExpiry(telegramID, intPtr(30))`
В `handleAdminCreateInvite`: `CreateInviteWithExpiry(telegramID, nil)`

**Step 5: Добавить telegram ID в список инвайтов модератора**

В `handleModeratorViewInvites` (moderator.go) — для использованных инвайтов добавить отображение telegram ID (как у админа), чтобы модератор мог скопировать ID для продления подписки:

```go
// Было:
if inv.UserUsername != "" {
    fmt.Fprintf(&msg, "👤 @%s", inv.UserUsername)
} else {
    msg.WriteString("👤 пользователь")
}

// Стало:
if inv.UserUsername != "" {
    fmt.Fprintf(&msg, "👤 @%s", inv.UserUsername)
} else {
    msg.WriteString("👤 пользователь")
}
fmt.Fprintf(&msg, " • ID: <code>%d</code>", *inv.UsedBy)
```

**Step 6: Проверить компиляцию**

---

### Task 3: Кнопка «Мои подписчики» для модератора

**Files:**
- Modify: `internal/bot/keyboards.go` (новая кнопка)
- Modify: `internal/bot/moderator.go` (новый handler)
- Modify: `internal/bot/handlers.go` (маршрутизация)
- Modify: `internal/database/invites.go` (запрос подписчиков модератора)

**Step 1: Добавить кнопку `BtnModSubscribers = "👥 Мои подписчики"`**

Добавить в клавиатуру модератора, в подменю «Приглашения».

**Step 2: SQL-запрос — получить подписчиков модератора**

```go
func (db *DB) GetSubscribersByModerator(moderatorID int64) ([]Subscriber, error)
```

Запрос: использовать `LEFT JOIN users ON invites.used_by = users.telegram_id` (не INNER JOIN), чтобы забаненные/удалённые подписчики тоже попадали в выборку.
WHERE: `created_by = moderatorID AND used_by IS NOT NULL`.

Возвращает: telegram_id (из invites.used_by), username, first_name, remnawave_uuid — последние три могут быть NULL если пользователь удалён из БД.

**Step 3: Handler `handleModSubscribers`**

1. Получить список подписчиков из БД
2. Для каждого — если пользователь есть в users (remnawave_uuid не NULL), запросить статус из Remnawave (ExpireAt, Status)
3. Если пользователь удалён из users (забанен/кикнут) — показать как `❌ Удалён`
4. Сформировать сообщение:

```
👥 Мои подписчики (7)

✅ @petya • Петя • ID: 123456
   до 02.04.26 (осталось 28 дн.)

⏰ @kolya • Коля • ID: 789012
   истёк 01.03.26 (кик через 2 дн.)

❌ ID: 345678 — удалён

───
✅ Активных: 5 │ ⏰ Истекших: 1 │ ❌ Удалённых: 1
```

**Step 4: Маршрутизация в handleTextMessage**

Добавить case `BtnModSubscribers`.

---

### Task 4: Кнопка «Продлить» для модератора

**Files:**
- Modify: `internal/bot/keyboards.go` (новая кнопка)
- Modify: `internal/bot/moderator.go` (handler продления)
- Modify: `internal/bot/handlers.go` (state + маршрутизация)
- Modify: `internal/remnawave/client.go` (функция продления ExpireAt)

**Step 1: Добавить кнопку `BtnModExtend = "⏳ Продлить подписку"`**

В подменю модератора «Приглашения».

**Step 2: Remnawave API — обновить ExpireAt**

Нужна функция для обновления `ExpireAt` пользователя. Использовать существующий `UpdateUser` или добавить новую:

```go
func (c *Client) ExtendUserSubscription(uuid string, days int) error
```

Логика:
- Получить текущего пользователя (`GetUser`)
- Если пользователь удалён из Remnawave (не найден) → отказать: `"❌ Пользователь уже удалён из системы."`
- **Защита от бесконечного продления:** если `ExpireAt > now + 30 дней` → отказать: `"❌ Подписка уже продлена до {дата}. Продлить можно не раньше чем за 30 дней до истечения."`
- Если `ExpireAt` ещё не истёк (и <= now + 30 дней) → `newExpireAt = ExpireAt + 30 дней` (не терять оплаченные дни)
- Если `ExpireAt` уже в прошлом → `newExpireAt = now + 30 дней`
- Если статус пользователя `EXPIRED` или `DISABLED` → сначала включить (`EnableUser`), затем обновить ExpireAt
- PATCH user с новым `ExpireAt`

**Step 3: Handler `handleModExtend`**

1. Модер нажимает «Продлить подписку»
2. Бот показывает список подписчиков с номерами (как в списке инвайтов)
3. Модер вводит telegram_id подписчика
4. Бот показывает: `"Продлить подписку @username на 30 дней? (до {новая_дата}). Отправьте 'да' для подтверждения или 'нет' для отмены."`
5. Модер отвечает текстом → бот продлевает или отменяет

**Step 4: States `StateWaitModExtendID` и `StateWaitModExtendConfirm`**

- `StateWaitModExtendID` — ожидание ввода telegram_id подписчика
- `StateWaitModExtendConfirm` — ожидание текстового подтверждения ("да" / "нет"), данные выбранного подписчика хранятся в state

**Step 5: Проверить, что модер может продлить ТОЛЬКО своих подписчиков**

Проверка: `invite.created_by = moderator_telegram_id AND invite.used_by = target_telegram_id`.

---

### Task 4b: Таблица банов + разделение бана и автокика

**Goal:** Различать забаненных (навсегда) и кикнутых по истечению подписки (могут вернуться с новым инвайтом).

**Files:**
- Modify: `internal/database/db.go` (миграция — CREATE TABLE)
- Create: `internal/database/bans.go` (CRUD для таблицы банов)
- Modify: `internal/bot/admin.go` (processBanUser — записывает в бан)
- Modify: `internal/bot/handlers.go` (handleStart — проверка бана)

**Step 1: Таблица `banned_users`**

```sql
CREATE TABLE IF NOT EXISTS banned_users (
    telegram_id INTEGER PRIMARY KEY,
    banned_by INTEGER NOT NULL,
    reason TEXT,
    banned_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

**Step 2: Функции в `bans.go`**

```go
func (db *DB) BanUser(telegramID, bannedBy int64) error
func (db *DB) IsBanned(telegramID int64) (bool, error)
```

**Step 3: Изменить `processBanUser` (admin.go)**

При бане:
1. Записать в `banned_users` (telegram_id, banned_by=admin_id)
2. Удалить из Remnawave
3. Удалить из `users`
4. Очистить `notifications_sent`
5. **НЕ обнулять** `used_by` в инвайте (история бана сохраняется)

**Step 4: Изменить `handleStart` (handlers.go)**

При /start, ДО проверки `GetUserByTelegramID`:
```go
if banned, _ := b.db.IsBanned(telegramID); banned {
    return c.Send("🚫 Ваш аккаунт заблокирован. Доступ запрещён.")
}
```

**Step 5: Автокик scheduler'ом (отличие от бана)**

При автокике в scheduler (Task 5, шаг 6):
1. **НЕ записывать** в `banned_users`
2. Удалить из Remnawave
3. Удалить из `users`
4. **Обнулить** `used_by` в старом инвайте (`UPDATE invites SET used_by = NULL, used_at = NULL WHERE used_by = ?`) — чтобы `GetInviteByUsedBy` не путался при повторной регистрации
5. Очистить `notifications_sent`

Итог:
- Забаненный нажимает /start → "🚫 Заблокирован"
- Кикнутый нажимает /start → "Введите инвайт-код" (может получить новый инвайт от любого модератора)

**Step 6: Проверить компиляцию**

---

### Task 5: Scheduler — уведомления и автокик

**Files:**
- Create: `internal/bot/scheduler.go` (новый, после удаления старого в плане remove-traffic-limits)
- Create: `internal/database/notifications.go` (таблица защиты от повторных уведомлений)
- Modify: `internal/database/db.go` (миграция новой таблицы)
- Modify: `internal/database/invites.go` (GetInviteByUsedBy — нужен для определения куратора)
- Modify: `internal/bot/bot.go` (запуск scheduler)

**Step 1: Таблица `notifications_sent`**

```sql
CREATE TABLE IF NOT EXISTS notifications_sent (
    telegram_id INTEGER NOT NULL,
    type TEXT NOT NULL,          -- "expire_3d", "expire_today"
    sent_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (telegram_id, type)
);
```

Очистка `notifications_sent`:
- При **продлении подписки** (Task 4, ExtendUserSubscription) — удалять записи пользователя, чтобы уведомления сработали заново в следующем цикле
- При **ручном бане** (processBanUser в admin.go) — удалять записи пользователя, чтобы при возврате с новым инвайтом старые записи не блокировали уведомления
- При **автокике** (scheduler, шаг 6) — удалять записи (очистка)

**Step 2: SQL-запрос `GetInviteByUsedBy`**

```go
func (db *DB) GetInviteByUsedBy(usedBy int64) (*Invite, error)
// SELECT * FROM invites WHERE used_by = ? LIMIT 1
```

Нужен для scheduler — определить, кто куратор подписчика и является ли он ещё модератором.

**Step 3: Новый scheduler**

Запуск: ежедневно в **12:00 MSK** (`Europe/Moscow`). Реализация — при старте вычислить `time.Until(next12PM_MSK)` для первого тика, далее `time.NewTicker(24h)`. Не просто тикер от момента запуска.

```go
msk, _ := time.LoadLocation("Europe/Moscow")
now := time.Now().In(msk)
next := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, msk)
if now.After(next) {
    next = next.AddDate(0, 0, 1)
}
time.Sleep(time.Until(next)) // первый тик
// далее ticker 24h
```

Логика за проход:

```
1. Получить всех пользователей из Remnawave ОДНИМ batch-запросом:
   GET /api/users?size=1000 — возвращает всех с ExpireAt, Status, TelegramID
   (НЕ делать поштучные запросы — это N+1 проблема)

2. Получить всех пользователей из БД бота (для связки telegram_id → invite)

3. Для каждого пользователя из batch-ответа:
   → Пропустить бессрочных (ExpireAt >= 2099)
   → Пропустить тех, кого нет в БД бота (удалены вручную)

3.5. Определить статус куратора:
   → GetInviteByUsedBy(telegramID) — найти инвайт
   → Если invite == nil или invite.ExpireDays == NULL → админский, пропустить
   → Если invite.CreatedBy ещё модератор (isModerator) → curatorActive = true
   → Иначе → curatorActive = false

4. Если ExpireAt - 3 дня == сегодня И уведомление "expire_3d" не отправлено:
   → Если curatorActive:
     "⏳ Ваша подписка заканчивается через 3 дня.
      Обратитесь к вашему куратору для продления."
   → Если !curatorActive:
     "⏳ Ваша подписка заканчивается через 3 дня.
      Ваш куратор больше не обслуживает подписки.
      Подписка не будет продлена."
   → Записать в notifications_sent
   → При ошибке отправки (403 Forbidden — бот заблокирован): логировать, НЕ записывать
     в notifications_sent (чтобы при разблокировке попробовать снова)

5. Если ExpireAt <= сегодня И уведомление "expire_today" не отправлено:
   → Если curatorActive:
     "⚠️ Ваша подписка истекла.
      У вас есть 3 дня, чтобы продлить через куратора,
      иначе доступ будет удалён."
   → Если !curatorActive:
     "⚠️ Ваша подписка истекла.
      Ваш куратор больше не обслуживает подписки.
      Доступ будет удалён через 3 дня."
   → Записать в notifications_sent
   → При ошибке отправки: логировать, НЕ записывать (аналогично п.4)

6. Если ExpireAt + 3 дня < сегодня (АВТОКИК — НЕ бан, см. Task 4b):
   → Проверить что пользователь ещё существует в Remnawave (может быть уже забанен админом)
   → Если существует → удалить из Remnawave
   → Удалить из БД бота
   → Обнулить used_by в старом инвайте (чтобы пользователь мог вернуться с новым инвайтом)
   → НЕ записывать в banned_users (это не бан, это истечение подписки)
   → Попытаться отправить: "❌ Ваш доступ удалён. Вы можете получить новое приглашение для повторного подключения." (ошибку игнорировать)
   → Удалить из notifications_sent (очистка)
```

**Step 4: Запуск scheduler в bot.go**

```go
go b.StartScheduler(ctx)
```

**Step 5: Проверить компиляцию**

---

### Task 6: Статистика модераторов для админа

**Files:**
- Modify: `internal/bot/admin.go` (handler статистики)
- Modify: `internal/bot/keyboards.go` (кнопка в подменю модераторов)
- Modify: `internal/bot/handlers.go` (маршрутизация)

**Step 1: Добавить кнопку `BtnAdminModStats = "📊 Статистика"`**

В подменю «Модераторы» (рядом с добавить/список/удалить).

**Step 2: Handler `handleAdminModStats`**

1. Получить всех модераторов
2. Для каждого — получить список его приглашённых через `GetSubscribersByModerator` (LEFT JOIN — считает и удалённых)
3. Данные из Remnawave брать из batch-запроса (`GET /api/users?size=1000`), а не поштучно — та же оптимизация что в scheduler
4. «Всего приглашено» считать по invites (включая удалённых из users), «Активных/Истекших» — только по тем, кто есть в Remnawave
5. Сформировать сообщение:

```
📊 Статистика модераторов

👤 @moderator1 • Иван
   ✅ Активных: 8
   ⏰ Истекших: 2
   👥 Всего приглашено: 10

👤 @moderator2 • Олег
   ✅ Активных: 3
   ⏰ Истекших: 0
   👥 Всего приглашено: 3

───
Итого: ✅ 11 активных │ ⏰ 2 истекших
```

**Step 3: Маршрутизация**

Добавить case `BtnAdminModStats` в handleTextMessage.

---

### Task 7: Валидация назначения модератора — только бессрочные пользователи

**Files:**
- Modify: `internal/bot/admin.go` (processAddModerator)

**Step 1: Проверка инвайта при назначении модератором**

В `processAddModerator`, после проверки что пользователь существует, добавить:

1. Найти инвайт, по которому пришёл этот пользователь: `SELECT expire_days FROM invites WHERE used_by = ?`
2. Если `expire_days IS NOT NULL` (месячный инвайт) → отказать:
   `"❌ Этот пользователь приглашён по месячному инвайту. Назначить модератором можно только пользователя с бессрочным (админским) приглашением."`
3. Если `expire_days IS NULL` или инвайт не найден (старый пользователь) → разрешить

**Step 2:** Использовать `GetInviteByUsedBy` (уже создан в Task 5, Step 2).

---

### Task 8: Финальная верификация

**Step 1: `make fmt`** — проверить форматирование
**Step 2: `make tests`** — запустить тесты
**Step 3: Обновить CLAUDE.md** — добавить описание:
- `internal/bot/scheduler.go` — уведомления об истечении подписки и автокик
- `internal/database/notifications.go` — защита от повторных уведомлений
- Обновить раздел «Важные заметки» — описать модель подписок

---

## Итого: что создаётся / меняется

| Компонент | Действие | Описание |
|-----------|----------|----------|
| `invites.expire_days` | ALTER TABLE | `NULL` = бессрочный, `30` = месячный |
| `banned_users` | CREATE TABLE | Перманентный бан (отличие от автокика) |
| `notifications_sent` | CREATE TABLE | Защита от повторных уведомлений |
| `CreateUser(expireAt)` | Modify | ExpireAt как параметр |
| `processInviteCode` | Modify | Читает expire_days, ставит ExpireAt |
| `processBanUser` | Modify | Записывает в banned_users + очистка notifications_sent |
| `handleStart` | Modify | Проверка бана при /start |
| Автокик (scheduler) | New | Обнуляет used_by в инвайте (пользователь может вернуться) |
| Модер: создание инвайта | Modify | Всегда expire_days=30 |
| Модер: список инвайтов | Modify | Показывает telegram ID подписчиков |
| Модер: «Мои подписчики» | New | LEFT JOIN — включая удалённых |
| Модер: «Продлить подписку» | New | +30 дней, защита от бесконечного продления, reactivate EXPIRED |
| `scheduler.go` | New | 12:00 MSK, batch API, уведомления с учётом статуса куратора + автокик |
| `notifications.go` | New | БД-слой для notifications_sent |
| `bans.go` | New | БД-слой для banned_users |
| Админ: «Статистика» | New | Batch API, LEFT JOIN, сводка по модераторам |
| Назначение модератора | Modify | Запрет назначения по месячному инвайту |