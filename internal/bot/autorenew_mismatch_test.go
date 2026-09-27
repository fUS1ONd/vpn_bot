package bot

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
)

// autorenewMismatchUserMarker — фрагмент сообщения человеку о несовпавшем
// автосписании: главное в нём — не платить второй раз.
const autorenewMismatchUserMarker = "повторно не нужно"

// autorenewMismatchBody — ответ кассы по автосписанию с чужим получателем:
// деньги (если статус succeeded) списаны, но ответ не сошёлся с записью.
func autorenewMismatchBody(id, status string) string {
	return `{"id":"` + id + `","status":"` + status + `","amount":{"value":"400.00","currency":"RUB"},
		"recipient":{"account_id":"other-shop"}}`
}

// userMismatchNotices — сообщения человеку о несовпавшем автосписании.
func userMismatchNotices(capture *telegramCapture) []sentMessage {
	var out []sentMessage
	for _, m := range messagesTo(capture, arEdgeUserID) {
		if strings.Contains(m.Text, autorenewMismatchUserMarker) {
			out = append(out, m)
		}
	}
	return out
}

// Деньги списаны без участия человека, а подписка не продлена: он должен
// узнать об этом от нас, иначе заплатит второй раз руками или пойдёт в банк.
// Вебхук и сверка по тому же платежу второго сообщения не дают.
func TestАвтосписание_НесовпадениеПриСписании_ЧеловекПолучаетОдноСообщение(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &arEdgeStub{expireAt: expireAt, responses: []string{
		autorenewMismatchBody("yo-mm-user-charge", "succeeded"),
	}}
	b, _, capture := setupAutorenewEdgeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())
	require.NoError(t, b.HandleYooKassaWebhook("payment.succeeded", "yo-mm-user-charge"))
	b.reconcilePendingPayments(time.Now().UTC().Add(time.Hour))

	notices := userMismatchNotices(capture)
	require.Len(t, notices, 1, "одно сообщение человеку на платёж, с какого бы входа ни пришло")
	assert.Contains(t, notices[0].Text, "400 ₽")
	assert.Contains(t, notices[0].Text, "автопродление")
	assert.NotContains(t, notices[0].Text, "попытка", "второй попытки по названному кассой платежу не будет")
}

// Касса ответила на автосписание pending, а сверка позже увидела «оплачено» с
// несовпадением: человек узнаёт о списании от сверки, и один раз, сколько бы
// проходов ни повторили то же несовпадение.
func TestАвтосписание_ДозрелоДоОплаченоНаСверке_ЧеловекПолучаетОдноСообщение(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &arEdgeStub{expireAt: expireAt, responses: []string{
		autorenewMismatchBody("yo-mm-user-reconcile", "pending"),
		autorenewMismatchBody("yo-mm-user-reconcile", "succeeded"),
	}}
	b, _, capture := setupAutorenewEdgeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())
	require.Empty(t, userMismatchNotices(capture), "за pending денег нет — писать не о чем")

	b.reconcilePendingPayments(time.Now().UTC().Add(time.Hour))
	b.reconcilePendingPayments(time.Now().UTC().Add(2 * time.Hour))

	assert.Len(t, userMismatchNotices(capture), 1)
}

// Несовпавший pending, дозревший до canceled: денег нет — ни сверка, ни вебхук
// человеку про списание не пишут.
func TestАвтосписание_НесовпадениеПриОтмене_ЧеловекуНичегоНеПишем(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &arEdgeStub{expireAt: expireAt, responses: []string{
		autorenewMismatchBody("yo-mm-user-canceled", "pending"),
		autorenewMismatchBody("yo-mm-user-canceled", "canceled"),
	}}
	b, _, capture := setupAutorenewEdgeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())
	b.reconcilePendingPayments(time.Now().UTC().Add(time.Hour))
	require.NoError(t, b.HandleYooKassaWebhook("payment.canceled", "yo-mm-user-canceled"))

	assert.Empty(t, userMismatchNotices(capture))
}

