package bot

import (
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
	tele "gopkg.in/telebot.v3"
)

// Состояния модератора.
const (
	StateWaitModDeleteInvite     = "wait_mod_delete_invite"      // Модератор ждёт код для удаления
	StateWaitModInvitePrice      = "wait_mod_invite_price"       // Ожидание цены нового инвайта
	StateWaitModChangePriceID    = "wait_mod_change_price_id"    // Ожидание telegram_id подписчика
	StateWaitModChangePriceValue = "wait_mod_change_price_value" // Ожидание новой цены подписки
	StateModSubscribers          = "mod_subscribers"             // Открыт экран списка подписчиков
)

type modChangePriceSession struct {
	SubscriberTelegramID int64
	SubscriberLabel      string
	CurrentPrice         int
}

// isModerator проверяет, является ли пользователь модератором.
func (b *Bot) isModerator(telegramID int64) bool {
	ok, err := b.db.IsModerator(telegramID)
	if err != nil {
		slog.Error("Failed to check moderator status", "error", err, "telegram_id", telegramID)
		return false
	}
	return ok
}

// handleModeratorMenu показывает подменю модератора.
func (b *Bot) handleModeratorMenu(c tele.Context) error {
	b.userStates.Delete(c.Sender().ID)
	return c.Send("<b>🎟 Приглашения</b>\n\nВыберите действие:", &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: ModeratorMenuKeyboard(),
	})
}

// handleModeratorCreateInvite запускает создание инвайта от имени модератора.
func (b *Bot) handleModeratorCreateInvite(c tele.Context) error {
	b.userStates.Set(c.Sender().ID, StateWaitModInvitePrice)
	return c.Send(
		fmt.Sprintf("Введите цену подписки (руб/мес).\nМинимум: %d руб.", b.minSubscriptionPrice()),
		&tele.SendOptions{ReplyMarkup: CancelKeyboard()},
	)
}

// processModeratorInvitePrice создаёт инвайт с ценой после ввода модератора.
func (b *Bot) processModeratorInvitePrice(c tele.Context, text string) error {
	moderatorID := c.Sender().ID
	price, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return c.Send("❌ Введите число.", &tele.SendOptions{ReplyMarkup: CancelKeyboard()})
	}
	if price < b.minSubscriptionPrice() {
		return c.Send(
			fmt.Sprintf("❌ Минимальная цена: %d руб.", b.minSubscriptionPrice()),
			&tele.SendOptions{ReplyMarkup: CancelKeyboard()},
		)
	}

	inviteCode, err := b.db.CreateInviteWithPrice(moderatorID, 30, price)
	if err != nil {
		slog.Error("Failed to create invite with price", "error", err, "moderator_id", moderatorID)
		return c.Send("Ошибка создания приглашения", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
	}

	b.userStates.Delete(moderatorID)
	msg := fmt.Sprintf(
		"✅ Приглашение создано!\nЦена подписки: %d руб/мес\n\nСсылка: https://t.me/%s?start=%s",
		price,
		b.getBotUsername(),
		inviteCode,
	)
	return c.Send(msg, &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
}

// handleModeratorViewInvites показывает список инвайтов модератора.
func (b *Bot) handleModeratorViewInvites(c tele.Context) error {
	telegramID := c.Sender().ID

	invites, err := b.db.GetInvitesWithUsersByCreator(telegramID)
	if err != nil {
		slog.Error("Failed to get moderator invites", "error", err, "moderator_id", telegramID)
		return c.Send("Ошибка получения списка приглашений", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
	}

	if len(invites) == 0 {
		return c.Send("📋 У вас пока нет приглашений", &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: ModeratorMenuKeyboard(),
		})
	}

	var msg strings.Builder
	fmt.Fprintf(&msg, "<b>📋 Мои приглашения (%d)</b>\n\n", len(invites))

	for _, inv := range invites {
		if inv.UsedBy != nil {
			msg.WriteString("✅ Использован\n")
			fmt.Fprintf(&msg, "🔹 Код: <code>%s</code>\n", inv.Code)
			fmt.Fprintf(&msg, "👤 %s\n", formatUserLabel(inv.UserFirstName, inv.UserUsername, *inv.UsedBy))
			if inv.UsedAt != nil {
				fmt.Fprintf(&msg, "📅 %s\n", inv.UsedAt.Format("02.01.06 15:04"))
			}
		} else {
			msg.WriteString("⭕ Не использован\n")
			fmt.Fprintf(&msg, "🔹 Код: <code>%s</code>\n", inv.Code)
			fmt.Fprintf(&msg, "📅 Создан: %s\n", inv.CreatedAt.Format("02.01.06 15:04"))
		}
		msg.WriteString("\n")
	}

	return c.Send(msg.String(), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: ModeratorMenuKeyboard(),
	})
}

