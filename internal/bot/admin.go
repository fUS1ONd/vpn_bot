package bot

import (
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
	tele "gopkg.in/telebot.v3"
)

// Состояния админа
const (
	StateWaitBanUser                          = "wait_ban_user"                             // Ожидание telegram_id для бана
	StateWaitDeleteInvite                     = "wait_delete_invite"                        // Ожидание кода для удаления
	StateWaitAddModerator                     = "wait_add_moderator"                        // Ожидание telegram_id для назначения модератора
	StateWaitRemoveModerator                  = "wait_remove_moderator"                     // Ожидание telegram_id для снятия модератора
	StateWaitAdminUserInfo                    = "wait_admin_user_info"                      // Ожидание telegram_id для карточки пользователя
	StateWaitAdminChangePriceID               = "wait_admin_change_price_id"                // Ожидание telegram_id для смены цены
	StateWaitAdminChangePriceValue            = "wait_admin_change_price_value"             // Ожидание новой цены подписки
	StateWaitAdminChangePriceMigrationConfirm = "wait_admin_change_price_migration_confirm" // Ожидание подтверждения migration-case
	StateWaitSwitchSubscriptionID             = "wait_switch_subscription_id"               // Ожидание telegram_id для смены тарифа
	StateWaitSwitchSubscriptionConfirm        = "wait_switch_subscription_confirm"          // Ожидание подтверждения смены тарифа
)

type adminSwitchSession struct {
	TargetTelegramID int64
	TargetLabel      string
	UserUUID         string
}

type adminChangePriceSession struct {
	TargetTelegramID          int64
	TargetLabel               string
	CurrentPrice              int
	HasCurrentPrice           bool
	PendingPrice              int
	HasPendingPrice           bool
	ShouldAskMigrationConfirm bool
	CurrentExpireAt           *time.Time
}

// isAdmin проверяет, является ли пользователь админом
func (b *Bot) isAdmin(c tele.Context) bool {
	return c.Sender().ID == b.config.AdminID
}

// handleAdminStart показывает главное меню админа
func (b *Bot) handleAdminStart(c tele.Context) error {
	return c.Send(MsgAdminWelcome, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminKeyboard(b.isMaintenanceMode()),
	})
}

// handleAdminManageMenu показывает меню управления
func (b *Bot) handleAdminManageMenu(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}
	return c.Send("<b>Управление</b>\n\nВыберите действие:", &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminManageKeyboard(),
	})
}

// handleCreateInvite создаёт новый инвайт-код
func (b *Bot) handleCreateInvite(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	// Создаём инвайт (код генерируется автоматически в БД)
	invite, err := b.db.CreateInviteWithExpiry(c.Sender().ID, nil)
	if err != nil {
		slog.Error("Failed to create invite", "error", err)
		return c.Send("Ошибка создания инвайта", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	msg := fmt.Sprintf(MsgInviteCreated, b.getBotUsername(), invite.Code)
	return c.Send(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminManageKeyboard(),
	})
}

// handleBanUserRequest запрашивает telegram_id для бана
func (b *Bot) handleBanUserRequest(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	b.userStates.Set(c.Sender().ID, StateWaitBanUser)
	return c.Send(MsgEnterBanUser, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: CancelKeyboard(),
	})
}

// handleSwitchSubscription показывает подменю смены тарифа.
func (b *Bot) handleSwitchSubscription(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	return c.Send("<b>♾️ Смена тарифа</b>\n\nВыберите действие:", &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminSwitchSubmenu(),
	})
}

// handleAdminSwitchInfiniteRequest запрашивает telegram_id для перевода на бессрочный тариф.
func (b *Bot) handleAdminSwitchInfiniteRequest(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	b.userStates.Set(c.Sender().ID, StateWaitSwitchSubscriptionID)
	b.clearAdminSwitchSession(c.Sender().ID)

	return c.Send("<b>♾️ Смена тарифа</b>\n\nВведите telegram_id пользователя:", &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: CancelKeyboard(),
	})
}

