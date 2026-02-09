# Субтитры (Render Integration) — План имплементации

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Добавить кнопку "🎤 Субтитры" для генерации видео с субтитрами через render-микросервис.

**Architecture:** Новый HTTP-клиент `internal/render/client.go` общается с render API (multipart upload + polling). Обработчик в `internal/bot/render_handler.go` управляет флоу: принимает голосовое/кружок, отправляет в render, поллит статус, доставляет результат.

**Tech Stack:** Go, gopkg.in/telebot.v3, net/http (multipart), render REST API

---

### Task 1: Конфигурация — добавить RENDER_URL и RENDER_API_KEY

**Files:**
- Modify: `internal/config/config.go:12-31` (struct Config)
- Modify: `internal/config/config.go:34-68` (func Load)

**Step 1: Добавить поля в struct Config**

В `internal/config/config.go`, после поля `VictoriaMetricsURL` (строка 30), добавить:

```go
// Render-сервис (субтитры)
RenderURL    string // URL render-сервиса (опционально)
RenderAPIKey string // API-ключ для render-сервиса
```

**Step 2: Добавить чтение env в func Load**

В `internal/config/config.go`, внутри инициализации `cfg := &Config{...}`, после строки `VictoriaMetricsURL` добавить:

```go
RenderURL:    os.Getenv("RENDER_URL"),
RenderAPIKey: os.Getenv("RENDER_API_KEY"),
```

**Step 3: Проверить компиляцию**

Run: `cd /home/krivonosov/projects/vpn_bot && go build ./...`
Expected: SUCCESS

**Step 4: Коммит**

```
feat: добавить конфигурацию render-сервиса (RENDER_URL, RENDER_API_KEY)
```

---

### Task 2: Render-клиент — HTTP-клиент к render API

**Files:**
- Create: `internal/render/client.go`

**Step 1: Создать пакет `internal/render/client.go`**

```go
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

// Client — HTTP-клиент для render-сервиса
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// NewClient создаёт новый клиент render API
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http: &http.Client{
			Timeout: 5 * time.Minute, // загрузка файлов может быть долгой
		},
	}
}

// TaskResponse — ответ при создании задачи
type TaskResponse struct {
	TaskID    string `json:"task_id"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// CreateVideoTask создаёт задачу рендеринга видео из аудио + аватарки
func (c *Client) CreateVideoTask(audio io.Reader, avatar io.Reader, username string) (*TaskResponse, error) {
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	// Записываем multipart в горутине, чтобы не буферизировать всё в памяти
	errCh := make(chan error, 1)
	go func() {
		defer pw.Close()
		defer writer.Close()

		if err := writer.WriteField("mode", "video"); err != nil {
			errCh <- err
			return
		}
		if username != "" {
			if err := writer.WriteField("username", username); err != nil {
				errCh <- err
				return
			}
		}

		audioPart, err := writer.CreateFormFile("audio_file", "audio.ogg")
		if err != nil {
			errCh <- err
			return
		}
		if _, err := io.Copy(audioPart, audio); err != nil {
			errCh <- err
			return
		}

		avatarPart, err := writer.CreateFormFile("avatar_file", "avatar.jpg")
		if err != nil {
			errCh <- err
			return
		}
		if _, err := io.Copy(avatarPart, avatar); err != nil {
			errCh <- err
			return
		}

		errCh <- nil
	}()

	req, err := http.NewRequest("POST", c.baseURL+"/api/v1/tasks", pr)
	if err != nil {
		return nil, fmt.Errorf("создание запроса: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("отправка запроса: %w", err)
	}
	defer resp.Body.Close()

	// Проверяем ошибку из горутины записи
	if writeErr := <-errCh; writeErr != nil {
		return nil, fmt.Errorf("запись multipart: %w", writeErr)
	}

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("render API ошибка %d: %s", resp.StatusCode, string(body))
	}

	var task TaskResponse
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		return nil, fmt.Errorf("декодирование ответа: %w", err)
	}

	return &task, nil
}