// handleModSubscribers показывает подписчиков модератора и их статусы.
func (b *Bot) handleModSubscribers(c tele.Context) error {
	telegramID := c.Sender().ID

	subscribers, err := b.db.GetSubscribersByModerator(telegramID)
	if err != nil {
		slog.Error("Failed to get subscribers by moderator", "error", err, "moderator_id", telegramID)
		return c.Send("Ошибка получения списка подписчиков", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
	}

	if len(subscribers) == 0 {
		return c.Send("👥 У вас пока нет подписчиков", &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: ModeratorMenuKeyboard(),
		})
	}

	remUsers, err := b.remnawave.GetAllUsers()
	if err != nil {
		slog.Error("Failed to get all users from Remnawave for subscribers list", "error", err, "moderator_id", telegramID)
		return c.Send("Ошибка получения данных из системы", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
	}

	remByUUID := make(map[string]remnawave.User, len(remUsers))
	for _, user := range remUsers {
		remByUUID[user.UUID] = user
	}

	sort.Slice(subscribers, func(i, j int) bool { return subscribers[i].TelegramID < subscribers[j].TelegramID })

	now := time.Now().UTC()
	trialCount := 0
	paidCount := 0
	graceCount := 0
	expiredCount := 0
	deletedCount := 0

	var msg strings.Builder
	fmt.Fprintf(&msg, "<b>👥 Мои подписчики (%d)</b>\n\n", len(subscribers))

	for _, sub := range subscribers {
		if sub.RemnawaveUUID == nil {
			deletedCount++
			fmt.Fprintf(&msg, "❌ ID: <code>%d</code> — удалён\n\n", sub.TelegramID)
			continue
		}

		remUser, exists := remByUUID[*sub.RemnawaveUUID]
		if !exists {
			deletedCount++
			fmt.Fprintf(&msg, "❌ ID: <code>%d</code> — удалён\n\n", sub.TelegramID)
			continue
		}

		label := formatSubscriberLabel(sub)
		priceLabel := formatPriceLabel(sub.SubscriptionPrice)

		switch b.describeSubscriberStatus(sub.TelegramID, remUser, now) {
		case "trial":
			trialCount++
			fmt.Fprintf(&msg, "⏳ %s — триал\n", label)
			fmt.Fprintf(&msg, "   до %s (осталось %d дн.)\n", remUser.ExpireAt.Format("02.01.06"), daysUntil(remUser.ExpireAt, now))
			fmt.Fprintf(&msg, "   цена: %s\n\n", priceLabel)
		case "grace":
			graceCount++
			fmt.Fprintf(&msg, "⚠️ %s — grace period\n", label)
			fmt.Fprintf(&msg, "   VPN деактивирован (кик через %d дн.)\n", daysUntil(remUser.ExpireAt.Add(72*time.Hour), now))
			fmt.Fprintf(&msg, "   цена: %s\n\n", priceLabel)
		case "expired":
			expiredCount++
			fmt.Fprintf(&msg, "⏰ %s — истёк\n", label)
			fmt.Fprintf(&msg, "   истёк %s (кик через %d дн.)\n", remUser.ExpireAt.Format("02.01.06"), daysUntil(remUser.ExpireAt.Add(72*time.Hour), now))
			fmt.Fprintf(&msg, "   цена: %s\n\n", priceLabel)
		default:
			paidCount++
			fmt.Fprintf(&msg, "💳 %s — оплачено\n", label)
			fmt.Fprintf(&msg, "   до %s (осталось %d дн.)\n", remUser.ExpireAt.Format("02.01.06"), daysUntil(remUser.ExpireAt, now))
			fmt.Fprintf(&msg, "   цена: %s\n\n", priceLabel)
		}
	}

	msg.WriteString("───\n")
	fmt.Fprintf(
		&msg,
		"💳 Платящих: %d │ ⏳ Триал: %d │ ⚠️ Grace: %d │ ⏰ Истекших: %d │ ❌ Удалённых: %d",
		paidCount,
		trialCount,
		graceCount,
		expiredCount,
		deletedCount,
	)
	b.userStates.Set(telegramID, StateModSubscribers)

	return c.Send(msg.String(), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: ModeratorSubscribersKeyboard(),
	})
}

