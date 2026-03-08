# Прогресс: Рефакторинг UX админ-панели

**План:** [docs/plans/2026-03-08-admin-ux-refactor-design.md](../plans/2026-03-08-admin-ux-refactor-design.md)
**Статус:** выполнено
**Коммиты:**
- `183493f` — plan: рефакторинг UX админ-панели
- `be9755c` — feat: рефакторинг UX админ-панели

## Что сделано

### 1. Перегруппировка кнопок

Модераторы вынесены с уровня "Управление" на верхний уровень главного меню.

**Было:**
```
/start (админ)
├── 📋 Управление
│   ├── 🎟 Создать инвайт
│   ├── 📋 Коды
│   ├── 🚫 Забанить
│   ├── 🗑 Удалить код
│   ├── ♾️ Сменить тариф
│   ├── 👥 Модераторы       ← вложены внутри
│   └── 🔙 В меню админа
├── 📢 Рассылка
└── 👤 Режим пользователя
```

**Стало:**
```
/start (админ)
├── 📋 Управление           ← инвайты + действия с пользователями
│   ├── 🎟 Создать инвайт
│   ├── 📋 Просмотр кодов
│   ├── 🚫 Забанить
│   ├── 🗑 Удалить код
│   ├── ♾️ Сменить тариф
│   └── 🔙 В меню админа
├── 🛡 Модераторы           ← на верхнем уровне
│   ├── ➕ Назначить
│   ├── 📋 Список
│   ├── 📊 Статистика
│   ├── ➖ Снять
│   └── 🔙 В меню админа
├── 📢 Рассылка
└── 👤 Режим пользователя
```

### 2. Единый формат отображения пользователей

Добавлена утилита `formatUserLabel` в `internal/bot/format.go`.

**Формат:** `Имя (deep link) | @username | ID в моно`

**Варианты:**
```
👤 Иван | @ivan | 123456789      — с именем и username
👤 Иван | 123456789              — только имя
👤 Пользователь | 123456789      — без имени и username
```

- **Имя** — всегда deep link `tg://user?id=...` (работает даже при смене username)
- **@username** — только если есть
- **ID** — всегда в `<code>` (копируется по тапу)
- **Разделитель** — ` | ` вместо ` • `

**Обновлено в 5 местах:**

| Файл | Функция | Изменение |
|------|---------|-----------|
| `admin.go` | `formatInviteEntry` | Новый формат пользователя |
| `admin.go` | `handleAdminListModerators` | Новый формат + убрана отдельная строка 🆔 |
| `admin.go` | `handleAdminModStats` | Новый формат модератора |
| `moderator.go` | `handleModeratorViewInvites` | Новый формат пользователя инвайта |
| `moderator.go` | `formatSubscriberLabel` | Переписан через `formatUserLabel` |

### 3. Тесты

Написаны по TDD (RED → GREEN → REFACTOR):

- `TestFormatUserLabel` — 4 сценария для утилиты форматирования
- `TestFormatInviteEntry_UsedInvite` — 2 сценария для карточки инвайта
- `TestFormatSubscriberLabel_NewFormat` — 3 сценария для лейбла подписчика
- `TestAdminListModeratorsFormat` — проверка что ID в одной строке без 🆔
- `TestAdminKeyboardContainsModeratorsOnTopLevel` — Модераторы в главном меню
- `TestAdminManageKeyboardDoesNotContainModerators` — Управление без Модераторов

Обновлён `TestModeratorViewInvites` под новый формат.

## Фиксы после code review (коммит `7bb674b`)

Все 4 находки подтверждены и исправлены по TDD.

### Medium: дублирование ID в списке подписчиков модератора

`handleModSubscribers` дописывал ` • ID: <code>%d</code>` поверх `formatSubscriberLabel`, который уже
содержит ID через `formatUserLabel`. Убраны лишние дописывания в строках 174 и 184 `moderator.go`.

### Medium: дублирование ID в диалоге продления подписки

`handleModExtend` строил `• <code>%d</code> — %s` где `%s` уже содержал ID. Заменено на `• %s`.

### Medium: firstName без HTML-экранирования

`formatUserLabel` вставлял `firstName` в HTML без экранирования — имя `<b>Alex</b>` ломало разметку.
Добавлен `html.EscapeString(name)`. Покрыто тестами `TestFormatUserLabel_HTMLEscaping`.

### Low: success-сообщение смены тарифа без ParseMode HTML

`processSwitchSubscriptionConfirm` отправлял финальное сообщение без `ParseMode: tele.ModeHTML`.
В fallback-сценарии (нет имени/username) `TargetLabel` содержит `<code>ID</code>` — пользователь видел
literal-теги. Добавлен `ParseMode: tele.ModeHTML`.

### Low: тест закрепил старый формат

`moderator_test.go:264` ожидал `"ID: <code>300</code>"` — старый формат. Обновлён на `<code>300</code>`.
Добавлен `TestFormatSubscriberLabel_ContainsIDOnce` — проверяет что ID в label ровно один раз.

## Фиксы второго code review (коммит `1bf933d`)

### Medium: HTML-инъекция в карточке смены тарифа

`formatAdminSwitchTargetLabel` и `formatAdminSwitchCurator` вставляли `FirstName` в HTML-строку без
экранирования. Карточка подтверждения отправляется с `ParseMode: HTML`, поэтому имена вроде
`<b>Alex</b>` или `Tom & Jerry` ломали разметку. Добавлен `html.EscapeString` в обоих местах.
Покрыто тестом `TestFormatAdminSwitchTargetLabel_HTMLEscaping`.

### Low: README не отражал новую структуру меню

`👥 Модераторы` была описана как вложенная кнопка внутри `📋 Управление`. После рефакторинга она
на верхнем уровне. README обновлён: добавлена таблица главного меню, панель "Управление" описана
отдельно без модераторов.

## Итоговый результат

Все тесты зелёные (`make tests`), форматирование чистое (`make fmt`).
