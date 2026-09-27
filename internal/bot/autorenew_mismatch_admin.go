package bot

import (
	"log/slog"
	"strconv"
	"time"

	tele "gopkg.in/telebot.v3"
)

// Разбор несовпавшего автосписания владельцем. Пока касса говорит «оплачено», а
// ответ не сошёлся с записью, цикл держится: человека не отключают и не
// кикают (autorenewMismatchHold). Снимает удержание либо подтверждение платежа,
// либо сдвиг expireAt (ручное продление), либо эта кнопка — когда владелец
// вернул деньги или решил иначе. Снятие ведёт к штатному отключению человека,
// с которого списаны деньги, поэтому идёт через подтверждение.

// adminMismatchLine — строка карточки пользователя про неразобранное
// несовпавшее автосписание или "", если разбирать нечего. Неразобранное
// показывается в любом цикле: человек мог оплатить сам, и удержание ушло вместе
// с циклом, а деньги по списанию остались. Про удержание строка говорит, только
// пока оно держит текущий цикл.
func (b *Bot) adminMismatchLine(targetID int64, expireAt time.Time) string {
	unresolved, err := b.db.HasUnresolvedAutorenewMismatch(targetID)
	if err != nil {
		slog.Error("Не удалось проверить неразобранные автосписания для карточки", "error", err, "telegram_id", targetID)
		return ""
	}
	if !unresolved {
		return ""
	}
	hold, err := b.db.AutorenewMismatchHold(targetID, expireAt)
	if err != nil {
		slog.Error("Не удалось проверить удержание для карточки", "error", err, "telegram_id", targetID)
	}
	if hold.Active {
		return "⏸ Отключение приостановлено: автосписание не сошлось с кассой, ждёт разбора\n"
	}
	return "⚠️ Автосписание не сошлось с кассой и ждёт разбора\n"
}

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
		"отсчитаются от этого момента; если ещё нет — всё пойдёт штатно от даты окончания."
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