// handleModeratorEarnings показывает финансовую сводку модератора.
func (b *Bot) handleModeratorEarnings(c tele.Context) error {
	moderatorID := c.Sender().ID
	now := time.Now().UTC()

	monthStats, err := b.db.GetModeratorEarningsByMonth(moderatorID, now.Year(), int(now.Month()))
	if err != nil {
		slog.Error("Failed to load moderator earnings by month", "error", err, "moderator_id", moderatorID)
		return c.Send("Ошибка получения статистики", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
	}

	totalEarnings, err := b.db.GetModeratorTotalEarnings(moderatorID)
	if err != nil {
		slog.Error("Failed to load moderator total earnings", "error", err, "moderator_id", moderatorID)
		return c.Send("Ошибка получения статистики", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
	}

	payingCount, err := b.db.CountPayingSubscribersByModerator(moderatorID)
	if err != nil {
		slog.Error("Failed to count paying subscribers", "error", err, "moderator_id", moderatorID)
		return c.Send("Ошибка получения статистики", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
	}

	sharePercent := monthStats.SharePercent
	if sharePercent == 0 {
		sharePercent = calculateSharePercent(payingCount)
	}

	msg := fmt.Sprintf(
		"<b>💰 Мой заработок</b>\n\nЗа %s %d:\n"+
			"├ Платящих клиентов: %d\n"+
			"├ Ваша доля: %d%%\n"+
			"├ Сумма платежей: %d руб\n"+
			"├ Комиссии Platega: -%d руб\n"+
			"├ Комиссия вывода: -%d руб\n"+
			"├ Чистый доход: %d руб\n"+
			"└ Ваша доля: %d руб\n\n"+
			"За всё время: %d руб",
		monthNameRu(now.Month()),
		now.Year(),
		payingCount,
		sharePercent,
		monthStats.GrossAmount,
		monthStats.TotalPlategaFee,
		monthStats.TotalWithdrawal,
		monthStats.TotalNetAmount,
		monthStats.TotalShareAmount,
		totalEarnings,
	)

	return c.Send(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: ModeratorMenuKeyboard(),
	})
}

// handleModChangePriceRequest запускает диалог изменения цены.
func (b *Bot) handleModChangePriceRequest(c tele.Context) error {
	telegramID := c.Sender().ID
	b.userStates.Set(telegramID, StateWaitModChangePriceID)
	b.clearModChangePriceSession(telegramID)
	return c.Send("Введите telegram_id подписчика:", &tele.SendOptions{
		ReplyMarkup: CancelKeyboard(),
	})
}

// processModChangePriceID выбирает подписчика для изменения цены.
func (b *Bot) processModChangePriceID(c tele.Context, text string) error {
	moderatorID := c.Sender().ID
	targetID, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil {
		return c.Send("❌ Введите корректный telegram_id.", &tele.SendOptions{ReplyMarkup: CancelKeyboard()})
	}

	owned, err := b.db.IsSubscriberOfModerator(moderatorID, targetID)
	if err != nil {
		slog.Error("Failed to verify subscriber ownership", "error", err, "moderator_id", moderatorID, "target_id", targetID)
		b.userStates.Delete(moderatorID)
		return c.Send("Ошибка проверки подписчика", &tele.SendOptions{ReplyMarkup: ModeratorSubscribersKeyboard()})
	}
	if !owned {
		return c.Send("❌ Можно менять цену только своим подписчикам.", &tele.SendOptions{ReplyMarkup: CancelKeyboard()})
	}

	dbUser, err := b.db.GetUserByTelegramID(targetID)
	if err != nil {
		slog.Error("Failed to load subscriber from DB", "error", err, "target_id", targetID)
		return c.Send("Ошибка получения данных подписчика", &tele.SendOptions{ReplyMarkup: CancelKeyboard()})
	}
	if dbUser == nil {
		b.userStates.Delete(moderatorID)
		return c.Send("❌ Пользователь удалён из системы.", &tele.SendOptions{ReplyMarkup: ModeratorSubscribersKeyboard()})
	}

	hasPaid, err := b.db.HasConfirmedPayment(targetID)
	if err != nil {
		slog.Error("Failed to check confirmed payments", "error", err, "target_id", targetID)
		return c.Send("Ошибка проверки подписчика", &tele.SendOptions{ReplyMarkup: CancelKeyboard()})
	}
	if hasPaid {
		b.userStates.Delete(moderatorID)
		return c.Send(
			"❌ Нельзя изменить цену — клиент уже оплатил подписку. Обратитесь к администратору.",
			&tele.SendOptions{ReplyMarkup: ModeratorSubscribersKeyboard()},
		)
	}

	remUser, err := b.remnawave.GetUser(dbUser.RemnawaveUUID)
	if err != nil {
		slog.Error("Failed to load subscriber from Remnawave", "error", err, "target_id", targetID)
		return c.Send("Ошибка проверки подписчика", &tele.SendOptions{ReplyMarkup: CancelKeyboard()})
	}

	switch b.describeSubscriberStatus(targetID, *remUser, time.Now().UTC()) {
	case "trial":
		// Только trial может менять цену.
	default:
		b.userStates.Delete(moderatorID)
		return c.Send(
			"❌ Нельзя изменить цену — клиент уже не на пробном периоде. Обратитесь к администратору.",
			&tele.SendOptions{ReplyMarkup: ModeratorSubscribersKeyboard()},
		)
	}

	currentPrice := 0
	if dbUser.SubscriptionPrice != nil {
		currentPrice = *dbUser.SubscriptionPrice
	}

	label := formatAdminSwitchTargetLabel(dbUser)
	b.setModChangePriceSession(moderatorID, modChangePriceSession{
		SubscriberTelegramID: targetID,
		SubscriberLabel:      label,
		CurrentPrice:         currentPrice,
	})
	b.userStates.Set(moderatorID, StateWaitModChangePriceValue)

	return c.Send(
		fmt.Sprintf(
			"Текущая цена для %s: %d руб/мес\nВведите новую цену (минимум %d руб):",
			label,
			currentPrice,
			b.minSubscriptionPrice(),
		),
		&tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: CancelKeyboard(),
		},
	)
}

// processModChangePriceValue завершает изменение цены.
func (b *Bot) processModChangePriceValue(c tele.Context, text string) error {
	moderatorID := c.Sender().ID
	newPrice, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return c.Send("❌ Введите число.", &tele.SendOptions{ReplyMarkup: CancelKeyboard()})
	}
	if newPrice < b.minSubscriptionPrice() {
		return c.Send(
			fmt.Sprintf("❌ Минимальная цена: %d руб.", b.minSubscriptionPrice()),
			&tele.SendOptions{ReplyMarkup: CancelKeyboard()},
		)
	}

	session, ok := b.getModChangePriceSession(moderatorID)
	if !ok {
		b.userStates.Delete(moderatorID)
		return c.Send("Сессия изменения цены потеряна. Начните заново.", &tele.SendOptions{ReplyMarkup: ModeratorSubscribersKeyboard()})
	}

	if err := b.db.UpdateSubscriptionPrice(session.SubscriberTelegramID, newPrice); err != nil {
		slog.Error("Failed to update subscription price", "error", err, "telegram_id", session.SubscriberTelegramID)
		b.userStates.Delete(moderatorID)
		b.clearModChangePriceSession(moderatorID)
		return c.Send("Ошибка изменения цены", &tele.SendOptions{ReplyMarkup: ModeratorSubscribersKeyboard()})
	}

	if err := b.db.UpdateInviteSubscriptionPrice(session.SubscriberTelegramID, newPrice); err != nil {
		slog.Error("Failed to update invite subscription price", "error", err, "telegram_id", session.SubscriberTelegramID)
	}

	b.userStates.Delete(moderatorID)
	b.clearModChangePriceSession(moderatorID)

	return c.Send(
		fmt.Sprintf(
			"✅ Цена подписки для %s изменена: %d → %d руб/мес",
			session.SubscriberLabel,
			session.CurrentPrice,
			newPrice,
		),
		&tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: ModeratorSubscribersKeyboard(),
		},
	)
}

