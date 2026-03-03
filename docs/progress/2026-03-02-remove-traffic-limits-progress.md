# Прогресс: удаление лимитов трафика

**План:** [docs/plans/2026-03-02-remove-traffic-limits-design.md](../plans/2026-03-02-remove-traffic-limits-design.md)

**Дата выполнения:** 2026-03-03

**Статус:** 🟡 В процессе (выполнен батч задач 1-3)

---

## Выполнено

### Task 1: Убрать константы и лимит при создании пользователя

- ✅ Удалена константа `TrafficLimit30GB`
- ✅ `CreateUser` теперь отправляет `trafficLimitBytes=0`
- ✅ Из `UpdateUserRequest` удалено поле `TrafficLimitBytes`
- ✅ Удалён метод `UpdateUserTraffic`

### Task 2: Удалить scheduler

- ✅ Удалён файл `internal/bot/scheduler.go`
- ✅ Удалён запуск scheduler из `cmd/bot/main.go`

### Task 3: Удалить кнопку «Добавить трафик» и логику

- ✅ Удалены `handleAddTrafficRequest` и `processAddTraffic` из `internal/bot/admin.go`
- ✅ Удалено состояние `StateWaitAddTraffic` из `internal/bot/handlers.go`
- ✅ Удалена маршрутизация `BtnAdminAddTraffic` из `internal/bot/handlers.go`
- ✅ Удалена кнопка `BtnAdminAddTraffic` из `internal/bot/keyboards.go`
- ✅ Удалён текст `MsgEnterAddTraffic` из `internal/bot/messages.go`

---

## TDD-верификация батча

Добавлены RED→GREEN тесты:
- `internal/remnawave/client_test.go`: `TestCreateUserSetsUnlimitedTraffic`
- `internal/bot/keyboards_test.go`: `TestAdminManageKeyboardDoesNotContainAddTrafficButton`

Команды:
- `GOCACHE=/tmp/go-build go test ./internal/remnawave ./internal/bot -run 'TestCreateUserSetsUnlimitedTraffic|TestAdminManageKeyboardDoesNotContainAddTrafficButton' -count=1` ✅
- `GOCACHE=/tmp/go-build go build ./...` ✅

---

## Осталось

- ⏳ Task 4: обновить отображение трафика (только использованный за месяц)
- ⏳ Task 5: финальная верификация (`make fmt`, `make tests`) и обновление документации
