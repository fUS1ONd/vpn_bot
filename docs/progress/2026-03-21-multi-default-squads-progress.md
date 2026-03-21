# Прогресс: multiple default squads

План: [2026-03-21-multi-default-squads-plan.md](../plans/2026-03-21-multi-default-squads-plan.md)

Дата выполнения: 2026-03-21

## Статус по задачам

### Task 1 — конфиг для списка default squads
- ✅ В `internal/config/config.go` поле `RemnawaveSquadUUIDs` переведено на `[]string`.
- ✅ Добавлен парсинг `REMNAWAVE_DEFAULT_SQUAD_UUIDS` через запятую с `trim` и пропуском пустых элементов.
- ✅ Добавлен fallback на legacy-переменную `REMNAWAVE_DEFAULT_SQUAD_UUID`, если новый список не задан.
- ✅ Добавлен тест `internal/config/config_test.go` на новый env и fallback.

### Task 2 — передача списка squads в Remnawave и регистрацию
- ✅ `internal/remnawave.Client` теперь хранит список `squadUUIDs []string`.
- ✅ `CreateUser` отправляет весь список в `activeInternalSquads`.
- ✅ `cmd/bot/main.go` и `cmd/migrator/main.go` передают в клиент `cfg.RemnawaveSquadUUIDs`.
- ✅ `cmd/backfill-used-at/main.go` и тестовые вызовы `NewClient(...)` переведены на новую сигнатуру.
- ✅ В `internal/remnawave/client_test.go` добавлен тест `TestCreateUserSetsMultipleInternalSquads`.
- ✅ В `internal/bot/handlers_test.go` проверено, что signup отправляет оба UUID в `activeInternalSquads`.

### Task 3 — конфиг и документация проекта
- ✅ Обновлены `.env.example` и `docker-compose.yml` под `REMNAWAVE_DEFAULT_SQUAD_UUIDS`.
- ✅ Обновлён `README.md`:
  - описан новый env;
  - добавлен пример списка UUID;
  - задокументирован legacy fallback.
- ✅ Обновлены `AGENTS.md` и `CLAUDE.md`:
  - путь к актуальной OpenAPI-документации (`docs/api-remnawave2.6.4.json`);
  - описание нового env для default squads.
- ✅ Добавлен этот progress-файл.

## Коммиты

- `d34f243` — `plan: описать добавление multiple default squads`
- `d6ce83b` — `feat: добавить назначение новых пользователей в несколько сквадов`

## Проверки

- `make fmt` — успешно.
- `make tests` — успешно.
