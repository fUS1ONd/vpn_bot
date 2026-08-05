package bot

import (
	"fmt"
	"log/slog"
	"strconv"

	tele "gopkg.in/telebot.v3"

	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

// buildDevicesMessage формирует текст экрана управления устройствами.
func buildDevicesMessage(devices []remnawave.HwidDevice) string {
	if len(devices) == 0 {
		return "<b>📱 Управление устройствами</b>\n\nУ вас нет подключённых устройств."
	}
	msg := "<b>📱 Управление устройствами</b>\n\n"
	msg += fmt.Sprintf("Подключено устройств: %d\n\n", len(devices))
	msg += "Нажмите на устройство, чтобы удалить его, либо сбросьте все сразу."
	return msg
}

// deviceByIndex возвращает устройство по строковому индексу из callback-данных.
func deviceByIndex(devices []remnawave.HwidDevice, idxStr string) (remnawave.HwidDevice, bool) {
	idx, err := strconv.Atoi(idxStr)
	if err != nil || idx < 0 || idx >= len(devices) {
		return remnawave.HwidDevice{}, false
	}
	return devices[idx], true
}

// resolveUserUUID возвращает remnawave UUID для отправителя или признак, что подписки нет.
func (b *Bot) resolveUserUUID(telegramID int64) (string, bool) {
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil || user.RemnawaveUUID == "" {
		return "", false
	}
	return user.RemnawaveUUID, true
}

// handleDevicesManage показывает экран управления устройствами (inline-список).
func (b *Bot) handleDevicesManage(c tele.Context) error {
	uuid, ok := b.resolveUserUUID(c.Sender().ID)
	if !ok {
		return c.RespondAlert("Сначала активируйте подписку")
	}

	devices, err := b.remnawave.GetUserHwidDevices(uuid)
	if err != nil {
		slog.Error("Failed to get HWID devices", "error", err, "telegram_id", c.Sender().ID)
		return c.RespondAlert("Ошибка получения списка устройств")
	}

	if err := c.Edit(buildDevicesMessage(devices), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: DevicesManagementKeyboard(devices),
	}); err != nil {
		// Если редактировать нечего (например, вызвано не из callback) — шлём новое сообщение.
		return c.Send(buildDevicesMessage(devices), &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: DevicesManagementKeyboard(devices),
		})
	}
	return c.Respond()
}

// handleDeviceDelete удаляет одно устройство по индексу и перерисовывает список.
func (b *Bot) handleDeviceDelete(c tele.Context) error {
	uuid, ok := b.resolveUserUUID(c.Sender().ID)
	if !ok {
		return c.RespondAlert("Сначала активируйте подписку")
	}

	args := c.Args()
	if len(args) == 0 {
		return c.RespondAlert("Некорректный запрос")
	}

	// Берём актуальный список и сопоставляем по индексу (индекс мог устареть).
	devices, err := b.remnawave.GetUserHwidDevices(uuid)
	if err != nil {
		slog.Error("Failed to get HWID devices before delete", "error", err, "telegram_id", c.Sender().ID)
		return c.RespondAlert("Ошибка получения списка устройств")
	}

	device, found := deviceByIndex(devices, args[0])
	if !found {
		// Список устарел — перерисуем актуальный.
		_ = c.Edit(buildDevicesMessage(devices), &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: DevicesManagementKeyboard(devices),
		})
		return c.RespondAlert("Список обновлён, попробуйте снова")
	}

	updated, err := b.remnawave.DeleteUserHwidDevice(uuid, device.Hwid)
	if err != nil {
		slog.Error("Failed to delete HWID device", "error", err, "telegram_id", c.Sender().ID)
		return c.RespondAlert("Ошибка удаления устройства")
	}

	_ = c.Edit(buildDevicesMessage(updated), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: DevicesManagementKeyboard(updated),
	})
	return c.Respond(&tele.CallbackResponse{Text: "Устройство удалено"})
}

// handleDevicesResetAll показывает экран подтверждения сброса всех устройств.
func (b *Bot) handleDevicesResetAll(c tele.Context) error {
	msg := "<b>🗑 Сбросить все устройства?</b>\n\n" +
		"Все подключённые устройства будут отключены. " +
		"Их нужно будет подключить заново по ссылке из подписки."
	if err := c.Edit(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: DevicesResetAllConfirmKeyboard(),
	}); err != nil {
		return c.RespondAlert("Ошибка")
	}
	return c.Respond()
}

// handleDevicesResetAllConfirm сбрасывает все устройства пользователя.
func (b *Bot) handleDevicesResetAllConfirm(c tele.Context) error {
	uuid, ok := b.resolveUserUUID(c.Sender().ID)
	if !ok {
		return c.RespondAlert("Сначала активируйте подписку")
	}

	if err := b.remnawave.DeleteAllUserHwidDevices(uuid); err != nil {
		slog.Error("Failed to reset all HWID devices", "error", err, "telegram_id", c.Sender().ID)
		return c.RespondAlert("Ошибка сброса устройств")
	}

	_ = c.Edit(buildDevicesMessage(nil), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: DevicesManagementKeyboard(nil),
	})
	return c.Respond(&tele.CallbackResponse{Text: "Все устройства сброшены"})
}
