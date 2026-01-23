# Дизайн: Отслеживание промокодов и уведомления админа

**Дата:** 2026-01-23
**Статус:** Утверждён

## Обзор

Расширение функциональности бота для админа:
1. Просмотр всех промокодов с детальной информацией
2. Удаление неиспользованных промокодов
3. Уведомление админа о новых пользователях при активации кода
4. Актуализация данных пользователей (username, first_name) в БД и панели Remnawave

## Требования

### 1. Управление промокодами
- Админ может просматривать список всех промокодов через кнопку в админ-панели
- Отображается: код, статус (использован/нет), кто активировал (с кликабельной ссылкой), дата активации
- Админ может удалять только неиспользованные промокоды

### 2. Уведомления о новых пользователях
При активации промокода админ получает уведомление:
- Дата и время активации в формате `23.01.26 15:30`
- Telegram ID с кликабельной ссылкой на пользователя
- Username (если есть)
- First name (если есть)

### 3. Актуализация данных пользователей
- Логика бота завязана на `telegram_id` (неизменный идентификатор)
- Username и first_name обновляются при каждом взаимодействии
- Username синхронизируется с панелью Remnawave

## Архитектурные решения

### База данных

#### Расширение таблицы `users`
```sql
ALTER TABLE users ADD COLUMN first_name TEXT;
```

