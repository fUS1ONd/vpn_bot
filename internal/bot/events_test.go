package bot

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/funnels"
	"github.com/fus1ond/vpn_bot/internal/journal"
	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
)

// fakeRecorder — журнал Событий в памяти.
type fakeRecorder struct {
	mu     sync.Mutex
	events []journal.Event
}

func (r *fakeRecorder) Record(event journal.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *fakeRecorder) recorded() []journal.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]journal.Event(nil), r.events...)
}

// Нажатие reply-кнопки «Приглашения» — Действие со стабильным id, от подписи
// не зависящее; время — момент нажатия, а не конец обработки.
func TestEventsMiddleware_RecordsInvitesTap(t *testing.T) {
	recorder := &fakeRecorder{}
	b := &Bot{events: recorder}

	before := time.Now()
	handler := b.eventsMiddleware(func(tele.Context) error {
		time.Sleep(10 * time.Millisecond)
		return nil
	})
	ctx := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{ID: 10, Text: BtnInvites}}
	require.NoError(t, handler(ctx))

	events := recorder.recorded()
	require.Len(t, events, 1)
	assert.Equal(t, int64(42), events[0].TelegramID)
	assert.Equal(t, funnels.ActionInvitesOpen, events[0].Action)
	assert.Equal(t, journal.SourceUser, events[0].Source)
	assert.Empty(t, events[0].Param)
	assert.WithinDuration(t, before, events[0].At, 5*time.Millisecond, "время — момент нажатия")
}

// Событие пишется и тогда, когда обработчик вернул ошибку, а ошибка доходит
// до вызывающего.
func TestEventsMiddleware_RecordsOnHandlerError(t *testing.T) {
	recorder := &fakeRecorder{}
	b := &Bot{events: recorder}
	boom := errors.New("boom")

	handler := b.eventsMiddleware(func(tele.Context) error { return boom })
	ctx := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{ID: 10, Text: BtnInvites}}

	assert.ErrorIs(t, handler(ctx), boom)
	require.Len(t, recorder.recorded(), 1)
}

// Событие пишется после обработчика.
func TestEventsMiddleware_RecordsAfterHandler(t *testing.T) {
	recorder := &fakeRecorder{}
	b := &Bot{events: recorder}

	var seenDuringHandler int
	handler := b.eventsMiddleware(func(tele.Context) error {
		seenDuringHandler = len(recorder.recorded())
		return nil
	})
	ctx := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{ID: 10, Text: BtnInvites}}
	require.NoError(t, handler(ctx))

	assert.Zero(t, seenDuringHandler)
	assert.Len(t, recorder.recorded(), 1)
}

// Набранный человеком текст, совпадающий с подписью лишь частично, — не
// нажатие «Приглашений».
func TestEventsMiddleware_TypedTextIsNotInvitesTap(t *testing.T) {
	recorder := &fakeRecorder{}
	b := &Bot{events: recorder}

	handler := b.eventsMiddleware(func(tele.Context) error { return nil })
	ctx := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{ID: 10, Text: BtnInvites + " не открываются"}}
	require.NoError(t, handler(ctx))

	for _, event := range recorder.recorded() {
		assert.NotEqual(t, funnels.ActionInvitesOpen, event.Action)
	}
}

// Без журнала бот работает как раньше.
func TestEventsMiddleware_WithoutJournal(t *testing.T) {
	b := &Bot{}
	called := false
	handler := b.eventsMiddleware(func(tele.Context) error {
		called = true
		return nil
	})
	ctx := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{ID: 10, Text: BtnInvites}}

	assert.NotPanics(t, func() { require.NoError(t, handler(ctx)) })
	assert.True(t, called)
}

// recordOne прогоняет апдейт через middleware и возвращает единственное
// записанное Событие.
func recordOne(t *testing.T, ctx *MockContext) journal.Event {
	t.Helper()
	recorder := &fakeRecorder{}
	b := &Bot{events: recorder}
	require.NoError(t, b.eventsMiddleware(func(tele.Context) error { return nil })(ctx))
	events := recorder.recorded()
	require.Len(t, events, 1)
	return events[0]
}

