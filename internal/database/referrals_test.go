package database

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateReferralInviteStoresSnapshotAndEnforcesLimits(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "referrals.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	for index := 0; index < MaxActiveReferralInvites; index++ {
		invite, createErr := db.CreateReferralInvite(100, 450+index, now.Add(time.Duration(index)*time.Second))
		require.NoError(t, createErr)
		require.Len(t, invite.Code, 16)
		assert.Equal(t, InviteKindReferral, invite.Kind)
		assert.True(t, invite.IsTrial)
		require.NotNil(t, invite.SubscriptionPrice)
		assert.Equal(t, 450+index, *invite.SubscriptionPrice)
		require.NotNil(t, invite.ExpiresAt)
		assert.Equal(t, now.Add(time.Duration(index)*time.Second).Add(30*24*time.Hour), invite.ExpiresAt.UTC())
	}

	_, err = db.CreateReferralInvite(100, 999, now.Add(time.Minute))
	assert.ErrorIs(t, err, ErrActiveInviteLimit)

	// Использование освобождает активный слот, но не суточный лимит.
	for index := 0; index < MaxDailyReferralInvites-MaxActiveReferralInvites; index++ {
		active, listErr := db.GetActiveReferralInvites(100, now.Add(2*time.Minute))
		require.NoError(t, listErr)
		require.NotEmpty(t, active)
		require.NoError(t, db.ClaimInvite(active[0].Code, int64(1000+index)))
		_, createErr := db.CreateReferralInvite(100, 500, now.Add(time.Duration(index+3)*time.Minute))
		require.NoError(t, createErr)
	}
	active, err := db.GetActiveReferralInvites(100, now.Add(20*time.Minute))
	require.NoError(t, err)
	require.NotEmpty(t, active)
	require.NoError(t, db.ClaimInvite(active[0].Code, 9999))
	_, err = db.CreateReferralInvite(100, 500, now.Add(21*time.Minute))
	assert.ErrorIs(t, err, ErrDailyInviteLimit)
}

func TestCreateReferralInviteConcurrentActiveLimit(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "concurrent.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	db.Conn().SetMaxOpenConns(8)

	start := make(chan struct{})
	results := make(chan error, 12)
	var group sync.WaitGroup
	for index := 0; index < 12; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, createErr := db.CreateReferralInvite(777, 400, time.Now().UTC())
			results <- createErr
		}()
	}
	close(start)
	group.Wait()
	close(results)

	created := 0
	for createErr := range results {
		if createErr == nil {
			created++
			continue
		}
		assert.True(t, errors.Is(createErr, ErrActiveInviteLimit) || errors.Is(createErr, ErrDailyInviteLimit), createErr)
	}
	assert.Equal(t, MaxActiveReferralInvites, created)
	count, err := db.CountActiveReferralInvites(777, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, MaxActiveReferralInvites, count)
}

func TestReferralInviteRevocationAndClaimErrors(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "revoke.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	now := time.Now().UTC().Truncate(time.Second)

	owned, err := db.CreateReferralInvite(10, 400, now)
	require.NoError(t, err)
	assert.ErrorIs(t, db.RevokeReferralInvite(owned.Code, 20, false, now.Add(time.Second)), ErrInviteNotOwned)
	require.NoError(t, db.RevokeReferralInvite(owned.Code, 99, true, now.Add(time.Second)))
	assert.ErrorIs(t, db.ClaimInvite(owned.Code, 50), ErrInviteRevoked)

	expired, err := db.CreateReferralInvite(10, 400, now.Add(2*time.Second))
	require.NoError(t, err)
	_, err = db.Conn().Exec(`UPDATE invites SET expires_at = ? WHERE code = ?`, now.Add(-time.Second), expired.Code)
	require.NoError(t, err)
	assert.ErrorIs(t, db.ClaimInvite(expired.Code, 51), ErrInviteExpired)
}

