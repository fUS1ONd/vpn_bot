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
		// Не сбрасываем состояние — пользователь может попробовать снова после установки фото
		b.userStates[telegramID] = StateWaitRender
		return nil
	}

	// Скачиваем первую аватарку (самую большую версию)
	photo := photos[0]
	avatarFile, err := b.bot.File(&photo.File)
	if err != nil {
		slog.Error("Не удалось скачать аватарку", "error", err, "telegram_id", telegramID)
		b.bot.Edit(statusMsg, MsgSubtitlesNoAvatar)
		b.userStates[telegramID] = StateWaitRender
		return nil
	}
	defer avatarFile.Close()

	// Читаем файлы в буферы для передачи в горутину
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

	// Определяем отображаемое имя
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
		return nil // Игнорируем кружки вне состояния ожидания
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

	// Определяем отображаемое имя
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

	slog.Info("Создана задача render (video)", "task_id", task.TaskID, "telegram_id", telegramID)

	// Поллинг статуса и скачивание результата
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

	// Удаляем статус-сообщение и возвращаем меню
	b.bot.Delete(statusMsg)
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

	slog.Info("Создана задача render (circle)", "task_id", task.TaskID, "telegram_id", telegramID)

	// Поллинг статуса и скачивание результата
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
		// Fallback: отправляем как обычное видео
		video := &tele.Video{File: tele.FromDisk(tmpFile.Name())}
		if _, err = b.bot.Send(chat, video); err != nil {
			b.bot.Edit(statusMsg, MsgSubtitlesError)
			return
		}
	}

	// Удаляем статус-сообщение и возвращаем меню
	b.bot.Delete(statusMsg)
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
				slog.Error("Render задача завершилась с ошибкой", "task_id", taskID, "error", task.Error)
				b.bot.Edit(statusMsg, MsgSubtitlesError)
				return nil, fmt.Errorf("render задача %s: %s", taskID, task.Error)

			case "processing", "pending":
				// Продолжаем поллить
			}
		}
	}
}