// Нажатие inline-кнопки — Действие по её Unique; payload (код приглашения,
// номер устройства) не попадает никуда.
func TestEventsMiddleware_InlineButtonByUnique(t *testing.T) {
	ctx := &MockContext{
		sender:   &tele.User{ID: 42},
		message:  &tele.Message{ID: 10, Text: "текст сообщения с кнопкой"},
		callback: &tele.Callback{Unique: cbReferralResend, Data: "SECRETCODE"},
	}

	event := recordOne(t, ctx)
	assert.Equal(t, cbReferralResend, event.Action)
	assert.Empty(t, event.Param)
	assert.NotContains(t, event.Action, "SECRETCODE")
}

// captureWarnings перехватывает логи на время теста.
func captureWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// Кнопка, у которой нет обработчика (старое сообщение, переименованный
// Unique), приходит сырой строкой «\f<unique>|<payload>»: пишется как
// cb:<unique> с предупреждением, payload по-прежнему не пишется.
func TestEventsMiddleware_UnroutedInlineButton(t *testing.T) {
	logs := captureWarnings(t)
	ctx := &MockContext{
		sender:   &tele.User{ID: 42},
		callback: &tele.Callback{Data: "\fold_button|SECRETCODE"},
	}

	event := recordOne(t, ctx)
	assert.Equal(t, "cb:old_button", event.Action)
	assert.Empty(t, event.Param)
	assert.Contains(t, logs.String(), "old_button")
	assert.NotContains(t, logs.String(), "SECRETCODE")
}

// Данные кнопки может подделать клиент: Unique вне каталога обрезается, и
// длинная строка на выбор нажавшего в журнал целиком не попадает.
func TestEventsMiddleware_UnroutedInlineButtonIsCapped(t *testing.T) {
	captureWarnings(t)
	long := strings.Repeat("a", 60)
	ctx := &MockContext{sender: &tele.User{ID: 42}, callback: &tele.Callback{Data: "\f" + long}}

	event := recordOne(t, ctx)
	assert.Equal(t, "cb:"+strings.Repeat("a", 32), event.Action)
}

// Обработчик есть, а в каталоге Действий кнопки нет — тоже cb:<unique> с
// предупреждением: id вне каталога не должен совпасть с id из каталога.
func TestEventsMiddleware_InlineButtonOutsideCatalog(t *testing.T) {
	logs := captureWarnings(t)
	ctx := &MockContext{
		sender:   &tele.User{ID: 42},
		callback: &tele.Callback{Unique: "brand_new", Data: "SECRETCODE"},
	}

	event := recordOne(t, ctx)
	assert.Equal(t, "cb:brand_new", event.Action)
	assert.Contains(t, logs.String(), "brand_new")
}

// Callback без Unique вовсе (кнопка старого формата без \f) — тот же
// cb:-префикс, данные кнопки в id не попадают.
func TestEventsMiddleware_LegacyInlineButton(t *testing.T) {
	captureWarnings(t)
	ctx := &MockContext{
		sender:   &tele.User{ID: 42},
		callback: &tele.Callback{Data: "SECRETCODE"},
	}

	event := recordOne(t, ctx)
	assert.Equal(t, "cb:", event.Action)
}

// messageEvent — Событие по входящему сообщению.
func messageEvent(t *testing.T, msg *tele.Message) journal.Event {
	t.Helper()
	return recordOne(t, &MockContext{sender: &tele.User{ID: 42}, message: msg})
}

// /start без аргумента и /start с приглашением — разные Действия; код
// приглашения в журнал не попадает.
func TestEventsMiddleware_Start(t *testing.T) {
	plain := messageEvent(t, &tele.Message{Text: "/start"})
	withInvite := messageEvent(t, &tele.Message{Text: "/start ABC123", Payload: "ABC123"})

	assert.Equal(t, actionStart, plain.Action)
	assert.Equal(t, actionStartInvite, withInvite.Action)
	assert.NotEqual(t, plain.Action, withInvite.Action)
	assert.Empty(t, withInvite.Param)
	assert.NotContains(t, withInvite.Action, "ABC123")
}

