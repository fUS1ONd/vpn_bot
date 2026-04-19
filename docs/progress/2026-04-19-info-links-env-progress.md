# Progress: вынести ссылки и контакт из `MsgInfo` в .env

**Дата:** 2026-04-19
**Ветка:** `feat/info-links-from-env`
**План:** [docs/plans/2026-04-19-info-links-from-env.md](../plans/2026-04-19-info-links-from-env.md)

## Итог

Задача выполнена полностью. URL политики конфиденциальности, URL
пользовательского соглашения и контакт поддержки вынесены из
константы `MsgInfo` в три переменные окружения. Все три обязательны
при старте — без них `config.Load()` возвращает понятную ошибку.

Старые ссылки на telegra.ph удалены из кода. Новые рабочие URL
администратор подставит в `.env` на сервере самостоятельно.

## Ревью плана

| Пункт плана | Статус | Комментарий |
|---|---|---|
| Добавить поля в `Config` и валидацию | ✅ | `PrivacyPolicyURL`, `TermsOfServiceURL`, `SupportContact` + 3 проверки в `Load()` |
| Удалить константу `MsgInfo`, добавить `BuildInfoMessage` | ✅ | Формат сообщения сохранён один-в-один |
| Экранирование: URL — да, контакт — нет | ✅ | `html.EscapeString` для URL; контакт вставляется как есть (как `DonateText`) |
| Заменить вызов в `handleInfo` | ✅ | `internal/bot/handlers.go:692` |
| Обновить тесты `messages_test.go` | ✅ | 2 новых теста: подстановка значений + экранирование URL |
| Обновить `handlers_test.go` (3 места) | ✅ | Плюс пробросил значения в `setupTestBot` |
| Добавить валидацию в `config_test.go` | ✅ | `TestLoadInfoEnvRequired` (3 кейса) + `TestLoadInfoEnvRead` |
| Обновить `.env.example` | ✅ | Блок с подробными комментариями по каждой переменной |
| Обновить `CLAUDE.md` | ✅ | Секция «Переменные окружения» расширена |
| `make fmt` | ✅ | Без замечаний |
| `make tests` | ⚠️ | См. ниже |
| Работа через git-ветку с атомарными коммитами | ✅ | 5 коммитов по шагам плана + первый коммит с планом |

## Коммиты

1. `plan: вынести ссылки и контакт из MsgInfo в .env`
2. `feat: добавить env-переменные для ссылок и контакта в config`
3. `refactor: перевести MsgInfo в функцию BuildInfoMessage(cfg)`
4. `test: обновить тесты под BuildInfoMessage`
5. `docs: описать env-переменные ссылок и контакта в .env.example и CLAUDE.md`
6. `docs: добавить progress-файл по вынесению ссылок в env` *(этот файл)*

## Изменённые файлы

- `internal/config/config.go` — 3 новых поля и 3 проверки в `Load()`
- `internal/config/config_test.go` — расширен `setRequiredEnv`, 2 новых теста
- `internal/bot/messages.go` — `MsgInfo` → `BuildInfoMessage(cfg)`
- `internal/bot/handlers.go:692` — вызов новой функции
- `internal/bot/handlers_test.go` — 3 места + расширение `setupTestBot`
- `internal/bot/messages_test.go` — 2 новых теста (`TestBuildInfoMessage*`)
- `.env.example` — новый блок с 3 переменными и комментариями
- `CLAUDE.md` — строчки в секции «Переменные окружения»

## Верификация

### `make fmt`
```
go vet ./...
go fmt ./...
```
Без ошибок.

### `make tests`
- Все новые тесты зелёные:
  - `TestLoadInfoEnvRequired` (3 sub-case)
  - `TestLoadInfoEnvRead`
  - `TestBuildInfoMessageSubstitutesValuesFromConfig`
  - `TestBuildInfoMessageEscapesURLs`
- Связанные существующие тесты тоже зелёные:
  - `TestHandleInfoSendsHelpMessage`
  - `TestHandleTextMessage_InfoButtonRoutesToHelpMessage`
  - `TestHandleTextMessage_PaymentFlowResetsOnMainMenuButtons`

⚠️ **Падает один тест — `TestAdminChangePriceFlow_PromptsForLegacyPaidMigration`.**
Проверено на чистом `main` — падает так же. Тест использует хардкод-
дату `2026-04-15`, которая стала прошлым (сегодня 2026-04-19), из-за
чего `processAdminChangePriceValue` не считает подписку «уже оплаченной
вручную». К этим изменениям отношения не имеет — это существующий
flaky-тест и требует отдельной починки.

### Ручная проверка при старте
Запуск бота с неполным `.env` падает с понятной ошибкой вида:
```
PRIVACY_POLICY_URL is required
```
(аналогично для `TERMS_OF_SERVICE_URL` и `SUPPORT_CONTACT`).

### Проверка на проде
После pull актуальной версии администратор должен:
1. Добавить в продовый `.env` три новые переменные.
2. Перезапустить контейнер (`make down && make up`).
3. Нажать кнопку «Информация» в боте и убедиться, что ссылки и
   контакт показываются корректно.

Без шага 1 бот упадёт на старте с валидационной ошибкой — это
задумано: не даёт по ошибке запустить бот с отсутствующими юридическими
ссылками.
