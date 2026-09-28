package bot

import (
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/fus1ond/vpn_bot/internal/funnels"
	"github.com/fus1ond/vpn_bot/internal/journal"
	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
	tele "gopkg.in/telebot.v3"
)

// eventRecorder — запись Событий в журнал. Реализация (journal.Journal) не
// блокирует и не возвращает ошибок: аналитика не имеет права замедлить или
// уронить обработку нажатия.
type eventRecorder interface {
	Record(event journal.Event)
}

// eventPurger — чистка журнала: удаляет События старше срока хранения
// (journal.Retention) относительно now.
type eventPurger interface {
	Purge(now time.Time) (int64, error)
}

// purgeEventsJournal — шаг прохода планировщика: удаляет сырые События старше
// срока хранения, без агрегатов (ADR-0005). Шаг изолирован от остальных: своя
// паника, ошибка только в лог — аналитика не имеет права остановить
// уведомления, отключения и чеки.
func (b *Bot) purgeEventsJournal(now time.Time) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("Шаг чистки журнала событий упал с паникой", "recover", r)
		}
	}()

	if b.eventsPurger == nil {
		return
	}
	// Бот останавливается — журнал вот-вот закроется, и чистка дала бы в лог
	// ложную ошибку «database is closed». Устаревшее дочистит следующий старт.
	select {
	case <-b.shutdownCh:
		return
	default:
	}
	deleted, err := b.eventsPurger.Purge(now)
	if err != nil {
		slog.Error("Scheduler: не удалось почистить журнал событий", "error", err)
		return
	}
	if deleted > 0 {
		slog.Info("Scheduler: удалены События старше срока хранения", "deleted", deleted)
	}
}

// recordEvent пишет Событие, если журнал подключён.
func (b *Bot) recordEvent(event journal.Event) {
	if b.events == nil {
		return
	}
	b.events.Record(event)
}

// eventsMiddleware пишет в журнал Действие пользователя.
//
// Запись идёт после обработчика и не зависит от его ошибки: нажатие было,
// чем бы оно ни кончилось. Время — момент нажатия: факты, которые обработчик
// кладёт в таблицы бота (платёж, приглашение), не должны оказаться в журнале
// раньше вызвавшего их Действия.
func (b *Bot) eventsMiddleware(next tele.HandlerFunc) tele.HandlerFunc {
	return func(c tele.Context) error {
		at := time.Now()
		err := next(c)

		if action, ok := userAction(c); ok {
			b.recordEvent(journal.Event{
				At:         at,
				TelegramID: c.Sender().ID,
				Action:     action,
				Source:     journal.SourceUser,
				Param:      actionParam(c, action),
			})
		}
		return err
	}
}

// Id Действий, которые не являются нажатием кнопки, и префиксы id вне
// каталогов.
const (
	actionStart        = "start"        // /start без аргумента
	actionStartInvite  = "start_invite" // /start с аргументом — кодом приглашения
	actionStartShare   = "start_share"  // /start из пустого ответа «Поделиться»
	actionOtherCommand = "cmd:other"    // команда вне белого списка — имя не пишется
	actionText         = "text"         // текст, не являющийся нажатием кнопки
	actionVoice        = "voice"        // голосовое
	actionVideoNote    = "video_note"   // кружок
	actionMedia        = "media"        // фото, видео или документ
	actionShareQuery   = "share_query"  // inline-запрос «Поделиться»: открыт выбор чата

	commandActionPrefix = "cmd:" // команда из белого списка: cmd:<имя>
	unknownInlinePrefix = "cb:"  // inline-кнопка вне каталога: cb:<unique>
)

// knownCommands — команды, которые пишутся по имени. Кроме /start бот команд
// не регистрирует; здесь общие команды, которые Telegram предлагает любому
// боту (/help, /settings). Имя вне списка набрал человек, и это уже
// содержимое сообщения (/hunter2), поэтому оно сводится к cmd:other.
var knownCommands = map[string]struct{}{
	"help":     {},
	"settings": {},
}

