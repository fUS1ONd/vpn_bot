package bot

import (
	"time"

	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

// isPayingUser — предикат «Платящий» из глоссария: пользователь с действующим
// доступом, который платит или платил.
//
// Условия: не забанен, есть в базе бота, доступ действует (бессрочный либо
// ACTIVE/LIMITED с неистёкшим сроком) и при этом есть подтверждённый платёж,
// либо стоит legacy_paid_migrated, либо доступ бессрочный. Триал и
// grace-период под определение не подпадают.
//
// Предикат один на два места: право создавать referral-приглашения и вход в
// Канал. Расходиться они не должны — иначе пользователь, которому бот
// разрешает звать друзей, получал бы отказ на заявке в сообщество.
func (b *Bot) isPayingUser(telegramID int64) (bool, error) {
	accessible, err := b.canAccessReferralSection(telegramID)
	if err != nil || !accessible {
		return false, err
	}
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil || user == nil {
		return false, err
	}
	remUser, err := b.remnawaveUser(telegramID)
	if err != nil || remUser == nil {
		return false, err
	}
	now := time.Now().UTC()
	accessActive := remUser.ExpireAt.Year() >= 2099 ||
		((remUser.Status == remnawave.StatusActive || remUser.Status == remnawave.StatusLimited) && remUser.ExpireAt.After(now))
	if !accessActive {
		return false, nil
	}
	if remUser.ExpireAt.Year() >= 2099 || user.LegacyPaidMigrated {
		return true, nil
	}
	return b.db.HasConfirmedPayment(telegramID)
}