**Структура после изменений:**
```sql
CREATE TABLE users (
    telegram_id INTEGER PRIMARY KEY,
    username TEXT,
    first_name TEXT,                    -- новое поле
    remnawave_uuid TEXT UNIQUE NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

#### Расширение таблицы `invites`
```sql
ALTER TABLE invites ADD COLUMN used_at TIMESTAMP;
```

**Структура после изменений:**
```sql
CREATE TABLE invites (
    code TEXT PRIMARY KEY,
    created_by INTEGER NOT NULL,
    used_by INTEGER,
    used_at TIMESTAMP,                  -- новое поле
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

### Типы данных в Go

#### `database/db.go`

**User:**
```go
type User struct {
    TelegramID    int64
    Username      string
    FirstName     string      // новое поле
    RemnawaveUUID string
    CreatedAt     time.Time
}
```

**Invite:**
```go
type Invite struct {
    Code      string
    CreatedBy int64
    UsedBy    *int64
    UsedAt    *time.Time    // новое поле
    CreatedAt time.Time
}
```

### API Remnawave

#### Обновление username пользователя

**Метод:** `PATCH /api/users`

**Схема запроса:**
```go
type UpdateUserRequest struct {
    UUID              string  `json:"uuid"`
    Username          *string `json:"username,omitempty"`          // новое поле
    TrafficLimitBytes *int64  `json:"trafficLimitBytes,omitempty"`
    Status            string  `json:"status,omitempty"`
}
```

**Новый метод в `remnawave/client.go`:**
```go
// UpdateUsername обновляет username пользователя в панели Remnawave
func (c *Client) UpdateUsername(uuid string, username string) error {
    req := UpdateUserRequest{
        UUID:     uuid,
        Username: &username,
    }

    body, err := json.Marshal(req)
    if err != nil {
        return fmt.Errorf("failed to marshal request: %w", err)
    }

    _, err = c.doRequest("PATCH", "/api/users", body)
    return err
}
```

### Механизм Upsert для актуализации данных

**Когда выполняется:**
- При каждом взаимодействии пользователя с ботом (любая команда, сообщение)
- Через middleware или в начале обработки сообщений

**Алгоритм:**
1. Получить актуальные данные из Telegram API (`c.Sender()`)
2. Сравнить с данными в БД
3. Если изменились - обновить в БД
4. Если изменился username - синхронизировать с Remnawave

**Новый метод в `database/users.go`:**
```go
// UpdateUserInfo обновляет username и first_name пользователя (Upsert)
func (db *DB) UpdateUserInfo(telegramID int64, username, firstName string) error {
    _, err := db.conn.Exec(
        `UPDATE users SET username = ?, first_name = ? WHERE telegram_id = ?`,
        username, firstName, telegramID,
    )
    if err != nil {
        return fmt.Errorf("failed to update user info: %w", err)
    }
    return nil
}
```

**Использование в `bot/handlers.go`:**
```go
// Пример в middleware или в начале handleTextMessage
func (b *Bot) syncUserInfo(c tele.Context) error {
    telegramID := c.Sender().ID
    currentUsername := c.Sender().Username
    currentFirstName := c.Sender().FirstName

    // Получаем данные из БД
    user, err := b.db.GetUserByTelegramID(telegramID)
    if err != nil || user == nil {
        return nil // Пользователь не зарегистрирован
    }

    // Проверяем изменения
    usernameChanged := user.Username != currentUsername
    firstNameChanged := user.FirstName != currentFirstName

    if !usernameChanged && !firstNameChanged {
        return nil // Нет изменений
    }

    // Обновляем в БД
    if err := b.db.UpdateUserInfo(telegramID, currentUsername, currentFirstName); err != nil {
        slog.Error("Failed to update user info", "error", err)
    }

    // Синхронизируем username с Remnawave
    if usernameChanged {
        if err := b.remnawave.UpdateUsername(user.RemnawaveUUID, currentUsername); err != nil {
            slog.Error("Failed to sync username to Remnawave", "error", err)
        }
    }

    return nil
}
```

## Функционал просмотра промокодов

### UI/UX

**Кнопка в админ-панели:** `📋 Коды` (добавить в `AdminManageKeyboard`)

**Формат отображения списка:**
```
📋 Список инвайт-кодов

✅ Использован
🔹 Код: abc12345
👤 @krivonosov (123456789)
📅 23.01.26 15:30

⭕ Не использован
🔹 Код: def67890
📅 Создан: 22.01.26 10:15
[Удалить]

⭕ Не использован
🔹 Код: ghi09876
📅 Создан: 21.01.26 18:45
[Удалить]
```

### Реализация

**Новый метод в `database/invites.go`:**
```go
// GetInviteWithUser получает инвайт с информацией о пользователе, который его активировал
type InviteWithUser struct {
    Invite
    UserUsername  string
    UserFirstName string
}

func (db *DB) GetAllInvitesWithUsers() ([]InviteWithUser, error) {
    query := `
        SELECT
            i.code, i.created_by, i.used_by, i.used_at, i.created_at,
            u.username, u.first_name
        FROM invites i
        LEFT JOIN users u ON i.used_by = u.telegram_id
        ORDER BY i.created_at DESC
    `

    rows, err := db.conn.Query(query)
    if err != nil {
        return nil, fmt.Errorf("failed to query invites: %w", err)
    }
    defer rows.Close()

    var invites []InviteWithUser
    for rows.Next() {
        var inv InviteWithUser
        var usedBy, usedAt, username, firstName sql.NullInt64, sql.NullTime, sql.NullString, sql.NullString

        err := rows.Scan(
            &inv.Code, &inv.CreatedBy, &usedBy, &usedAt, &inv.CreatedAt,
            &username, &firstName,
        )
        if err != nil {
            return nil, err
        }

        if usedBy.Valid {
            inv.UsedBy = &usedBy.Int64
        }
        if usedAt.Valid {
            t := time.Time(usedAt.Time)
            inv.UsedAt = &t
        }
        if username.Valid {
            inv.UserUsername = username.String
        }
        if firstName.Valid {
            inv.UserFirstName = firstName.String
        }

        invites = append(invites, inv)
    }

    return invites, rows.Err()
}
```

**Обработчик в `bot/admin.go`:**
```go
// handleViewInvites показывает список всех инвайтов
func (b *Bot) handleViewInvites(c tele.Context) error {
    if !b.isAdmin(c) {
        return nil
    }

    invites, err := b.db.GetAllInvitesWithUsers()
    if err != nil {
        slog.Error("Failed to get invites", "error", err)
        return c.Send("Ошибка получения списка кодов", &tele.SendOptions{
            ReplyMarkup: AdminManageKeyboard(),
        })
    }

    if len(invites) == 0 {
        return c.Send("📋 Инвайт-кодов пока нет", &tele.SendOptions{
            ParseMode:   tele.ModeHTML,
            ReplyMarkup: AdminManageKeyboard(),
        })
    }

    msg := formatInvitesList(invites)
    return c.Send(msg, &tele.SendOptions{
        ParseMode:   tele.ModeHTML,
        ReplyMarkup: AdminManageKeyboard(),
    })
}

// formatInvitesList форматирует список инвайтов для отображения
func formatInvitesList(invites []InviteWithUser) string {
    var msg strings.Builder
    msg.WriteString("<b>📋 Список инвайт-кодов</b>\n\n")

    for _, inv := range invites {
        if inv.UsedBy != nil {
            // Использованный код
            msg.WriteString("✅ <b>Использован</b>\n")
            msg.WriteString(fmt.Sprintf("🔹 Код: <code>%s</code>\n", inv.Code))

            // Ссылка на пользователя
            userLink := fmt.Sprintf("<a href=\"tg://user?id=%d\">%d</a>", *inv.UsedBy, *inv.UsedBy)
            if inv.UserUsername != "" {
                userLink = fmt.Sprintf("@%s (%s)", inv.UserUsername, userLink)
            }
            msg.WriteString(fmt.Sprintf("👤 %s", userLink))

            if inv.UserFirstName != "" {
                msg.WriteString(fmt.Sprintf(" • %s", inv.UserFirstName))
            }
            msg.WriteString("\n")

            // Дата активации
            if inv.UsedAt != nil {
                msg.WriteString(fmt.Sprintf("📅 %s\n", inv.UsedAt.Format("02.01.06 15:04")))
            }
        } else {
            // Неиспользованный код
            msg.WriteString("⭕ <b>Не использован</b>\n")
            msg.WriteString(fmt.Sprintf("🔹 Код: <code>%s</code>\n", inv.Code))
            msg.WriteString(fmt.Sprintf("📅 Создан: %s\n", inv.CreatedAt.Format("02.01.06 15:04")))
        }
        msg.WriteString("\n")
    }

    return msg.String()
}
```

## Удаление промокодов

### Ограничения
- Удалять можно только **неиспользованные** коды
- Использованные коды удалить нельзя (защита от потери истории)

### Реализация

**Новый метод в `database/invites.go`:**
```go
// DeleteUnusedInvite удаляет неиспользованный инвайт
func (db *DB) DeleteUnusedInvite(code string) error {
    result, err := db.conn.Exec(
        `DELETE FROM invites WHERE code = ? AND used_by IS NULL`,
        code,
    )
    if err != nil {
        return fmt.Errorf("failed to delete invite: %w", err)
    }

    rows, err := result.RowsAffected()
    if err != nil {
        return fmt.Errorf("failed to get affected rows: %w", err)
    }
    if rows == 0 {
        return fmt.Errorf("invite not found or already used")
    }

    return nil
}
```

**UI подход:**
Вариант 1 (простой): Админ вводит код вручную после нажатия кнопки "🗑 Удалить код"
Вариант 2 (сложный): Inline-кнопки рядом с каждым неиспользованным кодом

**Реализуем вариант 1 для простоты:**

**Добавить состояние в `bot/admin.go`:**
```go
const (
    StateWaitBanUser       = "wait_ban_user"
    StateWaitDeleteInvite  = "wait_delete_invite"  // новое
)
```

**Обработчик запроса удаления:**
```go
// handleDeleteInviteRequest запрашивает код для удаления
func (b *Bot) handleDeleteInviteRequest(c tele.Context) error {
    if !b.isAdmin(c) {
        return nil
    }

    b.userStates[c.Sender().ID] = StateWaitDeleteInvite
    return c.Send("<b>🗑 Удаление инвайт-кода</b>\n\nВведите код для удаления:", &tele.SendOptions{
        ParseMode:   tele.ModeHTML,
        ReplyMarkup: CancelKeyboard(),
    })
}

// processDeleteInvite обрабатывает удаление инвайта
func (b *Bot) processDeleteInvite(c tele.Context, code string) error {
    delete(b.userStates, c.Sender().ID)

    code = strings.TrimSpace(code)

    err := b.db.DeleteUnusedInvite(code)
    if err != nil {
        if strings.Contains(err.Error(), "not found or already used") {
            return c.Send("❌ Код не найден или уже использован.\nМожно удалить только неиспользованные коды.", &tele.SendOptions{
                ReplyMarkup: AdminManageKeyboard(),
            })
        }
        slog.Error("Failed to delete invite", "error", err)
        return c.Send("Ошибка удаления кода", &tele.SendOptions{
            ReplyMarkup: AdminManageKeyboard(),
        })
    }

    return c.Send(fmt.Sprintf("✅ Код <code>%s</code> удалён", code), &tele.SendOptions{
        ParseMode:   tele.ModeHTML,
        ReplyMarkup: AdminManageKeyboard(),
    })
}
```

**Добавить обработку в `handleTextMessage`:**
```go
case StateWaitDeleteInvite:
    if text == BtnCancel {
        delete(b.userStates, telegramID)
        return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminKeyboard()})
    }
    if b.isAdmin(c) {
        return b.processDeleteInvite(c, text)
    }
```

**Добавить кнопку в `keyboards.go`:**
```go
const (
    // ...
    BtnAdminViewInvites  = "📋 Коды"
    BtnAdminDeleteInvite = "🗑 Удалить код"
)

func AdminManageKeyboard() *tele.ReplyMarkup {
    menu := &tele.ReplyMarkup{ResizeKeyboard: true}
    menu.Reply(
        menu.Row(menu.Text(BtnAdminCreateInvite), menu.Text(BtnAdminViewInvites)),
        menu.Row(menu.Text(BtnAdminAddTraffic), menu.Text(BtnAdminBanUser)),
        menu.Row(menu.Text(BtnAdminDeleteInvite)),
        menu.Row(menu.Text(BtnAdminBack)),
    )
    return menu
}
```

## Уведомление админа о новых пользователях

### Формат уведомления

```
🆕 Новый пользователь

📅 23.01.26 15:30
🆔 123456789 (@krivonosov)
👤 Кирилл
```

или если нет username/first_name:
```
🆕 Новый пользователь

📅 23.01.26 15:30
🆔 123456789
```

### Реализация

**Обновить метод `UseInvite` в `database/invites.go`:**
```go
// UseInvite помечает инвайт как использованный с временем активации
func (db *DB) UseInvite(code string, usedBy int64) error {
    result, err := db.conn.Exec(
        `UPDATE invites SET used_by = ?, used_at = CURRENT_TIMESTAMP WHERE code = ? AND used_by IS NULL`,
        usedBy, code,
    )
    if err != nil {
        return fmt.Errorf("failed to use invite: %w", err)
    }

    rows, err := result.RowsAffected()
    if err != nil {
        return fmt.Errorf("failed to get affected rows: %w", err)
    }
    if rows == 0 {
        return fmt.Errorf("invite not found or already used")
    }

    return nil
}
```

**Добавить в `bot/handlers.go` после успешной активации кода:**
```go
// В функции processInviteCode после UseInvite:

// Помечаем инвайт как использованный
if err := b.db.UseInvite(code, telegramID); err != nil {
    slog.Error("Failed to mark invite as used", "error", err)
}

// Отправляем уведомление админу
go b.notifyAdminNewUser(telegramID, username, c.Sender().FirstName)

// Очищаем состояние и отправляем приветствие пользователю
// ...
```

**Новый метод уведомления:**
```go
// notifyAdminNewUser отправляет админу уведомление о новом пользователе
func (b *Bot) notifyAdminNewUser(telegramID int64, username, firstName string) {
    var msg strings.Builder
    msg.WriteString("🆕 <b>Новый пользователь</b>\n\n")

    // Дата и время
    now := time.Now()
    msg.WriteString(fmt.Sprintf("📅 %s\n", now.Format("02.01.06 15:04")))

    // Telegram ID с кликабельной ссылкой
    userLink := fmt.Sprintf("<a href=\"tg://user?id=%d\">%d</a>", telegramID, telegramID)
    if username != "" {
        msg.WriteString(fmt.Sprintf("🆔 %s (@%s)\n", userLink, username))
    } else {
        msg.WriteString(fmt.Sprintf("🆔 %s\n", userLink))
    }

    // First name (если есть)
    if firstName != "" {
        msg.WriteString(fmt.Sprintf("👤 %s\n", firstName))
    }

    admin := &tele.User{ID: b.config.AdminID}
    _, err := b.bot.Send(admin, msg.String(), &tele.SendOptions{
        ParseMode: tele.ModeHTML,
    })
    if err != nil {
        slog.Error("Failed to notify admin about new user", "error", err)
    }
}
```

## План миграции

### Шаг 1: Обновление БД
1. Добавить миграцию для новых полей (`first_name` в users, `used_at` в invites)
2. Обновить структуры `User` и `Invite` в `database/db.go`

### Шаг 2: Remnawave API
1. Расширить `UpdateUserRequest` полем `Username`
2. Добавить метод `UpdateUsername()` в `remnawave/client.go`

### Шаг 3: Механизм Upsert
1. Добавить `UpdateUserInfo()` в `database/users.go`
2. Добавить `syncUserInfo()` в `bot/handlers.go`
3. Вызывать синхронизацию при каждом взаимодействии

### Шаг 4: Управление кодами
1. Добавить `GetAllInvitesWithUsers()` в `database/invites.go`
2. Добавить `DeleteUnusedInvite()` в `database/invites.go`
3. Реализовать обработчики в `bot/admin.go`
4. Обновить клавиатуры в `bot/keyboards.go`

### Шаг 5: Уведомления
1. Обновить `UseInvite()` для записи `used_at`
2. Добавить `notifyAdminNewUser()` в `bot/handlers.go`
3. Вызывать уведомление после успешной активации кода

## Обратная совместимость

**Существующие данные:**
- Поле `first_name` в таблице `users` будет NULL для старых записей - это нормально
- Поле `used_at` в `invites` будет NULL для кодов, использованных до обновления - это нормально
- При первом взаимодействии механизм Upsert заполнит актуальные данные

**Безопасность:**
- Все изменения обратно совместимы
- Бот продолжит работать даже если Remnawave API недоступен (будут только логи с ошибками)
- Неудачная синхронизация username не блокирует работу пользователя

## Тестирование

### Ручное тестирование
1. Создать инвайт-код
2. Активировать его с другого аккаунта
3. Проверить уведомление админу
4. Проверить список кодов (использованный должен показывать юзера)
5. Попробовать удалить использованный код (должен отказать)
6. Попробовать удалить неиспользованный код (должен удалить)
7. Изменить username в Telegram и отправить команду боту
8. Проверить, что username обновился в БД и в панели Remnawave

### Граничные случаи
- Пользователь без username
- Пользователь без first_name
- Пользователь без username и first_name
- Очень длинные username (проверить отображение)
- Remnawave API недоступен при синхронизации

## Риски и ограничения

1. **Rate limiting Telegram API** - при частых изменениях username может быть много запросов к Remnawave. Решение: кешировать последнее значение и обновлять только при реальном изменении.

2. **Username может совпадать** - несколько пользователей Telegram могут иметь одинаковый username в разное время. Решение: логика завязана на `telegram_id`, который уникален.

3. **Длинные списки кодов** - если кодов много (>50), сообщение может превысить лимит Telegram. Решение: добавить пагинацию в будущем (сейчас показываем все).

4. **Первое имя может содержать эмодзи** - Telegram позволяет эмодзи в first_name. Решение: храним как есть, эмодзи поддерживаются в HTML режиме.

## Будущие улучшения

1. Пагинация для списка кодов (когда их станет много)
2. Фильтрация кодов (только использованные / только неиспользованные)
3. Поиск кодов по пользователю
4. Статистика использования кодов
5. Массовое создание кодов (например, 10 кодов за раз)
6. Экспорт истории кодов в CSV

## Заключение

Данный дизайн обеспечивает:
- ✅ Полный контроль админа над промокодами
- ✅ Своевременное уведомление о новых пользователях
- ✅ Актуальность данных в БД и панели Remnawave
- ✅ Обратную совместимость со старыми данными
- ✅ Устойчивость к ошибкам API
- ✅ Простоту расширения функционала в будущем
