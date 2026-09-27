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
// которая ничего не делает и отвечает false. label — метка для лога.
//
// Возвращённую отправку обязательно вызвать на любом пути, включая ранние
// выходы: занятая и не отправленная пометка — ровно то состояние, от которого
// защищает помощник (сообщения нет, а повторять его никто не станет до
// перезапуска).
//
// Занятие и отправка разделены намеренно: решение «сообщать или нет»
// принимается там, где известны данные платежа (в том числе под мьютексом
// платежа), а сетевой вызов можно выполнить позже, вне критической секции.
// Когда разделять нечего, отправку вызывают сразу: deliverOnce(...)().
func deliverOnce(reported *sync.Map, paymentID int64, label string, send func() error) func() bool {
	if _, alreadyReported := reported.LoadOrStore(paymentID, struct{}{}); alreadyReported {
		return func() bool { return false }
	}
	return func() bool {
		if err := send(); err != nil {
			reported.Delete(paymentID)
			slog.Error("Сообщение не доставлено, повторим при следующей встрече",
				"label", label, "error", err, "payment_id", paymentID)
			return false
		}
		return true
	}
}

// adminAlertOnce — deliverOnce для сообщения владельцу.
func (b *Bot) adminAlertOnce(reported *sync.Map, paymentID int64, label, msg string) func() bool {
	return deliverOnce(reported, paymentID, label, func() error {
		return b.sendSchedulerMessage(b.config.AdminID, msg)
	})
}

// afterUnlock копит отправки, решённые под getPaymentMutex, и выполняет их после
// его снятия — по порядку, в котором они решены. Медленный Telegram иначе держал
// бы ручную оплату, «Проверить оплату», перевыпуск ссылки и автосписание того же
// человека.
//
// Кто берёт мьютекс, тот владеет очередью и объявляет её выполнение ДО захвата:
//
//	var later afterUnlock
//	defer later.run()
//	mu.Lock()
//	defer mu.Unlock()
//
// defer, объявленный раньше Unlock, выполняется позже — и на любом пути, в том
// числе при панике, как того требует deliverOnce. Горутины здесь не подходят
// намеренно: с ними теряется порядок сообщений, а недоставка снимала бы пометку
// уже после того, как следующий вход её проверил.
type afterUnlock struct {
	sends []func() bool
}

// add ставит отправку в очередь. Для дедуплицированных сообщений это замыкание
// deliverOnce: пометка занята уже сейчас, под мьютексом.
func (a *afterUnlock) add(send func() bool) {
	a.sends = append(a.sends, send)
}

// alert ставит в очередь сообщение владельцу без дедупликации.
func (a *afterUnlock) alert(b *Bot, msg string) {
	a.add(func() bool {
		b.sendAdminAlert(msg)
		return true
	})
}

// run выполняет накопленные отправки и опустошает очередь.
func (a *afterUnlock) run() {
	sends := a.sends
	a.sends = nil
	for _, send := range sends {
		send()
	}
}

// adopt переносит в очередь отправки другой очереди — для обработчика,
// заведённого внутри чужой критической секции.
func (a *afterUnlock) adopt(other *afterUnlock) {
	a.sends = append(a.sends, other.sends...)
	other.sends = nil
}