func TestFirstReferralInviterSurvivesReentry(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "first-touch.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	now := time.Now().UTC().Add(-time.Hour)

	first, err := db.CreateReferralInvite(10, 400, now)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(first.Code, 500))
	_, err = db.CreateUserWithInviter(500, "guest", "Guest", strPtrTest("uuid-first"), nil, first.SubscriptionPrice, nil, &first.CreatedBy)
	require.NoError(t, err)
	_, err = db.Conn().Exec(`DELETE FROM users WHERE telegram_id = 500`)
	require.NoError(t, err)

	second, err := db.CreateReferralInvite(20, 400, now.Add(10*time.Minute))
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(second.Code, 500))
	inviter, err := db.GetFirstReferralInviter(500)
	require.NoError(t, err)
	require.NotNil(t, inviter)
	assert.Equal(t, int64(10), *inviter)
	_, err = db.CreateUserWithInviter(500, "guest2", "Guest", strPtrTest("uuid-second"), nil, second.SubscriptionPrice, nil, inviter)
	require.NoError(t, err)
	user, err := db.GetUserByTelegramID(500)
	require.NoError(t, err)
	require.NotNil(t, user.InvitedBy)
	assert.Equal(t, int64(10), *user.InvitedBy)
}

func TestAdminFirstTouchCannotBeClaimedByLaterReferral(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "admin-first.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	adminInvite, err := db.CreateInvite(999)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(adminInvite.Code, 600))
	_, err = db.Conn().Exec(`UPDATE invites SET used_at = '2026-01-01 00:00:00' WHERE code = ?`, adminInvite.Code)
	require.NoError(t, err)
	referral, err := db.CreateReferralInvite(10, 400, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(referral.Code, 600))

	inviter, err := db.GetFirstReferralInviter(600)
	require.NoError(t, err)
	assert.Nil(t, inviter)
	rows, err := db.GetReferralLeaderboard(nil, 10, 0)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestReferralOverviewCountsFirstPaymentByPaymentDate(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "overview.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	for _, item := range []struct {
		userID      int64
		inviteDate  string
		paymentDate string
	}{
		{userID: 701, inviteDate: "2026-06-01 00:00:00", paymentDate: "2026-07-15 00:00:00"},
		{userID: 702, inviteDate: "2026-07-12 00:00:00", paymentDate: "2026-07-11 00:00:00"},
	} {
		invite, createErr := db.CreateReferralInvite(10, 400, time.Now().UTC())
		require.NoError(t, createErr)
		require.NoError(t, db.ClaimInvite(invite.Code, item.userID))
		_, err = db.Conn().Exec(`UPDATE invites SET used_at = ? WHERE code = ?`, item.inviteDate, invite.Code)
		require.NoError(t, err)
		paymentID, createErr := db.CreatePayment(&Payment{TelegramID: item.userID, Amount: 400, PaymentMethod: "sbp", Status: "pending"})
		require.NoError(t, createErr)
		require.NoError(t, db.ConfirmPayment(paymentID))
		_, err = db.Conn().Exec(`UPDATE payments SET confirmed_at = ? WHERE id = ?`, item.paymentDate, paymentID)
		require.NoError(t, err)
	}
	start := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	overview, err := db.GetReferralOverview(&start, &end)
	require.NoError(t, err)
	assert.Equal(t, 1, overview.UniqueInvited)
	assert.Equal(t, 1, overview.FirstPaid, "учитывается событие первой оплаты в периоде, но не оплата до referral-входа")
}

