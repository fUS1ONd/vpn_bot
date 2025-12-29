package bot

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/threexui"
	"github.com/google/uuid"
	tele "gopkg.in/telebot.v3"
)

// isAdmin checks if the user is admin
func (b *Bot) isAdmin(c tele.Context) bool {
	return c.Sender().ID == b.config.AdminID
}

// handleAdminStart handles /start for admin
func (b *Bot) handleAdminStart(c tele.Context) error {
	return c.Send(MsgAdminWelcome, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: AdminKeyboard(),
	})
}

// handleAdminCreateCallback handles admin create button callback
func (b *Bot) handleAdminCreateCallback(c tele.Context) error {
	if err := c.Respond(); err != nil {
		slog.Error("Failed to respond to callback", "error", err)
	}

	if !b.isAdmin(c) {
		return nil
	}

	b.userStates[c.Sender().ID] = StateWaitClient

	return c.Edit(MsgAdminEnterClientName, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: BackKeyboard(),
	})
}

// handleAdminCreate handles creating a new client (admin only)
func (b *Bot) handleAdminCreate(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	args := strings.Fields(c.Text())
	if len(args) < 2 {
		return c.Send(MsgAdminEnterClientName, &tele.SendOptions{
			ParseMode: tele.ModeHTML,
		})
	}

	email := args[1]
	clientUUID := uuid.New().String()

	status, err := c.Bot().Send(c.Sender(), fmt.Sprintf("Создаю <b>%s</b>...", email), &tele.SendOptions{
		ParseMode: tele.ModeHTML,
	})
	if err != nil {
		return err
	}

	// Login to both servers
	if err := b.clientA.Login(); err != nil {
		slog.Error("Failed to login to Server A", "error", err)
		_, editErr := c.Bot().Edit(status, fmt.Sprintf("Ошибка подключения к Server A: %v", err))
		return editErr
	}

	if err := b.clientB.Login(); err != nil {
		slog.Error("Failed to login to Server B", "error", err)
		_, editErr := c.Bot().Edit(status, fmt.Sprintf("Ошибка подключения к Server B: %v", err))
		return editErr
	}

	// Calculate expiry time (1 month from now)
	expiryTime := time.Now().AddDate(0, 1, 0).UnixMilli()

	// Create settings for both servers
	settingsRU := threexui.ClientSettings{
		UUID:       clientUUID,
		Email:      email,
		LimitIP:    2,
		TotalGB:    b.config.ServerA.LimitBytes,
		ExpiryTime: expiryTime,
		Enable:     true,
	}

	settingsEU := threexui.ClientSettings{
		UUID:       clientUUID,
		Email:      email,
		LimitIP:    2,
		TotalGB:    0, // Unlimited
		ExpiryTime: expiryTime,
		Enable:     true,
	}

	// Add client to Server A (RU)
	errA := b.clientA.AddClientWithSettings(b.config.ServerA.InboundID, settingsRU)
	if errA != nil {
		slog.Error("Failed to add client to Server A", "error", errA)
	}

	// Add client to Server B (EU)
	errB := b.clientB.AddClientWithSettings(b.config.ServerB.InboundID, settingsEU)
	if errB != nil {
		slog.Error("Failed to add client to Server B", "error", errB)
	}

	if errA != nil || errB != nil {
		errorMsg := fmt.Sprintf("Ошибка:\nRU: %v\nEU: %v", errA, errB)
		_, editErr := c.Bot().Edit(status, errorMsg)
		return editErr
	}

	// Save to database
	endTime := time.Now().AddDate(0, 1, 0)
	user, dbErr := b.db.CreateUser(0, email, clientUUID)
	if dbErr != nil {
		slog.Error("Failed to add user to database", "error", dbErr)
		_, editErr := c.Bot().Edit(status, fmt.Sprintf("Ошибка сохранения в БД: %v", dbErr))
		return editErr
	}

	// Update subscription status
	if err := b.db.UpdateUserSubscription(user.ID, database.StatusActive, &endTime); err != nil {
		slog.Error("Failed to update subscription", "error", err)
	}

	// Generate subscription link
	subLink := b.generateSubLink(clientUUID)

	successMsg := fmt.Sprintf(MsgAdminClientCreated, email, clientUUID, subLink)

	_, editErr := c.Bot().Edit(status, successMsg, &tele.SendOptions{
		ParseMode: tele.ModeHTML,
	})
	return editErr
}

