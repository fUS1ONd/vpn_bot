package bot

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	tele "gopkg.in/telebot.v3"
)

// renderCancels хранит функции отмены активных рендер-задач по chatID:msgID
type renderCancels struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func newRenderCancels() *renderCancels {
	return &renderCancels{
		cancels: make(map[string]context.CancelFunc),
	}
}

func (rc *renderCancels) set(key string, cancel context.CancelFunc) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.cancels[key] = cancel
}

func (rc *renderCancels) cancel(key string) bool {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if fn, ok := rc.cancels[key]; ok {
		fn()
		delete(rc.cancels, key)
		return true
	}
	return false
}

func (rc *renderCancels) remove(key string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	delete(rc.cancels, key)
}

// renderCancelKeyboard возвращает inline-клавиатуру с кнопкой отмены
func renderCancelKeyboard(callbackData string) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	btn := menu.Data("❌ Отменить", "render_cancel", callbackData)
	menu.Inline(menu.Row(btn))
	return menu
}

// handleVoiceMessage обрабатывает голосовые сообщения — сразу отправляет на рендер
func (b *Bot) handleVoiceMessage(c tele.Context) error {
	telegramID := c.Sender().ID

	// Проверяем, что render включён
	if b.render == nil {
		return nil
	}

	// Проверяем регистрацию
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil {
		return nil
	}

	// Отправляем статус-сообщение с inline-кнопкой отмены
	cancelKey := fmt.Sprintf("%d:%d", c.Chat().ID, time.Now().UnixNano())
	statusMsg, err := b.bot.Send(c.Recipient(), MsgSubtitlesProcessing, renderCancelKeyboard(cancelKey))
	if err != nil {
		return err
	}

	// Скачиваем голосовое сообщение из Telegram
	voice := c.Message().Voice
	audioFile, err := b.bot.File(&voice.File)
	if err != nil {
		slog.Error("Не удалось скачать голосовое", "error", err, "telegram_id", telegramID)
		b.bot.Edit(statusMsg, MsgSubtitlesError)
		return nil
	}
	defer audioFile.Close()

	// Получаем аватарку пользователя
	photos, err := b.bot.ProfilePhotosOf(c.Sender())
	if err != nil || len(photos) == 0 {
		slog.Info("У пользователя нет аватарки", "telegram_id", telegramID)
		b.bot.Edit(statusMsg, MsgSubtitlesNoAvatar)
		return nil
	}

	// Скачиваем первую аватарку
	photo := photos[0]
	avatarFile, err := b.bot.File(&photo.File)
	if err != nil {
		slog.Error("Не удалось скачать аватарку", "error", err, "telegram_id", telegramID)
		b.bot.Edit(statusMsg, MsgSubtitlesNoAvatar)
		return nil
	}
	defer avatarFile.Close()

	// Читаем файлы в буферы для передачи в горутину
	audioData, err := io.ReadAll(audioFile)
	if err != nil {
		slog.Error("Не удалось прочитать аудио", "error", err)
		b.bot.Edit(statusMsg, MsgSubtitlesError)
		return nil
	}

	avatarData, err := io.ReadAll(avatarFile)
	if err != nil {
		slog.Error("Не удалось прочитать аватарку", "error", err)
		b.bot.Edit(statusMsg, MsgSubtitlesError)
		return nil
	}

	// Определяем отображаемое имя
	username := c.Sender().Username
	if username != "" {
		username = "@" + username
	} else if c.Sender().FirstName != "" {
		username = c.Sender().FirstName
	}

	// Создаём context с отменой
	ctx, cancel := context.WithCancel(context.Background())
	b.renderCancels.set(cancelKey, cancel)

	// Запускаем рендеринг в горутине
	go b.processVideoRender(ctx, cancelKey, c.Chat().ID, statusMsg.ID, telegramID, audioData, avatarData, username)

	return nil
}

// handleVideoNoteMessage обрабатывает видео-кружки — сразу отправляет на рендер
func (b *Bot) handleVideoNoteMessage(c tele.Context) error {
	telegramID := c.Sender().ID

	// Проверяем, что render включён
	if b.render == nil {
		return nil
	}

	// Проверяем регистрацию
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil {
		return nil
	}

	// Отправляем статус-сообщение с inline-кнопкой отмены
	cancelKey := fmt.Sprintf("%d:%d", c.Chat().ID, time.Now().UnixNano())
	statusMsg, err := b.bot.Send(c.Recipient(), MsgSubtitlesProcessing, renderCancelKeyboard(cancelKey))
	if err != nil {
		return err
	}

	// Скачиваем кружок из Telegram
	videoNote := c.Message().VideoNote
	videoFile, err := b.bot.File(&videoNote.File)
	if err != nil {
		slog.Error("Не удалось скачать кружок", "error", err, "telegram_id", telegramID)
		b.bot.Edit(statusMsg, MsgSubtitlesError)
		return nil
	}
	defer videoFile.Close()

	videoData, err := io.ReadAll(videoFile)
	if err != nil {
		slog.Error("Не удалось прочитать видео", "error", err)
		b.bot.Edit(statusMsg, MsgSubtitlesError)
		return nil
	}

	// Определяем отображаемое имя
	username := c.Sender().Username
	if username != "" {
		username = "@" + username
	} else if c.Sender().FirstName != "" {
		username = c.Sender().FirstName
	}

	// Создаём context с отменой
	ctx, cancel := context.WithCancel(context.Background())
	b.renderCancels.set(cancelKey, cancel)

	// Запускаем рендеринг в горутине
	go b.processCircleRender(ctx, cancelKey, c.Chat().ID, statusMsg.ID, telegramID, videoData, username)

	return nil
}

