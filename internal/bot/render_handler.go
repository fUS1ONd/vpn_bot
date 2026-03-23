package bot

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	tele "gopkg.in/telebot.v3"
)

//go:embed default_avatar.png
var defaultAvatar []byte

// resolveMessageAuthor определяет автора сообщения — для пересланных возвращает оригинального отправителя
func resolveMessageAuthor(c tele.Context) (author *tele.User, displayName string) {
	msg := c.Message()

	// Пересланное сообщение от пользователя с открытым аккаунтом
	if msg.OriginalSender != nil {
		user := msg.OriginalSender
		name := user.Username
		if name != "" {
			name = "@" + name
		} else if user.FirstName != "" {
			name = user.FirstName
		}
		return user, name
	}

	// Пересланное сообщение от пользователя со скрытым аккаунтом
	if msg.OriginalSenderName != "" {
		return nil, msg.OriginalSenderName
	}

	// Обычное сообщение — используем отправителя
	sender := c.Sender()
	name := sender.Username
	if name != "" {
		name = "@" + name
	} else if sender.FirstName != "" {
		name = sender.FirstName
	}
	return sender, name
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
		return nil
	}
	defer audioFile.Close()

	// Читаем аудио в буфер для передачи в горутину
	audioData, err := io.ReadAll(audioFile)
	if err != nil {
		slog.Error("Не удалось прочитать аудио", "error", err)
		b.bot.Edit(statusMsg, MsgSubtitlesError)
		return nil
	}

	// Определяем автора сообщения (для пересланных — оригинальный отправитель)
	author, username := resolveMessageAuthor(c)

	// Получаем аватарку автора (с fallback на дефолтную)
	avatarData := b.getUserAvatar(author, statusMsg, telegramID)

	// Запускаем рендеринг в горутине
	go b.processVideoRender(c.Chat().ID, statusMsg.ID, telegramID, audioData, avatarData, username)

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
		return nil
	}
	defer videoFile.Close()

	videoData, err := io.ReadAll(videoFile)
	if err != nil {
		slog.Error("Не удалось прочитать видео", "error", err)
		b.bot.Edit(statusMsg, MsgSubtitlesError)
		return nil
	}

	// Определяем автора сообщения (для пересланных — оригинальный отправитель)
	_, username := resolveMessageAuthor(c)

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
		return
	}

	slog.Info("Создана задача render (video)", "task_id", task.TaskID, "telegram_id", telegramID)

	// Поллинг статуса и скачивание результата
	resultBody, err := b.pollRenderTask(task.TaskID, statusMsg)
	if err != nil {
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

	if _, err := io.Copy(tmpFile, resultBody); err != nil {
		tmpFile.Close()
		slog.Error("Не удалось записать результат", "error", err)
		b.bot.Edit(statusMsg, MsgSubtitlesError)
		return
	}
	// Закрываем файл перед отправкой — иначе Telegram SDK не сможет его прочитать (на Windows — блокировка)
	tmpFile.Close()

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
		return
	}

	slog.Info("Создана задача render (circle)", "task_id", task.TaskID, "telegram_id", telegramID)

	// Поллинг статуса и скачивание результата
	resultBody, err := b.pollRenderTask(task.TaskID, statusMsg)
	if err != nil {
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

	if _, err := io.Copy(tmpFile, resultBody); err != nil {
		tmpFile.Close()
		slog.Error("Не удалось записать результат", "error", err)
		b.bot.Edit(statusMsg, MsgSubtitlesError)
		return
	}
	// Закрываем файл перед отправкой — иначе Telegram SDK не сможет его прочитать
	tmpFile.Close()

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

// getUserAvatar получает аватарку пользователя, при неудаче возвращает дефолтную.
// Если user == nil (пересланное от скрытого аккаунта), сразу возвращает дефолтную.
func (b *Bot) getUserAvatar(user *tele.User, statusMsg *tele.Message, telegramID int64) []byte {
	if user == nil {
		slog.Info("Автор скрыл аккаунт, используем дефолтную аватарку", "telegram_id", telegramID)
		b.bot.Edit(statusMsg, MsgSubtitlesNoAvatar)
		return defaultAvatar
	}

	photos, err := b.bot.ProfilePhotosOf(user)
	if err != nil || len(photos) == 0 {
		slog.Info("Аватарка недоступна, используем дефолтную", "telegram_id", telegramID)
		b.bot.Edit(statusMsg, MsgSubtitlesNoAvatar)
		return defaultAvatar
	}

	photo := photos[0]
	avatarFile, err := b.bot.File(&photo.File)
	if err != nil {
		slog.Warn("Не удалось скачать аватарку, используем дефолтную", "error", err, "telegram_id", telegramID)
		return defaultAvatar
	}
	defer avatarFile.Close()

	avatarData, err := io.ReadAll(avatarFile)
	if err != nil {
		slog.Warn("Не удалось прочитать аватарку, используем дефолтную", "error", err, "telegram_id", telegramID)
		return defaultAvatar
	}

	return avatarData
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