// handleAdminList handles listing all clients (admin only)
func (b *Bot) handleAdminList(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	// Sync clients from panel
	if err := b.syncClients(); err != nil {
		slog.Error("Failed to sync clients", "error", err)
	}

	// Get all users from database
	users, err := b.db.GetAllUsers()
	if err != nil {
		slog.Error("Failed to get users from database", "error", err)
		return c.Send("Ошибка получения списка клиентов")
	}

	if len(users) == 0 {
		return c.Send("Список клиентов пуст")
	}

	// Build message
	var sb strings.Builder
	for i, user := range users {
		subLink := b.generateSubLink(user.UUID)
		statusIcon := b.getStatusIcon(user.SubscriptionStatus)

		sb.WriteString(fmt.Sprintf("<b>%d. %s</b> %s\n", i+1, user.Email, statusIcon))
		sb.WriteString(fmt.Sprintf("   UUID: <code>%s</code>\n", user.UUID))
		sb.WriteString(fmt.Sprintf("   Ссылка: <code>%s</code>\n\n", subLink))
	}

	msg := fmt.Sprintf(MsgAdminClientList, len(users), sb.String())

	return c.Send(msg, &tele.SendOptions{
		ParseMode: tele.ModeHTML,
	})
}

// handlePromoAdd handles adding a promo code (admin only)
func (b *Bot) handlePromoAdd(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	// /promo_add <code> <type> <value> <max_uses> [valid_days]
	args := strings.Fields(c.Text())
	if len(args) < 5 {
		return c.Send("Использование: /promo_add &lt;code&gt; &lt;type&gt; &lt;value&gt; &lt;max_uses&gt; [valid_days]\n\nТипы: discount, free_days, extra_traffic", &tele.SendOptions{
			ParseMode: tele.ModeHTML,
		})
	}

	code := args[1]
	promoType := args[2]
	value, err := strconv.Atoi(args[3])
	if err != nil {
		return c.Send("Неверное значение value")
	}
	maxUses, err := strconv.Atoi(args[4])
	if err != nil {
		return c.Send("Неверное значение max_uses")
	}

	// Validate promo type
	if promoType != database.PromoTypeDiscount &&
		promoType != database.PromoTypeFreeDays &&
		promoType != database.PromoTypeExtraTraffic {
		return c.Send("Неверный тип промокода. Допустимые: discount, free_days, extra_traffic")
	}

	var validUntil *time.Time
	if len(args) > 5 {
		days, err := strconv.Atoi(args[5])
		if err == nil && days > 0 {
			t := time.Now().AddDate(0, 0, days)
			validUntil = &t
		}
	}

	promo, err := b.db.CreatePromoCode(code, promoType, value, maxUses, validUntil)
	if err != nil {
		slog.Error("Failed to create promo code", "error", err)
		return c.Send(fmt.Sprintf("Ошибка создания промокода: %v", err))
	}

	msg := fmt.Sprintf("<b>Промокод создан!</b>\n\nКод: <code>%s</code>\nТип: %s\nЗначение: %d\nМакс. использований: %d",
		promo.Code, promo.Type, promo.Value, promo.MaxUses)

	if validUntil != nil {
		msg += fmt.Sprintf("\nДействует до: %s", validUntil.Format("02.01.2006"))
	}

	return c.Send(msg, &tele.SendOptions{
		ParseMode: tele.ModeHTML,
	})
}

// handlePromoDel handles deleting a promo code (admin only)
func (b *Bot) handlePromoDel(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	args := strings.Fields(c.Text())
	if len(args) < 2 {
		return c.Send("Использование: /promo_del &lt;code&gt;", &tele.SendOptions{
			ParseMode: tele.ModeHTML,
		})
	}

	code := args[1]
	promo, err := b.db.GetPromoCodeByCode(code)
	if err != nil || promo == nil {
		return c.Send("Промокод не найден")
	}

	if err := b.db.DeletePromoCode(promo.ID); err != nil {
		slog.Error("Failed to delete promo code", "error", err)
		return c.Send(fmt.Sprintf("Ошибка удаления: %v", err))
	}

	return c.Send(fmt.Sprintf("Промокод <code>%s</code> удален", code), &tele.SendOptions{
		ParseMode: tele.ModeHTML,
	})
}

