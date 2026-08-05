package bot

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	tele "gopkg.in/telebot.v3"

	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

// subRevokeCooldownWindow — минимальный интервал между двумя перевыпусками
// ссылки одного пользователя. Каждый лишний перевыпуск рвёт связь и сбрасывает
// устройства, а дабл-клик по inline-кнопке — обычное дело.
const subRevokeCooldownWindow = 5 * time.Minute

// Ошибки applyRevoke, различаемые для показа нужного текста алерта.
var (
	errRevokeCooldown      = errors.New("ссылка уже перевыпущена недавно")
	errRevokeUserNotFound  = errors.New("пользователь не найден")
	errRevokeDevicesFailed = errors.New("не удалось сбросить устройства")
	errRevokeFailed        = errors.New("не удалось перевыпустить ссылку")
)

// revokeErrorAlert возвращает текст алерта пользователю для ошибки applyRevoke.
// Тексты различают падение сброса устройств и падение самого перевыпуска, чтобы
// пользователь понимал, изменилось ли что-то на самом деле.
func revokeErrorAlert(err error) string {
	switch {
	case errors.Is(err, errRevokeCooldown):
		return "Ссылка уже перевыпущена, повторное нажатие проигнорировано"
	case errors.Is(err, errRevokeUserNotFound):
		return "Сначала активируйте подписку"
	case errors.Is(err, errRevokeDevicesFailed):
		return "❌ Не удалось сбросить устройства. Ссылка не изменилась, попробуйте ещё раз."
	default:
		return "❌ Не удалось перевыпустить ссылку. Устройства сброшены — переподключите их по прежней ссылке."
	}
}

// buildSubscriptionCard собирает текст и клавиатуру карточки «Моя подписка».
func (b *Bot) buildSubscriptionCard(telegramID int64, remUser *remnawave.User) (string, *tele.ReplyMarkup) {
	dbUser, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil {
		slog.Error("Failed to load user for subscription card", "error", err, "telegram_id", telegramID)
	}

	var devicesCount *int
	count, err := b.remnawave.GetUserHwidDevicesCount(remUser.UUID)
	if err != nil {
		slog.Warn("Failed to get user HWID devices for status", "error", err, "telegram_id", telegramID)
	} else {
		devicesCount = &count
	}

	isTrial := b.isTrialUser(telegramID)
	msg := FormatUserStatus(remUser, dbUser, isTrial, devicesCount)
	markup := SubscriptionCardKeyboard(remUser.SubscriptionURL, SubscriptionLinkVisible(remUser, isTrial))

	return msg, markup
}

// sendWithInlineFallback отправляет сообщение с inline-клавиатурой, а при отказе
// Telegram (например, если URL-кнопка со ссылкой подписки окажется невалидной для
// Bot API) повторяет отправку без клавиатуры. Статус пользователь должен увидеть
// в любом случае — потеря кнопки терпима, потеря всего сообщения нет.
func sendWithInlineFallback(c tele.Context, msg string, markup *tele.ReplyMarkup) error {
	err := c.Send(msg, &tele.SendOptions{ParseMode: tele.ModeHTML, ReplyMarkup: markup})
	if err == nil {
		return nil
	}

	slog.Error("Failed to send message with inline keyboard, retrying without it",
		"error", err, "telegram_id", c.Sender().ID)
	return c.Send(msg, &tele.SendOptions{ParseMode: tele.ModeHTML})
}

// editWithInlineFallback — версия sendWithInlineFallback для редактирования.
// Если отредактировать не удалось (сообщение устарело или клавиатуру отверг
// Telegram), отправляем карточку новым сообщением.
func editWithInlineFallback(c tele.Context, msg string, markup *tele.ReplyMarkup) error {
	err := c.Edit(msg, &tele.SendOptions{ParseMode: tele.ModeHTML, ReplyMarkup: markup})
	if err == nil {
		return nil
	}

	slog.Warn("Failed to edit subscription card, sending new message",
		"error", err, "telegram_id", c.Sender().ID)
	return sendWithInlineFallback(c, msg, markup)
}

