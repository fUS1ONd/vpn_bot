package bot

import (
	"fmt"
	"log/slog"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
)

// Несовпадение на платеже автосписания касается не только владельца: деньги
// ушли с карты человека без его участия, а подписка не продлена. Без сообщения
// он заплатит второй раз руками или пойдёт в банк за chargeback. Дедупликации
// владельца и человека раздельные, чтобы недоставка одному не гасила и не
// дублировала сообщение другому.
//
// Входы, где несовпадение всплывает (сверка, вебхук ЮKassa, ручная проверка),
// зовут reportMismatchAndPrepareNotice — она выбирает заголовок владельцу и готовит
// сообщение человеку. Шаг списания и так знает, что платёж — автосписание, и
// зовёт reportAutorenewMismatch и holdAndNotifyAutorenewMismatch напрямую.

// reportMismatchAndPrepareNotice доносит несовпадение владельцу — для автосписания с
// его заголовком — и готовит сообщение человеку, если это автосписание и касса
// говорит «оплачено». Обе отправки ставятся в later: входы держат getPaymentMutex,
// а сообщения владельцу и человеку уходят вне мьютекса — сначала владельцу.
// Пометки «уже сообщили» занимаются уже здесь, поэтому later обязана быть
// выполнена на любом пути.
func (b *Bot) reportMismatchAndPrepareNotice(later *afterUnlock, payment *database.Payment, mismatch *paymentMismatchError, source string) {
	fromAutorenew, err := b.db.IsAutorenewPayment(payment.ID)
	if err != nil {
		slog.Error("Не удалось проверить, автосписание ли несовпавший платёж; сообщаем как об обычном",
			"error", err, "payment_id", payment.ID)
	}
	if !fromAutorenew {
		later.add(b.reportPaymentMismatch(payment, mismatch, source))
		return
	}
	later.add(b.reportAutorenewMismatch(payment, mismatch, source))
	later.add(b.holdAndNotifyAutorenewMismatch(payment, mismatch))
}

// reportAutorenewMismatch — алерт владельцу по автосписанию: заголовок говорит,
// что деньги списаны без участия человека и что его отключение приостановлено
// до разбора. Подробности и дедупликация общие с reportPaymentMismatch, так что
// с какого бы входа несовпадение ни пришло, сообщение по платежу одно. Как и
// reportPaymentMismatch, возвращает отправку для вызова вне мьютекса.
func (b *Bot) reportAutorenewMismatch(payment *database.Payment, mismatch *paymentMismatchError, source string) func() bool {
	headline := fmt.Sprintf("⚠️ Автосписание #%d (пользователь %d): ЮKassa говорит «оплачено», но ответ не сошёлся с записью платежа.\n"+
		"Отключение приостановлено до разбора: когда разберёте, нажмите «✅ Автосписание разобрано» в карточке пользователя (🔍 Инфо о пользователе).",
		payment.ID, payment.TelegramID)
	return b.reportPaymentMismatchWithHeadline(payment, mismatch, source, headline)
}

// holdAndNotifyAutorenewMismatch ставит удержание цикла и готовит сообщение
// человеку по платежу, о котором уже известно, что он автосписания. Ничего не
// делает и возвращает noNotice, если касса не говорит «оплачено»: за pending и
// canceled денег нет.
//
// Удержание — пометка paid_mismatch_at в базе: ни напоминаний «продлите», ни
// отключения, ни кика, пока владелец не разберёт платёж (AutorenewMismatchHold).
// Сбой записи сообщение человеку не отменяет: следующий вход пометит снова.
func (b *Bot) holdAndNotifyAutorenewMismatch(payment *database.Payment, mismatch *paymentMismatchError) func() bool {
	if mismatch.ProviderStatus != paymentprovider.StatusSucceeded {
		return noNotice
	}
	if err := b.db.MarkPaidMismatch(payment.ID); err != nil {
		slog.Error("Не удалось пометить несовпавшее автосписание, удержание цикла не поставлено",
			"error", err, "payment_id", payment.ID)
	}
	// Одно сообщение на платёж с любого входа; не принятое Telegram снимает
	// пометку, и его повторит следующий вход. Дедупликация отдельная от алерта
	// владельцу и живёт в памяти — после перезапуска допустимо ещё одно сообщение.
	telegramID := payment.TelegramID
	msg := autorenewMismatchUserText(mismatch)
	return deliverOnce(&b.autorenewMismatchNotified, payment.ID, "несовпавшее автосписание — человеку", func() error {
		return b.sendSchedulerMessage(telegramID, msg)
	})
}

// noNotice — отправка, которой нет: писать человеку нечего.
func noNotice() bool { return false }

// autorenewMismatchUserText — сообщение человеку. Следующую попытку не обещаем:
// по названному кассой платежу её не будет (autorenewCycleUnsettled). Сумму
// называем из ответа кассы — списано именно столько, — но только в рублях.
func autorenewMismatchUserText(mismatch *paymentMismatchError) string {
	charged := "Деньги за автопродление списаны с вашей карты"
	if mismatch.Currency == "RUB" {
		charged = fmt.Sprintf("С вашей карты списано <b>%d ₽</b> за автопродление", mismatch.ProviderAmount)
	}
	return "⚠️ " + charged + ", но платёж не прошёл автоматическую проверку, " +
		"и подписка пока не продлена.\n\n" +
		"Мы проверяем его вручную. <b>Платить повторно не нужно</b>: после разбора " +
		"подписка будет продлена или деньги вернутся на карту."
}