// Telegram не принял сообщение человеку (429 — точно не доставлено): пометка
// снимается, и следующий вход доводит сообщение. Владелец при этом получает своё
// одно сообщение — его дедупликация от недоставки человеку не зависит.
func TestАвтосписание_СообщениеЧеловекуНеДоставлено_СледующийВходПовторяет(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &arEdgeStub{expireAt: expireAt, responses: []string{
		autorenewMismatchBody("yo-mm-user-retry", "succeeded"),
	}}
	b, _, _ := setupAutorenewEdgeBot(t, stub)
	tg := userTelegram(t, b, true)

	b.runAutorenewCharges(time.Now().UTC())
	require.Empty(t, userMismatchNotices(tg.capture), "предпосылка: первое сообщение человеку отвергнуто")

	b.reconcilePendingPayments(time.Now().UTC().Add(time.Hour))
	b.reconcilePendingPayments(time.Now().UTC().Add(2 * time.Hour))

	assert.Len(t, userMismatchNotices(tg.capture), 1, "следующий вход доводит сообщение, дальше — тишина")
	assert.Len(t, messagesTo(tg.capture, b.config.AdminID), 1, "владелец получил одно сообщение")
}

// Недоставка владельцу не гасит сообщение человеку и не дублирует его.
func TestАвтосписание_АлертВладельцуНеДоставлен_ЧеловекПолучаетОдноСообщение(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &arEdgeStub{expireAt: expireAt, responses: []string{
		autorenewMismatchBody("yo-mm-owner-retry", "succeeded"),
	}}
	b, _, _ := setupAutorenewEdgeBot(t, stub)
	delivered := failFirstTelegramSend(t, b) // первым уходит алерт владельцу

	b.runAutorenewCharges(time.Now().UTC())
	b.reconcilePendingPayments(time.Now().UTC().Add(time.Hour))

	assert.Len(t, userMismatchNotices(delivered), 1)
	assert.Len(t, delivered.matching(mismatchAlertMarker), 1, "владелец получил своё на следующем входе")
}

// Сообщение человеку уходит вне getPaymentMutex на всех трёх входах: медленный
// Telegram не должен держать платежи человека.
func TestАвтосписание_СообщениеЧеловекуУходитВнеМьютекса(t *testing.T) {
	entries := map[string]func(b *Bot){
		"автосписание": func(b *Bot) { b.runAutorenewCharges(time.Now().UTC()) },
		"сверка": func(b *Bot) {
			b.runAutorenewCharges(time.Now().UTC())
			b.reconcilePendingPayments(time.Now().UTC().Add(time.Hour))
		},
		"вебхук": func(b *Bot) {
			b.runAutorenewCharges(time.Now().UTC())
			require.NoError(t, b.HandleYooKassaWebhook("payment.succeeded", "yo-mm-user-mutex"))
		},
	}
	for name, run := range entries {
		t.Run(name, func(t *testing.T) {
			responses := []string{autorenewMismatchBody("yo-mm-user-mutex", "succeeded")}
			if name != "автосписание" {
				responses = []string{
					autorenewMismatchBody("yo-mm-user-mutex", "pending"),
					autorenewMismatchBody("yo-mm-user-mutex", "succeeded"),
				}
			}
			stub := &arEdgeStub{expireAt: time.Now().UTC().Add(6 * time.Hour), responses: responses}
			b, _, _ := setupAutorenewEdgeBot(t, stub)
			tg := userTelegram(t, b, false)

			run(b)

			require.Len(t, userMismatchNotices(tg.capture), 1)
			assert.False(t, tg.lockedDuringSend.Load(), "сообщение человеку отправлено под мьютексом платежей")
		})
	}
}

// Обычный платёж с несовпавшим «оплачено»: человеку ничего нового не уходит —
// на ручной проверке он уже видит свой текст, а сверка и вебхук молчат.
func TestНесовпадение_ОбычныйПлатёж_ЧеловекуНичегоНовогоНеПишем(t *testing.T) {
	stub := &yooKassaStub{}
	stub.set("succeeded", "")
	stub.setMismatch("", "other-shop")
	b, db, capture := newReconcileTestBot(t, stub)
	pendingYooKassaPayment(t, db, time.Hour)

	b.reconcilePendingPayments(time.Now().UTC())

	require.Len(t, capture.matching(mismatchAlertMarker), 1, "предпосылка: владелец узнал о несовпадении")
	assert.Empty(t, messagesTo(capture, reconcileTestUserID))
}

// Человеку сказали «платить повторно не нужно» — следом «продлите сейчас» с
// кнопкой оплаты прямо противоречило бы этому и толкало ко второй оплате.
func TestАвтосписание_НесовпавшееОплачено_НапоминанийОбОкончанииНет(t *testing.T) {
	expireAt := time.Now().UTC().Add(20 * time.Hour)
	stub := &arEdgeStub{expireAt: expireAt, responses: []string{
		autorenewMismatchBody("yo-mm-hold-notice", "succeeded"),
	}}
	b, _, capture := setupAutorenewEdgeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())
	ref, ok := b.resolveUserRef(arEdgeUserID)
	require.True(t, ok)
	b.processPaidUser(arEdgeUserID, ref, expireAt, time.Now().UTC())

	require.Len(t, userMismatchNotices(capture), 1)
	for _, m := range messagesTo(capture, arEdgeUserID) {
		assert.NotContains(t, m.Text, "заканчивается", "напоминание противоречит «платить повторно не нужно»")
	}
}

