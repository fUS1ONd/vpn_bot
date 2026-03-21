# Multi Default Squads Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Добавить поддержку назначения всех новых пользователей сразу в несколько internal squads Remnawave через `.env`.

**Architecture:** Вместо одного `REMNAWAVE_DEFAULT_SQUAD_UUID` бот будет читать список UUID из `REMNAWAVE_DEFAULT_SQUAD_UUIDS`, парсить его в `[]string` и передавать массив в `activeInternalSquads` при создании пользователя. Для безопасного перехода сохраним обратную совместимость: если новый env не задан, используем старый одиночный `REMNAWAVE_DEFAULT_SQUAD_UUID`.

**Tech Stack:** Go, Telebot, SQLite, Remnawave HTTP API

---

### Task 1: Подготовить конфиг для списка default squads

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/config/config_test.go`

**Step 1: Написать падающий тест на парсинг списка**

В `internal/config/config_test.go` добавить тесты для `Load()`:
- читает `REMNAWAVE_DEFAULT_SQUAD_UUIDS=uuid-1, uuid-2 ,uuid-3` в слайс из трёх значений;
- игнорирует пустые элементы и пробелы;
- использует fallback на `REMNAWAVE_DEFAULT_SQUAD_UUID`, если новый env не задан.

**Step 2: Запустить тест и убедиться, что он падает**

Run: `GOCACHE=/tmp/go-build go test ./internal/config -run TestLoadRemnawaveSquadUUIDs -count=1`

Ожидание: `FAIL`, потому что в `Config` ещё нет списка и fallback-логики.

**Step 3: Реализовать парсинг**

В `internal/config/config.go`:
- заменить поле `RemnawaveSquadUUID string` на `RemnawaveSquadUUIDs []string`;
- добавить helper для CSV-парсинга списка UUID;
- читать `REMNAWAVE_DEFAULT_SQUAD_UUIDS`;
- если список пустой, использовать legacy `REMNAWAVE_DEFAULT_SQUAD_UUID`.

**Step 4: Перезапустить тест**

Run: `GOCACHE=/tmp/go-build go test ./internal/config -run TestLoadRemnawaveSquadUUIDs -count=1`

Ожидание: `PASS`.

**Step 5: Коммит**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: добавить парсинг списка default squads"
```

---

### Task 2: Передать список squads в Remnawave-клиент и регистрацию

**Files:**
- Modify: `internal/remnawave/client.go`
- Modify: `internal/remnawave/client_test.go`
- Modify: `internal/bot/handlers_test.go`
- Modify: `cmd/bot/main.go`
- Modify: `cmd/migrator/main.go`

**Step 1: Написать падающий тест для клиента**

В `internal/remnawave/client_test.go` добавить тест, который проверяет:
- `NewClient(..., []string{"uuid-1", "uuid-2"})` передаёт оба UUID в `CreateUserRequest.ActiveInternalSquads`;
- пустой список не сериализуется как заполненное поле.

**Step 2: Запустить тест и убедиться, что он падает**

Run: `GOCACHE=/tmp/go-build go test ./internal/remnawave -run TestCreateUserSetsMultipleInternalSquads -count=1`

Ожидание: `FAIL`, потому что клиент всё ещё принимает один `string`.

**Step 3: Изменить клиент**

В `internal/remnawave/client.go`:
- заменить поле `squadUUID string` на `squadUUIDs []string`;
- обновить сигнатуру `NewClient(baseURL, apiToken string, squadUUIDs []string)`;
- в `CreateUser` передавать весь список в `ActiveInternalSquads`.

**Step 4: Обновить вызовы**

В `cmd/bot/main.go` и `cmd/migrator/main.go` передать `cfg.RemnawaveSquadUUIDs`.

**Step 5: Добавить интеграционный тест на signup**

В `internal/bot/handlers_test.go` расширить тест `processInviteCode`, чтобы он проверял, что при наличии списка сквадов бот отправляет оба UUID в `activeInternalSquads`.

**Step 6: Перезапустить целевые тесты**

Run: `GOCACHE=/tmp/go-build go test ./internal/remnawave ./internal/bot -run 'TestCreateUserSetsMultipleInternalSquads|TestProcessInviteCode_UsesInviteExpireDays' -count=1`

Ожидание: `PASS`.

**Step 7: Коммит**

```bash
git add internal/remnawave/client.go internal/remnawave/client_test.go internal/bot/handlers_test.go cmd/bot/main.go cmd/migrator/main.go
git commit -m "feat: передавать несколько default squads в remnawave"
```

---

### Task 3: Обновить конфигурацию и документацию проекта

**Files:**
- Modify: `.env.example`
- Modify: `docker-compose.yml`
- Modify: `README.md`
- Modify: `AGENTS.md`
- Modify: `CLAUDE.md`
- Create: `docs/progress/2026-03-21-multi-default-squads-progress.md`

**Step 1: Обновить `.env.example`**

Заменить одиночный env на:
- `REMNAWAVE_DEFAULT_SQUAD_UUIDS=uuid-1,uuid-2`
- комментарий, что список применяется ко всем новым пользователям;
- при необходимости оставить строку про legacy env как fallback.

**Step 2: Обновить docker-compose и README**

- В `docker-compose.yml` пробросить новый env.
- В `README.md` описать формат списка UUID через запятую.

**Step 3: Обновить внутреннюю документацию**

В `AGENTS.md` и `CLAUDE.md` заменить описание одиночного сквада на список default squads.

**Step 4: Написать progress-файл**

Создать `docs/progress/2026-03-21-multi-default-squads-progress.md` со ссылкой на этот план, списком выполненных задач и результатами проверки.

**Step 5: Финальная проверка**

Run: `make fmt`

Run: `make tests`

Ожидание: обе команды завершаются успешно.

**Step 6: Коммит**

```bash
git add .env.example docker-compose.yml README.md AGENTS.md CLAUDE.md docs/progress/2026-03-21-multi-default-squads-progress.md
git commit -m "docs: зафиксировать прогресс по multiple default squads"
```
