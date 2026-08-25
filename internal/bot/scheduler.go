package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
	tele "gopkg.in/telebot.v3"
)

const (
	// Триал
	notificationTrialExpire1d = "trial_expire_1d" // За 1 день до конца триала
	notificationTrialExpired  = "trial_expired"   // Триал истёк

	// Оплаченная подписка
	notificationExpire3d  = "expire_3d"  // За 3 дня до конца
	notificationExpire1d  = "expire_1d"  // За 1 день до конца
	notificationExpired   = "expired"    // Подписка истекла (начало grace)
	notificationGraceKick = "grace_kick" // Кик после grace period
)

// StartScheduler запускает проверку подписок каждые 30 минут + первый проход при старте.
func (b *Bot) StartScheduler(ctx context.Context) {
	// Первый проход при старте — не ждём 30 минут
	slog.Info("Scheduler: running initial pass on startup")
	b.runSubscriptionSchedulerPass()

	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	slog.Info("Subscription scheduler started", "interval", "30m")

	for {
		select {
		case <-ctx.Done():
			slog.Info("Subscription scheduler stopped")
			return
		case <-ticker.C:
			b.runSubscriptionSchedulerPass()
		}
	}
}

func (b *Bot) runSubscriptionSchedulerPass() {
	now := time.Now().UTC()

	// 0. Периодическая сверка версии панели. Владелец обновляет панель, не
	// перезапуская бота: так апгрейд замечается даже без пользовательской
	// активности, а не только по 400 на первом же вызове.
	if version, err := b.remnawave.RefreshAPIVersion(); err != nil {
		slog.Warn("Scheduler: не удалось перечитать версию панели", "error", err)
		b.reportPanelAuthError(err, "сверка версии панели")
	} else {
		slog.Debug("Scheduler: версия панели подтверждена", "contract", version.String())
	}

	// 1. Протухание старых PENDING платежей
	expired, err := b.db.ExpireOldPendingPayments()
	if err != nil {
		slog.Error("Scheduler: ошибка при протухании pending платежей", "error", err)
	} else if expired > 0 {
		slog.Info("Scheduler: протухли pending платежи", "count", expired)
	}

	// 2. Retry confirmed_not_activated платежей
	b.retryConfirmedNotActivated()

	// 3. Списания по Автопродлению — отдельным шагом и до основного цикла:
	// попытка T−0 обязана отработать раньше ветки disable, а успевшие
	// продлиться попадут в цикл уже с новым expireAt.
	b.runAutorenewCharges(now)

	// 4. Добиваем чеки «Моего налога», не пробитые на платёжном пути — последним
	// шагом и через defer. Поход в ФНС долгий, а уведомления, отключения и автокики
	// ждать его не должны: налоговая подождёт полчаса, а просроченный доступ — нет.
	// defer, а не просто вызов в конце, потому что шаги ниже умеют выходить раньше
	// по ошибке Remnawave, а чеки от неё не зависят.
	defer b.issuePendingReceipts()

	// 5. Получаем пользователей
	remUsers, err := b.remnawave.GetAllUsers()
	if err != nil {
		slog.Error("Scheduler failed to get users from Remnawave", "error", err)
		b.reportPanelAuthError(err, "список пользователей панели")
		return
	}
	b.reportPanelAuthError(nil, "список пользователей панели")

	dbUsers, err := b.db.GetAllUsers()
	if err != nil {
		slog.Error("Scheduler failed to get users from DB", "error", err)
		return
	}

	dbByTelegramID := make(map[int64]database.User, len(dbUsers))
	for _, user := range dbUsers {
		dbByTelegramID[user.TelegramID] = user
	}

	for _, user := range remUsers {
		if user.TelegramID == nil || *user.TelegramID == 0 {
			continue
		}

		telegramID := *user.TelegramID
		dbUser, existsInDB := dbByTelegramID[telegramID]
		if !existsInDB {
			continue
		}

		// Проход по панели заодно доливает недостающий remnawave_id: список
		// пользователей панели тут уже загружен, лишних запросов это не стоит.
		ref := b.schedulerUserRef(dbUser, user)

		invite, err := b.db.GetInviteByUsedBy(telegramID)
		if err != nil {
			slog.Warn("Scheduler: не удалось получить инвайт пользователя", "error", err, "telegram_id", telegramID)
			continue
		}

		// Legacy-пользователи без инвайта и без цены остаются на старой модели:
		// scheduler оплаты их не трогает, чтобы не ломать обратную совместимость.
		if invite == nil && dbUser.SubscriptionPrice == nil {
			slog.Info("Scheduler: пропускаем legacy-пользователя без инвайта и цены", "telegram_id", telegramID)
			continue
		}

		// Бесконечная подписка — пропуск
		if user.ExpireAt.Year() >= 2099 {
			continue
		}

		// Определяем тип подписки
		isTrial := b.isTrialUser(telegramID)

		if isTrial {
			b.processTrialUser(telegramID, ref, user.ExpireAt, now)
		} else {
			b.processPaidUser(telegramID, ref, user.ExpireAt, now)
		}
	}
}