// Подписка истекла, а владелец ещё не разобрал списанные деньги: не отключаем и
// не кикаем даже после трёх суток grace — человек заплатил.
func TestАвтосписание_НесовпавшееОплачено_НеОтключаемИНеКикаемДоРазбора(t *testing.T) {
	expireAt := time.Now().UTC().Add(20 * time.Hour)
	stub := &arEdgeStub{expireAt: expireAt, responses: []string{
		autorenewMismatchBody("yo-mm-hold-kick", "succeeded"),
	}}
	b, _, capture := setupAutorenewEdgeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())
	ref, ok := b.resolveUserRef(arEdgeUserID)
	require.True(t, ok)
	b.processPaidUser(arEdgeUserID, ref, expireAt, expireAt.Add(time.Hour))
	b.processPaidUser(arEdgeUserID, ref, expireAt, expireAt.Add(73*time.Hour))

	for _, m := range messagesTo(capture, arEdgeUserID) {
		assert.NotContains(t, m.Text, "истекла", "отключать человека, с которого списаны деньги, нельзя")
		assert.NotContains(t, m.Text, "удалён", "кикать человека, с которого списаны деньги, нельзя")
	}
	assert.Zero(t, stub.panelDisables(), "в панели пользователь не отключён")
}

// Владелец разобрал платёж (деньги вернул): удержание снято, человек
// отключается штатно, но три дня grace отсчитываются от разбора, а не от
// давно прошедшего expireAt — иначе «подписка истекла» и «доступ удалён»
// пришли бы в одном проходе.
func TestАвтосписание_РазобраноВладельцем_ОтключениеСGraceОтРазбора(t *testing.T) {
	expireAt := time.Now().UTC().Add(-100 * time.Hour)
	stub := &arEdgeStub{expireAt: expireAt, responses: []string{
		autorenewMismatchBody("yo-mm-hold-release", "succeeded"),
	}}
	b, db, capture := setupAutorenewEdgeBot(t, stub)
	markHeldCycle(t, b, db, expireAt, "yo-mm-hold-release")

	resolved, err := db.ResolveAutorenewMismatches(arEdgeUserID)
	require.NoError(t, err)
	require.Equal(t, int64(1), resolved)

	ref, ok := b.resolveUserRef(arEdgeUserID)
	require.True(t, ok)
	now := time.Now().UTC()
	b.processPaidUser(arEdgeUserID, ref, expireAt, now)

	var expired, kicked int
	for _, m := range messagesTo(capture, arEdgeUserID) {
		if strings.Contains(m.Text, "истекла") {
			expired++
		}
		if strings.Contains(m.Text, "доступ удалён") {
			kicked++
		}
	}
	assert.Equal(t, 1, expired, "после разбора — штатное отключение")
	assert.Zero(t, kicked, "grace считается от разбора: кикать в том же проходе нельзя")

	b.processPaidUser(arEdgeUserID, ref, expireAt, now.Add(73*time.Hour))
	assert.NotEmpty(t, capture.matching("доступ удалён"), "три дня после разбора — штатный кик")
}