// CreateCircleTask создаёт задачу рендеринга субтитров на кружок
func (c *Client) CreateCircleTask(video io.Reader, username string) (*TaskResponse, error) {
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	errCh := make(chan error, 1)
	go func() {
		defer pw.Close()
		defer writer.Close()

		if err := writer.WriteField("mode", "circle"); err != nil {
			errCh <- err
			return
		}
		if username != "" {
			if err := writer.WriteField("username", username); err != nil {
				errCh <- err
				return
			}
		}

		videoPart, err := writer.CreateFormFile("video_file", "circle.mp4")
		if err != nil {
			errCh <- err
			return
		}
		if _, err := io.Copy(videoPart, video); err != nil {
			errCh <- err
			return
		}

		errCh <- nil
	}()

	req, err := http.NewRequest("POST", c.baseURL+"/api/v1/tasks", pr)
	if err != nil {
		return nil, fmt.Errorf("создание запроса: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("отправка запроса: %w", err)
	}
	defer resp.Body.Close()

	if writeErr := <-errCh; writeErr != nil {
		return nil, fmt.Errorf("запись multipart: %w", writeErr)
	}

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("render API ошибка %d: %s", resp.StatusCode, string(body))
	}

	var task TaskResponse
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		return nil, fmt.Errorf("декодирование ответа: %w", err)
	}

	return &task, nil
}

// GetTaskStatus получает статус задачи
func (c *Client) GetTaskStatus(taskID string) (*TaskResponse, error) {
	req, err := http.NewRequest("GET", c.baseURL+"/api/v1/tasks/"+taskID, nil)
	if err != nil {
		return nil, fmt.Errorf("создание запроса: %w", err)
	}
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("отправка запроса: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("render API ошибка %d: %s", resp.StatusCode, string(body))
	}

	var task TaskResponse
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		return nil, fmt.Errorf("декодирование ответа: %w", err)
	}

	return &task, nil
}

// DownloadResult скачивает результат задачи (MP4-файл)
func (c *Client) DownloadResult(taskID string) (io.ReadCloser, error) {
	req, err := http.NewRequest("GET", c.baseURL+"/api/v1/tasks/"+taskID+"/result", nil)
	if err != nil {
		return nil, fmt.Errorf("создание запроса: %w", err)
	}
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("отправка запроса: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("render API ошибка %d: %s", resp.StatusCode, string(body))
	}

	return resp.Body, nil
}
```

**Step 2: Проверить компиляцию**

Run: `cd /home/krivonosov/projects/vpn_bot && go build ./...`
Expected: SUCCESS

**Step 3: Коммит**

```
feat: добавить HTTP-клиент render-сервиса (multipart upload + polling)
```

---

### Task 3: Клавиатура — кнопка "Субтитры" и сообщения

**Files:**
- Modify: `internal/bot/keyboards.go:8-15` (константы кнопок)
- Modify: `internal/bot/keyboards.go:42-50` (UserMenuKeyboard)
- Modify: `internal/bot/messages.go` (добавить сообщения)

**Step 1: Добавить константу кнопки**

В `internal/bot/keyboards.go`, после `BtnCancel = "🚫 Отмена"` (строка 15), добавить:

```go
BtnSubtitles = "🎤 Субтитры"
```

**Step 2: Изменить UserMenuKeyboard — сделать динамической**

Сейчас `UserMenuKeyboard()` не принимает параметров. Нужно добавить параметр `renderEnabled bool`, чтобы кнопка показывалась только если render-сервис настроен.

Заменить функцию `UserMenuKeyboard()` на:

```go
// UserMenuKeyboard возвращает главное меню пользователя
func UserMenuKeyboard(renderEnabled bool) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	rows := []tele.Row{
		menu.Row(menu.Text(BtnStatus), menu.Text(BtnConnect)),
		menu.Row(menu.Text(BtnServers), menu.Text(BtnInstructions)),
	}
	if renderEnabled {
		rows = append(rows, menu.Row(menu.Text(BtnSubtitles)))
	}
	rows = append(rows, menu.Row(menu.Text(BtnDonate)))
	menu.Reply(rows...)
	return menu
}
```

**Step 3: Обновить все вызовы UserMenuKeyboard()**

Найти все вызовы `UserMenuKeyboard()` и заменить на `UserMenuKeyboard(b.renderEnabled())`.

Добавить хелпер в `handlers.go`:

```go
// renderEnabled проверяет, настроен ли render-сервис
func (b *Bot) renderEnabled() bool {
	return b.config.RenderURL != ""
}
```

Файлы с вызовами `UserMenuKeyboard()`:
- `internal/bot/handlers.go` — строки 149, 322, 377, 400, 408, 434
- `internal/bot/admin.go` — строка (handleUserMode)

Все `UserMenuKeyboard()` → `UserMenuKeyboard(b.renderEnabled())`

**Step 4: Добавить сообщения в messages.go**

В `internal/bot/messages.go`, в блок `const` пользовательских сообщений, добавить:

```go
MsgSubtitlesWait = `<b>🎤 Субтитры</b>

Отправь голосовое сообщение или видео-кружок, и я добавлю субтитры.`

