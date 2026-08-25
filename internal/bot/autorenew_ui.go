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

// autorenewChargeLead задаёт и окно T−24ч в scheduler, и дату, которую видит
// пользователь: расходиться им нельзя.
const autorenewChargeLead = 24 * time.Hour

// Состояния автопродления в карточке. «Выключено» и «недоступно» требуют разных
// объяснений, поэтому сводить их к одному нельзя.
type autorenewCardState int

const (
	autorenewHidden   autorenewCardState = iota // строки нет: фича выключена, бессрочная или legacy без цены
	autorenewOn                                 // согласие дано и Способ есть
	autorenewOff                                // Способ есть, подписка активна, согласия нет
	autorenewNoMethod                           // Способа нет; согласие при этом может быть живо
	autorenewExpired                            // подписка истекла или в grace: включать нельзя
)

// autorenewView — всё, что нужно и строке в карточке, и экрану автопродления.
type autorenewView struct {
	state       autorenewCardState
	price       int       // актуальная цена, snapshot не хранится
	methodTitle string    // «•••• 4242» или «СБП»
	chargeAt    time.Time // момент первой попытки; ноль — окна в цикле уже нет
	expireAt    time.Time
	// consent отдельно от state: согласие живёт и у истёкшей подписки, и
	// выключить его человек вправе в любом состоянии карточки.
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
		// Списывать нечем: честнее «недоступно», чем обещание списания.
		view.state = autorenewNoMethod
	case !subscriptionRenewable(remUser):
		// Истёкшая подписка перебивает согласие: окна в цикле уже нет. Само
		// согласие живо и сработает после ручной оплаты.
		view.state = autorenewExpired
	case renewal.IsEnabled():
		view.state = autorenewOn
	default:
		view.state = autorenewOff
	}
	return view
}

// subscriptionRenewable — можно ли сейчас включить автопродление. Включивший
// при истёкшей подписке ждал бы списания, которого не будет.
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

// handleAutorenewOpen показывает экран автопродления, редактируя карточку:
// «🔙 Назад» рисует её на место, а не удаляет сообщение.
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

// handleAutorenewEnable включает автопродление — только при действующей
// подписке и наличии Способа.
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

// handleAutorenewDisable выключает автопродление одним тапом: переспрашивать на
// выключении — удерживающий паттерн. Способ остаётся, включить можно сразу.
func (b *Bot) handleAutorenewDisable(c tele.Context) error {
	telegramID := c.Sender().ID

	if err := b.db.SetAutorenewEnabled(telegramID, false); err != nil {
		slog.Error("Не удалось выключить автопродление", "error", err, "telegram_id", telegramID)
		return c.RespondAlert("Не удалось выключить автопродление. Попробуйте позже.")
	}

	msg := "🔄 Автопродление выключено. Дальше продлевать нужно вручную."
	markup := AutorenewDisabledKeyboard()
	// Экран перерисовываем только там, откуда его открывали: в сообщении об
	// автосписании кнопке «🔙 Назад» возвращаться некуда.
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

// handleAutorenewDismiss — «Не сейчас». Убираем только кнопки: под ними лежит
// подтверждение оплаты, и затирать его нельзя. Предложение придёт после
// следующей оплаты.
func (b *Bot) handleAutorenewDismiss(c tele.Context) error {
	if err := c.Edit(&tele.ReplyMarkup{}); err != nil {
		slog.Warn("Не удалось убрать предложение автопродления", "error", err, "telegram_id", c.Sender().ID)
	}
	return c.Respond(&tele.CallbackResponse{Text: "Хорошо. Включить можно в «Моя подписка»"})
}

// handleAutorenewPayManually ведёт из сообщения о неудачной попытке туда же,
// куда «💳 Продлить подписку».
func (b *Bot) handleAutorenewPayManually(c tele.Context) error {
	if err := b.handlePayButton(c); err != nil {
		slog.Error("Не удалось открыть оплату из сообщения об автосписании", "error", err, "telegram_id", c.Sender().ID)
		return c.RespondAlert("Не удалось открыть оплату. Откройте «💳 Продлить подписку» в меню.")
	}
	return c.Respond()
}

// loadAutorenewView перечитывает состояние: inline-кнопки живут в чате вечно и
// остаются нажимаемыми после того, как подписка истекла.
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

// autorenewOfferMarkup — предложение включить автопродление в сообщении об
// оплате или nil. Приходит после каждой оплаты, пока автопродление выключено:
// счётчик показов не заводим, кнопка ничего не стоит.
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

// adminAutorenewLine — строка состояния в карточке админ-панели.
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
// Включить админ не может: согласие на списание денег даёт только сам человек.
// Способ остаётся — пользователь может включить обратно сам.
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