// /start из пустого ответа «Поделиться» несёт не код приглашения, а
// служебный параметр — это своё Действие, а не «пришёл по приглашению».
func TestEventsMiddleware_StartFromShare(t *testing.T) {
	event := messageEvent(t, &tele.Message{Text: "/start " + StartParamInvites, Payload: StartParamInvites})
	assert.Equal(t, actionStartShare, event.Action)
}

// Прочие команды пишутся по имени, без аргументов; упоминание бота
// (/help@bot) имя не меняет.
func TestEventsMiddleware_OtherCommandByName(t *testing.T) {
	assert.Equal(t, "cmd:help", messageEvent(t, &tele.Message{Text: "/help"}).Action)
	assert.Equal(t, "cmd:help", messageEvent(t, &tele.Message{Text: "/help@some_bot"}).Action)
	assert.Equal(t, "cmd:help", messageEvent(t, &tele.Message{Text: "/Help мой пароль 12345"}).Action)
	assert.Equal(t, "cmd:settings", messageEvent(t, &tele.Message{Text: "/settings"}).Action)
}

// Reply-кнопки пишутся по карте, а не подписью: несколько кнопок разных
// экранов, включая «Да», которого нет среди убираемых из чата подписей.
func TestEventsMiddleware_ReplyButtonsByCatalog(t *testing.T) {
	cases := map[string]string{
		BtnStatus:       "subscription_open",
		BtnPay:          "pay_menu",
		BtnInviteCreate: "invite_create",
		BtnAdminFunnels: "admin_funnels",
		BtnConfirmYes:   "confirm_yes",
	}
	for caption, want := range cases {
		assert.Equal(t, want, messageEvent(t, &tele.Message{Text: caption}).Action, caption)
	}
}

// Имя команды набирает человек: имя вне белого списка — уже содержимое
// (/hunter2), и в журнал оно не попадает.
func TestEventsMiddleware_UnknownCommandHidesName(t *testing.T) {
	event := messageEvent(t, &tele.Message{Text: "/hunter2"})
	assert.Equal(t, actionOtherCommand, event.Action)
	assert.NotContains(t, event.Action, "hunter2")
}

// Присланный текст, голосовое, кружок и медиа — Действие-факт: что пришло,
// а не что в нём. Ни текст, ни подпись к медиа в Событие не попадают.
func TestEventsMiddleware_MessageFacts(t *testing.T) {
	cases := []struct {
		name string
		msg  *tele.Message
		want string
	}{
		{"текст", &tele.Message{Text: "мой адрес ул. Ленина 1"}, actionText},
		{"текст, похожий на кнопку", &tele.Message{Text: BtnInvites + " не открываются"}, actionText},
		{"голосовое", &tele.Message{Voice: &tele.Voice{}}, actionVoice},
		{"кружок", &tele.Message{VideoNote: &tele.VideoNote{}}, actionVideoNote},
		{"фото", &tele.Message{Photo: &tele.Photo{}, Caption: "подпись"}, actionMedia},
		{"видео", &tele.Message{Video: &tele.Video{}, Caption: "подпись"}, actionMedia},
		{"документ", &tele.Message{Document: &tele.Document{}}, actionMedia},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event := messageEvent(t, tc.msg)
			assert.Equal(t, tc.want, event.Action)
			assert.Empty(t, event.Param)
		})
	}
}

// Кнопка без обработчика получает ответ: без него «часики» на ней крутятся
// до таймаута Telegram.
func TestHandleUnroutedCallback_Responds(t *testing.T) {
	b := &Bot{}
	ctx := &MockContext{sender: &tele.User{ID: 42}, callback: &tele.Callback{Data: "\fold_button|x"}}

	require.NoError(t, b.handleUnroutedCallback(ctx))
	assert.True(t, ctx.responded)
}