MsgSubtitlesProcessing = `⏳ Рендеринг видео...`

MsgSubtitlesNoAvatar = `❌ Не удалось получить фото профиля.

Установите фото профиля в Telegram и попробуйте снова.`

MsgSubtitlesError = `❌ Не удалось создать видео. Попробуйте позже.`

MsgSubtitlesTimeout = `⏰ Рендеринг занял слишком долго. Попробуйте позже.`

MsgSubtitlesUnavailable = `❌ Сервис временно недоступен. Попробуйте позже.`

MsgSubtitlesWrongType = `Отправь голосовое сообщение или видео-кружок.
Или нажми 🚫 Отмена.`
```

**Step 5: Проверить компиляцию**

Run: `cd /home/krivonosov/projects/vpn_bot && go build ./...`
Expected: SUCCESS

**Step 6: Коммит**

```
feat: добавить кнопку "Субтитры" в меню и тексты сообщений
```

---

### Task 4: Render-клиент в Bot struct — инициализация

**Files:**
- Modify: `internal/bot/handlers.go:26-35` (struct Bot)
- Modify: `internal/bot/handlers.go:38-88` (func New)
- Modify: `cmd/bot/main.go:42-53` (инициализация)

**Step 1: Добавить render-клиент в struct Bot**

В `internal/bot/handlers.go`, в struct Bot, добавить после `sdConfigsPath`:

```go
render *render.Client // клиент render-сервиса (nil если не настроен)
```

Добавить импорт:
```go
"github.com/fus1ond/vpn_bot/internal/render"
```

**Step 2: Инициализировать render-клиент в func New**

В `internal/bot/handlers.go`, в func New, после `sdConfigsPath: cfg.SDConfigsPath,`:

```go
// Инициализация render-клиента (опционально)
if cfg.RenderURL != "" {
	bot.render = render.NewClient(cfg.RenderURL, cfg.RenderAPIKey)
	slog.Info("Render service enabled", "url", cfg.RenderURL)
}
```

**Step 3: Зарегистрировать обработчики Voice и VideoNote**

В `internal/bot/handlers.go`, в func New, после регистрации `tele.OnDocument` (строка 86):

```go
b.Handle(tele.OnVoice, bot.handleVoiceMessage)
b.Handle(tele.OnVideoNote, bot.handleVideoNoteMessage)
```

**Step 4: Добавить состояние StateWaitRender**

В `internal/bot/handlers.go`, в блок констант состояний, добавить:

```go
StateWaitRender = "wait_render" // Ожидание голосового или кружка для субтитров
```

**Step 5: Проверить компиляцию**

Run: `cd /home/krivonosov/projects/vpn_bot && go build ./...`
Expected: FAIL (handleVoiceMessage и handleVideoNoteMessage ещё не существуют — это нормально, создадим в Task 5)

**Step 6: Коммит**

Коммит будет после Task 5 (зависимость на реализацию обработчиков).

---

### Task 5: Обработчики — render_handler.go

**Files:**
- Create: `internal/bot/render_handler.go`
- Modify: `internal/bot/handlers.go:153-266` (handleTextMessage — добавить StateWaitRender и BtnSubtitles)

**Step 1: Создать `internal/bot/render_handler.go`**

```go
package bot

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	tele "gopkg.in/telebot.v3"
)

// handleSubtitlesButton обрабатывает нажатие кнопки "Субтитры"
func (b *Bot) handleSubtitlesButton(c tele.Context) error {
	telegramID := c.Sender().ID

	// Проверяем, зарегистрирован ли пользователь
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil {
		return c.Send(MsgNotRegistered, &tele.SendOptions{ParseMode: tele.ModeHTML})
	}

	b.userStates[telegramID] = StateWaitRender
	return c.Send(MsgSubtitlesWait, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: CancelKeyboard(),
	})
}