// processTrialUser обрабатывает триального пользователя.
// Триал: уведомление за 1 день → кик при expireAt (без grace period).
func (b *Bot) processTrialUser(telegramID int64, ref remnawave.UserRef, expireAt, now time.Time) {
	// За 1 день до конца триала
	if notificationWindow(now, expireAt, 0, 24*time.Hour) {
		b.sendNotification(telegramID, notificationTrialExpire1d,
			"⏳ Ваш пробный период заканчивается менее чем через 24 часа.\n\nОплатите подписку, чтобы сохранить доступ к VPN.")
	}

	// Триал истёк — кик
	if !now.Before(expireAt) {
		if b.isMaintenanceMode() {
			slog.Info("Scheduler: maintenance mode, пропускаем кик триального пользователя", "telegram_id", telegramID)
			return
		}

		// Защита: проверяем, не оплатил ли пользователь.
		// confirmed_not_activated тоже защищает от кика: деньги уже подтверждены,
		// даже если активация в панели ещё не завершилась.
		hasPaid, err := b.db.HasConfirmedPaymentSince(telegramID, expireAt)
		if err != nil {
			slog.Error("Scheduler: ошибка проверки оплаты при кике триала", "error", err, "telegram_id", telegramID)
			return
		}
		if hasPaid {
			slog.Info("Scheduler: пользователь оплатил во время триала, пропускаем кик", "telegram_id", telegramID)
			return
		}

		b.sendNotification(telegramID, notificationTrialExpired,
			"❌ Ваш пробный период закончился.\n\nДля продолжения использования VPN оплатите подписку по новому приглашению.")
		b.handleAutoKick(telegramID, ref)
	}
}

