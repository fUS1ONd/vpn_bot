package bot

import (
	"errors"
	"fmt"
	"html"
	"log/slog"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
)

// Несовпадение ответа провайдера: провайдер ответил, но ответ не сошёлся с
// локальной записью платежа (идентификатор, сумма, валюта, у ЮKassa — получатель).
// Это не молчание провайдера: ждать тут нечего, ответ уже есть, и он другой.
// Сверка, вебхук ЮKassa, ручная проверка и автосписание подписку по такому
// ответу не выдают, а если провайдер говорит «оплачено», владелец узнаёт об
// этом — деньги у провайдера есть. Callback Platega тело не сверяет и сюда не
// ходит — известное ограничение, отдельная задача.

// errPaymentMismatch отличает несовпадение от недоступности провайдера и от
// сбоев нашей базы: проверять через errors.Is.
var errPaymentMismatch = errors.New("ответ провайдера не сошёлся с записью платежа")

// paymentMismatchError несёт то, что нужно логу и оповещению. Статус провайдера
// лежит здесь, чтобы вызывающий решил судьбу оповещения без повторного запроса.
type paymentMismatchError struct {
	PaymentID         int64
	Provider          string
	LocalProviderID   string // идентификатор платежа у провайдера по нашей записи
	ProviderID        string // идентификатор в ответе провайдера
	LocalAmount       int
	ProviderAmount    int
	Currency          string
	ExpectedRecipient string // пусто, если получатель не сверяется (Platega)
	Recipient         string
	ProviderStatus    string
}

func (e *paymentMismatchError) Error() string {
	msg := fmt.Sprintf("%s: платёж #%d, провайдер %s, статус %q, сумма %d/%d, валюта %q, id %q/%q",
		errPaymentMismatch, e.PaymentID, e.Provider, e.ProviderStatus, e.LocalAmount, e.ProviderAmount,
		e.Currency, e.LocalProviderID, e.ProviderID)
	if e.recipientChecked() {
		msg += fmt.Sprintf(", получатель %q/%q", e.ExpectedRecipient, e.Recipient)
	}
	return msg
}

// recipientChecked — сверялся ли получатель (у Platega его в ответе нет).
func (e *paymentMismatchError) recipientChecked() bool {
	return e.ExpectedRecipient != "" || e.Recipient != ""
}

func (e *paymentMismatchError) Is(target error) bool { return target == errPaymentMismatch }

// asPaymentMismatch достаёт подробности несовпадения из цепочки ошибок.
func asPaymentMismatch(err error) (*paymentMismatchError, bool) {
	var mismatch *paymentMismatchError
	if errors.As(err, &mismatch) {
		return mismatch, true
	}
	return nil, false
}

// findPaymentMismatch сверяет ответ с записью: nil, если сошлись, иначе —
// подробности несовпадения. checkRecipient включает сверку получателя (у ЮKassa
// она есть, у Platega — нет).
func findPaymentMismatch(payment *database.Payment, verified *paymentprovider.Payment, expectedRecipient string, checkRecipient bool) *paymentMismatchError {
	localID := ""
	if payment.ProviderPaymentID != nil {
		localID = *payment.ProviderPaymentID
	}
	matches := payment.ProviderPaymentID != nil && verified.ID == localID &&
		verified.Amount == payment.Amount && verified.Currency == "RUB" &&
		(!checkRecipient || verified.RecipientID == expectedRecipient)
	if matches {
		return nil
	}
	mismatch := &paymentMismatchError{
		PaymentID:       payment.ID,
		Provider:        payment.Provider,
		LocalProviderID: localID,
		ProviderID:      verified.ID,
		LocalAmount:     payment.Amount,
		ProviderAmount:  verified.Amount,
		Currency:        verified.Currency,
		ProviderStatus:  verified.Status,
	}
	if checkRecipient {
		mismatch.ExpectedRecipient = expectedRecipient
		mismatch.Recipient = verified.RecipientID
	}
	return mismatch
}

// reportPaymentMismatch — единственная точка, где несовпадение становится видно.
// Лог уровня Error пишется всегда: несовпадение само по себе повод для разбора.
// Владелец получает сообщение, только если провайдер говорит «оплачено» (за
// неоплаченным и отменённым денег нет), и один раз на платёж: сверка видит
// несовпадение каждые 30 минут, вебхук повторяется. Дедупликация живёт в памяти —
// после перезапуска допустимо ещё одно сообщение. Пометка ставится только при
// статусе «оплачено», поэтому платёж, который стал «оплачено» после несовпавшего
// pending, своё сообщение получит; не принятое Telegram сообщение пометку снимает,
// и его повторит следующий вход.
func (b *Bot) reportPaymentMismatch(payment *database.Payment, mismatch *paymentMismatchError, source string) {
	headline := fmt.Sprintf("⚠️ Платёж #%d (пользователь %d): провайдер %s говорит «оплачено», но ответ не сошёлся с записью платежа.",
		payment.ID, payment.TelegramID, html.EscapeString(payment.Provider))
	b.reportPaymentMismatchWithHeadline(payment, mismatch, source, headline)
}

func (b *Bot) reportPaymentMismatchWithHeadline(payment *database.Payment, mismatch *paymentMismatchError, source, headline string) {
	slog.Error("Ответ провайдера не сошёлся с записью платежа",
		"payment_id", payment.ID, "telegram_id", payment.TelegramID, "provider", payment.Provider,
		"local_status", payment.Status, "provider_status", mismatch.ProviderStatus,
		"local_amount", mismatch.LocalAmount, "provider_amount", mismatch.ProviderAmount,
		"currency", mismatch.Currency,
		"local_provider_payment_id", mismatch.LocalProviderID, "provider_payment_id", mismatch.ProviderID,
		"expected_recipient", mismatch.ExpectedRecipient, "recipient", mismatch.Recipient,
		"source", source)

	if mismatch.ProviderStatus != paymentprovider.StatusSucceeded {
		return
	}
	recipient := ""
	if mismatch.recipientChecked() {
		recipient = fmt.Sprintf("\nПолучатель: наш <code>%s</code>, в ответе <code>%s</code>",
			html.EscapeString(mismatch.ExpectedRecipient), html.EscapeString(mismatch.Recipient))
	}
	b.sendPaymentMismatchAlert(payment.ID, fmt.Sprintf(
		"%s\n\n"+
			"Сумма: наша %d ₽, в ответе %d\nВалюта: <code>%s</code>\n"+
			"Идентификатор: наш <code>%s</code>, в ответе <code>%s</code>%s\nСтатус провайдера: <b>%s</b>\n\n"+
			"Подписка по этому ответу не выдана, чек не пробит — разберите операцию вручную.",
		headline,
		mismatch.LocalAmount, mismatch.ProviderAmount, html.EscapeString(mismatch.Currency),
		html.EscapeString(mismatch.LocalProviderID), html.EscapeString(mismatch.ProviderID), recipient,
		html.EscapeString(mismatch.ProviderStatus),
	))
}

// sendPaymentMismatchAlert отправляет владельцу сообщение о несовпадении — одно
// на платёж, с какого бы входа несовпадение ни пришло. Не принятое Telegram
// сообщение пометку снимает (deliverOnce): иначе она стояла бы без сообщения, а
// человеку на ручной проверке сказали бы, что мы разбираемся.
func (b *Bot) sendPaymentMismatchAlert(paymentID int64, msg string) {
	b.adminAlertOnce(&b.paymentMismatchReported, paymentID, "несовпадение ответа провайдера", msg)()
}
