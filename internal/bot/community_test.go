package bot

import (
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// enableCommunity включает фичу Канала в тестовом боте.
func enableCommunity(b *Bot) {
	b.config.CommunityChatID = -1001234567890
	b.config.CommunityInviteLink = "https://t.me/+testinvite"
}

// makePayingUser создаёт пользователя, проходящего предикат «Платящий».
func makePayingUser(t *testing.T, b *Bot, db *database.DB, telegramID int64, uuid string) {
	t.Helper()
	_, err := db.CreateUser(telegramID, "paid", "Paid", strPtrTest(uuid), nil, nil, nil)
	require.NoError(t, err)
	paymentID, err := db.CreatePayment(&database.Payment{TelegramID: telegramID, Amount: 400, PaymentMethod: "sbp", Status: "pending"})
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(paymentID))
	setReferralEligibilityRemote(t, b, uuid, "ACTIVE", "2098-01-01T00:00:00Z")
}

func TestResolveJoinRequestByUserClass(t *testing.T) {
	t.Run("paying user is approved and marked after approval", func(t *testing.T) {
		b, db := setupTestBot(t)
		enableCommunity(b)
		makePayingUser(t, b, db, 301, "uuid-join-paid")

		outcome := b.resolveJoinRequest(301)
		assert.True(t, outcome.approve)

		// Пометку ставит обработчик — и только после успешного вызова Telegram.
		member, err := db.IsCommunityMember(301)
		require.NoError(t, err)
		assert.False(t, member, "решение само по себе участником не делает")

		b.markCommunityJoined(301)
		member, err = db.IsCommunityMember(301)
		require.NoError(t, err)
		assert.True(t, member)
	})

	t.Run("infinite access is approved", func(t *testing.T) {
		b, db := setupTestBot(t)
		enableCommunity(b)
		_, err := db.CreateUser(302, "infinite", "Infinite", strPtrTest("uuid-join-infinite"), nil, nil, nil)
		require.NoError(t, err)
		setReferralEligibilityRemote(t, b, "uuid-join-infinite", "ACTIVE", "2099-01-01T00:00:00Z")

		assert.True(t, b.resolveJoinRequest(302).approve)
	})

	t.Run("legacy paid is approved", func(t *testing.T) {
		b, db := setupTestBot(t)
		enableCommunity(b)
		_, err := db.CreateUser(303, "legacy", "Legacy", strPtrTest("uuid-join-legacy"), nil, nil, nil)
		require.NoError(t, err)
		require.NoError(t, db.SetLegacyPaidMigrated(303, true))
		setReferralEligibilityRemote(t, b, "uuid-join-legacy", "ACTIVE", "2098-01-01T00:00:00Z")

		assert.True(t, b.resolveJoinRequest(303).approve)
	})

	t.Run("trial is declined with explanation", func(t *testing.T) {
		b, db := setupTestBot(t)
		enableCommunity(b)
		_, err := db.CreateUser(304, "trial", "Trial", strPtrTest("uuid-join-trial"), nil, nil, nil)
		require.NoError(t, err)
		setReferralEligibilityRemote(t, b, "uuid-join-trial", "ACTIVE", "2098-01-01T00:00:00Z")

		outcome := b.resolveJoinRequest(304)
		assert.False(t, outcome.approve)
		assert.True(t, outcome.decline)
		assert.True(t, outcome.explain)

		member, err := db.IsCommunityMember(304)
		require.NoError(t, err)
		assert.False(t, member)
	})

	t.Run("grace is declined with explanation", func(t *testing.T) {
		b, db := setupTestBot(t)
		enableCommunity(b)
		_, err := db.CreateUser(305, "grace", "Grace", strPtrTest("uuid-join-grace"), nil, nil, nil)
		require.NoError(t, err)
		paymentID, err := db.CreatePayment(&database.Payment{TelegramID: 305, Amount: 400, PaymentMethod: "sbp", Status: "pending"})
		require.NoError(t, err)
		require.NoError(t, db.ConfirmPayment(paymentID))
		setReferralEligibilityRemote(t, b, "uuid-join-grace", "DISABLED", "2020-01-01T00:00:00Z")

		outcome := b.resolveJoinRequest(305)
		assert.False(t, outcome.approve)
		assert.True(t, outcome.explain)
	})

	t.Run("banned user is declined", func(t *testing.T) {
		b, db := setupTestBot(t)
		enableCommunity(b)
		makePayingUser(t, b, db, 306, "uuid-join-banned")
		require.NoError(t, db.BanUser(306, 999999))

		assert.False(t, b.resolveJoinRequest(306).approve)
	})

	t.Run("unknown user is declined", func(t *testing.T) {
		b, _ := setupTestBot(t)
		enableCommunity(b)

		outcome := b.resolveJoinRequest(307)
		assert.False(t, outcome.approve)
		assert.True(t, outcome.explain)
	})
}