// processPaidUser обрабатывает пользователя с оплаченной подпиской.
// Уведомления за 3д/1д → disable при expireAt → кик через 72ч grace.
func (b *Bot) processPaidUser(telegramID int64, ref remnawave.UserRef, expireAt, now time.Time) {
	// С включённым автопродлением про скорое окончание не пишем: для него это
	// ложная тревога, а окно за сутки занято попыткой списания.
	silent := b.autorenewSuppressesExpiryNotice(telegramID, expireAt)

	// За 3 дня до конца
	if !silent && notificationWindow(now, expireAt, 48*time.Hour, 72*time.Hour) {
		b.sendNotification(telegramID, notificationExpire3d,
			"⏳ Ваша подписка заканчивается через 3 дня.\n\nНажмите \"💳 Продлить подписку\" чтобы продлить доступ.")
	}

	// За 1 день до конца
	if !silent && notificationWindow(now, expireAt, 0, 24*time.Hour) {
		b.sendNotification(telegramID, notificationExpire1d,
			"⚠️ Ваша подписка заканчивается менее чем через 24 часа.\n\nПродлите сейчас, чтобы не потерять доступ к VPN.")
	}

	// Подписка истекла — disable + начало grace period
	if !now.Before(expireAt) {
		// Защита: проверяем, не оплатил ли пользователь после expireAt.
		// confirmed_not_activated тоже считается оплатой для scheduler:
		// пользователя нельзя disable-ить как должника, пока retry активации продолжается.
		hasPaid, err := b.db.HasConfirmedPaymentSince(telegramID, expireAt)
		if err != nil {
			slog.Error("Scheduler: ошибка проверки оплаты", "error", err, "telegram_id", telegramID)
			return
		}
		if hasPaid {
			return // Оплатил — callback уже обработал
		}

		if !b.isMaintenanceMode() {
			// Disable в Remnawave (если ещё не disabled)
			if err := b.remnawave.DisableUser(ref); err != nil {
				slog.Warn("Scheduler: не удалось disable пользователя", "error", err, "telegram_id", telegramID)
			}
		}

		b.sendNotification(telegramID, notificationExpired,
			"⚠️ Ваша подписка истекла. VPN деактивирован.\n\nУ вас есть 3 дня, чтобы оплатить и восстановить доступ.\nПосле этого аккаунт будет удалён.")
	}

	// Grace period кик: expireAt + 72 часа
	graceDeadline := expireAt.Add(72 * time.Hour)
	if !now.Before(graceDeadline) {
		if b.isMaintenanceMode() {
			slog.Info("Scheduler: maintenance mode, пропускаем grace kick", "telegram_id", telegramID)
			return
		}

		// Защита: проверяем оплату за весь grace period.
		// confirmed_not_activated здесь тоже блокирует кик: деньги уже получены.
		hasPaid, err := b.db.HasConfirmedPaymentSince(telegramID, expireAt)
		if err != nil {
			slog.Error("Scheduler: ошибка проверки оплаты перед grace kick", "error", err, "telegram_id", telegramID)
			return
		}
		if hasPaid {
			return
		}

		// Перед киком проверяем свежий статус через API — вдруг callback прошёл
		freshUser, err := b.remnawave.GetUser(ref)
		if err != nil {
			slog.Warn("Scheduler: не удалось проверить свежий статус перед grace kick",
				"error", err, "telegram_id", telegramID)
			return
		}
		if freshUser.Status == "ACTIVE" && freshUser.ExpireAt.After(now) {
			slog.Info("Scheduler: пользователь активен при проверке перед grace kick, пропускаем",
				"telegram_id", telegramID)
			return
		}

		b.sendNotification(telegramID, notificationGraceKick,
			"❌ Ваш доступ удалён. Вы можете получить новое приглашение для повторного подключения.")
		b.handleAutoKick(telegramID, ref)
	}
}

// isTrialUser проверяет, находится ли пользователь на триале.
// Триальный = активирован trial-инвайтом и ни разу не платил.
// confirmed_not_activated уже не считается "не платил": деньги подтверждены, просто
// активация доступа в панели временно отложена на retry.
func (b *Bot) isTrialUser(telegramID int64) bool {
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil {
		return false
	}
	if user.LegacyPaidMigrated {
		return false
	}

	invite, err := b.db.GetInviteByUsedBy(telegramID)
	if err != nil || invite == nil || !invite.IsTrial {
		return false // Админский инвайт или нет инвайта — не триал
	}

	hasPaid, err := b.db.HasConfirmedPayment(telegramID)
	if err != nil {
		return false
	}
	return !hasPaid
}

// retryConfirmedNotActivated повторяет активацию для платежей со статусом confirmed_not_activated
func (b *Bot) retryConfirmedNotActivated() {
	payments, err := b.db.GetConfirmedNotActivated()
	if err != nil {
		slog.Error("Scheduler: ошибка получения confirmed_not_activated", "error", err)
		return
	}

	if len(payments) == 0 {
		return
	}

	slog.Info("Scheduler: retry confirmed_not_activated", "count", len(payments))

	for _, p := range payments {
		if !b.retryConfirmedPaymentActivation(p.ID, "scheduler") {
			continue
		}
	}
}

