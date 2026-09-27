package bot

import (
	"fmt"
	"log/slog"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
)

// Несовпадение на платеже автосписания касается не только владельца: деньги
// ушли с карты человека без его участия, а подписка не продлена. Без сообщения
// он заплатит второй раз руками или пойдёт в банк за chargeback. Владельцу о том
// же сообщает reportPaymentMismatch — дедупликации у них раздельные, чтобы
// недоставка одному не гасила и не дублировала сообщение другому.

// autorenewMismatchUserNotice возвращает отправку сообщения человеку о
// несовпавшем автосписании или nil, если писать нечего: платёж не автосписания
// или касса не говорит «оплачено» (за pending и canceled денег нет). Отправка
// отдаётся замыканием, потому что входы держат getPaymentMutex, а сообщения
// пользователю уходят вне мьютекса.
func (b *Bot) autorenewMismatchUserNotice(payment *database.Payment, mismatch *paymentMismatchError) func() {
	if mismatch.ProviderStatus != paymentprovider.StatusSucceeded {
		return nil
	}
	fromAutorenew, err := b.db.IsAutorenewPayment(payment.ID)
	if err != nil {
		slog.Error("Не удалось проверить, автосписание ли несовпавший платёж; человеку не пишем",
			"error", err, "payment_id", payment.ID)
		return nil
	}
	if !fromAutorenew {
		return nil
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
