package bot

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
)

// Тексты автосписания не совпадают со штатным «Оплата прошла!»: та
// формулировка подразумевает действие пользователя, а он ничего не делал.
// Приглашение в Канал сюда не подмешивается — кулдаун приписки жалко тратить на
// сообщение, которое человек пролистает не глядя.

// autorenewActivatedMessage — текст продления для платежа автосписания.
func (b *Bot) autorenewActivatedMessage(payment *database.Payment) string {
	if until, ok := b.subscriptionExpiryFor(payment.TelegramID); ok {
		return fmt.Sprintf("🔄 Подписка продлена автоматически до <b>%s</b>. Списано <b>%d ₽</b>.",
			until.Format("02.01.2006"), payment.Amount)
	}
	return fmt.Sprintf("🔄 Подписка продлена автоматически. Списано <b>%d ₽</b>.", payment.Amount)
}

// notifyAutorenewSuccess сообщает о продлении подписки автосписанием.
func (b *Bot) notifyAutorenewSuccess(telegramID int64, price, previous int, hasPrevious bool) {
	msg := fmt.Sprintf("🔄 Подписка продлена автоматически. Списано <b>%d ₽</b>.", price)
	if until, ok := b.subscriptionExpiryFor(telegramID); ok {
		msg = fmt.Sprintf("🔄 Подписка продлена автоматически до <b>%s</b>. Списано <b>%d ₽</b>.",
			until.Format("02.01.2006"), price)
	}
	if hasPrevious && price > previous {
		// Молча списать больше, чем в прошлый раз, — кратчайший путь к chargeback.
		msg += fmt.Sprintf("\n\nЦена подписки изменилась: в прошлый раз было %d ₽.", previous)
	}

	if err := b.sendSchedulerMessageWithKeyboard(telegramID, msg, AutorenewDisableKeyboard()); err != nil {
		logSchedulerSendError("autorenew_success", telegramID, err)
	}
}

// notifyAutorenewFailure сообщает о неудачной первой попытке. Текстов два:
// обычный отказ карты оставляет вторую попытку, а пропавший Способ — нет, и
// обещание «попытка завтра» отправило бы человека класть деньги на мёртвую карту.
func (b *Bot) notifyAutorenewFailure(telegramID int64, price int, expireAt time.Time, methodGone bool) {
	msg := fmt.Sprintf("⚠️ Не удалось списать <b>%d ₽</b> за автопродление.\n\n"+
		"Следующая попытка — <b>%s</b>, в день окончания подписки. "+
		"Убедитесь, что на карте есть %d ₽, или продлите вручную.",
		price, expireAt.Format("02.01"), price)
	if methodGone {
		msg = fmt.Sprintf("⚠️ Не удалось списать <b>%d ₽</b> за автопродление: "+
			"сохранённый способ оплаты больше не действует.\n\n"+
			"Списать повторно нам нечем — продлите подписку вручную до <b>%s</b>. "+
			"После оплаты картой или через СБП автопродление заработает снова.",
			price, expireAt.Format("02.01"))
	}

	if err := b.sendSchedulerMessageWithKeyboard(telegramID, msg, AutorenewFailureKeyboard()); err != nil {
		logSchedulerSendError("autorenew_failure", telegramID, err)
	}
}

// reportAutorenewOutage — один алерт, если ВСЕ попытки прохода уткнулись в сбой
// транспорта или 5xx. Провал у конкретного человека норма, а сломанный ключ или
// отозванные рекурренты выглядят как «ни одного успеха» и должны быть видны сразу.
func (b *Bot) reportAutorenewOutage(attempted, transportFailures int) {
	if attempted == 0 || transportFailures < attempted {
		return
	}
	b.sendAdminAlert(fmt.Sprintf(
		"⚠️ Автосписания: ни одна попытка не дошла до кассы (%d из %d — сбой связи или 5xx).\n\n"+
			"Так выглядят сломанный ключ ЮKassa и отозванное разрешение на автоплатежи. Проверьте кабинет кассы.",
		transportFailures, attempted))
}

// subscriptionExpiryFor возвращает дату окончания подписки после продления.
func (b *Bot) subscriptionExpiryFor(telegramID int64) (time.Time, bool) {
	ref, ok := b.resolveUserRef(telegramID)
	if !ok {
		return time.Time{}, false
	}
	remUser, err := b.remnawave.GetUser(ref)
	if err != nil || remUser == nil {
		slog.Warn("Автосписание: не удалось прочитать новую дату окончания", "error", err, "telegram_id", telegramID)
		return time.Time{}, false
	}
	return remUser.ExpireAt, true
}

// previousAutorenewCharge возвращает сумму прошлого успешного автосписания.
func (b *Bot) previousAutorenewCharge(telegramID int64) (int, bool) {
	amount, ok, err := b.db.LastAutorenewChargeAmount(telegramID)
	if err != nil {
		slog.Warn("Автосписание: не удалось прочитать прошлое списание", "error", err, "telegram_id", telegramID)
		return 0, false
	}
	return amount, ok
}

// autorenewSuppressesExpiryNotice — молчать ли про скорое окончание подписки.
// Молчим, пока автопродление ещё обещает сработать; после неуспешной попытки
// цикла предупреждение возвращается, иначе человек дошёл бы до отключения без
// единого сообщения.
func (b *Bot) autorenewSuppressesExpiryNotice(telegramID int64, expireAt time.Time) bool {
	if !b.autorenewAvailable() {
		return false
	}
	renewal, err := b.db.GetAutorenewal(telegramID)
	if err != nil {
		slog.Warn("Не удалось прочитать автопродление перед уведомлением", "error", err, "telegram_id", telegramID)
		return false
	}
	if !renewal.IsEnabled() || !renewal.HasMethod() {
		return false
	}

	failed, err := b.db.HasFailedAutorenewAttempt(telegramID, expireAt)
	if err != nil {
		slog.Warn("Не удалось прочитать попытки автосписания перед уведомлением", "error", err, "telegram_id", telegramID)
		return false
	}
	return !failed
}