func formatSubscriberLabel(sub database.Subscriber) string {
	firstName := ""
	if sub.FirstName != nil {
		firstName = *sub.FirstName
	}
	username := ""
	if sub.Username != nil {
		username = *sub.Username
	}
	return formatUserLabel(firstName, username, sub.TelegramID)
}

func (b *Bot) setModChangePriceSession(moderatorID int64, session modChangePriceSession) {
	b.modChangePriceMu.Lock()
	defer b.modChangePriceMu.Unlock()
	if b.modChangePriceData == nil {
		b.modChangePriceData = make(map[int64]modChangePriceSession)
	}
	b.modChangePriceData[moderatorID] = session
}

func (b *Bot) getModChangePriceSession(moderatorID int64) (modChangePriceSession, bool) {
	b.modChangePriceMu.RLock()
	defer b.modChangePriceMu.RUnlock()
	session, ok := b.modChangePriceData[moderatorID]
	return session, ok
}

func (b *Bot) clearModChangePriceSession(moderatorID int64) {
	b.modChangePriceMu.Lock()
	defer b.modChangePriceMu.Unlock()
	delete(b.modChangePriceData, moderatorID)
}

// handleModeratorDeleteInviteRequest запрашивает код для удаления.
func (b *Bot) handleModeratorDeleteInviteRequest(c tele.Context) error {
	b.userStates.Set(c.Sender().ID, StateWaitModDeleteInvite)
	return c.Send("<b>🗑 Удаление приглашения</b>\n\nВведите код для удаления:", &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: CancelKeyboard(),
	})
}