// commandRx узнаёт команду так же, как telebot: «/<имя>[@<бот>]» в начале
// текста, дальше пробел или конец строки. Имя — только латиница, цифры и «_».
var commandRx = regexp.MustCompile(`^/(\w+)(?:@\w+)?(?:\s|$)`)

// userAction определяет id Действия по апдейту. Содержимое сообщения, payload
// кнопок и аргументы команд в id не попадают никогда.
//
// Точка расширения каталога: новый вид апдейта, дошедший до middleware,
// получает здесь свою ветку.
func userAction(c tele.Context) (string, bool) {
	if c.Sender() == nil {
		return "", false
	}
	if cb := c.Callback(); cb != nil {
		return inlineButtonAction(cb), true
	}
	if msg := c.Message(); msg != nil {
		return messageAction(msg)
	}
	// Inline-режим у бота один — «Поделиться», поэтому запрос и выбор
	// результата пишутся без разбора: текст запроса и id результата — код
	// приглашения, в журнал они не идут.
	if c.Query() != nil {
		return actionShareQuery, true
	}
	if c.InlineResult() != nil {
		return funnels.ActionShareSent, true
	}
	return "", false
}

// messageAction — id Действия входящего сообщения. Reply-кнопка узнаётся по
// точному совпадению подписи и проверяется первой: подпись не бывает командой.
func messageAction(msg *tele.Message) (string, bool) {
	if action, ok := replyButtonActions[msg.Text]; ok {
		return action, true
	}
	if match := commandRx.FindStringSubmatch(msg.Text); match != nil {
		payload := strings.TrimSpace(msg.Text[len(match[0]):])
		return commandAction(match[1], payload), true
	}
	switch {
	case msg.Text != "":
		return actionText, true
	case msg.Voice != nil:
		return actionVoice, true
	case msg.VideoNote != nil:
		return actionVideoNote, true
	case msg.Photo != nil, msg.Video != nil, msg.Document != nil:
		return actionMedia, true
	}
	return "", false
}

// commandAction — id Действия команды. У /start аргумент различает, откуда
// пришёл человек, но сам аргумент (код приглашения) не пишется.
func commandAction(name, payload string) string {
	name = strings.ToLower(name)
	if name != "start" {
		if _, ok := knownCommands[name]; ok {
			return commandActionPrefix + name
		}
		return actionOtherCommand
	}
	switch payload {
	case "":
		return actionStart
	case StartParamInvites:
		return actionStartShare
	default:
		return actionStartInvite
	}
}

// actionParam — короткий параметр События. Разрешён точечно и только
// перечислением без персональных данных; сейчас это способ оплаты у выбора
// способа и повтора создания платежа. Данные кнопки может подделать клиент,
// поэтому в журнал идёт значение перечисления, а не они сами.
func actionParam(c tele.Context, action string) string {
	switch action {
	case funnels.ActionPayMethod, funnels.ActionRetryPayment:
		if cb := c.Callback(); cb != nil {
			return payMethodParams[cb.Data]
		}
	}
	return ""
}

// payMethodParams — провайдер из данных кнопки способа → параметр События.
var payMethodParams = map[string]string{
	paymentprovider.YooKassa: funnels.PayMethodYooKassa,
	paymentprovider.Platega:  funnels.PayMethodCrypto,
}

// handleUnroutedCallback принимает нажатия inline-кнопок, под Unique которых
// нет обработчика (кнопки из старых сообщений). Без него такой апдейт не
// проходит через middleware и выпадает из журнала, а «часики» на кнопке
// крутятся до таймаута Telegram.
func (b *Bot) handleUnroutedCallback(c tele.Context) error {
	return c.Respond()
}

// AttachAnalytics подключает журнал Событий и модуль воронок. Вызывается до
// Run; любой из них может быть nil — тогда бот работает без записи Событий
// или без экрана воронок, а остальное не меняется. Проверка на nil здесь, а не
// у вызывающего: nil-указатель, положенный в поле-интерфейс, не равен nil.
func (b *Bot) AttachAnalytics(events *journal.Journal, reporter *funnels.Funnels) {
	if events != nil {
		b.events = events
		b.eventsPurger = events
	}
	if reporter != nil {
		b.funnels = reporter
	}
}