// Inline-запрос «Поделиться» — отдельное Действие: человек открыл выбор чата.
// Набранный в запросе текст (это может быть код приглашения) не пишется.
func TestEventsMiddleware_RecordsShareQuery(t *testing.T) {
	recorder := &fakeRecorder{}
	b := &Bot{events: recorder}
	sender := &tele.User{ID: 42}
	ctx := &MockContext{sender: sender, query: &tele.Query{Sender: sender, Text: "ABC123"}}

	require.NoError(t, b.eventsMiddleware(func(tele.Context) error { return nil })(ctx))

	events := recorder.recorded()
	require.Len(t, events, 1)
	assert.Equal(t, actionShareQuery, events[0].Action)
	assert.Equal(t, int64(42), events[0].TelegramID)
	assert.Empty(t, events[0].Param)
}

// Выбор результата «Поделиться» — Шаг «отправил»: приглашение ушло в чат. id
// результата (код приглашения) не пишется.
func TestEventsMiddleware_RecordsChosenShareResult(t *testing.T) {
	recorder := &fakeRecorder{}
	b := &Bot{events: recorder}
	sender := &tele.User{ID: 42}
	ctx := &MockContext{sender: sender, inlineResult: &tele.InlineResult{Sender: sender, ResultID: "ABC123", Query: "ABC"}}

	require.NoError(t, b.eventsMiddleware(b.handleShareChosen)(ctx))

	events := recorder.recorded()
	require.Len(t, events, 1)
	assert.Equal(t, funnels.ActionShareSent, events[0].Action)
	assert.Equal(t, journal.SourceUser, events[0].Source)
	assert.Empty(t, events[0].Param)
	assert.NotContains(t, events[0].Action, "ABC")
}

// Выбор способа оплаты пишется с параметром-перечислением способа: из данных
// кнопки узнаётся только известный провайдер, а в журнал идёт значение из
// перечисления, не сами данные.
func TestEventsMiddleware_PayMethodParam(t *testing.T) {
	cases := []struct {
		name   string
		unique string
		data   string
		action string
		param  string
	}{
		{"ЮKassa на экране оплаты", cbPayMethod, paymentprovider.YooKassa, funnels.ActionPayMethod, funnels.PayMethodYooKassa},
		{"крипта на экране оплаты", cbPayMethod, paymentprovider.Platega, funnels.ActionPayMethod, funnels.PayMethodCrypto},
		{"повтор тем же способом", cbRetryPayment, paymentprovider.Platega, funnels.ActionRetryPayment, funnels.PayMethodCrypto},
		{"подделанные данные", cbPayMethod, "SECRET", funnels.ActionPayMethod, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &MockContext{sender: &tele.User{ID: 42}, callback: &tele.Callback{Unique: tc.unique, Data: tc.data}}

			event := recordOne(t, ctx)
			assert.Equal(t, tc.action, event.Action)
			assert.Equal(t, tc.param, event.Param)
		})
	}
}

// У остальных Действий параметра нет, даже если данные кнопки похожи на способ.
func TestEventsMiddleware_NoParamOutsidePayMethod(t *testing.T) {
	ctx := &MockContext{sender: &tele.User{ID: 42}, callback: &tele.Callback{Unique: cbPayCancel, Data: paymentprovider.YooKassa}}

	assert.Empty(t, recordOne(t, ctx).Param)
}

// Входы в оплату пишутся теми id, на которые опирается Шаг «вошёл в оплату».
func TestEventsMiddleware_PaymentEntries(t *testing.T) {
	for caption, action := range map[string]string{
		BtnPay:         funnels.ActionPayMenu,
		BtnRenew:       funnels.ActionRenewMenu,
		BtnPayYooKassa: funnels.ActionPayYooKassaReply,
		BtnPayCrypto:   funnels.ActionPayCryptoReply,
	} {
		ctx := &MockContext{sender: &tele.User{ID: 42}, message: &tele.Message{ID: 10, Text: caption}}
		assert.Equal(t, action, recordOne(t, ctx).Action, caption)
	}
	for unique, action := range map[string]string{
		cbPayOpen:              funnels.ActionPayOpen,
		cbAutorenewPayManually: funnels.ActionAutorenewPayManually,
	} {
		ctx := &MockContext{sender: &tele.User{ID: 42}, callback: &tele.Callback{Unique: unique}}
		assert.Equal(t, action, recordOne(t, ctx).Action, unique)
	}
}