// Повторная заявка отклоняется снова, но объяснение уходит один раз за окно
// кулдауна — иначе спамящий пользователь завалил бы собственную личку.
func TestResolveJoinRequestExplainsDeclineOncePerCooldown(t *testing.T) {
	b, db := setupTestBot(t)
	enableCommunity(b)
	_, err := db.CreateUser(308, "trial", "Trial", strPtrTest("uuid-join-repeat"), nil, nil, nil)
	require.NoError(t, err)
	setReferralEligibilityRemote(t, b, "uuid-join-repeat", "ACTIVE", "2098-01-01T00:00:00Z")

	first := b.resolveJoinRequest(308)
	assert.True(t, first.explain)

	second := b.resolveJoinRequest(308)
	assert.False(t, second.approve)
	assert.False(t, second.explain)

	// Кулдаун истёк — объяснение уходит снова.
	b.communityDeclineCooldown.Store(int64(308), time.Now().Add(-communityDeclineCooldownWindow-time.Minute))
	assert.True(t, b.resolveJoinRequest(308).explain)
}

// Отказ не приговор: после оплаты та же ссылка проходит.
func TestResolveJoinRequestApprovesAfterPayment(t *testing.T) {
	b, db := setupTestBot(t)
	enableCommunity(b)
	_, err := db.CreateUser(309, "trial", "Trial", strPtrTest("uuid-join-after-pay"), nil, nil, nil)
	require.NoError(t, err)
	setReferralEligibilityRemote(t, b, "uuid-join-after-pay", "ACTIVE", "2098-01-01T00:00:00Z")
	require.False(t, b.resolveJoinRequest(309).approve)

	paymentID, err := db.CreatePayment(&database.Payment{TelegramID: 309, Amount: 400, PaymentMethod: "sbp", Status: "pending"})
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(paymentID))

	assert.True(t, b.resolveJoinRequest(309).approve)
}

// Повторное одобрение не должно ломаться и не сдвигает момент вступления.
func TestMarkCommunityJoinedIsIdempotent(t *testing.T) {
	b, db := setupTestBot(t)
	enableCommunity(b)
	makePayingUser(t, b, db, 310, "uuid-join-twice")

	require.True(t, b.resolveJoinRequest(310).approve)
	b.markCommunityJoined(310)
	require.True(t, b.resolveJoinRequest(310).approve)
	b.markCommunityJoined(310)

	member, err := db.IsCommunityMember(310)
	require.NoError(t, err)
	assert.True(t, member)
}

func TestCommunityDeclineMessageNamesWayIn(t *testing.T) {
	assert.Contains(t, MsgCommunityDeclined, "оплаченной подпиской")
	assert.Contains(t, MsgCommunityDeclined, "подайте заявку по той же ссылке снова")
}

// Забаненному объяснение отказа не уходит: обещать ему путь внутрь ложно.
func TestResolveJoinRequestStaysSilentForBannedUser(t *testing.T) {
	b, db := setupTestBot(t)
	enableCommunity(b)
	_, err := db.CreateUser(311, "banned", "Banned", strPtrTest("uuid-join-silent"), nil, nil, nil)
	require.NoError(t, err)
	require.NoError(t, db.BanUser(311, 999999))

	outcome := b.resolveJoinRequest(311)
	assert.False(t, outcome.approve)
	assert.False(t, outcome.explain)
}
