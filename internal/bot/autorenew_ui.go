package bot

import (
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"time"

	tele "gopkg.in/telebot.v3"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

// autorenewChargeLead — за сколько до конца подписки идёт первая попытка
// списания. Та же величина задаёт и окно T−24ч в scheduler, и дату, которую
// показываем пользователю: расходиться им нельзя.
const autorenewChargeLead = 24 * time.Hour

// Состояния автопродления в карточке «👤 Моя подписка». Их четыре, и различать
// их обязательно: «выключено» и «недоступно» требуют разных объяснений, а
// сводить их к одному значило бы угадывать, что человеку сказать.
type autorenewCardState int

const (
	// autorenewHidden — строки в карточке нет вовсе: фича выключена,
	// подписка бессрочная либо у legacy-пользователя нет цены (там и оплата
	// скрыта, продлевать нечего).
	autorenewHidden autorenewCardState = iota
	// autorenewOn — согласие дано и Способ есть.
	autorenewOn
	// autorenewOff — Способ есть, подписка активна, согласия нет.
	autorenewOff
	// autorenewNoMethod — Способа нет. Согласие при этом может быть живо:
	// это нормальное состояние, но списывать нечем.
	autorenewNoMethod
	// autorenewExpired — подписка истекла или в grace: включать нельзя,
	// окна списания уже нет (Р7).
	autorenewExpired
)

// autorenewView — всё, что нужно и строке в карточке, и экрану автопродления.
type autorenewView struct {
	state autorenewCardState
	// price — актуальная цена на момент показа. Snapshot не хранится: он
	// породил бы вечных плательщиков по старой цене (Р5).
	price int
	// methodTitle — чем платим: «•••• 4242» или «СБП».
	methodTitle string
	// chargeAt — момент первой попытки списания (T−24ч). Нулевое время
	// означает, что окна списания в этом цикле уже нет.
	chargeAt time.Time
	// expireAt — конец текущего цикла подписки.
	expireAt time.Time
	// consent — дал ли пользователь согласие. Отдельно от state: согласие
	// живёт и у того, чья подписка истекла, и выключить его он вправе в любом
	// состоянии карточки.
	consent bool
}

// autorenewViewFor собирает состояние автопродления для пользователя.
func (b *Bot) autorenewViewFor(telegramID int64, remUser *remnawave.User, dbUser *database.User) autorenewView {
	view := autorenewView{state: autorenewHidden}
	if !b.autorenewAvailable() || remUser == nil {
		return view
	}
	// Бессрочные и legacy без цены: оплата у них уже скрыта, продлевать нечего.
	if remUser.ExpireAt.Year() >= 2099 {
		return view
	}
	if dbUser == nil || dbUser.SubscriptionPrice == nil {
		return view
	}

	view.price = *dbUser.SubscriptionPrice
	view.expireAt = remUser.ExpireAt
	if charge := remUser.ExpireAt.Add(-autorenewChargeLead); charge.After(time.Now().UTC()) {
		view.chargeAt = charge
	}

	renewal, err := b.db.GetAutorenewal(telegramID)
	if err != nil {
		slog.Error("Не удалось прочитать автопродление для карточки", "error", err, "telegram_id", telegramID)
		return view
	}
	if renewal != nil && renewal.MethodTitle != nil {
		view.methodTitle = *renewal.MethodTitle
	}

	view.consent = renewal.IsEnabled()

	switch {
	case !renewal.HasMethod():
		// Согласие без Способа выглядит так же: списывать нечем, и честнее
		// сказать «недоступно», чем обещать списание.
		view.state = autorenewNoMethod
	case !subscriptionRenewable(remUser):
		// Истёкшая подписка перебивает согласие: окна списания в этом цикле уже
		// нет, и обещать «спишем» человеку в grace значит обещать то, чего не
		// будет. Само согласие при этом живо — оно сработает после ручной оплаты.
		view.state = autorenewExpired
	case renewal.IsEnabled():
		view.state = autorenewOn
	default:
		view.state = autorenewOff
	}
	return view
}

// subscriptionRenewable сообщает, можно ли сейчас включить автопродление:
// подписка должна быть действующей. Включивший её при истёкшей подписке ждал бы
// списания, которого не будет — окна T−24ч уже нет.
func subscriptionRenewable(remUser *remnawave.User) bool {
	return remUser != nil &&
		remUser.Status == remnawave.StatusActive &&
		remUser.ExpireAt.After(time.Now().UTC())
}

// cardLine — строка автопродления в карточке подписки.
func (v autorenewView) cardLine() string {
	switch v.state {
	case autorenewOn:
		if !v.chargeAt.IsZero() {
			return fmt.Sprintf("\n<b>🔄 Автопродление:</b> включено, спишем %d ₽ %s\n",
				v.price, v.chargeAt.Format("02.01"))
		}
		return "\n<b>🔄 Автопродление:</b> включено\n"
	case autorenewOff:
		return "\n<b>🔄 Автопродление:</b> выключено\n"
	case autorenewNoMethod, autorenewExpired:
		return "\n<b>🔄 Автопродление:</b> недоступно\n"
	default:
		return ""
	}
}

// screen — текст экрана автопродления, открываемого из карточки.
func (b *Bot) autorenewScreen(v autorenewView) string {
	switch v.state {
	case autorenewOn:
		msg := "<b>🔄 Автопродление включено</b>\n\n"
		if !v.chargeAt.IsZero() {
			msg += fmt.Sprintf("Спишем <b>%d ₽</b> %s — за сутки до окончания подписки (%s).\n",
				v.price, v.chargeAt.Format("02.01.2006"), v.expireAt.Format("02.01.2006"))
		} else {
			msg += fmt.Sprintf("Сумма списания — <b>%d ₽</b> в месяц.\n", v.price)
		}
		if v.methodTitle != "" {
			msg += fmt.Sprintf("Платим: <b>%s</b>\n", html.EscapeString(v.methodTitle))
		}
		msg += "\nВыключить можно в один тап — деньги за уже оплаченный месяц не пропадут."
		return msg
	case autorenewOff:
		return b.autorenewTermsText(v)
	case autorenewNoMethod:
		return "<b>🔄 Автопродление недоступно</b>\n\n" +
			"Мы запомним способ оплаты при следующей оплате картой или через СБП — после этого автопродление можно будет включить.\n\n" +
			"При оплате криптовалютой автопродление недоступно: списать по такому платежу технически нельзя."
	case autorenewExpired:
		if v.consent {
			return "<b>🔄 Автопродление недоступно</b>\n\n" +
				"Автопродление у вас включено, но подписка уже истекла: списание идёт за сутки до её окончания, и это окно прошло.\n\n" +
				"Продлите подписку вручную — дальше автопродление снова заработает само."
		}
		return "<b>🔄 Автопродление недоступно</b>\n\n" +
			"Продлите подписку — после оплаты автопродление можно будет включить.\n\n" +
			"Включить его сейчас нельзя: списание идёт за сутки до окончания подписки, и это окно уже прошло."
	default:
		return "<b>🔄 Автопродление</b>\n\nСейчас недоступно."
	}
}

// autorenewTermsText — экран условий: сумма, периодичность, дата первого
// списания, как отключить и ссылка на оферту. Один текст на два входа — из
// карточки и из сообщения об успешной оплате.
func (b *Bot) autorenewTermsText(v autorenewView) string {
	msg := "<b>🔄 Автопродление</b>\n\n"
	msg += fmt.Sprintf("Будем списывать <b>%d ₽</b> раз в месяц, за сутки до окончания подписки", v.price)
	if !v.chargeAt.IsZero() {
		msg += fmt.Sprintf(" — первое списание %s", v.chargeAt.Format("02.01.2006"))
	}
	msg += ".\n"
	if v.chargeAt.IsZero() {
		// До конца подписки меньше суток: окно за сутки уже прошло, и списание
		// уйдёт ближайшим проходом scheduler — то есть в считаные минуты.
		// Молчать об этом нельзя: человек включал автопродление, а не покупал
		// прямо сейчас, и внезапное списание будет для него сюрпризом.
		msg += "\n<b>Внимание:</b> до конца подписки осталось меньше суток, поэтому первое списание пройдёт в ближайшие полчаса.\n"
	}
	if v.methodTitle != "" {
		msg += fmt.Sprintf("Платим: <b>%s</b>\n", html.EscapeString(v.methodTitle))
	}
	msg += "\nЕсли цена подписки изменится, спишем новую и скажем об этом в сообщении.\n"
	msg += "Выключить — в один тап в «👤 Моя подписка», в любой момент.\n"
	if b.config.TermsOfServiceURL != "" {
		msg += fmt.Sprintf("\n<a href=\"%s\">Условия</a>", b.config.TermsOfServiceURL)
	}
	return msg
}

// handleAutorenewOpen показывает экран автопродления, редактируя карточку.
// Возврат «🔙 Назад» рисует карточку на место — сообщение не удаляется.
func (b *Bot) handleAutorenewOpen(c tele.Context) error {
	telegramID := c.Sender().ID
	view, ok := b.loadAutorenewView(telegramID)
	if !ok {
		return c.RespondAlert("Сначала активируйте подписку")
	}
	if view.state == autorenewHidden {
		return c.RespondAlert("Автопродление недоступно")
	}

	if err := editWithInlineFallback(c, b.autorenewScreen(view), AutorenewScreenKeyboard(view)); err != nil {
		slog.Error("Не удалось показать экран автопродления", "error", err, "telegram_id", telegramID)
	}
	return c.Respond()
}

// handleAutorenewOffer показывает экран условий из сообщения об успешной оплате.
func (b *Bot) handleAutorenewOffer(c tele.Context) error {
	telegramID := c.Sender().ID
	view, ok := b.loadAutorenewView(telegramID)
	if !ok {
		return c.RespondAlert("Сначала активируйте подписку")
	}
	if view.state != autorenewOff {
		return c.RespondAlert(autorenewUnavailableAlert(view.state))
	}

	if err := editWithInlineFallback(c, b.autorenewTermsText(view), AutorenewOfferKeyboard()); err != nil {
		slog.Error("Не удалось показать условия автопродления", "error", err, "telegram_id", telegramID)
	}
	return c.Respond()
}

// handleAutorenewEnable включает автопродление. Доступно только при
// действующей подписке и наличии Способа (Р7).
func (b *Bot) handleAutorenewEnable(c tele.Context) error {
	telegramID := c.Sender().ID
	view, ok := b.loadAutorenewView(telegramID)
	if !ok {
		return c.RespondAlert("Сначала активируйте подписку")
	}
	if view.state != autorenewOff {
		return c.RespondAlert(autorenewUnavailableAlert(view.state))
	}

	if err := b.db.SetAutorenewEnabled(telegramID, true); err != nil {
		slog.Error("Не удалось включить автопродление", "error", err, "telegram_id", telegramID)
		return c.RespondAlert("Не удалось включить автопродление. Попробуйте позже.")
	}

	view.state = autorenewOn
	if err := editWithInlineFallback(c, b.autorenewScreen(view), AutorenewScreenKeyboard(view)); err != nil {
		slog.Error("Не удалось перерисовать экран автопродления", "error", err, "telegram_id", telegramID)
	}
	return c.Respond(&tele.CallbackResponse{Text: "Автопродление включено"})
}

// handleAutorenewDisable выключает автопродление одним тапом, без
// подтверждения: переспрашивать на выключении — удерживающий паттерн, а цена
// ошибочного нажатия нулевая. Способ при этом остаётся: включить обратно можно
// сразу, без повторной оплаты.
func (b *Bot) handleAutorenewDisable(c tele.Context) error {
	telegramID := c.Sender().ID

	if err := b.db.SetAutorenewEnabled(telegramID, false); err != nil {
		slog.Error("Не удалось выключить автопродление", "error", err, "telegram_id", telegramID)
		return c.RespondAlert("Не удалось выключить автопродление. Попробуйте позже.")
	}

	msg := "🔄 Автопродление выключено. Дальше продлевать нужно вручную."
	markup := AutorenewDisabledKeyboard()
	// Экран автопродления перерисовываем только там, откуда его открывали. На
	// сообщении об автосписании кнопка та же, но подменять его экраном с
	// «🔙 Назад» нельзя: назад там некуда, карточки в этом сообщении не было.
	if c.Data() == autorenewDisableFromCard {
		if view, ok := b.loadAutorenewView(telegramID); ok && view.state != autorenewHidden {
			markup = AutorenewScreenKeyboard(view)
			msg = b.autorenewScreen(view) + "\n\n<i>Автопродление выключено. Дальше продлевать нужно вручную.</i>"
		}
	}
	if err := editWithInlineFallback(c, msg, markup); err != nil {
		slog.Error("Не удалось перерисовать экран после выключения", "error", err, "telegram_id", telegramID)
	}
	return c.Respond(&tele.CallbackResponse{Text: "Автопродление выключено"})
}

// handleAutorenewDismiss — «Не сейчас»: убираем кнопки, ничего не меняя.
// Предложение придёт снова после следующей оплаты — кнопка ничего не стоит.
func (b *Bot) handleAutorenewDismiss(c tele.Context) error {
	// Убираем только кнопки, а не текст: под ними лежит подтверждение оплаты,
	// и затирать его отказом от автопродления значит стереть из чата
	// единственное свидетельство, что деньги дошли.
	if err := c.Edit(&tele.ReplyMarkup{}); err != nil {
		slog.Warn("Не удалось убрать предложение автопродления", "error", err, "telegram_id", c.Sender().ID)
	}
	return c.Respond(&tele.CallbackResponse{Text: "Хорошо. Включить можно в «Моя подписка»"})
}

// handleAutorenewPayManually открывает обычный экран оплаты из сообщения о
// неудачной попытке списания: кнопка ведёт туда же, куда «💳 Продлить подписку»,
// чтобы человеку не пришлось искать её в меню.
func (b *Bot) handleAutorenewPayManually(c tele.Context) error {
	if err := b.handlePayButton(c); err != nil {
		slog.Error("Не удалось открыть оплату из сообщения об автосписании", "error", err, "telegram_id", c.Sender().ID)
		return c.RespondAlert("Не удалось открыть оплату. Откройте «💳 Продлить подписку» в меню.")
	}
	return c.Respond()
}

// loadAutorenewView перечитывает состояние подписки и автопродления. Inline-кнопки
// живут в чате вечно: карточка, отрисованная при активной подписке, остаётся
// нажимаемой и после того, как подписка истекла.
func (b *Bot) loadAutorenewView(telegramID int64) (autorenewView, bool) {
	ref, ok := b.resolveUserRef(telegramID)
	if !ok {
		return autorenewView{}, false
	}
	remUser, err := b.remnawave.GetUser(ref)
	if err != nil {
		slog.Error("Не удалось получить пользователя панели для автопродления", "error", err, "telegram_id", telegramID)
		return autorenewView{}, false
	}
	dbUser, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil {
		slog.Error("Не удалось получить пользователя БД для автопродления", "error", err, "telegram_id", telegramID)
		return autorenewView{}, false
	}
	return b.autorenewViewFor(telegramID, remUser, dbUser), true
}

func autorenewUnavailableAlert(state autorenewCardState) string {
	switch state {
	case autorenewOn:
		return "Автопродление уже включено"
	case autorenewNoMethod:
		return "Способ оплаты не сохранён — оплатите картой или через СБП"
	case autorenewExpired:
		return "Продлите подписку — потом можно будет включить автопродление"
	default:
		return "Автопродление недоступно"
	}
}

// autorenewOfferMarkup возвращает предложение включить автопродление для
// сообщения об успешной ручной оплате — или nil, если предлагать нечего.
//
// Предложение приходит после каждой оплаты, пока автопродление выключено:
// счётчик «сколько раз спрашивали» не заводим, кнопка ничего не стоит в отличие
// от push-сообщения.
func (b *Bot) autorenewOfferMarkup(telegramID int64) *tele.ReplyMarkup {
	if !b.autorenewAvailable() {
		return nil
	}
	view, ok := b.loadAutorenewView(telegramID)
	if !ok || view.state != autorenewOff {
		return nil
	}
	return AutorenewOfferPromptKeyboard()
}

// adminAutorenewLine — строка состояния автопродления в карточке пользователя
// админ-панели: включено / выключено / недоступно, чем платим и когда спишем.
func adminAutorenewLine(view autorenewView) string {
	switch view.state {
	case autorenewOn:
		line := "🔄 Автопродление: включено"
		if view.methodTitle != "" {
			line += fmt.Sprintf(" (%s)", html.EscapeString(view.methodTitle))
		}
		if !view.chargeAt.IsZero() {
			line += fmt.Sprintf(", спишем %d ₽ %s", view.price, view.chargeAt.Format("02.01.2006"))
		}
		return line + "\n"
	case autorenewOff:
		line := "🔄 Автопродление: выключено"
		if view.methodTitle != "" {
			line += fmt.Sprintf(" (способ сохранён: %s)", html.EscapeString(view.methodTitle))
		}
		return line + "\n"
	case autorenewNoMethod:
		return "🔄 Автопродление: недоступно (способ не сохранён)\n"
	case autorenewExpired:
		return "🔄 Автопродление: недоступно (подписка истекла)\n"
	default:
		return ""
	}
}

// handleAdminAutorenewDisable выключает автопродление за пользователя.
//
// Выключить админ может, включить — нет: согласие на списание денег даёт
// только сам человек, и кнопка «включить за пользователя» — ровно тот случай,
// когда «я не включал» превращается в chargeback. Способ при этом остаётся:
// пользователь может включить обратно сам.
func (b *Bot) handleAdminAutorenewDisable(c tele.Context) error {
	if c.Sender().ID != b.config.AdminID {
		return c.RespondAlert("Недостаточно прав")
	}
	targetID, err := strconv.ParseInt(c.Data(), 10, 64)
	if err != nil {
		return c.RespondAlert("Не удалось определить пользователя")
	}

	if err := b.db.SetAutorenewEnabled(targetID, false); err != nil {
		slog.Error("Админ: не удалось выключить автопродление", "error", err, "telegram_id", targetID)
		return c.RespondAlert("Не удалось выключить автопродление")
	}
	slog.Info("Админ выключил автопродление пользователя", "telegram_id", targetID)

	if err := b.editAdminUserInfo(c, targetID); err != nil {
		slog.Error("Админ: не удалось перерисовать карточку", "error", err, "telegram_id", targetID)
	}
	return c.Respond(&tele.CallbackResponse{Text: "Автопродление выключено"})
}
