package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	tele "gopkg.in/telebot.v3"
)

const (
	notificationExpire3d    = "expire_3d"
	notificationExpireToday = "expire_today"
)

type subscriptionDecision struct {
	ThreeDaysMessage   string
	ExpireTodayMessage string
	ShouldKick         bool
}

// StartScheduler запускает ежедневную проверку подписок в 12:00 по Москве.
func (b *Bot) StartScheduler(ctx context.Context) {
	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		slog.Error("Failed to load Europe/Moscow location", "error", err)
		return
	}

	now := time.Now().In(msk)
	nextRun := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, msk)
	if !now.Before(nextRun) {
		nextRun = nextRun.AddDate(0, 0, 1)
	}

	firstTimer := time.NewTimer(time.Until(nextRun))
	defer firstTimer.Stop()

	slog.Info("Subscription scheduler initialized", "first_run", nextRun.Format(time.RFC3339))

	select {
	case <-ctx.Done():
		slog.Info("Subscription scheduler stopped before first run")
		return
	case <-firstTimer.C:
		b.runSubscriptionSchedulerPass()
	}

	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

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
	remUsers, err := b.remnawave.GetAllUsers()
	if err != nil {
		slog.Error("Scheduler failed to get users from Remnawave", "error", err)
		return
	}

	dbUsers, err := b.db.GetAllUsers()
	if err != nil {
		slog.Error("Scheduler failed to get users from DB", "error", err)
		return
	}

	dbByTelegramID := make(map[int64]database.User, len(dbUsers))
	for _, user := range dbUsers {
		dbByTelegramID[user.TelegramID] = user
	}

	now := time.Now().UTC()

	for _, user := range remUsers {
		if user.TelegramID == nil || *user.TelegramID == 0 {
			continue
		}

		telegramID := *user.TelegramID
		dbUser, existsInDB := dbByTelegramID[telegramID]
		if !existsInDB {
			continue
		}

		if user.ExpireAt.Year() >= 2099 {
			continue
		}

		invite, err := b.db.GetInviteByUsedBy(telegramID)
		if err != nil {
			slog.Error("Scheduler failed to get invite by used_by", "error", err, "telegram_id", telegramID)
			continue
		}

		// Админские (бессрочные) и старые записи без инвайта не участвуют в монетизационной логике.
		if invite == nil || invite.ExpireDays == nil {
			continue
		}

		curatorActive := b.isModerator(invite.CreatedBy)

		sent3d, err := b.db.WasNotificationSent(telegramID, notificationExpire3d)
		if err != nil {
			slog.Error("Scheduler failed to check expire_3d marker", "error", err, "telegram_id", telegramID)
			continue
		}
		sentToday, err := b.db.WasNotificationSent(telegramID, notificationExpireToday)
		if err != nil {
			slog.Error("Scheduler failed to check expire_today marker", "error", err, "telegram_id", telegramID)
			continue
		}

		decision := decideSubscriptionActions(user.ExpireAt, now, curatorActive, sent3d, sentToday)

		if decision.ThreeDaysMessage != "" {
			if err := b.sendSchedulerMessage(telegramID, decision.ThreeDaysMessage); err == nil {
				if err := b.db.MarkNotificationSent(telegramID, notificationExpire3d); err != nil {
					slog.Error("Scheduler failed to persist expire_3d marker", "error", err, "telegram_id", telegramID)
				}
			} else {
				logSchedulerSendError("expire_3d", telegramID, err)
			}
		}

		if decision.ExpireTodayMessage != "" {
			if err := b.sendSchedulerMessage(telegramID, decision.ExpireTodayMessage); err == nil {
				if err := b.db.MarkNotificationSent(telegramID, notificationExpireToday); err != nil {
					slog.Error("Scheduler failed to persist expire_today marker", "error", err, "telegram_id", telegramID)
				}
			} else {
				logSchedulerSendError("expire_today", telegramID, err)
			}
		}

		if decision.ShouldKick {
			b.handleAutoKick(telegramID, dbUser.RemnawaveUUID)
		}
	}
}

func (b *Bot) handleAutoKick(telegramID int64, userUUID string) {
	if err := b.remnawave.DeleteUser(userUUID); err != nil {
		slog.Warn("Scheduler failed to delete user from Remnawave during auto-kick", "error", err, "telegram_id", telegramID)
	}

	if err := b.db.DeleteUser(telegramID); err != nil {
		slog.Warn("Scheduler failed to delete user from DB during auto-kick", "error", err, "telegram_id", telegramID)
	}

	if err := b.db.ResetInviteUsageByTelegramID(telegramID); err != nil {
		slog.Warn("Scheduler failed to reset invite usage during auto-kick", "error", err, "telegram_id", telegramID)
	}

	if err := b.db.ClearNotifications(telegramID); err != nil {
		slog.Warn("Scheduler failed to clear notifications during auto-kick", "error", err, "telegram_id", telegramID)
	}

	_ = b.sendSchedulerMessage(telegramID, "❌ Ваш доступ удалён. Вы можете получить новое приглашение для повторного подключения.")
}

func (b *Bot) sendSchedulerMessage(telegramID int64, message string) error {
	if b.bot == nil {
		return fmt.Errorf("telegram bot is not initialized")
	}
	_, err := b.bot.Send(&tele.User{ID: telegramID}, message)
	return err
}

func logSchedulerSendError(msgType string, telegramID int64, err error) {
	if strings.Contains(strings.ToLower(err.Error()), "403") {
		slog.Warn("Scheduler message skipped: bot blocked by user", "type", msgType, "telegram_id", telegramID, "error", err)
		return
	}
	slog.Warn("Scheduler failed to send message", "type", msgType, "telegram_id", telegramID, "error", err)
}

func decideSubscriptionActions(expireAt, now time.Time, curatorActive bool, sent3d, sentToday bool) subscriptionDecision {
	expireDay := dayUTC(expireAt)
	nowDay := dayUTC(now)

	decision := subscriptionDecision{}

	if expireDay.Equal(nowDay.AddDate(0, 0, 3)) && !sent3d {
		if curatorActive {
			decision.ThreeDaysMessage = "⏳ Ваша подписка заканчивается через 3 дня.\nОбратитесь к вашему куратору для продления."
		} else {
			decision.ThreeDaysMessage = "⏳ Ваша подписка заканчивается через 3 дня.\nВаш куратор больше не обслуживает подписки.\nПодписка не будет продлена."
		}
	}

	if !expireDay.After(nowDay) && !sentToday {
		if curatorActive {
			decision.ExpireTodayMessage = "⚠️ Ваша подписка истекла.\nУ вас есть 3 дня, чтобы продлить через куратора,\nиначе доступ будет удалён."
		} else {
			decision.ExpireTodayMessage = "⚠️ Ваша подписка истекла.\nВаш куратор больше не обслуживает подписки.\nДоступ будет удалён через 3 дня."
		}
	}

	if expireDay.AddDate(0, 0, 3).Before(nowDay) {
		decision.ShouldKick = true
	}

	return decision
}

func dayUTC(t time.Time) time.Time {
	utc := t.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}
