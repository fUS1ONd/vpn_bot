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
// зовут reportPaymentMismatchFor — она выбирает заголовок владельцу и готовит
// сообщение человеку. Шаг списания и так знает, что платёж — автосписание, и
// зовёт reportAutorenewMismatch и autorenewMismatchNotice напрямую.

// reportPaymentMismatchFor доносит несовпадение владельцу — для автосписания с
// его заголовком — и готовит сообщение человеку, если это автосписание и касса
// говорит «оплачено». Отправка человеку отдаётся замыканием: входы держат
// getPaymentMutex, а сообщения пользователю уходят вне мьютекса.
func (b *Bot) reportPaymentMismatchFor(payment *database.Payment, mismatch *paymentMismatchError, source string) func() {
	fromAutorenew, err := b.db.IsAutorenewPayment(payment.ID)
	if err != nil {
		slog.Error("Не удалось проверить, автосписание ли несовпавший платёж; сообщаем как об обычном",
			"error", err, "payment_id", payment.ID)
	}
	if !fromAutorenew {
		b.reportPaymentMismatch(payment, mismatch, source)
		return nil
	}
	b.reportAutorenewMismatch(payment, mismatch, source)
	return b.autorenewMismatchNotice(payment, mismatch)
}

// reportAutorenewMismatch — алерт владельцу по автосписанию: заголовок говорит,
// что деньги списаны без участия человека и что его отключение приостановлено
// до разбора. Подробности и дедупликация общие с reportPaymentMismatch, так что
// с какого бы входа несовпадение ни пришло, сообщение по платежу одно.
func (b *Bot) reportAutorenewMismatch(payment *database.Payment, mismatch *paymentMismatchError, source string) {
	headline := fmt.Sprintf("⚠️ Автосписание #%d (пользователь %d): ЮKassa говорит «оплачено», но ответ не сошёлся с записью платежа.\n"+
		"Отключение приостановлено до разбора: когда разберёте, нажмите «✅ Автосписание разобрано» в карточке пользователя (🔍 Инфо о пользователе).",
		payment.ID, payment.TelegramID)
	b.reportPaymentMismatchWithHeadline(payment, mismatch, source, headline)
}

// autorenewMismatchNotice — то же для платежа, о котором уже известно, что он
// автосписания (шаг списания): лишний поход в базу там только добавил бы способ
// потерять сообщение. nil, если касса не говорит «оплачено»: за pending и
// canceled денег нет.
//
// Заодно платёж помечается в базе (paid_mismatch_at): пометка держит цикл —
// ни напоминаний «продлите», ни отключения, ни кика, пока владелец не разберёт
// платёж (autorenewMismatchHeld). Сбой записи сообщение человеку не отменяет:
// следующий вход пометит снова.
func (b *Bot) autorenewMismatchNotice(payment *database.Payment, mismatch *paymentMismatchError) func() {
	if mismatch.ProviderStatus != paymentprovider.StatusSucceeded {
		return nil
	}
	if err := b.db.MarkPaidMismatch(payment.ID); err != nil {
		slog.Error("Не удалось пометить несовпавшее автосписание, удержание цикла не поставлено",
			"error", err, "payment_id", payment.ID)
	}
	telegramID, paymentID := payment.TelegramID, payment.ID
	msg := autorenewMismatchUserText(mismatch)
	return func() { b.sendAutorenewMismatchUserNotice(telegramID, paymentID, msg) }
}

// sendAutorenewMismatchUserNotice отправляет одно сообщение на платёж, с какого
// бы входа несовпадение ни пришло. Пометка занимается до отправки, чтобы
// параллельные входы не прислали дубль, и снимается, если Telegram сообщение не
// принял: следующий вход (сверка, повторный вебхук) повторит его. Дедупликация
// живёт в памяти — после перезапуска допустимо ещё одно сообщение.
func (b *Bot) sendAutorenewMismatchUserNotice(telegramID, paymentID int64, msg string) {
	if _, alreadySent := b.autorenewMismatchNotified.LoadOrStore(paymentID, struct{}{}); alreadySent {
		return
	}
	if err := b.sendSchedulerMessage(telegramID, msg); err != nil {
		b.autorenewMismatchNotified.Delete(paymentID)
		slog.Error("Не удалось сообщить человеку о несовпавшем автосписании, повторим при следующей встрече",
			"error", err, "payment_id", paymentID, "telegram_id", telegramID)
	}
}

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