// processSwitchSubscriptionID проверяет пользователя и показывает карточку подтверждения.
func (b *Bot) processSwitchSubscriptionID(c tele.Context, text string) error {
	adminID := c.Sender().ID
	targetID, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil {
		return c.Send("Неверный telegram_id", &tele.SendOptions{ReplyMarkup: CancelKeyboard()})
	}

	isBanned, err := b.db.IsBanned(targetID)
	if err != nil {
		slog.Error("Failed to check ban status before switching subscription", "error", err, "telegram_id", targetID)
		b.userStates.Delete(adminID)
		return c.Send("Ошибка проверки пользователя", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}
	if isBanned {
		b.userStates.Delete(adminID)
		return c.Send("Этот пользователь забанен", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	user, err := b.db.GetUserByTelegramID(targetID)
	if err != nil {
		slog.Error("Failed to get user before switching subscription", "error", err, "telegram_id", targetID)
		b.userStates.Delete(adminID)
		return c.Send("Ошибка получения пользователя", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}
	if user == nil {
		b.userStates.Delete(adminID)
		return c.Send("Пользователь не найден", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	invite, err := b.db.GetInviteByUsedBy(targetID)
	if err != nil {
		slog.Error("Failed to get invite before switching subscription", "error", err, "telegram_id", targetID)
		b.userStates.Delete(adminID)
		return c.Send("Ошибка проверки инвайта пользователя", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}
	if invite == nil {
		b.userStates.Delete(adminID)
		return c.Send("Инвайт для этого пользователя не найден", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}
	if invite.ExpireDays == nil {
		b.userStates.Delete(adminID)
		return c.Send("Этот пользователь уже на бессрочном тарифе", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	remUser, err := b.remnawave.GetUser(user.RemnawaveUUID)
	if err != nil {
		slog.Error("Failed to get user from Remnawave before switching subscription", "error", err, "telegram_id", targetID)
		b.userStates.Delete(adminID)
		return c.Send("Ошибка при получении данных подписки, попробуйте позже", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	targetLabel := formatAdminSwitchTargetLabel(user)
	curatorLabel := b.formatAdminSwitchCurator(invite.CreatedBy)
	expireAt := remUser.ExpireAt.UTC().Format("02.01.2006")

	b.setAdminSwitchSession(adminID, adminSwitchSession{
		TargetTelegramID: targetID,
		TargetLabel:      targetLabel,
		UserUUID:         user.RemnawaveUUID,
	})
	b.userStates.Set(adminID, StateWaitSwitchSubscriptionConfirm)

	msg := fmt.Sprintf(
		"<b>Перевод на бессрочный тариф</b>\n\nИмя: %s\nКуратор: %s\nТекущий срок: до %s\n\nПеревести на бессрочную подписку?",
		targetLabel,
		curatorLabel,
		expireAt,
	)

	return c.Send(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: ConfirmKeyboard(),
	})
}

// handleAdminUserInfoRequest запускает диалог просмотра карточки пользователя.
func (b *Bot) handleAdminUserInfoRequest(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	b.userStates.Set(c.Sender().ID, StateWaitAdminUserInfo)
	return c.Send("Введите telegram_id пользователя:", &tele.SendOptions{
		ReplyMarkup: CancelKeyboard(),
	})
}

// processAdminUserInfo показывает полную карточку пользователя.
func (b *Bot) processAdminUserInfo(c tele.Context, text string) error {
	adminID := c.Sender().ID
	targetID, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil {
		return c.Send("❌ Неверный telegram_id.", &tele.SendOptions{ReplyMarkup: CancelKeyboard()})
	}

	b.userStates.Delete(adminID)

	isBanned, err := b.db.IsBanned(targetID)
	if err != nil {
		slog.Error("Failed to check ban status for admin user info", "error", err, "telegram_id", targetID)
		return c.Send("Ошибка проверки пользователя", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}
	if isBanned {
		return c.Send("🚫 Пользователь забанен.", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	dbUser, err := b.db.GetUserByTelegramID(targetID)
	if err != nil {
		slog.Error("Failed to load DB user for admin info", "error", err, "telegram_id", targetID)
		return c.Send("Ошибка получения пользователя", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}
	if dbUser == nil {
		return c.Send("❌ Пользователь не найден.", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	remUser, err := b.remnawave.GetUser(dbUser.RemnawaveUUID)
	if err != nil {
		slog.Error("Failed to load Remnawave user for admin info", "error", err, "telegram_id", targetID)
		return c.Send("Ошибка получения данных подписки", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	invite, err := b.db.GetInviteByUsedBy(targetID)
	if err != nil {
		slog.Error("Failed to load invite for admin info", "error", err, "telegram_id", targetID)
		return c.Send("Ошибка получения данных пользователя", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	curatorLabel := "админ"
	switch {
	case dbUser.ModeratorID != nil:
		curatorLabel = b.formatAdminSwitchCurator(*dbUser.ModeratorID)
	case invite != nil:
		curatorLabel = b.formatAdminSwitchCurator(invite.CreatedBy)
	}

	devicesLabel := "н/д"
	devicesCount, err := b.remnawave.GetUserHwidDevicesCount(dbUser.RemnawaveUUID)
	if err != nil {
		slog.Error("Failed to load user HWID devices for admin info", "error", err, "telegram_id", targetID)
	} else if remUser.HwidDeviceLimit > 0 {
		devicesLabel = fmt.Sprintf("%d / %d", devicesCount, remUser.HwidDeviceLimit)
	} else {
		devicesLabel = fmt.Sprintf("%d", devicesCount)
	}

	trafficLabel := "0.00 GB"
	if remUser.UserTraffic != nil {
		trafficLabel = fmt.Sprintf("%.2f GB", float64(remUser.UserTraffic.UsedTrafficBytes)/(1024*1024*1024))
	}

	typeLabel, statusLabel := b.describeAdminUserSubscription(targetID, remUser)
	statusEmoji := adminStatusEmoji(statusLabel)

	var msg strings.Builder
	msg.WriteString("<b>🔍 Информация о пользователе</b>\n\n")
	fmt.Fprintf(&msg, "👤 %s\n", formatUserLabel(dbUser.FirstName, dbUser.Username, dbUser.TelegramID))
	fmt.Fprintf(&msg, "📋 Куратор: %s\n", curatorLabel)
	fmt.Fprintf(&msg, "💳 Цена подписки: %s\n", formatPriceLabel(dbUser.SubscriptionPrice))
	if remUser.ExpireAt.Year() < 2099 {
		fmt.Fprintf(
			&msg,
			"📅 Подписка до: %s (%s)\n",
			remUser.ExpireAt.UTC().Format("02.01.2006"),
			describeAdminRemaining(remUser.ExpireAt, time.Now().UTC()),
		)
	}
	fmt.Fprintf(&msg, "📊 Трафик за месяц: %s\n", trafficLabel)
	fmt.Fprintf(&msg, "📡 Устройства: %s\n", devicesLabel)
	fmt.Fprintf(&msg, "🏷 Тип: %s\n", typeLabel)
	fmt.Fprintf(&msg, "%s Статус: %s", statusEmoji, statusLabel)

	return c.Send(msg.String(), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminUserInfoKeyboard(targetID, remUser),
	})
}

// handleAdminChangePriceRequest запускает диалог изменения цены.
func (b *Bot) handleAdminChangePriceRequest(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	telegramID := c.Sender().ID
	b.userStates.Set(telegramID, StateWaitAdminChangePriceID)
	b.clearAdminChangePriceSession(telegramID)
	return c.Send("Введите telegram_id пользователя:", &tele.SendOptions{
		ReplyMarkup: CancelKeyboard(),
	})
}

// processAdminChangePriceID выбирает пользователя для смены цены.
func (b *Bot) processAdminChangePriceID(c tele.Context, text string) error {
	adminID := c.Sender().ID
	targetID, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil {
		return c.Send("❌ Неверный telegram_id.", &tele.SendOptions{ReplyMarkup: CancelKeyboard()})
	}

	isBanned, err := b.db.IsBanned(targetID)
	if err != nil {
		slog.Error("Failed to check ban status before admin price change", "error", err, "telegram_id", targetID)
		b.userStates.Delete(adminID)
		return c.Send("Ошибка проверки пользователя", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}
	if isBanned {
		b.userStates.Delete(adminID)
		return c.Send("❌ Этот пользователь забанен.", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	dbUser, err := b.db.GetUserByTelegramID(targetID)
	if err != nil {
		slog.Error("Failed to load DB user before admin price change", "error", err, "telegram_id", targetID)
		b.userStates.Delete(adminID)
		return c.Send("Ошибка получения пользователя", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}
	if dbUser == nil {
		b.userStates.Delete(adminID)
		return c.Send("❌ Пользователь не найден.", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	invite, err := b.db.GetInviteByUsedBy(targetID)
	if err != nil {
		slog.Error("Failed to load invite before admin price change", "error", err, "telegram_id", targetID)
		b.userStates.Delete(adminID)
		return c.Send("Ошибка получения пользователя", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}
	if invite == nil {
		b.userStates.Delete(adminID)
		return c.Send("❌ Пользователь не найден.", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}
	if invite.ExpireDays == nil {
		b.userStates.Delete(adminID)
		return c.Send("❌ У этого пользователя бессрочная подписка, цена не применяется.", &tele.SendOptions{
			ReplyMarkup: AdminManageKeyboard(),
		})
	}

	label := formatAdminSwitchTargetLabel(dbUser)
	session := adminChangePriceSession{
		TargetTelegramID: targetID,
		TargetLabel:      label,
	}
	if dbUser.SubscriptionPrice != nil {
		session.CurrentPrice = *dbUser.SubscriptionPrice
		session.HasCurrentPrice = true
	}
	if dbUser.SubscriptionPrice == nil {
		remUser, err := b.remnawave.GetUser(dbUser.RemnawaveUUID)
		if err != nil {
			slog.Error("Failed to load Remnawave user before migration check", "error", err, "telegram_id", targetID)
			b.userStates.Delete(adminID)
			b.clearAdminChangePriceSession(adminID)
			return c.Send("Ошибка проверки пользователя, попробуйте позже", &tele.SendOptions{
				ReplyMarkup: AdminManageKeyboard(),
			})
		} else if b.shouldPromptAdminChangePriceMigration(dbUser, invite, remUser) {
			session.ShouldAskMigrationConfirm = true
			expireAt := remUser.ExpireAt
			session.CurrentExpireAt = &expireAt
		}
	}

	b.setAdminChangePriceSession(adminID, session)
	b.userStates.Set(adminID, StateWaitAdminChangePriceValue)

	return c.Send(
		fmt.Sprintf(
			"Текущая цена для %s: %s\nВведите новую цену (минимум %d руб):",
			label,
			formatAdminPriceValue(dbUser.SubscriptionPrice),
			b.minSubscriptionPrice(),
		),
		&tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: CancelKeyboard(),
		},
	)
}

// processAdminChangePriceValue завершает изменение цены.
func (b *Bot) processAdminChangePriceValue(c tele.Context, text string) error {
	adminID := c.Sender().ID
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

	session, ok := b.getAdminChangePriceSession(adminID)
	if !ok {
		b.userStates.Delete(adminID)
		return c.Send("Сессия изменения цены потеряна. Начните заново.", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	if session.ShouldAskMigrationConfirm {
		session.PendingPrice = newPrice
		session.HasPendingPrice = true
		b.setAdminChangePriceSession(adminID, session)
		b.userStates.Set(adminID, StateWaitAdminChangePriceMigrationConfirm)

		expireText := "неизвестна"
		if session.CurrentExpireAt != nil {
			expireText = session.CurrentExpireAt.Format("02.01.2006")
		}

		return c.Send(
			fmt.Sprintf(
				"Срок в панели: до <b>%s</b>\n\nТекущий период уже оплачен вручную?\n\nНовая цена подписки: <b>%d руб.</b>",
				expireText,
				newPrice,
			),
			&tele.SendOptions{
				ParseMode:   tele.ModeHTML,
				ReplyMarkup: AdminChangePriceMigrationKeyboard(),
			},
		)
	}

	if err := b.applyAdminChangePrice(session.TargetTelegramID, newPrice, nil); err != nil {
		slog.Error("Failed to update subscription price by admin", "error", err, "telegram_id", session.TargetTelegramID)
		b.userStates.Delete(adminID)
		b.clearAdminChangePriceSession(adminID)
		return c.Send("Ошибка изменения цены", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	b.userStates.Delete(adminID)
	b.clearAdminChangePriceSession(adminID)
	b.notifyUserAboutPriceChange(session.TargetTelegramID, newPrice)

	return c.Send(
		fmt.Sprintf(
			"✅ Цена подписки для %s изменена: %s → %d руб/мес",
			session.TargetLabel,
			formatAdminOldPrice(session),
			newPrice,
		),
		&tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: AdminManageKeyboard(),
		},
	)
}

// processAdminChangePriceMigrationConfirm завершает смену цены после migration-question.
func (b *Bot) processAdminChangePriceMigrationConfirm(c tele.Context, text string) error {
	adminID := c.Sender().ID
	answer := strings.TrimSpace(text)

	session, ok := b.getAdminChangePriceSession(adminID)
	if !ok {
		b.userStates.Delete(adminID)
		return c.Send("Сессия изменения цены потеряна. Начните заново.", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}
	if !session.HasPendingPrice {
		b.userStates.Delete(adminID)
		b.clearAdminChangePriceSession(adminID)
		return c.Send("Сессия изменения цены потеряна. Начните заново.", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	var legacyPaidMigrated *bool
	var successSuffix string
	switch answer {
	case BtnAdminMigrationPaidYes:
		value := true
		legacyPaidMigrated = &value
		successSuffix = "Пользователь помечен как уже оплаченный."
	case BtnAdminMigrationPaidNo:
		value := false
		legacyPaidMigrated = &value
		successSuffix = "Пользователь оставлен в trial до первой оплаты."
	case BtnCancel:
		b.userStates.Delete(adminID)
		b.clearAdminChangePriceSession(adminID)
		return c.Send("Отменено", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	default:
		return c.Send("Выберите вариант ответа или нажмите «Отмена».", &tele.SendOptions{
			ReplyMarkup: AdminChangePriceMigrationKeyboard(),
		})
	}

	if err := b.applyAdminChangePrice(session.TargetTelegramID, session.PendingPrice, legacyPaidMigrated); err != nil {
		slog.Error("Failed to finalize admin price change", "error", err, "telegram_id", session.TargetTelegramID)
		b.userStates.Delete(adminID)
		b.clearAdminChangePriceSession(adminID)
		return c.Send("Ошибка изменения цены", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	b.userStates.Delete(adminID)
	b.clearAdminChangePriceSession(adminID)
	b.notifyUserAboutPriceChange(session.TargetTelegramID, session.PendingPrice)

	return c.Send(
		fmt.Sprintf(
			"✅ Цена подписки для %s изменена: %s → %d руб/мес\n%s",
			session.TargetLabel,
			formatAdminOldPrice(session),
			session.PendingPrice,
			successSuffix,
		),
		&tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: AdminManageKeyboard(),
		},
	)
}

// processSwitchSubscriptionConfirm подтверждает перевод на бессрочный тариф.
func (b *Bot) processSwitchSubscriptionConfirm(c tele.Context, text string) error {
	adminID := c.Sender().ID
	answer := strings.TrimSpace(text)
	if answer != BtnConfirmYes {
		return c.Send("Нажмите 'Да' для подтверждения или 'Отмена' для выхода.", &tele.SendOptions{ReplyMarkup: ConfirmKeyboard()})
	}

	session, ok := b.getAdminSwitchSession(adminID)
	if !ok {
		b.userStates.Delete(adminID)
		return c.Send("Сессия смены тарифа потеряна. Начните заново.", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	remUser, err := b.remnawave.GetUser(session.UserUUID)
	if err != nil {
		slog.Error("Failed to refresh user before switching subscription", "error", err, "telegram_id", session.TargetTelegramID)
		b.userStates.Delete(adminID)
		b.clearAdminSwitchSession(adminID)
		return c.Send("Ошибка при обновлении подписки, попробуйте позже", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	unlimitedExpireAt := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)

	if remUser.Status == remnawave.StatusExpired || remUser.Status == remnawave.StatusDisabled {
		// EnableUser одним вызовом ставит ACTIVE + ExpireAt + безлимит трафика
		if err := b.remnawave.EnableUser(session.UserUUID, unlimitedExpireAt); err != nil {
			slog.Error("Failed to enable user before switching subscription", "error", err, "telegram_id", session.TargetTelegramID)
			b.userStates.Delete(adminID)
			b.clearAdminSwitchSession(adminID)
			return c.Send("Ошибка при обновлении подписки, попробуйте позже", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
		}
	}

	if err := b.remnawave.UpdateUser(session.UserUUID, remnawave.UpdateUserRequest{
		UUID:     session.UserUUID,
		ExpireAt: strPtr(unlimitedExpireAt.Format(time.RFC3339)),
	}); err != nil {
		slog.Error("Failed to patch user expireAt while switching subscription", "error", err, "telegram_id", session.TargetTelegramID)
		b.userStates.Delete(adminID)
		b.clearAdminSwitchSession(adminID)
		return c.Send("Ошибка при обновлении подписки, попробуйте позже", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	if err := b.db.UpdateInviteExpireDays(session.TargetTelegramID, nil); err != nil {
		slog.Error("Failed to update invite expire_days while switching subscription", "error", err, "telegram_id", session.TargetTelegramID)
		b.userStates.Delete(adminID)
		b.clearAdminSwitchSession(adminID)
		return c.Send("Ошибка при обновлении подписки, попробуйте позже", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	if err := b.db.ClearNotifications(session.TargetTelegramID); err != nil {
		slog.Error("Failed to clear notifications after switching subscription", "error", err, "telegram_id", session.TargetTelegramID)
	}

	b.userStates.Delete(adminID)
	b.clearAdminSwitchSession(adminID)

	return c.Send(
		fmt.Sprintf("✅ Пользователь %s переведён на бессрочный тариф.", session.TargetLabel),
		&tele.SendOptions{ParseMode: tele.ModeHTML, ReplyMarkup: AdminManageKeyboard()},
	)
}

// processBanUser обрабатывает бан пользователя
func (b *Bot) processBanUser(c tele.Context, text string) error {
	b.userStates.Delete(c.Sender().ID)

	telegramID, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil {
		return c.Send("Неверный telegram_id", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	// Защита от само-бана
	if telegramID == b.config.AdminID {
		return c.Send("❌ Нельзя забанить себя", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	// Находим пользователя в БД
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil {
		return c.Send("Пользователь не найден", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	// Фиксируем перманентный бан.
	if err := b.db.BanUser(telegramID, c.Sender().ID); err != nil {
		slog.Error("Failed to persist user ban", "error", err, "telegram_id", telegramID)
		return c.Send("Ошибка сохранения бана", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	// Каскадное удаление: если пользователь — модератор
	if b.isModerator(telegramID) {
		b.cascadeDeleteModerator(telegramID)
	}

	// Удаляем из Remnawave (отключаем доступ к серверам)
	err = b.remnawave.DeleteUser(user.RemnawaveUUID)
	if err != nil {
		slog.Error("Failed to delete user from Remnawave", "error", err)
		// Продолжаем удаление из БД даже если не удалось удалить из Remnawave
	}

	// Удаляем из БД бота (отключаем доступ к боту)
	err = b.db.DeleteUser(telegramID)
	if err != nil {
		slog.Error("Failed to delete user from DB", "error", err)
		return c.Send("Ошибка удаления пользователя из БД", &tele.SendOptions{ReplyMarkup: AdminManageKeyboard()})
	}

	// Очищаем маркеры отправленных уведомлений.
	if err := b.db.ClearNotifications(telegramID); err != nil {
		slog.Error("Failed to clear notifications for banned user", "error", err, "telegram_id", telegramID)
	}

	msg := fmt.Sprintf("🚫 Пользователь %d забанен\n• Удалён из БД бота\n• Удалён из Remnawave", telegramID)
	return c.Send(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminManageKeyboard(),
	})
}

func formatAdminSwitchTargetLabel(user *database.User) string {
	if user == nil {
		return "пользователь"
	}
	if user.FirstName != "" && user.Username != "" {
		return fmt.Sprintf("%s (@%s)", html.EscapeString(user.FirstName), user.Username)
	}
	if user.Username != "" {
		return "@" + user.Username
	}
	if user.FirstName != "" {
		return html.EscapeString(user.FirstName)
	}
	return fmt.Sprintf("<code>%d</code>", user.TelegramID)
}

func (b *Bot) formatAdminSwitchCurator(curatorID int64) string {
	user, err := b.db.GetUserByTelegramID(curatorID)
	if err != nil {
		slog.Error("Failed to get curator while building switch subscription message", "error", err, "telegram_id", curatorID)
	}
	if user != nil {
		if user.Username != "" {
			return "@" + user.Username
		}
		if user.FirstName != "" {
			return html.EscapeString(user.FirstName)
		}
	}
	if curatorID == b.config.AdminID {
		return "админ"
	}
	return fmt.Sprintf("<code>%d</code>", curatorID)
}

func (b *Bot) setAdminSwitchSession(adminID int64, session adminSwitchSession) {
	b.adminSwitchMu.Lock()
	defer b.adminSwitchMu.Unlock()
	if b.adminSwitchData == nil {
		b.adminSwitchData = make(map[int64]adminSwitchSession)
	}
	b.adminSwitchData[adminID] = session
}

func (b *Bot) getAdminSwitchSession(adminID int64) (adminSwitchSession, bool) {
	b.adminSwitchMu.RLock()
	defer b.adminSwitchMu.RUnlock()
	session, ok := b.adminSwitchData[adminID]
	return session, ok
}

func (b *Bot) clearAdminSwitchSession(adminID int64) {
	b.adminSwitchMu.Lock()
	defer b.adminSwitchMu.Unlock()
	delete(b.adminSwitchData, adminID)
}

func strPtr(v string) *string {
	return &v
}

// calculateMonthlyPaymentFinance считает комиссии платежа по текущим конфигурационным ставкам.
// Используется для расчёта финансовой статистики в отчёте админа.
func (b *Bot) calculateMonthlyPaymentFinance(payment database.MonthlyConfirmedPayment) (plategaFee, withdrawalFee, netAmount int) {
	feePercent := b.getPlategaFeePercent(payment.PaymentMethod)
	grossAmount := payment.Amount
	plategaFee = grossAmount * feePercent / 100
	afterPlatega := grossAmount - plategaFee
	withdrawalFee = afterPlatega * b.config.PlategaFeeWithdrawal / 100
	netAmount = afterPlatega - withdrawalFee
	return
}

// handleAdminStats показывает общую финансовую и пользовательскую статистику за текущий месяц.
func (b *Bot) handleAdminStats(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	now := time.Now().UTC()
	year := now.Year()
	month := int(now.Month())

	// Источник финансовой статистики — все подтверждённые платежи месяца.
	// Платежи без earnings (админские) включаются с share_amount = 0.
	confirmedPayments, err := b.db.GetConfirmedPaymentsByMonth(year, month)
	if err != nil {
		slog.Error("Failed to load monthly confirmed payments for admin stats", "error", err)
		return c.Send("Ошибка получения статистики", &tele.SendOptions{ReplyMarkup: AdminKeyboard(b.isMaintenanceMode())})
	}

	monthEarnings := &database.MonthlyEarnings{}
	for _, payment := range confirmedPayments {
		plategaFee, withdrawalFee, netAmount := b.calculateMonthlyPaymentFinance(payment)
		monthEarnings.TotalPayments++
		monthEarnings.GrossAmount += payment.Amount
		monthEarnings.TotalPlategaFee += plategaFee
		monthEarnings.TotalWithdrawal += withdrawalFee
		monthEarnings.TotalNetAmount += netAmount
		monthEarnings.TotalShareAmount += payment.ShareAmount
	}

	trialsThisMonth, err := b.db.CountTrialsByMonth(year, month)
	if err != nil {
		slog.Error("Failed to count trials for admin stats", "error", err)
		return c.Send("Ошибка получения статистики", &tele.SendOptions{ReplyMarkup: AdminKeyboard(b.isMaintenanceMode())})
	}

	firstPayments, err := b.db.CountFirstPaymentsByMonth(year, month)
	if err != nil {
		slog.Error("Failed to count first payments for admin stats", "error", err)
		return c.Send("Ошибка получения статистики", &tele.SendOptions{ReplyMarkup: AdminKeyboard(b.isMaintenanceMode())})
	}

	dbUsers, err := b.db.GetAllUsers()
	if err != nil {
		slog.Error("Failed to load DB users for admin stats", "error", err)
		return c.Send("Ошибка получения статистики", &tele.SendOptions{ReplyMarkup: AdminKeyboard(b.isMaintenanceMode())})
	}

	remUsers, err := b.remnawave.GetAllUsers()
	if err != nil {
		slog.Error("Failed to load Remnawave users for admin stats", "error", err)
		return c.Send("Ошибка получения статистики из панели", &tele.SendOptions{ReplyMarkup: AdminKeyboard(b.isMaintenanceMode())})
	}

	byTelegramID := make(map[int64]remnawave.User, len(remUsers))
	for _, user := range remUsers {
		if user.TelegramID == nil || *user.TelegramID == 0 {
			continue
		}
		byTelegramID[*user.TelegramID] = user
	}

	payingCount := 0
	trialCount := 0
	graceCount := 0
	infiniteCount := 0

	// graceDeadline — граница 72 часа назад для определения grace period
	graceDeadline := now.Add(-72 * time.Hour)

	for _, user := range dbUsers {
		remUser, ok := byTelegramID[user.TelegramID]
		if !ok {
			continue
		}

		switch {
		case remUser.ExpireAt.Year() >= 2099:
			infiniteCount++
		case remUser.Status == remnawave.StatusDisabled &&
			!remUser.ExpireAt.After(now) &&
			remUser.ExpireAt.After(graceDeadline):
			graceCount++
		case b.isTrialUser(user.TelegramID):
			trialCount++
		case remUser.Status == remnawave.StatusActive && remUser.ExpireAt.After(now):
			payingCount++
		}
	}
	totalUsers := payingCount + trialCount + graceCount + infiniteCount

	conversion := 0
	if trialsThisMonth > 0 {
		conversion = firstPayments * 100 / trialsThisMonth
	}

	ownerIncome := monthEarnings.TotalNetAmount - monthEarnings.TotalShareAmount

	msg := fmt.Sprintf(
		"<b>📊 Общая статистика — %s %d</b>\n\n"+
			"💰 <b>Финансы за %s %d</b>\n"+
			"├ Платежей за месяц: %d\n"+
			"├ Сумма платежей (грязная): %d руб\n"+
			"├ Комиссии Platega: -%d руб\n"+
			"├ Комиссия вывода (2%%): -%d руб\n"+
			"├ Чистый доход: %d руб\n"+
			"├ Выплаты модераторам: -%d руб\n"+
			"└ Доход владельца: %d руб\n\n"+
			"📈 <b>Воронка за %s %d</b>\n"+
			"└ Конверсия триал → оплата: %d%%\n\n"+
			"👥 <b>Текущее состояние пользователей</b>\n"+
			"├ Всего в системе: %d\n"+
			"├ 💳 Платящих: %d\n"+
			"├ ⏳ Триал: %d\n"+
			"├ ⚠️ Grace period: %d\n"+
			"└ ♾️ Бессрочных: %d",
		monthNameRu(now.Month()),
		now.Year(),
		monthNameRu(now.Month()),
		now.Year(),
		monthEarnings.TotalPayments,
		monthEarnings.GrossAmount,
		monthEarnings.TotalPlategaFee,
		monthEarnings.TotalWithdrawal,
		monthEarnings.TotalNetAmount,
		monthEarnings.TotalShareAmount,
		ownerIncome,
		monthNameRu(now.Month()),
		now.Year(),
		conversion,
		totalUsers,
		payingCount,
		trialCount,
		graceCount,
		infiniteCount,
	)

	return c.Send(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminKeyboard(b.isMaintenanceMode()),
	})
}

// handleAdminMaintenanceToggle переключает режим обслуживания.
func (b *Bot) handleAdminMaintenanceToggle(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	enabled := b.toggleMaintenanceMode()

	msg := "🔧 Режим обслуживания включён. Оплата и кики приостановлены."
	if !enabled {
		msg = "▶️ Штатный режим восстановлен. Оплата и scheduler работают."
	}

	return c.Send(msg, &tele.SendOptions{
		ReplyMarkup: AdminKeyboard(enabled),
	})
}

// handleAdminBroadcastMenu показывает меню рассылки
func (b *Bot) handleAdminBroadcastMenu(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	// Получаем количество активных пользователей из Remnawave
	users, err := b.remnawave.GetAllUsers()
	activeCount := 0
	if err != nil {
		slog.Error("Failed to get users from Remnawave for broadcast menu", "error", err)
	} else {
		slog.Info("Got users from Remnawave", "total", len(users))
		for _, u := range users {
			// Считаем только активных пользователей с привязанным Telegram ID
			if u.Status == remnawave.StatusActive && u.TelegramID != nil && *u.TelegramID != 0 {
				activeCount++
			}
		}
		slog.Info("Active users with TelegramID", "count", activeCount)
	}

	msg := fmt.Sprintf(MsgAdminBroadcastMenu, activeCount)
	return c.Send(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminBroadcastKeyboard(),
	})
}

// handleBroadcastActiveRequest запрашивает сообщение для рассылки активным
func (b *Bot) handleBroadcastActiveRequest(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	// Получаем количество активных с Telegram ID
	users, err := b.remnawave.GetAllUsers()
	activeCount := 0
	if err == nil {
		for _, u := range users {
			// Считаем только активных пользователей с привязанным Telegram ID
			if u.Status == remnawave.StatusActive && u.TelegramID != nil && *u.TelegramID != 0 {
				activeCount++
			}
		}
	}

	b.userStates.Set(c.Sender().ID, StateWaitBroadcastActive)
	return c.Send(fmt.Sprintf(MsgAdminEnterBroadcast, activeCount), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: CancelKeyboard(),
	})
}

// processBroadcastMessage отправляет рассылку активным пользователям
func (b *Bot) processBroadcastMessage(c tele.Context) error {
	b.userStates.Delete(c.Sender().ID)

	// Получаем всех активных пользователей из Remnawave
	remnawaveUsers, err := b.remnawave.GetAllUsers()
	if err != nil {
		slog.Error("Failed to get users from Remnawave", "error", err)
		return c.Send("Ошибка получения списка пользователей", &tele.SendOptions{ReplyMarkup: AdminBroadcastKeyboard()})
	}

	// Фильтруем только активных с TelegramID
	var activeUsers []remnawave.User
	for _, u := range remnawaveUsers {
		if u.Status == remnawave.StatusActive && u.TelegramID != nil && *u.TelegramID != 0 {
			activeUsers = append(activeUsers, u)
		}
	}

	if len(activeUsers) == 0 {
		return c.Send("Нет активных пользователей для рассылки", &tele.SendOptions{ReplyMarkup: AdminBroadcastKeyboard()})
	}

	// Отправляем статус
	statusMsg, err := c.Bot().Send(c.Sender(), fmt.Sprintf("Начинаю рассылку %d активным пользователям...", len(activeUsers)))
	if err != nil {
		return err
	}

	successCount := 0
	failCount := 0
	msg := c.Message()

	for _, user := range activeUsers {
		recipient := &tele.User{ID: *user.TelegramID}

		// Копируем сообщение (работает для текста, фото, видео и т.д.)
		_, err := c.Bot().Copy(recipient, msg, &tele.SendOptions{
			ParseMode: tele.ModeHTML,
		})

		if err != nil {
			slog.Error("Failed to send broadcast", "telegram_id", *user.TelegramID, "error", err)
			failCount++
		} else {
			successCount++
		}

		// Задержка для избежания rate limiting
		time.Sleep(50 * time.Millisecond)
	}

	resultMsg := fmt.Sprintf(MsgAdminBroadcastResult, successCount, failCount)
	_, _ = c.Bot().Edit(statusMsg, resultMsg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminBroadcastKeyboard(),
	})

	return nil
}

// handleViewInvites показывает список всех инвайт-кодов
func (b *Bot) handleViewInvites(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	invites, err := b.db.GetAllInvitesWithUsers()
	if err != nil {
		slog.Error("Failed to get invites", "error", err)
		return c.Send("Ошибка получения списка кодов", &tele.SendOptions{
			ReplyMarkup: AdminManageKeyboard(),
		})
	}

	if len(invites) == 0 {
		return c.Send("📋 Инвайт-кодов пока нет", &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: AdminManageKeyboard(),
		})
	}

	chunks := FormatInvitesListChunked(invites, 4000)
	for i, chunk := range chunks {
		opts := &tele.SendOptions{ParseMode: tele.ModeHTML}
		// Клавиатуру показываем только в последнем сообщении
		if i == len(chunks)-1 {
			opts.ReplyMarkup = AdminManageKeyboard()
		}
		if err := c.Send(chunk, opts); err != nil {
			return err
		}
	}
	return nil
}

// FormatInvitesListChunked разбивает список инвайтов на части, не превышающие maxLen символов
func FormatInvitesListChunked(invites []database.InviteWithUser, maxLen int) []string {
	if len(invites) == 0 {
		return nil
	}

	var chunks []string
	var current strings.Builder
	current.WriteString("<b>📋 Список инвайт-кодов</b>\n\n")

	for _, inv := range invites {
		entry := formatInviteEntry(inv)

		// Если добавление записи превысит лимит — сохраняем текущий чанк и начинаем новый
		if current.Len()+len(entry) > maxLen && current.Len() > 0 {
			chunks = append(chunks, current.String())
			current.Reset()
			current.WriteString("<b>📋 Список инвайт-кодов (продолжение)</b>\n\n")
		}

		current.WriteString(entry)
	}

	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}

	return chunks
}

// formatInviteEntry форматирует один инвайт для списка
func formatInviteEntry(inv database.InviteWithUser) string {
	var msg strings.Builder
	if inv.UsedBy != nil {
		msg.WriteString("✅ <b>Использован</b>\n")
		msg.WriteString(fmt.Sprintf("🔹 Код: <code>%s</code>\n", inv.Code))

		msg.WriteString("👤 " + formatUserLabel(inv.UserFirstName, inv.UserUsername, *inv.UsedBy) + "\n")

		if inv.UsedAt != nil {
			msg.WriteString(fmt.Sprintf("📅 %s\n", inv.UsedAt.Format("02.01.06 15:04")))
		}
	} else {
		msg.WriteString("⭕ <b>Не использован</b>\n")
		msg.WriteString(fmt.Sprintf("🔹 Код: <code>%s</code>\n", inv.Code))
		msg.WriteString(fmt.Sprintf("📅 Создан: %s\n", inv.CreatedAt.Format("02.01.06 15:04")))
	}

	// Автор кода (модератор или админ)
	if inv.CreatorUsername != "" {
		fmt.Fprintf(&msg, "✍️ @%s\n", inv.CreatorUsername)
	} else if inv.CreatorFirstName != "" {
		fmt.Fprintf(&msg, "✍️ %s\n", inv.CreatorFirstName)
	}

	msg.WriteString("\n")
	return msg.String()
}

// handleDeleteInviteRequest запрашивает код для удаления
func (b *Bot) handleDeleteInviteRequest(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	b.userStates.Set(c.Sender().ID, StateWaitDeleteInvite)
	return c.Send("<b>🗑 Удаление инвайт-кода</b>\n\nВведите код для удаления:", &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: CancelKeyboard(),
	})
}

// processDeleteInvite обрабатывает удаление инвайта
func (b *Bot) processDeleteInvite(c tele.Context, code string) error {
	b.userStates.Delete(c.Sender().ID)

	code = strings.TrimSpace(code)

	err := b.db.DeleteUnusedInvite(code)
	if err != nil {
		if strings.Contains(err.Error(), "not found or already used") {
			return c.Send("❌ Код не найден или уже использован.\nМожно удалить только неиспользованные коды.", &tele.SendOptions{
				ReplyMarkup: AdminManageKeyboard(),
			})
		}
		slog.Error("Failed to delete invite", "error", err)
		return c.Send("Ошибка удаления кода", &tele.SendOptions{
			ReplyMarkup: AdminManageKeyboard(),
		})
	}

	return c.Send(fmt.Sprintf("✅ Код <code>%s</code> удалён", code), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminManageKeyboard(),
	})
}

// --- Управление модераторами ---

// handleAdminModeratorMenu показывает меню управления модераторами
func (b *Bot) handleAdminModeratorMenu(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}
	return c.Send("<b>👥 Модераторы</b>\n\nВыберите действие:", &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminModeratorKeyboard(),
	})
}

// handleAdminAddModeratorRequest запрашивает telegram_id для назначения модератора
func (b *Bot) handleAdminAddModeratorRequest(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	b.userStates.Set(c.Sender().ID, StateWaitAddModerator)
	return c.Send("<b>➕ Назначить модератора</b>\n\nВведите telegram_id зарегистрированного пользователя:", &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: CancelKeyboard(),
	})
}

// processAddModerator обрабатывает назначение модератора
func (b *Bot) processAddModerator(c tele.Context, text string) error {
	b.userStates.Delete(c.Sender().ID)

	telegramID, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil {
		return c.Send("Неверный telegram_id", &tele.SendOptions{ReplyMarkup: AdminModeratorKeyboard()})
	}

	// Проверяем что пользователь существует
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil {
		return c.Send("❌ Пользователь не найден в БД бота", &tele.SendOptions{ReplyMarkup: AdminModeratorKeyboard()})
	}

	// Назначать модератором можно только бессрочных пользователей (админский инвайт).
	invite, err := b.db.GetInviteByUsedBy(telegramID)
	if err != nil {
		slog.Error("Failed to get invite for moderator validation", "error", err, "telegram_id", telegramID)
		return c.Send("Ошибка проверки приглашения пользователя", &tele.SendOptions{ReplyMarkup: AdminModeratorKeyboard()})
	}
	if invite != nil && invite.ExpireDays != nil {
		return c.Send("❌ Этот пользователь приглашён по месячному инвайту. Назначить модератором можно только пользователя с бессрочным (админским) приглашением.", &tele.SendOptions{
			ReplyMarkup: AdminModeratorKeyboard(),
		})
	}

	err = b.db.AddModerator(telegramID, c.Sender().ID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return c.Send("❌ Этот пользователь уже является модератором", &tele.SendOptions{ReplyMarkup: AdminModeratorKeyboard()})
		}
		slog.Error("Failed to add moderator", "error", err)
		return c.Send("Ошибка назначения модератора", &tele.SendOptions{ReplyMarkup: AdminModeratorKeyboard()})
	}

	msg := fmt.Sprintf("✅ Пользователь <code>%d</code> (@%s) назначен модератором", telegramID, user.Username)
	return c.Send(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminModeratorKeyboard(),
	})
}

// handleAdminListModerators показывает список модераторов
func (b *Bot) handleAdminListModerators(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	mods, err := b.db.GetAllModerators()
	if err != nil {
		slog.Error("Failed to get moderators", "error", err)
		return c.Send("Ошибка получения списка модераторов", &tele.SendOptions{ReplyMarkup: AdminModeratorKeyboard()})
	}

	if len(mods) == 0 {
		return c.Send("📋 Модераторов пока нет", &tele.SendOptions{
			ParseMode:   tele.ModeHTML,
			ReplyMarkup: AdminModeratorKeyboard(),
		})
	}

	var msg strings.Builder
	fmt.Fprintf(&msg, "<b>📋 Модераторы (%d)</b>\n\n", len(mods))

	for _, mod := range mods {
		fmt.Fprintf(&msg, "👤 %s\n", formatUserLabel(mod.FirstName, mod.Username, mod.TelegramID))
		fmt.Fprintf(&msg, "📨 Приглашено: %d\n\n", mod.InvitesCount)
	}

	return c.Send(msg.String(), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminModeratorKeyboard(),
	})
}

// handleAdminModStats показывает сводную статистику модераторов.
func (b *Bot) handleAdminModStats(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	mods, err := b.db.GetAllModerators()
	if err != nil {
		slog.Error("Failed to load moderators for stats", "error", err)
		return c.Send("Ошибка получения списка модераторов", &tele.SendOptions{ReplyMarkup: AdminModeratorKeyboard()})
	}
	if len(mods) == 0 {
		return c.Send("📊 Модераторов пока нет", &tele.SendOptions{ReplyMarkup: AdminModeratorKeyboard()})
	}

	remUsers, err := b.remnawave.GetAllUsers()
	if err != nil {
		slog.Error("Failed to load users from Remnawave for moderator stats", "error", err)
		return c.Send("Ошибка получения статистики из панели", &tele.SendOptions{ReplyMarkup: AdminModeratorKeyboard()})
	}

	byTelegramID := make(map[int64]remnawave.User, len(remUsers))
	for _, user := range remUsers {
		if user.TelegramID == nil || *user.TelegramID == 0 {
			continue
		}
		byTelegramID[*user.TelegramID] = user
	}

	now := time.Now().UTC()
	reportDate := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	reportYear := reportDate.Year()
	reportMonth := int(reportDate.Month())
	totalPayments := 0
	totalGross := 0
	totalShare := 0

	for _, mod := range mods {
		subs, err := b.db.GetSubscribersByModerator(mod.TelegramID)
		if err != nil {
			slog.Error("Failed to load subscribers for moderator stats", "error", err, "moderator_id", mod.TelegramID)
			continue
		}

		currentState := b.summarizeModeratorSubscriberStates(subs, byTelegramID, now)

		monthStats, err := b.db.GetModeratorEarningsByMonth(mod.TelegramID, reportYear, reportMonth)
		if err != nil {
			slog.Error("Failed to load moderator month earnings", "error", err, "moderator_id", mod.TelegramID)
			continue
		}
		totalEarnings, err := b.db.GetModeratorTotalEarnings(mod.TelegramID)
		if err != nil {
			slog.Error("Failed to load moderator total earnings", "error", err, "moderator_id", mod.TelegramID)
			continue
		}

		totalPayments += monthStats.TotalPayments
		totalGross += monthStats.GrossAmount
		totalShare += monthStats.TotalShareAmount

		sharePercent := monthStats.SharePercent
		msg := fmt.Sprintf(
			"📊 <b>Статистика: %s — %s %d</b>\n\n"+
				"💰 <b>Финансы за %s %d</b>\n"+
				"├ Платежи: %d руб\n"+
				"├ Комиссии Platega: -%d руб\n"+
				"├ Комиссия вывода (2%%): -%d руб\n"+
				"├ Чистый доход: %d руб\n"+
				"└ Доля модератора (%d%%): %d руб\n\n"+
				"💰 <b>За всё время</b>\n"+
				"└ Заработано: %d руб\n\n"+
				"👥 <b>Текущее состояние клиентов</b>\n"+
				"└ 💳 Платящих: %d │ ⏳ Триал: %d │ ⚠️ Grace: %d",
			formatAdminModeratorLabel(mod.FirstName, mod.Username, mod.TelegramID),
			monthNameRu(reportDate.Month()),
			reportYear,
			monthNameRu(reportDate.Month()),
			reportYear,
			monthStats.GrossAmount,
			monthStats.TotalPlategaFee,
			monthStats.TotalWithdrawal,
			monthStats.TotalNetAmount,
			sharePercent,
			monthStats.TotalShareAmount,
			totalEarnings,
			currentState.Paying,
			currentState.Trial,
			currentState.Grace,
		)

		if err := c.Send(msg, &tele.SendOptions{ParseMode: tele.ModeHTML}); err != nil {
			return err
		}
	}

	summary := fmt.Sprintf(
		"<b>Итого за %s %d</b>\n\n📥 Платежей: %d\n💰 Сумма платежей: %d руб\n💸 Выплаты модераторам: %d руб",
		monthNameRu(reportDate.Month()),
		reportYear,
		totalPayments,
		totalGross,
		totalShare,
	)

	return c.Send(summary, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminModeratorKeyboard(),
	})
}

// handleAdminRemoveModeratorRequest запрашивает telegram_id для снятия модератора
func (b *Bot) handleAdminRemoveModeratorRequest(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	b.userStates.Set(c.Sender().ID, StateWaitRemoveModerator)
	return c.Send("<b>➖ Снять модератора</b>\n\nВведите telegram_id модератора:", &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: CancelKeyboard(),
	})
}

// processRemoveModerator обрабатывает снятие модератора
func (b *Bot) processRemoveModerator(c tele.Context, text string) error {
	b.userStates.Delete(c.Sender().ID)

	telegramID, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil {
		return c.Send("Неверный telegram_id", &tele.SendOptions{ReplyMarkup: AdminModeratorKeyboard()})
	}

	if !b.isModerator(telegramID) {
		return c.Send("❌ Этот пользователь не является модератором", &tele.SendOptions{ReplyMarkup: AdminModeratorKeyboard()})
	}

	// Каскадное удаление инвайтов и снятие роли
	b.cascadeDeleteModerator(telegramID)

	msg := fmt.Sprintf("✅ Модератор <code>%d</code> снят. Неиспользованные инвайты удалены.", telegramID)
	return c.Send(msg, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminModeratorKeyboard(),
	})
}

func (b *Bot) setAdminChangePriceSession(adminID int64, session adminChangePriceSession) {
	b.adminPriceMu.Lock()
	defer b.adminPriceMu.Unlock()
	if b.adminPriceData == nil {
		b.adminPriceData = make(map[int64]adminChangePriceSession)
	}
	b.adminPriceData[adminID] = session
}

func (b *Bot) getAdminChangePriceSession(adminID int64) (adminChangePriceSession, bool) {
	b.adminPriceMu.RLock()
	defer b.adminPriceMu.RUnlock()
	session, ok := b.adminPriceData[adminID]
	return session, ok
}

func (b *Bot) clearAdminChangePriceSession(adminID int64) {
	b.adminPriceMu.Lock()
	defer b.adminPriceMu.Unlock()
	delete(b.adminPriceData, adminID)
}

func (b *Bot) notifyUserAboutPriceChange(telegramID int64, newPrice int) {
	if b.bot == nil {
		return
	}

	_, err := b.bot.Send(&tele.User{ID: telegramID}, fmt.Sprintf("💳 Цена вашей подписки изменена: %d руб/мес", newPrice))
	if err != nil {
		slog.Error("Failed to notify user about price change", "error", err, "telegram_id", telegramID)
	}
}

func (b *Bot) applyAdminChangePrice(telegramID int64, newPrice int, legacyPaidMigrated *bool) error {
	if err := b.db.UpdateSubscriptionPriceAndLegacyPaidMigrated(telegramID, newPrice, legacyPaidMigrated); err != nil {
		return err
	}
	if err := b.db.UpdateInviteSubscriptionPrice(telegramID, newPrice); err != nil {
		slog.Error("Failed to update invite subscription price by admin", "error", err, "telegram_id", telegramID)
	}
	return nil
}

func (b *Bot) shouldPromptAdminChangePriceMigration(dbUser *database.User, invite *database.Invite, remUser *remnawave.User) bool {
	if dbUser == nil || invite == nil || invite.ExpireDays == nil || dbUser.SubscriptionPrice != nil || remUser == nil {
		return false
	}
	if remUser.Status != remnawave.StatusActive {
		return false
	}
	if remUser.ExpireAt.Year() >= 2099 || !remUser.ExpireAt.After(time.Now().UTC()) {
		return false
	}
	hasPaid, err := b.db.HasConfirmedPayment(dbUser.TelegramID)
	if err != nil {
		slog.Error("Failed to check confirmed payments before migration prompt", "error", err, "telegram_id", dbUser.TelegramID)
		return false
	}
	return !hasPaid
}

func (b *Bot) describeAdminUserSubscription(telegramID int64, remUser *remnawave.User) (string, string) {
	if remUser == nil {
		return "неизвестно", "неизвестно"
	}

	switch {
	case remUser.ExpireAt.Year() >= 2099:
		return "♾️ Безлимитная", "Активен"
	case remUser.Status == remnawave.StatusDisabled && !remUser.ExpireAt.After(time.Now().UTC()):
		return "💳 Подписка", "Grace period"
	case b.isTrialUser(telegramID):
		return "⏳ Триал", "Активен"
	default:
		return "💳 Подписка", humanizeAdminStatus(remUser.Status)
	}
}

func describeAdminRemaining(expireAt, now time.Time) string {
	if !expireAt.After(now) {
		return "истекла"
	}

	days := daysUntil(expireAt, now)
	return fmt.Sprintf("осталось %d дн.", days)
}

func humanizeAdminStatus(status string) string {
	switch status {
	case remnawave.StatusActive:
		return "Активен"
	case remnawave.StatusDisabled:
		return "Отключён"
	case remnawave.StatusExpired:
		return "Истёк"
	case remnawave.StatusLimited:
		return "Лимит трафика"
	default:
		return status
	}
}

func adminStatusEmoji(status string) string {
	switch status {
	case "Активен":
		return "✅"
	case "Grace period", "Отключён":
		return "⛔"
	case "Истёк":
		return "⏰"
	case "Лимит трафика":
		return "⚠️"
	default:
		return "❌"
	}
}

func formatAdminPriceValue(price *int) string {
	if price == nil {
		return "не установлена"
	}
	return fmt.Sprintf("%d руб/мес", *price)
}

func formatAdminOldPrice(session adminChangePriceSession) string {
	if !session.HasCurrentPrice {
		return "не установлена"
	}
	return fmt.Sprintf("%d", session.CurrentPrice)
}

func formatAdminModeratorLabel(firstName, username string, telegramID int64) string {
	switch {
	case firstName != "" && username != "":
		return fmt.Sprintf("%s (@%s)", html.EscapeString(firstName), username)
	case username != "":
		return "@" + username
	case firstName != "":
		return html.EscapeString(firstName)
	default:
		return fmt.Sprintf("<code>%d</code>", telegramID)
	}
}
