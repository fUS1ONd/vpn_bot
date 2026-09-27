package bot

import (
	"testing"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/stretchr/testify/assert"
)

// Оповещения владельцу о непринятой и о воскрешённой оплате дедуплицируются по
// платежу. Пометка «уже сообщили» должна означать доставленное сообщение: если
// Telegram его не принял, следующий вход обязан прислать его снова, иначе о
// деньгах никто не узнает до перезапуска.

const (
	ignoredAlertMarker = "не допускает подтверждения"
	revivedAlertMarker = "был локально закрыт"
)

func newAlertOnceBot() *Bot {
	return &Bot{config: &config.Config{AdminID: 999}}
}

func alertOncePayment() *database.Payment {
	return &database.Payment{ID: 77, TelegramID: 4242, Amount: 400, Status: "expired"}
}

func TestНепринятаяОплата_ОповещениеНеДоставлено_СледующийВходПовторяетЕго(t *testing.T) {
	b := newAlertOnceBot()
	delivered := failFirstTelegramSend(t, b)

	b.reportIgnoredPaymentConfirmation(alertOncePayment())
	b.reportIgnoredPaymentConfirmation(alertOncePayment())

	assert.Len(t, delivered.matching(ignoredAlertMarker), 1,
		"первое оповещение отвергнуто Telegram — владелец так и не узнал о непринятой оплате")
}

func TestНепринятаяОплата_ОповещениеДоставлено_ВторойВходМолчит(t *testing.T) {
	b := newAlertOnceBot()
	tg := captureTelegram(t, b)

	b.reportIgnoredPaymentConfirmation(alertOncePayment())
	b.reportIgnoredPaymentConfirmation(alertOncePayment())

	assert.Len(t, tg.matching(ignoredAlertMarker), 1, "одно сообщение на платёж")
}

func TestВоскрешённыйПлатёж_ОповещениеНеДоставлено_СледующийВходПовторяетЕго(t *testing.T) {
	b := newAlertOnceBot()
	delivered := failFirstTelegramSend(t, b)

	b.reportRevivedPayment(alertOncePayment())
	b.reportRevivedPayment(alertOncePayment())

	assert.Len(t, delivered.matching(revivedAlertMarker), 1,
		"первое оповещение отвергнуто Telegram — владелец так и не узнал о воскрешённом платеже")
}

func TestВоскрешённыйПлатёж_ОповещениеДоставлено_ВторойВходМолчит(t *testing.T) {
	b := newAlertOnceBot()
	tg := captureTelegram(t, b)

	b.reportRevivedPayment(alertOncePayment())
	b.reportRevivedPayment(alertOncePayment())

	assert.Len(t, tg.matching(revivedAlertMarker), 1, "одно сообщение на платёж")
}