// inlineButtonActions — каталог Действий inline-кнопок: id Действия равен
// Unique кнопки. Каталог нужен, чтобы отличить известную кнопку от случайной:
// кнопка вне каталога пишется с префиксом cb: и не совпадёт ни с одним id, на
// который опираются Шаги воронок. Тест сверяет каталог со всеми cb*-константами.
var inlineButtonActions = map[string]struct{}{
	cbDevicesManage:          {},
	cbDeviceDelete:           {},
	cbDevicesResetAll:        {},
	cbDevicesResetAllConfirm: {},
	cbSubRevoke:              {},
	cbSubRevokeConfirm:       {},
	cbSubRevokeCancel:        {},
	cbSubCard:                {},
	cbAutorenewOpen:          {},
	cbAutorenewOffer:         {},
	cbAutorenewEnable:        {},
	cbAutorenewDisable:       {},
	cbAutorenewDismiss:       {},
	cbAutorenewPayManually:   {},
	cbPaymentMethod:          {},
	cbPaymentMethodUnlink:    {},
	cbPaymentMethodConfirm:   {},
	cbPayMethod:              {},
	cbPayCheck:               {},
	cbPayCancel:              {},
	cbPayOpen:                {},
	cbRetryPayment:           {},
	cbRetryPaymentCheck:      {},
	cbRetryInvite:            {},
	cbBugServer:              {},
	cbBugServerDone:          {},
	cbBugCategory:            {},
	cbBugCancel:              {},
	cbAdminExtendMonth:       {},
	cbAdminExtendConfirm:     {},
	cbAdminExtendCancel:      {},
	cbReferralResend:         {},
	cbReferralRevoke:         {},
	cbReferralRevokeOK:       {},
	cbReferralPage:           {},
	cbReferralBack:           {},
	cbAdminReferralOverview:  {},
	cbAdminReferralLeaders:   {},
	cbAdminUserReferrals:     {},
	cbAdminReferralRevoke:    {},
	cbAdminReferralRevokeOK:  {},
	cbAdminReferralBack:      {},
	cbAdminAutorenewOff:      {},
	cbAdminMismatchResolve:   {},
	cbAdminMismatchResolveOK: {},
	cbAdminMismatchBack:      {},
	cbAdminFunnel:            {},
	cbAdminFunnelsBack:       {},
}

// maxUnknownUniqueLen — предел длины Unique кнопки вне каталога в id Действия.
// Unique из каталога не длиннее: предел обрезает только чужие строки.
const maxUnknownUniqueLen = 32

// rawCallbackRx разбирает сырые данные кнопки «\f<unique>|<payload>», которые
// telebot оставляет нетронутыми, если обработчика под Unique нет.
var rawCallbackRx = regexp.MustCompile(`^\f([-\w]+)`)

// inlineButtonAction — id Действия нажатия inline-кнопки: её Unique. Payload
// (Data) не пишется никогда — в нём коды приглашений, номера устройств и id
// платежей.
//
// Кнопка вне каталога пишется как cb:<unique> с предупреждением: это либо
// новая кнопка, забытая в каталоге, либо кнопка из старого сообщения, чей
// Unique бот больше не обслуживает.
func inlineButtonAction(cb *tele.Callback) string {
	unique := cb.Unique
	if unique == "" {
		// Нет обработчика: telebot не разобрал данные, Unique лежит в Data.
		if match := rawCallbackRx.FindStringSubmatch(cb.Data); match != nil {
			unique = match[1]
		}
	}
	if _, ok := inlineButtonActions[unique]; ok {
		return unique
	}
	// Данные кнопки может подделать клиент: хвост на его выбор режется.
	if len(unique) > maxUnknownUniqueLen {
		unique = unique[:maxUnknownUniqueLen]
	}
	slog.Warn("Inline-кнопка вне каталога Действий", "unique", unique)
	return unknownInlinePrefix + unique
}