// processModeratorDeleteInvite обрабатывает удаление инвайта модератором.
func (b *Bot) processModeratorDeleteInvite(c tele.Context, code string) error {
	b.userStates.Delete(c.Sender().ID)
	code = strings.TrimSpace(code)

	err := b.db.DeleteUnusedInviteByOwner(code, c.Sender().ID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "not owned") {
			return c.Send("❌ Код не найден, уже использован или не ваш.\nМожно удалить только свои неиспользованные коды.", &tele.SendOptions{
				ReplyMarkup: ModeratorMenuKeyboard(),
			})
		}
		slog.Error("Failed to delete invite by moderator", "error", err)
		return c.Send("Ошибка удаления кода", &tele.SendOptions{ReplyMarkup: ModeratorMenuKeyboard()})
	}

	return c.Send(fmt.Sprintf("✅ Код <code>%s</code> удалён", code), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: ModeratorMenuKeyboard(),
	})
}

// handleModeratorBack возвращает модератора в пользовательское меню.
func (b *Bot) handleModeratorBack(c tele.Context) error {
	b.userStates.Delete(c.Sender().ID)
	b.clearModChangePriceSession(c.Sender().ID)
	return c.Send(MsgWelcomeBack, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: b.userKeyboard(c.Sender().ID),
	})
}

// cascadeDeleteModerator удаляет все неиспользованные инвайты модератора и снимает роль.
func (b *Bot) cascadeDeleteModerator(telegramID int64) {
	count, err := b.db.DeleteUnusedInvitesByCreator(telegramID)
	if err != nil {
		slog.Error("Failed to delete moderator invites", "error", err, "telegram_id", telegramID)
	} else if count > 0 {
		slog.Info("Deleted unused invites of moderator", "count", count, "telegram_id", telegramID)
	}

	if err := b.db.RemoveModerator(telegramID); err != nil {
		slog.Error("Failed to remove moderator", "error", err, "telegram_id", telegramID)
	}
}

func (b *Bot) minSubscriptionPrice() int {
	if b.config != nil && b.config.MinSubscriptionPrice > 0 {
		return b.config.MinSubscriptionPrice
	}
	return 400
}

func (b *Bot) describeSubscriberStatus(telegramID int64, remUser remnawave.User, now time.Time) string {
	if remUser.Status == remnawave.StatusDisabled && !remUser.ExpireAt.After(now) {
		return "grace"
	}
	if b.isTrialUser(telegramID) {
		return "trial"
	}
	if remUser.Status == remnawave.StatusExpired || !remUser.ExpireAt.After(now) {
		return "expired"
	}
	return "paid"
}

func formatPriceLabel(price *int) string {
	if price == nil {
		return "не установлена"
	}
	return fmt.Sprintf("%d руб/мес", *price)
}

func daysUntil(target, now time.Time) int {
	days := int(target.Sub(now).Hours()/24) + 1
	if days < 0 {
		return 0
	}
	return days
}

func monthNameRu(month time.Month) string {
	switch month {
	case time.January:
		return "январь"
	case time.February:
		return "февраль"
	case time.March:
		return "март"
	case time.April:
		return "апрель"
	case time.May:
		return "май"
	case time.June:
		return "июнь"
	case time.July:
		return "июль"
	case time.August:
		return "август"
	case time.September:
		return "сентябрь"
	case time.October:
		return "октябрь"
	case time.November:
		return "ноябрь"
	case time.December:
		return "декабрь"
	default:
		return ""
	}
}
