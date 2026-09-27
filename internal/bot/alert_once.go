package bot

import (
	"log/slog"
	"sync"
)

// Дедуплицированные сообщения по платежу: одно сообщение на payment_id, с какого
// бы входа (вебхук, сверка, кнопка, автосписание) повод ни пришёл. Пометка
// «уже сообщили» живёт в памяти и обязана означать доставленное сообщение: не
// принятое Telegram сообщение пометку снимает, и его повторит следующий вход, а
// не перезапуск бота.

// deliverOnce занимает пометку paymentID в reported и возвращает отправку.
// Пометка занимается сразу, до отправки: параллельные входы дубля не пришлют.
// Отправка при ошибке снимает пометку и пишет slog.Error; результат — доставлено
// ли сообщение этим вызовом. Если пометка уже занята, возвращается отправка,
// которая ничего не делает и отвечает false.
//
// Занятие и отправка разделены намеренно: решение «сообщать или нет»
// принимается там, где известны данные платежа (в том числе под мьютексом
// платежа), а сетевой вызов можно выполнить позже, вне критической секции.
// Когда разделять нечего, отправку вызывают сразу: deliverOnce(...)().
func deliverOnce(reported *sync.Map, paymentID int64, what string, send func() error) func() bool {
	if _, alreadyReported := reported.LoadOrStore(paymentID, struct{}{}); alreadyReported {
		return func() bool { return false }
	}
	return func() bool {
		if err := send(); err != nil {
			reported.Delete(paymentID)
			slog.Error("Сообщение не доставлено, повторим при следующей встрече",
				"what", what, "error", err, "payment_id", paymentID)
			return false
		}
		return true
	}
}

// adminAlertOnce — deliverOnce для сообщения владельцу.
func (b *Bot) adminAlertOnce(reported *sync.Map, paymentID int64, what, msg string) func() bool {
	return deliverOnce(reported, paymentID, what, func() error {
		return b.sendSchedulerMessage(b.config.AdminID, msg)
	})
}
