package bot

import (
	"testing"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIsPayingUserClasses фиксирует предикат «Платящий» по классам пользователей.
// Referral-правила переиспользуют его, поэтому расхождение здесь сразу ломает
// и раздел приглашений, и гейт Канала.
func TestIsPayingUserClasses(t *testing.T) {
	t.Run("confirmed payment and active access", func(t *testing.T) {
		b, db := setupTestBot(t)
		_, err := db.CreateUser(201, "paid", "Paid", strPtrTest("uuid-paying"), nil, nil, nil)
		require.NoError(t, err)
		paymentID, err := db.CreatePayment(&database.Payment{TelegramID: 201, Amount: 400, PaymentMethod: "sbp", Status: "pending"})
		require.NoError(t, err)
		require.NoError(t, db.ConfirmPayment(paymentID))
		setReferralEligibilityRemote(t, b, "uuid-paying", "ACTIVE", "2098-01-01T00:00:00Z")

		paying, err := b.isPayingUser(201)
		require.NoError(t, err)
		assert.True(t, paying)
	})

	t.Run("legacy paid migrated", func(t *testing.T) {
		b, db := setupTestBot(t)
		_, err := db.CreateUser(202, "legacy", "Legacy", strPtrTest("uuid-paying-legacy"), nil, nil, nil)
		require.NoError(t, err)
		require.NoError(t, db.SetLegacyPaidMigrated(202, true))
		setReferralEligibilityRemote(t, b, "uuid-paying-legacy", "ACTIVE", "2098-01-01T00:00:00Z")

		paying, err := b.isPayingUser(202)
		require.NoError(t, err)
		assert.True(t, paying)
	})

	t.Run("infinite access without payment", func(t *testing.T) {
		b, db := setupTestBot(t)
		_, err := db.CreateUser(203, "infinite", "Infinite", strPtrTest("uuid-paying-infinite"), nil, nil, nil)
		require.NoError(t, err)
		setReferralEligibilityRemote(t, b, "uuid-paying-infinite", "ACTIVE", "2099-01-01T00:00:00Z")

		paying, err := b.isPayingUser(203)
		require.NoError(t, err)
		assert.True(t, paying)
	})

	t.Run("trial without payment", func(t *testing.T) {
		b, db := setupTestBot(t)
		_, err := db.CreateUser(204, "trial", "Trial", strPtrTest("uuid-paying-trial"), nil, nil, nil)
		require.NoError(t, err)
		setReferralEligibilityRemote(t, b, "uuid-paying-trial", "ACTIVE", "2098-01-01T00:00:00Z")

		paying, err := b.isPayingUser(204)
		require.NoError(t, err)
		assert.False(t, paying)
	})

	t.Run("grace period keeps confirmed payment", func(t *testing.T) {
		b, db := setupTestBot(t)
		_, err := db.CreateUser(205, "grace", "Grace", strPtrTest("uuid-paying-grace"), nil, nil, nil)
		require.NoError(t, err)
		paymentID, err := db.CreatePayment(&database.Payment{TelegramID: 205, Amount: 400, PaymentMethod: "sbp", Status: "pending"})
		require.NoError(t, err)
		require.NoError(t, db.ConfirmPayment(paymentID))
		setReferralEligibilityRemote(t, b, "uuid-paying-grace", "DISABLED", "2020-01-01T00:00:00Z")

		paying, err := b.isPayingUser(205)
		require.NoError(t, err)
		assert.False(t, paying)
	})

	t.Run("banned user", func(t *testing.T) {
		b, db := setupTestBot(t)
		_, err := db.CreateUser(206, "banned", "Banned", strPtrTest("uuid-paying-banned"), nil, nil, nil)
		require.NoError(t, err)
		paymentID, err := db.CreatePayment(&database.Payment{TelegramID: 206, Amount: 400, PaymentMethod: "sbp", Status: "pending"})
		require.NoError(t, err)
		require.NoError(t, db.ConfirmPayment(paymentID))
		require.NoError(t, db.BanUser(206, 999999))

		paying, err := b.isPayingUser(206)
		require.NoError(t, err)
		assert.False(t, paying)
	})

	t.Run("unknown to the bot", func(t *testing.T) {
		b, _ := setupTestBot(t)

		paying, err := b.isPayingUser(207)
		require.NoError(t, err)
		assert.False(t, paying)
	})
}
