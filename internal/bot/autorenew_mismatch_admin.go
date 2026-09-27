package bot

import (
	"log/slog"
	"strconv"

	tele "gopkg.in/telebot.v3"
)

// Разбор несовпавшего автосписания владельцем. Пока касса говорит «оплачено», а
// ответ не сошёлся с записью, цикл держится: человека не отключают и не
// кикают (autorenewMismatchHold). Снимает удержание либо подтверждение платежа,
// либо сдвиг expireAt (ручное продление), либо эта кнопка — когда владелец
// вернул деньги или решил иначе. Снятие ведёт к штатному отключению человека,
// с которого списаны деньги, поэтому идёт через подтверждение.

// adminMismatchHoldLine — строка карточки пользователя про удержание.
const adminMismatchHoldLine = "⏸ Отключение приостановлено: автосписание не сошлось с кассой, ждёт разбора\n"

// adminMismatchTarget проверяет права и достаёт targetID из кнопки.
func (b *Bot) adminMismatchTarget(c tele.Context) (int64, bool) {
	if !b.isAdmin(c) {
		_ = c.RespondAlert("Недостаточно прав")
		return 0, false
	}
	targetID, err := strconv.ParseInt(c.Data(), 10, 64)
	if err != nil {
		_ = c.RespondAlert("Не удалось определить пользователя")
		return 0, false
	}
	return targetID, true
}

// handleAdminMismatchResolve показывает экран подтверждения.
func (b *Bot) handleAdminMismatchResolve(c tele.Context) error {
	targetID, ok := b.adminMismatchTarget(c)
	if !ok {
		return nil
	}
	text := "Снять удержание несовпавшего автосписания?\n\n" +
		"Нажимайте, когда платёж разобран: деньги возвращены или подписка продлена вручную. " +
		"Если подписка уже истекла, бот отключит человека штатно, а три дня до удаления " +
		"отсчитаются от этого момента."
	if err := c.Edit(text, &tele.SendOptions{ReplyMarkup: AdminMismatchResolveKeyboard(targetID)}); err != nil {
		return err
	}
	return c.Respond()
}

// handleAdminMismatchResolveConfirm снимает удержание и перерисовывает карточку.
func (b *Bot) handleAdminMismatchResolveConfirm(c tele.Context) error {
	targetID, ok := b.adminMismatchTarget(c)
	if !ok {
		return nil
	}
	resolved, err := b.db.ResolveAutorenewMismatches(targetID)
	if err != nil {
		slog.Error("Админ: не удалось снять удержание несовпавшего автосписания", "error", err, "telegram_id", targetID)
		return c.RespondAlert("Не удалось снять удержание")
	}
	slog.Info("Админ разобрал несовпавшее автосписание", "telegram_id", targetID, "payments", resolved)

	if err := b.editAdminUserInfo(c, targetID); err != nil {
		slog.Error("Админ: не удалось перерисовать карточку", "error", err, "telegram_id", targetID)
	}
	return c.Respond(&tele.CallbackResponse{Text: "Удержание снято"})
}

// handleAdminMismatchBack возвращает карточку пользователя.
func (b *Bot) handleAdminMismatchBack(c tele.Context) error {
	targetID, ok := b.adminMismatchTarget(c)
	if !ok {
		return nil
	}
	return b.editAdminUserInfo(c, targetID)
}