// handleRenderCancel обрабатывает нажатие inline-кнопки "Отменить"
func (b *Bot) handleRenderCancel(c tele.Context) error {
	cancelKey := c.Callback().Data
	if b.renderCancels.cancel(cancelKey) {
		return c.Edit(MsgSubtitlesCancelled)
	}
	// Задача уже завершилась — просто убираем кнопку
	return c.Respond()
}

// processVideoRender — горутина рендеринга видео из голосового
func (b *Bot) processVideoRender(ctx context.Context, cancelKey string, chatID int64, statusMsgID int, telegramID int64, audioData, avatarData []byte, username string) {
	defer b.renderCancels.remove(cancelKey)

	chat := &tele.Chat{ID: chatID}
	statusMsg := &tele.Message{ID: statusMsgID, Chat: &tele.Chat{ID: chatID}}

	// Проверяем отмену перед отправкой в render
	if ctx.Err() != nil {
		return
	}

	// Создаём задачу в render
	task, err := b.render.CreateVideoTask(
		bytes.NewReader(audioData),
		bytes.NewReader(avatarData),
		username,
	)
	if err != nil {
		slog.Error("Не удалось создать задачу render", "error", err, "telegram_id", telegramID)
		b.bot.Edit(statusMsg, MsgSubtitlesUnavailable)
		return
	}

	slog.Info("Создана задача render (video)", "task_id", task.TaskID, "telegram_id", telegramID)

	// Поллинг статуса
	resultBody, err := b.pollRenderTask(ctx, task.TaskID, statusMsg)
	if err != nil {
		if ctx.Err() != nil {
			return // Отменено пользователем — сообщение уже обновлено в handleRenderCancel
		}
		slog.Error("Ошибка поллинга render", "error", err, "telegram_id", telegramID)
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
}

// processCircleRender — горутина рендеринга кружка с субтитрами
func (b *Bot) processCircleRender(ctx context.Context, cancelKey string, chatID int64, statusMsgID int, telegramID int64, videoData []byte, username string) {
	defer b.renderCancels.remove(cancelKey)

	chat := &tele.Chat{ID: chatID}
	statusMsg := &tele.Message{ID: statusMsgID, Chat: &tele.Chat{ID: chatID}}

	// Проверяем отмену перед отправкой в render
	if ctx.Err() != nil {
		return
	}

	// Создаём задачу в render
	task, err := b.render.CreateCircleTask(
		bytes.NewReader(videoData),
		username,
	)
	if err != nil {
		slog.Error("Не удалось создать задачу render (circle)", "error", err, "telegram_id", telegramID)
		b.bot.Edit(statusMsg, MsgSubtitlesUnavailable)
		return
	}

	slog.Info("Создана задача render (circle)", "task_id", task.TaskID, "telegram_id", telegramID)

	// Поллинг статуса
	resultBody, err := b.pollRenderTask(ctx, task.TaskID, statusMsg)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		slog.Error("Ошибка поллинга render (circle)", "error", err, "telegram_id", telegramID)
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
		// Fallback: отправляем как обычное видео
		video := &tele.Video{File: tele.FromDisk(tmpFile.Name())}
		if _, err = b.bot.Send(chat, video); err != nil {
			b.bot.Edit(statusMsg, MsgSubtitlesError)
			return
		}
	}

	// Удаляем статус-сообщение
	b.bot.Delete(statusMsg)
}

// pollRenderTask поллит статус задачи render с таймаутом 2 минуты и поддержкой отмены
func (b *Bot) pollRenderTask(ctx context.Context, taskID string, statusMsg *tele.Message) (io.ReadCloser, error) {
	timeout := time.After(2 * time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()

		case <-timeout:
			b.bot.Edit(statusMsg, MsgSubtitlesTimeout)
			return nil, fmt.Errorf("таймаут ожидания render задачи %s", taskID)

		case <-ticker.C:
			task, err := b.render.GetTaskStatus(taskID)
			if err != nil {
				slog.Warn("Ошибка получения статуса render", "error", err, "task_id", taskID)
				continue
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
				slog.Error("Render задача завершилась с ошибкой", "task_id", taskID, "error", task.Error)
				b.bot.Edit(statusMsg, MsgSubtitlesError)
				return nil, fmt.Errorf("render задача %s: %s", taskID, task.Error)

			case "processing", "pending":
				// Продолжаем поллить
			}
		}
	}
}