func TestLegacyMigrationPreservesHistoryAndBackfillsFirstTouch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	statements := []string{
		`CREATE TABLE users (telegram_id INTEGER PRIMARY KEY, username TEXT, remnawave_uuid TEXT UNIQUE NOT NULL, subscription_price INTEGER, moderator_id INTEGER, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE invites (code TEXT PRIMARY KEY, created_by INTEGER NOT NULL, used_by INTEGER, used_at TIMESTAMP, expire_days INTEGER, subscription_price INTEGER, is_trial INTEGER NOT NULL DEFAULT 0, kicked_at TIMESTAMP, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE payments (id INTEGER PRIMARY KEY AUTOINCREMENT, telegram_id INTEGER NOT NULL, moderator_id INTEGER, amount INTEGER NOT NULL, payment_method TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending', platega_transaction_id TEXT UNIQUE, redirect_url TEXT, expires_at TIMESTAMP, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, confirmed_at TIMESTAMP)`,
		`CREATE TABLE moderator_earnings (id INTEGER PRIMARY KEY AUTOINCREMENT, payment_id INTEGER NOT NULL REFERENCES payments(id), moderator_id INTEGER NOT NULL, gross_amount INTEGER NOT NULL, platega_fee INTEGER NOT NULL, withdrawal_fee INTEGER NOT NULL, net_amount INTEGER NOT NULL, share_percent INTEGER NOT NULL, share_amount INTEGER NOT NULL, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		`INSERT INTO users (telegram_id, username, remnawave_uuid, subscription_price, moderator_id) VALUES (500, 'legacy', 'uuid-legacy', 650, 10)`,
		`INSERT INTO users (telegram_id, username, remnawave_uuid, subscription_price, moderator_id) VALUES (501, 'fallback', 'uuid-fallback', 700, 11)`,
		`INSERT INTO invites (code, created_by, used_by, expire_days, subscription_price, is_trial, created_at) VALUES ('USEDOLD1', 10, 500, 3, 650, 1, '2026-01-01 09:00:00')`,
		`INSERT INTO invites (code, created_by, expire_days, subscription_price, is_trial) VALUES ('UNUSED01', 10, 3, 650, 1)`,
		`INSERT INTO invites (code, created_by) VALUES ('ADMIN001', 99)`,
		`INSERT INTO payments (telegram_id, moderator_id, amount, payment_method, status, confirmed_at) VALUES (500, 10, 650, 'sbp', 'confirmed', CURRENT_TIMESTAMP)`,
		`INSERT INTO moderator_earnings (payment_id, moderator_id, gross_amount, platega_fee, withdrawal_fee, net_amount, share_percent, share_amount) VALUES (1, 10, 650, 71, 11, 568, 15, 85)`,
	}
	for _, statement := range statements {
		_, err = legacy.Exec(statement)
		require.NoError(t, err)
	}
	require.NoError(t, legacy.Close())

	db, err := New(path)
	require.NoError(t, err)
	user, err := db.GetUserByTelegramID(500)
	require.NoError(t, err)
	require.NotNil(t, user.InvitedBy)
	assert.Equal(t, int64(10), *user.InvitedBy)
	require.NotNil(t, user.SubscriptionPrice)
	assert.Equal(t, 650, *user.SubscriptionPrice)
	usedLegacy, err := db.GetInviteByCode("USEDOLD1")
	require.NoError(t, err)
	require.NotNil(t, usedLegacy.UsedAt)
	assert.Equal(t, usedLegacy.CreatedAt, *usedLegacy.UsedAt)
	fallbackInviter, err := db.GetFirstReferralInviter(501)
	require.NoError(t, err)
	require.NotNil(t, fallbackInviter)
	assert.Equal(t, int64(11), *fallbackInviter)
	_, err = db.Conn().Exec(`DELETE FROM users WHERE telegram_id = 501`)
	require.NoError(t, err)
	fallbackInviter, err = db.GetFirstReferralInviter(501)
	require.NoError(t, err)
	require.NotNil(t, fallbackInviter)
	assert.Equal(t, int64(11), *fallbackInviter, "архивная атрибуция переживает автокик")
	unused, err := db.GetInviteByCode("UNUSED01")
	require.NoError(t, err)
	assert.Equal(t, InviteKindReferral, unused.Kind)
	require.NotNil(t, unused.RevokedAt)
	admin, err := db.GetInviteByCode("ADMIN001")
	require.NoError(t, err)
	assert.Equal(t, InviteKindAdmin, admin.Kind)
	assert.Nil(t, admin.RevokedAt)
	var earnings int
	require.NoError(t, db.Conn().QueryRow(`SELECT COUNT(*) FROM moderator_earnings`).Scan(&earnings))
	assert.Equal(t, 1, earnings)
	require.NoError(t, db.Close())

	// Повторный запуск миграции не меняет историю и не дублирует данные.
	db, err = New(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.Conn().QueryRow(`SELECT COUNT(*) FROM moderator_earnings`).Scan(&earnings))
	assert.Equal(t, 1, earnings)
}