// handleVoiceMessage обрабатывает голосовые сообщения
func (b *Bot) handleVoiceMessage(c tele.Context) error {
	telegramID := c.Sender().ID
	state := b.userStates[telegramID]

	if state != StateWaitRender {
		return nil // Игнорируем голосовые вне состояния ожидания
	}

	if b.render == nil {
		return c.Send(MsgSubtitlesUnavailable, &tele.SendOptions{
			ReplyMarkup: UserMenuKeyboard(b.renderEnabled()),
		})
	}

	delete(b.userStates, telegramID)

	// Отправляем статус-сообщение
	statusMsg, err := b.bot.Send(c.Recipient(), MsgSubtitlesProcessing)
	if err != nil {
		return err
	}

	// Скачиваем голосовое сообщение из Telegram
	voice := c.Message().Voice
	audioFile, err := b.bot.File(&voice.File)
	if err != nil {
		slog.Error("Не удалось скачать голосовое", "error", err, "telegram_id", telegramID)
		b.bot.Edit(statusMsg, MsgSubtitlesError)
		return c.Send(MsgWelcomeBack, &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: UserMenuKeyboard(b.renderEnabled()),
		})
	}
	defer audioFile.Close()

	// Получаем аватарку пользователя
	photos, err := b.bot.ProfilePhotosOf(c.Sender())
	if err != nil || len(photos) == 0 {
		slog.Info("У пользователя нет аватарки", "telegram_id", telegramID)
		b.bot.Edit(statusMsg, MsgSubtitlesNoAvatar)
		// Не сбрасываем состояние — пользователь может попробовать снова
		b.userStates[telegramID] = StateWaitRender
		return nil
	}

	// Скачиваем первую аватарку (самую большую версию)
	photo := photos[0]
	biggestPhoto := photo.File
	avatarFile, err := b.bot.File(&biggestPhoto)
	if err != nil {
		slog.Error("Не удалось скачать аватарку", "error", err, "telegram_id", telegramID)
		b.bot.Edit(statusMsg, MsgSubtitlesNoAvatar)
		b.userStates[telegramID] = StateWaitRender
		return nil
	}
	defer avatarFile.Close()

	// Читаем файлы в буферы
	audioData, err := io.ReadAll(audioFile)
	if err != nil {
		slog.Error("Не удалось прочитать аудио", "error", err)
		b.bot.Edit(statusMsg, MsgSubtitlesError)
		return c.Send(MsgWelcomeBack, &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: UserMenuKeyboard(b.renderEnabled()),
		})
	}

	avatarData, err := io.ReadAll(avatarFile)
	if err != nil {
		slog.Error("Не удалось прочитать аватарку", "error", err)
		b.bot.Edit(statusMsg, MsgSubtitlesError)
		return c.Send(MsgWelcomeBack, &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: UserMenuKeyboard(b.renderEnabled()),
		})
	}

	username := c.Sender().Username
	if username != "" {
		username = "@" + username
	} else if c.Sender().FirstName != "" {
		username = c.Sender().FirstName
	}

	// Запускаем рендеринг в горутине
	go b.processVideoRender(c.Chat().ID, statusMsg.ID, telegramID, audioData, avatarData, username)

	return nil
}

// handleVideoNoteMessage обрабатывает видео-кружки
func (b *Bot) handleVideoNoteMessage(c tele.Context) error {
	telegramID := c.Sender().ID
	state := b.userStates[telegramID]

	if state != StateWaitRender {
		return nil
	}

	if b.render == nil {
		return c.Send(MsgSubtitlesUnavailable, &tele.SendOptions{
			ReplyMarkup: UserMenuKeyboard(b.renderEnabled()),
		})
	}

	delete(b.userStates, telegramID)

	// Отправляем статус-сообщение
	statusMsg, err := b.bot.Send(c.Recipient(), MsgSubtitlesProcessing)
	if err != nil {
		return err
	}

	// Скачиваем кружок из Telegram
	videoNote := c.Message().VideoNote
	videoFile, err := b.bot.File(&videoNote.File)
	if err != nil {
		slog.Error("Не удалось скачать кружок", "error", err, "telegram_id", telegramID)
		b.bot.Edit(statusMsg, MsgSubtitlesError)
		return c.Send(MsgWelcomeBack, &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: UserMenuKeyboard(b.renderEnabled()),
		})
	}
	defer videoFile.Close()

	videoData, err := io.ReadAll(videoFile)
	if err != nil {
		slog.Error("Не удалось прочитать видео", "error", err)
		b.bot.Edit(statusMsg, MsgSubtitlesError)
		return c.Send(MsgWelcomeBack, &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: UserMenuKeyboard(b.renderEnabled()),
		})
	}

	username := c.Sender().Username
	if username != "" {
		username = "@" + username
	} else if c.Sender().FirstName != "" {
		username = c.Sender().FirstName
	}

	// Запускаем рендеринг в горутине
	go b.processCircleRender(c.Chat().ID, statusMsg.ID, telegramID, videoData, username)

	return nil
}

