# CLAUDE.md

Telegram-бот управления VPN на базе [Remnawave](https://remnawave.com)

./docs/platega/README.md содержит информацию по взаимодействию с платежной системой 

Бот одной сборкой поддерживает **обе** версии панели: 2.8.x (пользователь адресуется UUID)
и 3.x (пользователь адресуется числовым `id`). Версия определяется автоматически, см. пункт 22
в «Важных заметках». Документация API:

- `./docs/api-remnawave3.2.3.json` — OpenAPI 3.2.3, актуальный контракт 3.x
- `./docs/api-remnawave2.8.1.json` — OpenAPI 2.8.1, нужен пока поддерживаем 2.8.x

Используй поиск по этим файлам, но не читай их целиком (спека 3.2.3 — 1.2 МБ, контекст забьётся мгновенно).

## Архитектура (v2)

Бот работает как **пульт управления** для Remnawave API. База данных хранит только связь
`Telegram ID <-> пользователь панели` (UUID на 2.8.x, числовой `id` на 3.x, в переходный период — оба).

### Основные компоненты

- **`internal/remnawave/client.go`** — HTTP-клиент Remnawave API
- **`internal/remnawave/version.go`** — тип `APIVersion` (V2/V3/Unknown), автодетект версии панели и потокобезопасный кэш
- **`internal/remnawave/userref.go`** — `UserRef` (UUID + числовой id) и методы клиента по конкретному пользователю
- **`internal/remnawave/errors.go`** — типизированная `APIError`, `ErrUserNotFound`, `APIStatusCode`, `IsAuthError`
- **`internal/remnawave/lookup.go`** — поиск пользователя панели по Telegram ID (массив на 2.8.x, `users/stream` на 3.x) и `ResolveUserByUUID`
- **`internal/bot/userref.go`** — шов `b.userRef(telegramID)`: единственный источник `UserRef` в пакете bot
- **`internal/bot/backfill.go`** — `BackfillRemnawaveIDs`: заполнение `users.remnawave_id` при старте
- **`internal/database/migrate_users.go`** — одноразовая перестройка таблицы `users` (снятие `NOT NULL` с `remnawave_uuid`)
- **`internal/platega/client.go`** — HTTP-клиент Platega
- **`internal/callback/server.go`** — встроенный HTTP-сервер для callback и health-check
- **`internal/database/users.go`** — таблица users (`telegram_id`, `username`, `first_name`, `remnawave_uuid` nullable, `remnawave_id`, `subscription_price`, `invited_by`; `moderator_id` архивное)
- **`internal/database/invites.go`** / **`referrals.go`** — служебные и referral-инвайты, лимиты, отзыв, first-touch и статистика
- **`internal/database/payments.go`** — таблица payments и логика подтверждения/ретраев платежей
- **`internal/database/earnings.go`** — архивное чтение старых `moderator_earnings`; новые начисления не создаются
- **`internal/database/bans.go`** — таблица `banned_users` и проверки перманентных банов
- **`internal/database/notifications.go`** — таблица `notifications_sent` (защита от повторных уведомлений)
- **`internal/database/receipts.go`** — таблица `receipts` (чеки «Моего налога»): ключ `payment_id`, состояния `pending`/`created`/`canceled`/`unknown`/`rejected`, столбление права на пробитие через `ClaimReceipt`
- **`internal/moynalog/client.go`** — HTTP-клиент закрытого API кабинета самозанятого «Мой налог» (логин по ИНН/паролю, `CreateIncome`, `ListIncomes`, `CancelIncome`)
- **`internal/bot/receipt.go`** — пробитие чека после активации подписки, добивание чеков плановым проходом, сверка по метке и оповещения владельцу
- **`internal/bot/handlers.go`** — обработчики сообщений, команд и синхронизация данных пользователей
- **`internal/bot/admin.go`** — админ-панель (инвайты, просмотр кодов, бан, уведомления, статистика, режим обслуживания)
- **`internal/bot/subscription_card.go`** — карточка «Моя подписка» (сборка текста и inline-клавиатуры, перевыпуск ссылки)
- **`internal/bot/payment_handler.go`** — пользовательский flow оплаты и ручная проверка платежей
- **`internal/bot/payment.go`** — callback-активация и retry платежей
- **`internal/bot/scheduler.go`** — scheduler подписок и платежей: каждые 30 минут + первый проход при старте
- **`internal/bot/dashboard.go`** — Session Manager и движок live-дашборда мониторинга
- **`internal/bot/dashboard_render.go`** — визуализация дашборда (прогресс-бары, флаги, метрики)
- **`internal/monitoring/`** — пакет мониторинга (MetricsClient, SyncNodes, LoadIndex, Alerter)
- **`internal/render/client.go`** — HTTP-клиент render-сервиса (субтитры на видео)
- **`internal/bot/render_handler.go`** — обработчики субтитров (голосовое → видео, кружок → кружок)
- **`cmd/migrator/main.go`** — миграция активных пользователей из старой БД

## Переменные окружения

```env
# Telegram
BOT_TOKEN=...
ADMIN_ID=...

# Юридические страницы и контакт поддержки (обязательные, показываются в кнопке «Информация»)
PRIVACY_POLICY_URL=https://example.com/privacy
TERMS_OF_SERVICE_URL=https://example.com/terms
SUPPORT_CONTACT=@your_support_handle  # вставляется как есть, можно HTML (<a href=...>)

# Remnawave
REMNAWAVE_URL=https://panel.example.com
REMNAWAVE_API_TOKEN=...
REMNAWAVE_DEFAULT_SQUAD_UUIDS=  # опционально, UUID сквадов через запятую; новые пользователи попадут во все перечисленные

# База данных
DB_PATH=/app/data/bot.db

# Мониторинг (опционально, включается автоматически если VM доступна)
SD_CONFIGS_PATH=/app/sd_configs
VICTORIA_METRICS_URL=http://victoriametrics:8428

# Платежи Platega (опционально; если не заданы — бот работает как раньше)
PLATEGA_MERCHANT_ID=...
PLATEGA_SECRET=...
PLATEGA_CALLBACK_URL=https://vpn.example.com/platega/callback
CALLBACK_PORT=8080
MIN_SUBSCRIPTION_PRICE=400
DEFAULT_SUBSCRIPTION_PRICE=400
TRIAL_TRAFFIC_LIMIT_GB=1
PLATEGA_FEE_SBP=11
PLATEGA_FEE_CARD=12
PLATEGA_FEE_CRYPTO=5
PLATEGA_FEE_WITHDRAWAL=2

# Чеки «Мой налог» (опционально; включается наличием ИНН и пароля)
MOYNALOG_INN=...
MOYNALOG_PASSWORD=...
MOYNALOG_SERVICE_NAME=Sarvizza - Подписка на месяц  # базовая часть наименования услуги

# Канал сообщества (опционально; включается только когда заданы обе переменные)
COMMUNITY_CHAT_ID=-1001234567890            # числовой ID форум-супергруппы
COMMUNITY_INVITE_LINK=https://t.me/+xxxxx   # постоянная инвайт-ссылка с заявками

# Render-сервис субтитров (опционально, кнопка скрыта если не задан)
RENDER_URL=http://render:8080
RENDER_API_KEY=ключ_render_сервиса
```

## Разработка в докере

### Все команды

```bash
go mod download      # Установить зависимости
make down # Остановить бота
make up # Пересобрать докер с ботом
make tests        # Запустить тесты
make fmt         # Проверить код
make logs            # Показать логи
```

### Разработка и Проверка (ОБЯЗАТЕЛЬНО)

Доступные команды:
`make down` / `make up` - управление докером
`make logs` - логи

**Твои обязательные шаги перед завершением любой задачи:**

1. Написал код -> проверь форматирование: `make fmt`
2. Изменил логику -> запусти тесты: `make tests`
   Никогда не рапортуй о завершении задачи, если `make tests` или `make fmt` выдают ошибки. Исправь их сам.

В этом репозитории разрешается делать коммиты, чтобы фиксировать последовательные изменения чаще

## Правило названий коммитов

Используй формат: `<type>: <краткое описание>`

- `type` только в нижнем регистре: `feat`, `fix`, `refactor`, `chore`, `docs`, `plan`, `init`
- описание на русском, краткое, с глаголом действия
- без точки в конце

### Тип коммита определяет версию релиза

Версия при `/release` вычисляется автоматически из типов коммитов
(conventional commits), поэтому тип надо выбирать осознанно:

| Тип коммита | Влияние на версию |
|---|---|
| `feat:` | minor (`x.Y.z`) |
| `fix:` | patch (`x.y.Z`) |
| `feat!:` / `fix!:` или футер `BREAKING CHANGE:` | major (`X.y.z`) |
| `chore:` / `docs:` / `refactor:` / `plan:` / `init:` | patch |

- **Breaking change** помечается либо `!` после типа (`feat!: ...`), либо
  строкой `BREAKING CHANGE: ...` в теле/футере коммита.
- **Оговорка 0.x:** пока версия ниже `1.0.0`, breaking-коммиты двигают minor,
  а не major (стандартное поведение для нестабильных версий).
- Приоритет bump: breaking → feat → иначе patch. Любой релиз получает номер,
  даже если в диапазоне только `chore`/`docs`.

## Синхронизация CLAUDE.md и AGENTS.md

`CLAUDE.md` и `AGENTS.md` обязаны быть идентичны по содержимому (AGENTS.md читают
агенты и инструменты, не понимающие CLAUDE.md). При правке одного файла
синхронизируй второй: `cp CLAUDE.md AGENTS.md`.

## Важные заметки

1. **Рассылка** отправляется только активным пользователям (status=ACTIVE в Remnawave)
2. **Типы инвайтов:** служебный админский — бессрочный и бесплатный; referral — ссылка на 30 суток, trial на 72 часа и snapshot цены из `DEFAULT_SUBSCRIPTION_PRICE`
3. **Платежи Platega опциональны:** без `PLATEGA_MERCHANT_ID` и `PLATEGA_SECRET` бот работает как раньше, callback-сервер не стартует, кнопки оплаты не показываются
4. **Legacy-пользователи:** при `subscription_price = NULL` кнопка оплаты скрыта; scheduler пропускает старые записи без инвайта и цены
5. **Платёжный flow:** callback обрабатывается быстро, долгие retry не держат HTTP-запрос
   открытым; при сбое активации платёж переходит в `confirmed_not_activated`, а scheduler
   повторяет активацию без перезаписи исходного `confirmed_at`.
   **Переиспользуемый pending закрывать нельзя.** В `createPaymentForProvider`
   (`internal/bot/payment.go`) висящий pending того же провайдера без сохранённого
   `confirmation_url` (прошлый вызов кассы сорвался, не дойдя до записи ссылки)
   переиспользуется вместе с ключом идемпотентности — касса вернёт по нему тот же платёж.
   Раньше эта ветка проваливалась дальше без `else` и помечала свою же запись `expired`,
   а ссылку по ней всё равно выдавала: пользователь платил по локально закрытому платежу,
   подтверждение отвергалось как неактуальное — деньги приняты, услуга не оказана.
   `expired` ставится только платежу **другого** провайдера, который мы действительно бросаем.
   **Воскрешение платежа — только по ответу API провайдера.**
   `handleConfirmedWithNotification` принимает третий параметр `providerVerified`;
   входов три: `handleConfirmed` (тело callback Platega — `false`),
   `handleConfirmedFromProviderState` (вебхук ЮKassa после `GetPayment` и
   `verifyYooKassaPayment` — `true`), `handleConfirmedSilently` (ручная проверка через
   API — `true`). Из статусов `revivablePaymentStatuses` = {`expired`, `canceled`} платёж
   возвращается к жизни, если оплату подтвердил сверенный ответ провайдера: тело вебхука
   не авторитетно и воскрешать по нему нечего, а ответ API, сверенный с неизменной локальной
   записью, — единственный источник, которому можно доверить выдачу подписки. `chargebacked`
   (деньги уже вернули) и `confirmed_activation_failed` (активация признана невозможной,
   разбор ручной) не воскрешаются намеренно. Молчания нет ни в одном исходе: не смогли
   принять — `reportIgnoredPaymentConfirmation`, приняли по ответу провайдера —
   `reportRevivedPayment`, оба дедуплицируются по `payment_id` (`ignoredConfirmationReported`
   / `revivedPaymentReported` в `Bot`, `internal/bot/handlers.go`). Дедупликация обязательна,
   а сообщение — тем более: вебхуку мы отвечаем успехом, повторной доставки, которая
   напомнила бы о проблеме, не будет, и раньше на этом месте оставался только `slog.Warn`,
   который никто не читает.
   **Повторы запросов к кассе** (`internal/yookassa/client.go`): `maxAttempts` = 3,
   `defaultAttemptTimeout` = 15 с на попытку через `context`, `defaultRetryBackoff` = 1 с
   с линейным ростом. Повторяются транспортные ошибки (с «TLS handshake timeout» до
   `api.yookassa.ru` и началась потеря платежа) и 5xx; 4xx (401/400/404) не повторяются —
   ответ будет тем же. Повтор `POST /v3/payments` безопасен из-за ключа идемпотентности:
   касса вернёт тот же платёж, а не создаст второй, `GET` идемпотентен сам. Суммарный бюджет
   повторов намеренно уложен в `WriteTimeout` = 65 с вебхук-сервера
   (`internal/callback/server.go`): серия, вылезшая за него, обернулась бы для кассы обрывом
   вместо ответа. `SetRetryBackoff` существует только для тестов, `doPayment` разбит на
   `sendOnce` + `parsePayment`
6. **Плановый scheduler:** стартует сразу при запуске и далее работает каждые 30 минут; обрабатывает pending/confirmed_not_activated, уведомления, disable и grace kick, а **последним** шагом добивает непробитые чеки «Моего налога». Шаг чеков объявлен через `defer` в начале прохода, но выполняется в конце: поход в ФНС долгий, и уведомления с автокиками ждать его не должны, а `defer` нужен потому, что шаги между ним и концом функции умеют выходить раньше по ошибке Remnawave — от неё чеки не зависят
7. **Maintenance mode:** скрывает оплату и блокирует disable/автокики, но остальная функциональность бота продолжает работать
8. **Бан и автокик различаются:** бан пишет в `banned_users` (перманентно), автокик бан не ставит (пользователь может вернуться по новому инвайту)
9. **Сброс трафика** — счётчик `usedTrafficBytes` автоматически сбрасывается Remnawave 1-го числа при стратегии `MONTH`
10. **Сквады** опциональны — если пользователи не видят серверы, создайте internal squads в панели и добавьте UUID в `REMNAWAVE_DEFAULT_SQUAD_UUIDS`
11. **Трафик** — без лимита для оплаченных и админских пользователей (`trafficLimitBytes=0`); для триала лимит задаётся через `TRIAL_TRAFFIC_LIMIT_GB`
12. **Актуализация данных** — при каждом /start бот обновляет username и first_name в БД и синхронизирует username с Remnawave
13. **Отзыв кодов** — автор или админ может мягко отозвать только активный неиспользованный referral-код; строки и история не удаляются
14. **Субтитры** — опционально, требует запущенный render-сервис. Голосовое → видео с субтитрами, кружок → кружок с субтитрами
15. **Управление устройствами** — inline-кнопка «📱 Мои устройства» в карточке
    «👤 Моя подписка» (`internal/bot/devices.go`). Инвариант: экран устройств
    **редактирует** сообщение карточки, поэтому «🔙 Назад» (`cbSubCard`) возвращает
    карточку на место, а не удаляет сообщение. Подробности — `docs/behavior/devices.md`.
16. **Багрепорт** — кнопка «🛠 Сообщить о проблеме» в главном меню: мультивыбор серверов,
    категория, опциональный комментарий, структурированный репорт админу в личку
    (`internal/bot/bug_report.go`). Инвариант: без таблиц БД — сессия и кулдаун
    (10 минут на пользователя) живут in-memory. Подробности — `docs/behavior/bug-report.md`.
17. **Ручное продление подписки админом** — inline-кнопка «➕ Продлить на месяц» в карточке
    пользователя (админ-панель, «🔍 Инфо о пользователе»), реализация —
    `internal/bot/admin_extend.go`. Инварианты: продление чисто техническое, запись в
    `payments` **не** создаётся, меняется только Remnawave; защита от дабл-клика
    двухуровневая (`getPaymentMutex` + `adminExtendCooldown`), а походы в Telegram Bot API
    делаются уже вне мьютекса. Подробности — `docs/behavior/admin-extend.md`.
18. **Общие приглашения** — referral-ссылки создают только пользователи с действующей
    оплаченной, `legacy_paid_migrated` или бессрочной подпиской; сам раздел доступен всем
    зарегистрированным (`internal/database/referrals.go`). Инварианты: лимиты 3 активных и
    15 созданий за скользящие 24 часа; отзыв мягкий, строки и история не удаляются;
    moderator-поля и `moderator_earnings` — архив, новые выплаты не начисляются.
    Подробности — `docs/behavior/referral-invites.md`.
19. **Страница подписки — единственный источник инструкций.** Раздела «📚 Инструкции» в боте
    нет: приложения под платформу и подключение в один тап живут на subscription page
    Remnawave и настраиваются в панели, без релиза бота. Карточка «👤 Моя подписка»
    самодостаточна (`internal/bot/subscription_card.go`). Инвариант: ссылка и кнопки
    подключения/перевыпуска показываются только по предикату `SubscriptionLinkVisible`, а
    URL-кнопка — только после `isValidSubscriptionURL` (битый URL Telegram отвергает вместе
    со всем сообщением). Подробности — `docs/specs/2026-08-05-subscription-page-flow-design.md`.
20. **Перевыпуск ссылки подписки пользователем** — кнопка «🔄 Перевыпустить ссылку» в
    карточке ведёт на экран подтверждения, далее `applyRevoke`
    (`internal/bot/subscription_card.go`). Инварианты: сначала `DeleteAllUserHwidDevices`,
    только потом `RevokeUserSubscription` (обратный порядок дал бы новую ссылку при старых
    HWID-привязках); кулдаун 30 секунд; состояние подписки перечитывается перед выполнением;
    критическая секция общая с платежами (`getPaymentMutex`). Подробности —
    `docs/specs/2026-08-05-subscription-page-flow-design.md`.
21. **Автопробитие чеков в «Мой налог»** — включается наличием `MOYNALOG_INN` и
    `MOYNALOG_PASSWORD`; клиент — `internal/moynalog/client.go`, пробитие —
    `internal/bot/receipt.go`, состояние — таблица `receipts`. Инварианты: связь
    `receipts.payment_id` с платежом 1:1 плюс столбление права `ClaimReceipt` **до**
    обращения к ФНС — физический барьер против второго чека; пробитие всегда идёт в
    отдельной горутине вне мьютекса платежа и вне ответа вебхуку; `rejected` — терминальное
    состояние; `ClearNotifications` не стирает маркеры, начинающиеся на `receipt`; явные DNS
    в `docker-compose.yml` обязательны, иначе `lknpd.nalog.ru` не резолвится. Подробности и
    обоснования — `docs/specs/2026-08-06-moynalog-receipts-design.md` и
    `docs/adr/0001-own-moynalog-client.md`.
22. **Поддержка Remnawave 2.8.x и 3.x одной сборкой** — версия панели определяется **только**
    автодетектом (`internal/remnawave/version.go`), пользователь панели адресуется `UserRef`
    (UUID на 2.8.x, числовой `id` на 3.x). Инварианты: в пакете bot `UserRef` берётся
    **только** через шов `b.userRef(telegramID)` (`internal/bot/userref.go`) — руками из
    полей `database.User` ссылку не собирать; `remnawave_uuid` nullable, пустой UUID пишется
    как `NULL`, а не пустой строкой; порядок выката (сначала бот на панели 2.8.x с backfill,
    только потом апгрейд панели) обязателен — апгрейд удаляет UUID безвозвратно. Различия
    API, обоснования решений и порядок выката —
    `docs/specs/2026-08-13-remnawave-3x-compat-design.md`.
23. **Канал сообщества.** Форум-супергруппа, включается наличием **обеих** переменных
    `COMMUNITY_CHAT_ID` и `COMMUNITY_INVITE_LINK` (`config.CommunityEnabled()`, паттерн
    Platega/«Моего налога»); половина настройки роняет старт. Вход по заявке: гейт —
    предикат «Платящий» (`internal/bot/paying.go`), он же даёт право создавать
    referral-приглашения. Код: `internal/bot/community.go` (гейт, приписки, кик),
    `internal/database/community.go` (таблица `community_members`). Домен, инварианты и
    обоснования решений — в `docs/specs/2026-08-14-community-channel-design.md` и
    `docs/adr/0002-community-channel-gate.md`.

## Мониторинг нод

### Архитектура

- **VictoriaMetrics** — база метрик (порт 8428)
- **vmagent** — скрейпит Node Exporter на нодах
- **Бот** — генерирует `targets.json`, читает метрики через PromQL

### Конвенция тегов

На нодах в Remnawave задаётся тег `bw:<число>` для указания bandwidth в Mbps.
Пример: `bw:1000` = 1 Gbit. Дефолт: 1000 Mbps.

### Алерты

Бот отправляет админу алерты при:

- Нода OFFLINE (Node Exporter не отвечает)
- Load Index > 80% (перегрузка)

### Установка Node Exporter на ноду

```bash
bash scripts/install-node-exporter.sh <IP_СЕРВЕРА_БОТА>
```

## Agent skills

### Issue tracker

Задачи и спеки живут локальными markdown-файлами в `.scratch/<feature>/`. См. `docs/agents/issue-tracker.md`.

### Triage labels

Пять канонических ролей без переименований (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`). См. `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `CONTEXT.md` и `docs/adr/` в корне репозитория. См. `docs/agents/domain.md`.

## Ссылки (Документация)

Если тебе нужен контекст по конкретной фиче, прочитай соответствующий файл перед написанием кода:

- **План миграции**: `docs/plans/2026-01-17-remnawave-migration-design.md`
- **Совместимость с Remnawave 2.8.x и 3.x**: `docs/specs/2026-08-13-remnawave-3x-compat-design.md` — различия API, обоснования решений и порядок выката
- **Канал сообщества**: `docs/specs/2026-08-14-community-channel-design.md` — гейт заявок по предикату «Платящий», дискавери и обоснования решений
- **Чеки «Мой налог»**: `docs/specs/2026-08-06-moynalog-receipts-design.md` — дизайн и обоснования решений
- **Страница подписки и перевыпуск ссылки**: `docs/specs/2026-08-05-subscription-page-flow-design.md` — карточка подписки, условия видимости ссылки, порядок операций при перевыпуске
- **Свой клиент «Моего налога»**: `docs/adr/0001-own-moynalog-client.md` — почему написан собственный HTTP-клиент, а не взята готовая библиотека
- **Гейт в Канал**: `docs/adr/0002-community-channel-gate.md` — почему заявки, один предикат на два места и отсутствие учёта состава
- **Управление устройствами**: `docs/behavior/devices.md` — вход из карточки, доступные действия, редактирование сообщения карточки
- **Багрепорт**: `docs/behavior/bug-report.md` — flow выбора серверов и категории, состав репорта админу, in-memory сессия и кулдаун
- **Ручное продление админом**: `docs/behavior/admin-extend.md` — расчёт даты, защита от дабл-клика, границы критической секции
- **Общие приглашения**: `docs/behavior/referral-invites.md` — кто может создавать ссылки, лимиты, отзыв и архивные moderator-поля
- **Термины домена**: `CONTEXT.md`