// handlePromoList handles listing promo codes (admin only)
func (b *Bot) handlePromoList(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	promos, err := b.db.GetAllPromoCodes()
	if err != nil {
		slog.Error("Failed to get promo codes", "error", err)
		return c.Send("Ошибка получения промокодов")
	}

	if len(promos) == 0 {
		return c.Send("Нет активных промокодов")
	}

	var sb strings.Builder
	for _, p := range promos {
		statusIcon := "OK"
		if p.UsedCount >= p.MaxUses {
			statusIcon = "X"
		}
		if p.ValidUntil != nil && p.ValidUntil.Before(time.Now()) {
			statusIcon = "X"
		}

		sb.WriteString(fmt.Sprintf("%s <code>%s</code> (%s: %d) — %d/%d\n",
			statusIcon, p.Code, p.Type, p.Value, p.UsedCount, p.MaxUses))
	}

	msg := fmt.Sprintf(MsgAdminPromoList, sb.String())
	return c.Send(msg, &tele.SendOptions{
		ParseMode: tele.ModeHTML,
	})
}

// handleAdminDelete handles deleting a client (admin only)
func (b *Bot) handleAdminDelete(c tele.Context) error {
	if !b.isAdmin(c) {
		return nil
	}

	args := strings.Fields(c.Text())
	if len(args) < 2 {
		return c.Send("Использование: /delete &lt;email&gt;", &tele.SendOptions{
			ParseMode: tele.ModeHTML,
		})
	}

	email := args[1]

	// Find user in database
	user, err := b.db.GetUserByEmail(email)
	if err != nil {
		slog.Error("Failed to get user", "error", err)
		return c.Send("Ошибка поиска пользователя")
	}
	if user == nil {
		return c.Send(fmt.Sprintf("Пользователь <code>%s</code> не найден", email), &tele.SendOptions{
			ParseMode: tele.ModeHTML,
		})
	}

	// Login to servers
	if err := b.clientA.Login(); err != nil {
		slog.Error("Failed to login to Server A", "error", err)
		return c.Send(fmt.Sprintf("Ошибка подключения к Server A: %v", err))
	}
	if err := b.clientB.Login(); err != nil {
		slog.Error("Failed to login to Server B", "error", err)
		return c.Send(fmt.Sprintf("Ошибка подключения к Server B: %v", err))
	}

	// Delete from Server A
	errA := b.clientA.DeleteClient(b.config.ServerA.InboundID, user.UUID)
	if errA != nil {
		slog.Error("Failed to delete client from Server A", "error", errA)
	}

	// Delete from Server B
	errB := b.clientB.DeleteClient(b.config.ServerB.InboundID, user.UUID)
	if errB != nil {
		slog.Error("Failed to delete client from Server B", "error", errB)
	}

	// Delete from database
	if err := b.db.DeleteUser(user.ID); err != nil {
		slog.Error("Failed to delete user from database", "error", err)
		return c.Send(fmt.Sprintf("Ошибка удаления из БД: %v", err))
	}

	msg := fmt.Sprintf("<b>Клиент удалён</b>\n\nEmail: <code>%s</code>", email)
	if errA != nil || errB != nil {
		msg += fmt.Sprintf("\n\n<i>Предупреждения:\nRU: %v\nEU: %v</i>", errA, errB)
	}

	return c.Send(msg, &tele.SendOptions{
		ParseMode: tele.ModeHTML,
	})
}

// getStatusIcon returns status icon for user status
func (b *Bot) getStatusIcon(status string) string {
	switch status {
	case database.StatusActive:
		return "[OK]"
	case database.StatusTrial:
		return "[TRIAL]"
	case database.StatusExpired:
		return "[X]"
	default:
		return "[-]"
	}
}

// generateSubLink generates subscription link for a user
func (b *Bot) generateSubLink(clientUUID string) string {
	myIP := extractIP(b.config.ServerA.BaseURL)
	return fmt.Sprintf("http://%s:%d/sub/%s", myIP, b.config.SubPort, clientUUID)
}