// Владелец видит удержание в карточке пользователя и снимает его сам — с
// подтверждением: снятие ведёт к отключению человека, с которого списаны деньги.
func TestАвтосписание_КарточкаАдмина_УдержаниеВидноИСнимаетсяСПодтверждением(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(-2 * time.Hour), responses: []string{
		autorenewMismatchBody("yo-mm-hold-card", "succeeded"),
	}}
	b, db, _ := setupAutorenewEdgeBot(t, stub)
	markHeldCycle(t, b, db, stub.expireAt, "yo-mm-hold-card")

	text, markup, err := b.buildAdminUserInfo(arEdgeUserID)
	require.NoError(t, err)
	assert.Contains(t, text, "Отключение приостановлено")
	resolve := findInlineButton(markup, cbAdminMismatchResolve)
	require.NotNil(t, resolve, "в карточке есть кнопка разбора")

	data := fmt.Sprint(arEdgeUserID)
	ask := &MockContext{sender: &tele.User{ID: b.config.AdminID}, message: &tele.Message{},
		callback: &tele.Callback{Data: data}}
	require.NoError(t, b.handleAdminMismatchResolve(ask))
	require.NotNil(t, ask.editedMsg, "сначала — экран подтверждения")
	hold, err := db.AutorenewMismatchHold(arEdgeUserID, stub.expireAt)
	require.NoError(t, err)
	require.True(t, hold.Active, "без подтверждения удержание не снимается")

	confirm := &MockContext{sender: &tele.User{ID: b.config.AdminID}, message: &tele.Message{},
		callback: &tele.Callback{Data: data}}
	require.NoError(t, b.handleAdminMismatchResolveConfirm(confirm))
	hold, err = db.AutorenewMismatchHold(arEdgeUserID, stub.expireAt)
	require.NoError(t, err)
	assert.False(t, hold.Active)
	assert.NotContains(t, fmt.Sprint(confirm.editedMsg), "Отключение приостановлено", "карточка перерисована")

	stranger := &MockContext{sender: &tele.User{ID: arEdgeUserID}, message: &tele.Message{},
		callback: &tele.Callback{Data: data}}
	require.NoError(t, b.handleAdminMismatchResolveConfirm(stranger))
	assert.Nil(t, stranger.editedMsg, "не владельцу кнопка ничего не делает")
}

// Владелец из алерта узнаёт, что человек удержан, и где снять удержание:
// иначе он разберёт деньги в кабинете кассы, а человек так и останется
// неотключаемым.
func TestАвтосписание_АлертВладельцу_ГоворитОбУдержании(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(6 * time.Hour), responses: []string{
		autorenewMismatchBody("yo-mm-hold-alert", "succeeded"),
	}}
	b, _, capture := setupAutorenewEdgeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())

	alerts := capture.matching(mismatchAlertMarker)
	require.Len(t, alerts, 1)
	assert.Contains(t, alerts[0].Text, "Отключение приостановлено")
	assert.Contains(t, alerts[0].Text, "Автосписание разобрано")
}

// То же, когда несовпадение впервые всплыло не на шаге списания, а в сверке:
// алерт всё равно говорит, что это автосписание и что человек удержан.
func TestАвтосписание_АлертВладельцуИзСверки_ГоворитОбАвтосписанииИУдержании(t *testing.T) {
	stub := &arEdgeStub{expireAt: time.Now().UTC().Add(6 * time.Hour), responses: []string{
		autorenewMismatchBody("yo-mm-hold-alert-rec", "pending"),
		autorenewMismatchBody("yo-mm-hold-alert-rec", "succeeded"),
	}}
	b, _, capture := setupAutorenewEdgeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())
	b.reconcilePendingPayments(time.Now().UTC().Add(time.Hour))

	alerts := capture.matching(mismatchAlertMarker)
	require.Len(t, alerts, 1)
	assert.Contains(t, alerts[0].Text, "Автосписание #")
	assert.Contains(t, alerts[0].Text, "Отключение приостановлено")
}

// Человек сам оплатил вручную, expireAt сдвинулся — удержание цикла ушло, но
// несовпавшее списание никто не разобрал, и деньги могли уйти дважды: карточка
// обязана продолжать показывать его владельцу вместе с кнопкой разбора.
func TestАвтосписание_КарточкаАдмина_НеразобранноеВидноИПослеСдвигаЦикла(t *testing.T) {
	oldCycle := time.Now().UTC().Add(-2 * time.Hour)
	stub := &arEdgeStub{expireAt: oldCycle.AddDate(0, 1, 0), responses: []string{
		autorenewMismatchBody("yo-mm-hold-moved", "succeeded"),
	}}
	b, db, _ := setupAutorenewEdgeBot(t, stub)
	markHeldCycle(t, b, db, oldCycle, "yo-mm-hold-moved")

	text, markup, err := b.buildAdminUserInfo(arEdgeUserID)
	require.NoError(t, err)
	assert.Contains(t, text, "ждёт разбора")
	assert.NotContains(t, text, "Отключение приостановлено", "цикл новый — удержания уже нет")
	assert.NotNil(t, findInlineButton(markup, cbAdminMismatchResolve))
}

// findInlineButton ищет inline-кнопку по Unique.
func findInlineButton(markup *tele.ReplyMarkup, unique string) *tele.InlineButton {
	if markup == nil {
		return nil
	}
	for _, row := range markup.InlineKeyboard {
		for i := range row {
			if row[i].Unique == unique {
				return &row[i]
			}
		}
	}
	return nil
}