// handleSubscriptionCard перерисовывает карточку подписки в текущем сообщении.
// Используется как возврат из экрана устройств и при отмене перевыпуска.
func (b *Bot) handleSubscriptionCard(c tele.Context) error {
	telegramID := c.Sender().ID

	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil {
		return c.RespondAlert("Сначала активируйте подписку")
	}

	remUser, err := b.remnawave.GetUser(user.RemnawaveUUID)
	if err != nil {
		slog.Error("Failed to get user from Remnawave for card", "error", err, "telegram_id", telegramID)
		return c.RespondAlert("Ошибка получения статуса. Попробуйте позже.")
	}

	msg, markup := b.buildSubscriptionCard(telegramID, remUser)
	if err := editWithInlineFallback(c, msg, markup); err != nil {
		slog.Error("Failed to render subscription card", "error", err, "telegram_id", telegramID)
	}
	return c.Respond()
}

// handleSubRevoke показывает экран подтверждения перевыпуска ссылки.
func (b *Bot) handleSubRevoke(c tele.Context) error {
	if _, ok := b.resolveUserUUID(c.Sender().ID); !ok {
		return c.RespondAlert("Сначала активируйте подписку")
	}

	if err := c.Edit(MsgRevokeConfirm, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: SubscriptionRevokeConfirmKeyboard(),
	}); err != nil {
		slog.Warn("Failed to show revoke confirmation", "error", err, "telegram_id", c.Sender().ID)
	}
	return c.Respond()
}

// handleSubRevokeCancel возвращает карточку подписки без изменений.
func (b *Bot) handleSubRevokeCancel(c tele.Context) error {
	return b.handleSubscriptionCard(c)
}

// handleSubRevokeConfirm выполняет перевыпуск и перерисовывает карточку.
func (b *Bot) handleSubRevokeConfirm(c tele.Context) error {
	telegramID := c.Sender().ID

	remUser, err := b.applyRevoke(telegramID)
	if err != nil {
		return c.RespondAlert(revokeErrorAlert(err))
	}

	msg, markup := b.buildSubscriptionCard(telegramID, remUser)
	if err := editWithInlineFallback(c, MsgRevokeDone+msg, markup); err != nil {
		slog.Error("Failed to render card after revoke", "error", err, "telegram_id", telegramID)
	}
	return c.Respond(&tele.CallbackResponse{Text: "Ссылка перевыпущена"})
}

// applyRevoke сбрасывает устройства и перевыпускает подписку, возвращая
// обновлённого пользователя с новой ссылкой.
//
// Порядок операций важен: сначала сброс устройств, потом перевыпуск. Если сброс
// упадёт — состояние не тронуто и ссылка прежняя. Обратный порядок оставил бы
// пользователя с новой ссылкой и старыми HWID-привязками: он переподключился бы
// и упёрся в лимит устройств, что чинится только вручную.
func (b *Bot) applyRevoke(telegramID int64) (*remnawave.User, error) {
	// Сериализуем с платёжными операциями по этому юзеру: активация подписки
	// тоже читает и пишет состояние в Remnawave.
	mu := getPaymentMutex(telegramID)
	mu.Lock()
	defer mu.Unlock()

	if last, ok := b.subRevokeCooldown.Load(telegramID); ok {
		if time.Since(last.(time.Time)) < subRevokeCooldownWindow {
			return nil, errRevokeCooldown
		}
	}

	uuid, ok := b.resolveUserUUID(telegramID)
	if !ok {
		return nil, errRevokeUserNotFound
	}

	if err := b.remnawave.DeleteAllUserHwidDevices(uuid); err != nil {
		slog.Error("Failed to reset devices before revoke", "error", err, "telegram_id", telegramID)
		return nil, fmt.Errorf("%w: %v", errRevokeDevicesFailed, err)
	}

	remUser, err := b.remnawave.RevokeUserSubscription(uuid)
	if err != nil {
		slog.Error("Failed to revoke subscription", "error", err, "telegram_id", telegramID)
		return nil, fmt.Errorf("%w: %v", errRevokeFailed, err)
	}

	b.subRevokeCooldown.Store(telegramID, time.Now())

	return remUser, nil
}
