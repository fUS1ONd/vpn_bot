# Дизайн: кнопка "Субтитры" — рендеринг видео с субтитрами

**Дата:** 2026-02-10

## Обзор

Добавить кнопку "🎤 Субтитры" в меню пользователя. Пользователь отправляет голосовое сообщение или видео-кружок, бот передаёт файл в render-микросервис, который добавляет субтитры, и возвращает результат:

- **Голосовое** → render (video-режим) → вертикальное видео с эквалайзером, аватаркой и субтитрами
- **Кружок** → render (circle-режим) → кружок с наложенными субтитрами

## Пользовательский флоу

```
1. Пользователь нажимает «🎤 Субтитры»
2. Бот: "Отправь голосовое сообщение или видео-кружок, и я добавлю субтитры."
   Клавиатура: [🚫 Отмена]
   Состояние: StateWaitRender

3a. Голосовое (tele.OnVoice):
    → Бот скачивает voice из Telegram
    → Бот получает аватарку пользователя (GetProfilePhotos)
    → username из c.Sender()
    → POST render API: mode=video, audio_file, avatar_file, username
    → Статус-сообщение: "⏳ Рендеринг видео..."
    → Поллинг GET /api/v1/tasks/{id} каждые 2 сек
    → done → скачать результат → отправить как Video (SendVideo)
    → error → сообщить об ошибке

3b. Кружок (tele.OnVideoNote):
    → Бот скачивает video_note из Telegram
    → username из c.Sender()
    → POST render API: mode=circle, video_file, username
    → Статус-сообщение: "⏳ Рендеринг видео..."
    → Поллинг GET /api/v1/tasks/{id} каждые 2 сек
    → done → скачать результат → отправить как VideoNote (SendVideoNote)
    → error → сообщить об ошибке
```

## Архитектура компонентов

### Новый пакет: `internal/render/client.go`

HTTP-клиент к render-сервису (по аналогии с `internal/remnawave/client.go`).

Методы:
- `CreateVideoTask(audioFile io.Reader, avatarFile io.Reader, username string) (taskID string, err error)` — POST multipart, mode=video
- `CreateCircleTask(videoFile io.Reader, username string) (taskID string, err error)` — POST multipart, mode=circle
- `GetTaskStatus(taskID string) (status string, err error)` — GET статус задачи
- `DownloadResult(taskID string) (io.ReadCloser, err error)` — GET скачать MP4

Конфигурация:
- `RENDER_URL` — базовый URL (например `http://render:8080`)
- `RENDER_API_KEY` — ключ для заголовка `X-API-Key`

### Изменения в `internal/bot/`

**keyboards.go:**
- Константа `BtnSubtitles = "🎤 Субтитры"`
- Добавить в `UserMenuKeyboard()` (кнопка показывается только если `RENDER_URL` задан)

**handlers.go:**
- Состояние `StateWaitRender = "wait_render"`
- Регистрация хендлеров `tele.OnVoice` и `tele.OnVideoNote`

**render_handler.go** (новый файл):
- `handleSubtitlesButton()` — вход в состояние, показ CancelKeyboard
- `handleVoiceMessage()` — обработка голосового (проверка состояния, скачивание, отправка в render)
- `handleVideoNoteMessage()` — обработка кружка
- `processRenderTask()` — горутина: поллинг статуса, скачивание результата, отправка пользователю

### `internal/config/config.go`

Новые поля:
- `RenderURL string` (env: `RENDER_URL`)
- `RenderAPIKey string` (env: `RENDER_API_KEY`)

Функция рендеринга **опциональна**: если `RENDER_URL` не задан, кнопка не отображается.

## Обработка ошибок

| Ситуация | Поведение |
|----------|-----------|
| Нет аватарки у пользователя | "Установите фото профиля в Telegram и попробуйте снова." Состояние сохраняется. |
| Render-сервис недоступен | "Сервис временно недоступен, попробуйте позже." Возврат в главное меню. |
| Render вернул `status: error` | "Не удалось создать видео." Возврат в главное меню. |
| Таймаут поллинга (2 мин) | "Рендеринг занял слишком долго, попробуйте позже." Возврат в главное меню. |
| Текст/фото в StateWaitRender | Напоминание: "Отправь голосовое или кружок. Или нажми 🚫 Отмена." |
| Нажатие 🚫 Отмена | Сброс состояния, возврат в главное меню. |
| /start во время рендеринга | Горутина рендеринга продолжает работу и доставит результат. |

## Переменные окружения (дополнение к .env)

| Переменная | По умолчанию | Описание |
|------------|-------------|----------|
| `RENDER_URL` | *(пусто)* | URL render-сервиса. Если не задан — кнопка скрыта |
| `RENDER_API_KEY` | *(пусто)* | API-ключ для render-сервиса |