// processVideoRender — горутина рендеринга видео из голосового
func (b *Bot) processVideoRender(chatID int64, statusMsgID int, telegramID int64, audioData, avatarData []byte, username string) {
	chat := &tele.Chat{ID: chatID}
	statusMsg := &tele.Message{ID: statusMsgID, Chat: &tele.Chat{ID: chatID}}

	// Создаём задачу в render
	task, err := b.render.CreateVideoTask(
		bytes.NewReader(audioData),
		bytes.NewReader(avatarData),
		username,
	)
	if err != nil {
		slog.Error("Не удалось создать задачу render", "error", err, "telegram_id", telegramID)
		b.bot.Edit(statusMsg, MsgSubtitlesUnavailable)
		b.bot.Send(chat, MsgWelcomeBack, &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: UserMenuKeyboard(b.renderEnabled()),
		})
		return
	}

	// Поллинг статуса
	resultBody, err := b.pollRenderTask(task.TaskID, statusMsg)
	if err != nil {
		slog.Error("Ошибка поллинга render", "error", err, "telegram_id", telegramID)
		b.bot.Send(chat, MsgWelcomeBack, &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: UserMenuKeyboard(b.renderEnabled()),
		})
		return
	}
	defer resultBody.Close()

	// Сохраняем во временный файл
	tmpFile, err := os.CreateTemp("", "render-*.mp4")
	if err != nil {
		slog.Error("Не удалось создать временный файл", "error", err)
		b.bot.Edit(statusMsg, MsgSubtitlesError)
		return
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	if _, err := io.Copy(tmpFile, resultBody); err != nil {
		slog.Error("Не удалось записать результат", "error", err)
		b.bot.Edit(statusMsg, MsgSubtitlesError)
		return
	}

	// Отправляем видео пользователю
	video := &tele.Video{File: tele.FromDisk(tmpFile.Name())}
	_, err = b.bot.Send(chat, video)
	if err != nil {
		slog.Error("Не удалось отправить видео", "error", err, "telegram_id", telegramID)
		b.bot.Edit(statusMsg, MsgSubtitlesError)
		return
	}

	// Удаляем статус-сообщение
	b.bot.Delete(statusMsg)

	// Возвращаем меню
	b.bot.Send(chat, MsgWelcomeBack, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: UserMenuKeyboard(b.renderEnabled()),
	})
}

// processCircleRender — горутина рендеринга кружка с субтитрами
func (b *Bot) processCircleRender(chatID int64, statusMsgID int, telegramID int64, videoData []byte, username string) {
	chat := &tele.Chat{ID: chatID}
	statusMsg := &tele.Message{ID: statusMsgID, Chat: &tele.Chat{ID: chatID}}

	// Создаём задачу в render
	task, err := b.render.CreateCircleTask(
		bytes.NewReader(videoData),
		username,
	)
	if err != nil {
		slog.Error("Не удалось создать задачу render (circle)", "error", err, "telegram_id", telegramID)
		b.bot.Edit(statusMsg, MsgSubtitlesUnavailable)
		b.bot.Send(chat, MsgWelcomeBack, &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: UserMenuKeyboard(b.renderEnabled()),
		})
		return
	}

	// Поллинг статуса
	resultBody, err := b.pollRenderTask(task.TaskID, statusMsg)
	if err != nil {
		slog.Error("Ошибка поллинга render (circle)", "error", err, "telegram_id", telegramID)
		b.bot.Send(chat, MsgWelcomeBack, &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: UserMenuKeyboard(b.renderEnabled()),
		})
		return
	}
	defer resultBody.Close()

	// Сохраняем во временный файл
	tmpFile, err := os.CreateTemp("", "render-circle-*.mp4")
	if err != nil {
		slog.Error("Не удалось создать временный файл", "error", err)
		b.bot.Edit(statusMsg, MsgSubtitlesError)
		return
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	if _, err := io.Copy(tmpFile, resultBody); err != nil {
		slog.Error("Не удалось записать результат", "error", err)
		b.bot.Edit(statusMsg, MsgSubtitlesError)
		return
	}

	// Отправляем как кружок (VideoNote)
	videoNote := &tele.VideoNote{File: tele.FromDisk(tmpFile.Name())}
	_, err = b.bot.Send(chat, videoNote)
	if err != nil {
		slog.Error("Не удалось отправить кружок", "error", err, "telegram_id", telegramID)
		// Пробуем отправить как обычное видео (fallback)
		video := &tele.Video{File: tele.FromDisk(tmpFile.Name())}
		_, err = b.bot.Send(chat, video)
		if err != nil {
			b.bot.Edit(statusMsg, MsgSubtitlesError)
			return
		}
	}

	// Удаляем статус-сообщение
	b.bot.Delete(statusMsg)

	// Возвращаем меню
	b.bot.Send(chat, MsgWelcomeBack, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: UserMenuKeyboard(b.renderEnabled()),
	})
}