// sendNotification отправляет уведомление, если оно ещё не было отправлено
func (b *Bot) sendNotification(telegramID int64, notificationType, message string) {
	sent, err := b.db.WasNotificationSent(telegramID, notificationType)
	if err != nil {
		slog.Error("Scheduler: ошибка проверки уведомления", "error", err, "type", notificationType, "telegram_id", telegramID)
		return
	}
	if sent {
		return
	}

	if err := b.sendSchedulerMessage(telegramID, message); err != nil {
		logSchedulerSendError(notificationType, telegramID, err)
		return
	}

	if err := b.db.MarkNotificationSent(telegramID, notificationType); err != nil {
		slog.Error("Scheduler: ошибка сохранения маркера уведомления", "error", err, "type", notificationType, "telegram_id", telegramID)
	}
}

// isAutoKickNotFoundError проверяет, является ли ошибка признаком того,
// что пользователь уже удалён из Remnawave (например, администратором вручную).
// Признак — HTTP-статус 404 от панели, а не подстрока в тексте ошибки.
func isAutoKickNotFoundError(err error) bool {
	return isRemnawaveNotFound(err)
}

func (b *Bot) handleAutoKick(telegramID int64, ref remnawave.UserRef) {
	if err := b.remnawave.DeleteUser(ref); err != nil {
		if isAutoKickNotFoundError(err) {
			// Пользователь уже удалён из Remnawave (например, забанен вручную) — не шумим в логах.
			slog.Debug("Scheduler auto-kick: user already absent in Remnawave", "telegram_id", telegramID)
		} else {
			slog.Warn("Scheduler failed to delete user from Remnawave during auto-kick", "error", err, "telegram_id", telegramID)
			b.sendAdminAlert(fmt.Sprintf(
				"⚠️ Auto-kick: не удалось удалить пользователя %d из Remnawave: %v. Продолжаем удаление из БД.",
				telegramID, err,
			))
			// НЕ делаем return — продолжаем удаление из БД, чтобы избежать partial failure
		}
	}

	if err := b.db.DeleteUser(telegramID); err != nil {
		slog.Warn("Scheduler failed to delete user from DB during auto-kick", "error", err, "telegram_id", telegramID)
	}

	if err := b.db.MarkInviteKickedByTelegramID(telegramID); err != nil {
		slog.Warn("Scheduler failed to mark invite as kicked during auto-kick", "error", err, "telegram_id", telegramID)
	}

	if err := b.db.ClearNotifications(telegramID); err != nil {
		slog.Warn("Scheduler failed to clear notifications during auto-kick", "error", err, "telegram_id", telegramID)
	}
}

func (b *Bot) sendSchedulerMessage(telegramID int64, message string) error {
	if b.bot == nil {
		return fmt.Errorf("telegram bot is not initialized")
	}
	_, err := b.bot.Send(&tele.User{ID: telegramID}, message, &tele.SendOptions{ParseMode: tele.ModeHTML})
	return err
}

// sendSchedulerMessageWithKeyboard отправляет сообщение с клавиатурой (для замены текущих кнопок)
func (b *Bot) sendSchedulerMessageWithKeyboard(telegramID int64, message string, markup *tele.ReplyMarkup) error {
	if b.bot == nil {
		return fmt.Errorf("telegram bot is not initialized")
	}
	_, err := b.bot.Send(&tele.User{ID: telegramID}, message, &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: markup,
	})
	return err
}

// isSchedulerForbiddenError проверяет, заблокировал ли пользователь бот или деактивирован.
func isSchedulerForbiddenError(err error) bool {
	return errors.Is(err, tele.ErrBlockedByUser) ||
		errors.Is(err, tele.ErrUserIsDeactivated) ||
		errors.Is(err, tele.ErrNotStartedByUser)
}

func logSchedulerSendError(msgType string, telegramID int64, err error) {
	if isSchedulerForbiddenError(err) {
		slog.Warn("Scheduler message skipped: bot blocked by user", "type", msgType, "telegram_id", telegramID, "error", err)
		return
	}
	slog.Warn("Scheduler failed to send message", "type", msgType, "telegram_id", telegramID, "error", err)
}

func notificationWindow(now, target time.Time, minLeft, maxLeft time.Duration) bool {
	if !target.After(now) {
		return false
	}

	timeLeft := target.Sub(now)
	return timeLeft > minLeft && timeLeft <= maxLeft
}