// markHeldCycle заводит цикл, который держит несовпавшее «оплачено»: запись
// автосписания с попыткой цикла expireAt, помеченная как несовпавшая.
func markHeldCycle(t *testing.T, b *Bot, db *database.DB, expireAt time.Time, providerID string) int64 {
	t.Helper()
	payment, err := b.autorenewChargePayment(arEdgeUserID, 400, expireAt)
	require.NoError(t, err)
	require.NoError(t, db.SetProviderPaymentDetails(payment.ID, providerID, "", nil))
	require.NoError(t, db.RecordAutorenewAttempt(&database.AutorenewAttempt{
		TelegramID: arEdgeUserID, ExpireAt: expireAt, AttemptNo: 1,
		Outcome: database.AutorenewOutcomeUnknown, PaymentID: &payment.ID,
	}))
	require.NoError(t, db.MarkPaidMismatch(payment.ID))
	return payment.ID
}

// userTelegramStub — Telegram, который запоминает доставленные сообщения,
// может отвергнуть первое сообщение человеку и отмечает, была ли отправка
// человеку сделана под мьютексом его платежей.
type userTelegramStub struct {
	capture          *telegramCapture
	lockedDuringSend atomic.Bool
}

func userTelegram(t *testing.T, b *Bot, failFirstToUser bool) *userTelegramStub {
	t.Helper()
	stub := &userTelegramStub{capture: &telegramCapture{}}
	var userSends atomic.Int32
	b.bot = newOfflineTelegramBotForTest(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "/sendMessage") {
			return nil, nil
		}
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		msg := parseSentMessage(string(body))
		if msg.ChatID == fmt.Sprint(arEdgeUserID) {
			mu := getPaymentMutex(arEdgeUserID)
			if mu.TryLock() {
				mu.Unlock()
			} else {
				stub.lockedDuringSend.Store(true)
			}
			if userSends.Add(1) == 1 && failFirstToUser {
				return &http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header),
					Body: io.NopCloser(strings.NewReader(
						`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 1","parameters":{"retry_after":1}}`))}, nil
			}
		}
		stub.capture.mu.Lock()
		stub.capture.messages = append(stub.capture.messages, msg)
		stub.capture.mu.Unlock()
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"ok":true,"result":{"message_id":1,"date":1710000000,"chat":{"id":1,"type":"private"},"text":"ok"}}`))}, nil
	}))
	return stub
}

// Человек нажал «Проверить оплату», пока запись автосписания ещё висит pending:
// ручная проверка — такой же вход, и сообщение о списании одно на платёж —
// сверка после неё второго не присылает.
func TestАвтосписание_НесовпадениеНаРучнойПроверке_ЧеловекПолучаетОдноСообщение(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &arEdgeStub{expireAt: expireAt, responses: []string{
		autorenewMismatchBody("yo-mm-user-manual", "pending"),
		autorenewMismatchBody("yo-mm-user-manual", "succeeded"),
	}}
	b, _, _ := setupAutorenewEdgeBot(t, stub)
	tg := userTelegram(t, b, false)

	b.runAutorenewCharges(time.Now().UTC())
	_, err := b.checkPaymentStatus(arEdgeUserID)
	require.ErrorIs(t, err, errPaymentMismatch, "предпосылка: ручная проверка нашла запись автосписания")
	require.Len(t, userMismatchNotices(tg.capture), 1, "ручная проверка — такой же вход")
	b.reconcilePendingPayments(time.Now().UTC().Add(time.Hour))

	assert.Len(t, userMismatchNotices(tg.capture), 1)
	assert.False(t, tg.lockedDuringSend.Load(), "сообщение человеку отправлено под мьютексом платежей")
}

// То же через вебхук ЮKassa: повторная доставка второго сообщения не даёт.
func TestАвтосписание_ДозрелоДоОплаченоНаВебхуке_ЧеловекПолучаетОдноСообщение(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	stub := &arEdgeStub{expireAt: expireAt, responses: []string{
		autorenewMismatchBody("yo-mm-user-webhook", "pending"),
		autorenewMismatchBody("yo-mm-user-webhook", "succeeded"),
	}}
	b, _, capture := setupAutorenewEdgeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())
	require.NoError(t, b.HandleYooKassaWebhook("payment.succeeded", "yo-mm-user-webhook"))
	require.NoError(t, b.HandleYooKassaWebhook("payment.succeeded", "yo-mm-user-webhook"))

	assert.Len(t, userMismatchNotices(capture), 1)
}