// pollRenderTask поллит статус задачи render с таймаутом 2 минуты
func (b *Bot) pollRenderTask(taskID string, statusMsg *tele.Message) (io.ReadCloser, error) {
	timeout := time.After(2 * time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			b.bot.Edit(statusMsg, MsgSubtitlesTimeout)
			return nil, fmt.Errorf("таймаут ожидания render задачи %s", taskID)

		case <-ticker.C:
			task, err := b.render.GetTaskStatus(taskID)
			if err != nil {
				slog.Warn("Ошибка получения статуса render", "error", err, "task_id", taskID)
				continue // Продолжаем поллить при транзитных ошибках
			}

			switch task.Status {
			case "done":
				result, err := b.render.DownloadResult(taskID)
				if err != nil {
					b.bot.Edit(statusMsg, MsgSubtitlesError)
					return nil, fmt.Errorf("скачивание результата: %w", err)
				}
				return result, nil

			case "error":
				errMsg := MsgSubtitlesError
				if task.Error != "" {
					slog.Error("Render задача завершилась с ошибкой", "task_id", taskID, "error", task.Error)
				}
				b.bot.Edit(statusMsg, errMsg)
				return nil, fmt.Errorf("render задача %s завершилась с ошибкой: %s", taskID, task.Error)

			case "processing":
				// Продолжаем поллить
			}
		}
	}
}
```

**Step 2: Добавить обработку StateWaitRender и BtnSubtitles в handleTextMessage**

В `internal/bot/handlers.go`, в функции `handleTextMessage`:

a) Добавить case для `StateWaitRender` в switch state (после case `StateWaitDeleteInvite`, перед закрывающей `}`):

```go
case StateWaitRender:
	if text == BtnCancel {
		delete(b.userStates, telegramID)
		return c.Send(MsgWelcomeBack, &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: UserMenuKeyboard(b.renderEnabled()),
		})
	}
	// В состоянии ожидания рендера текст не принимаем
	return c.Send(MsgSubtitlesWrongType, &tele.SendOptions{
		ReplyMarkup: CancelKeyboard(),
	})
```

b) Добавить case для `BtnSubtitles` в switch кнопок пользователя (после `BtnInstructions`, перед `BtnBack`):

```go
case BtnSubtitles:
	return b.handleSubtitlesButton(c)
```

**Step 3: Добавить обработку StateWaitRender в handleMediaMessage**

В `internal/bot/handlers.go`, в функции `handleMediaMessage`, добавить case перед закрывающей `}`:

```go
case StateWaitRender:
	// Фото/видео/документы в состоянии ожидания рендера — подсказка
	return c.Send(MsgSubtitlesWrongType, &tele.SendOptions{
		ReplyMarkup: CancelKeyboard(),
	})
```

**Step 4: Проверить компиляцию**

Run: `cd /home/krivonosov/projects/vpn_bot && go build ./...`
Expected: SUCCESS

**Step 5: Коммит**

```
feat: добавить обработчики субтитров (голосовое → видео, кружок → кружок с субтитрами)
```

---

### Task 6: Финальная проверка и vet

**Step 1: Полная сборка**

Run: `cd /home/krivonosov/projects/vpn_bot && go build ./...`
Expected: SUCCESS

**Step 2: go vet**

Run: `cd /home/krivonosov/projects/vpn_bot && go vet ./...`
Expected: SUCCESS (0 issues)

**Step 3: Тесты**

Run: `cd /home/krivonosov/projects/vpn_bot && go test ./...`
Expected: PASS

**Step 4: Финальный коммит (если были правки)**

```
fix: исправления после go vet
```
