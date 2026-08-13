package bot

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
)

func setReferralEligibilityRemote(t *testing.T, b *Bot, uuid, status, expireAt string) {
	t.Helper()
	b.remnawave.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, "/api/users/"+uuid, r.URL.Path)
		payload := `{"response":{"uuid":"` + uuid + `","username":"user","status":"` + status + `","expireAt":"` + expireAt + `"}}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(payload)), Header: make(http.Header)}, nil
	})})
}

func TestReferralInviteMessageUsesPriceSnapshotAndMoscowDeadline(t *testing.T) {
	b, _ := setupTestBot(t)
	b.config.DefaultSubscriptionPrice = 400
	b.config.TrialTrafficLimitGB = 1
	price := 650
	expiresAt := time.Date(2026, 7, 20, 12, 30, 0, 0, time.UTC)
	message := b.referralInviteMessage(&database.Invite{
		Code:              "0123456789abcdef",
		SubscriptionPrice: &price,
		ExpiresAt:         &expiresAt,
	})

	assert.Contains(t, message, "72 часа, 1 ГБ")
	assert.Contains(t, message, "650 ₽/мес")
	assert.Contains(t, message, "автоматически отключится")
	assert.Contains(t, message, "исключён из системы")
	assert.Contains(t, message, "20.07.2026 15:30 МСК")
	assert.Contains(t, message, "https://t.me/bot?start=0123456789abcdef")
}

func TestCanCreateReferralInviteRules(t *testing.T) {
	t.Run("confirmed payment and active access", func(t *testing.T) {
		b, db := setupTestBot(t)
		_, err := db.CreateUser(101, "paid", "Paid", strPtrTest("uuid-paid-ref"), nil, nil, nil)
		require.NoError(t, err)
		paymentID, err := db.CreatePayment(&database.Payment{TelegramID: 101, Amount: 400, PaymentMethod: "sbp", Status: "pending"})
		require.NoError(t, err)
		require.NoError(t, db.ConfirmPayment(paymentID))
		setReferralEligibilityRemote(t, b, "uuid-paid-ref", "ACTIVE", "2098-01-01T00:00:00Z")
		allowed, err := b.canCreateReferralInvite(101)
		require.NoError(t, err)
		assert.True(t, allowed)
	})

	t.Run("legacy-paid active access", func(t *testing.T) {
		b, db := setupTestBot(t)
		_, err := db.CreateUser(102, "legacy", "Legacy", strPtrTest("uuid-legacy-ref"), nil, nil, nil)
		require.NoError(t, err)
		require.NoError(t, db.SetLegacyPaidMigrated(102, true))
		setReferralEligibilityRemote(t, b, "uuid-legacy-ref", "ACTIVE", "2098-01-01T00:00:00Z")
		allowed, err := b.canCreateReferralInvite(102)
		require.NoError(t, err)
		assert.True(t, allowed)
	})

	t.Run("infinite access without payment", func(t *testing.T) {
		b, db := setupTestBot(t)
		_, err := db.CreateUser(104, "infinite", "Infinite", strPtrTest("uuid-infinite-ref"), nil, nil, nil)
		require.NoError(t, err)
		setReferralEligibilityRemote(t, b, "uuid-infinite-ref", "ACTIVE", "2099-01-01T00:00:00Z")
		allowed, err := b.canCreateReferralInvite(104)
		require.NoError(t, err)
		assert.True(t, allowed)
	})

	t.Run("trial and grace are rejected", func(t *testing.T) {
		for _, testCase := range []struct {
			name, status, expire string
		}{
			{name: "trial", status: "ACTIVE", expire: "2098-01-01T00:00:00Z"},
			{name: "grace", status: "DISABLED", expire: "2020-01-01T00:00:00Z"},
		} {
			t.Run(testCase.name, func(t *testing.T) {
				b, db := setupTestBot(t)
				_, err := db.CreateUser(103, "trial", "Trial", strPtrTest("uuid-trial-ref"), nil, nil, nil)
				require.NoError(t, err)
				setReferralEligibilityRemote(t, b, "uuid-trial-ref", testCase.status, testCase.expire)
				allowed, err := b.canCreateReferralInvite(103)
				require.NoError(t, err)
				assert.False(t, allowed)
			})
		}
	})
}

func TestAdminPriceChangeDoesNotRewriteInviteSnapshot(t *testing.T) {
	b, db := setupTestBot(t)
	invite, err := db.CreateReferralInvite(10, 450, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(invite.Code, 800))
	_, err = db.CreateUserWithInviter(800, "guest", "Guest", strPtrTest("uuid-price-snapshot"), nil, invite.SubscriptionPrice, nil, &invite.CreatedBy)
	require.NoError(t, err)

	require.NoError(t, b.applyAdminChangePrice(800, 700, nil))
	user, err := db.GetUserByTelegramID(800)
	require.NoError(t, err)
	require.NotNil(t, user.SubscriptionPrice)
	assert.Equal(t, 700, *user.SubscriptionPrice)
	storedInvite, err := db.GetInviteByCode(invite.Code)
	require.NoError(t, err)
	require.NotNil(t, storedInvite.SubscriptionPrice)
	assert.Equal(t, 450, *storedInvite.SubscriptionPrice)
}

func TestProcessInviteCodeDistinguishesUsedAndUnknown(t *testing.T) {
	b, db := setupTestBot(t)
	invite, err := db.CreateReferralInvite(10, 400, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(invite.Code, 900))

	usedContext := &MockContext{sender: &tele.User{ID: 901}}
	require.NoError(t, b.processInviteCode(usedContext, invite.Code))
	usedMessage, ok := usedContext.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, usedMessage, "уже использовано")

	unknownContext := &MockContext{sender: &tele.User{ID: 902}}
	require.NoError(t, b.processInviteCode(unknownContext, "does-not-exist"))
	unknownMessage, ok := unknownContext.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, unknownMessage, "не найден")
	assert.NotContains(t, unknownMessage, "уже использовано")
}

func TestReferralSectionRequiresCurrentRegistrationAndNoBan(t *testing.T) {
	b, db := setupTestBot(t)
	_, err := db.CreateUser(950, "member", "Member", strPtrTest("uuid-ref-access"), nil, nil, nil)
	require.NoError(t, err)
	accessible, err := b.canAccessReferralSection(950)
	require.NoError(t, err)
	assert.True(t, accessible)

	require.NoError(t, db.BanUser(950, 999999))
	accessible, err = b.canAccessReferralSection(950)
	require.NoError(t, err)
	assert.False(t, accessible)

	require.NoError(t, db.DeleteUser(950))
	accessible, err = b.canAccessReferralSection(950)
	require.NoError(t, err)
	assert.False(t, accessible)
}

func TestRegistrationRollbackKeepsClaimUntilRemoteDeletionIsConfirmed(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		status      int
		wantClaimed bool
	}{
		{name: "temporary remote failure", status: http.StatusInternalServerError, wantClaimed: true},
		{name: "remote user already absent", status: http.StatusNotFound, wantClaimed: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			b, db := setupTestBot(t)
			invite, err := db.CreateReferralInvite(10, 400, time.Now().UTC())
			require.NoError(t, err)
			require.NoError(t, db.ClaimInvite(invite.Code, 990))
			b.remnawave.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				assert.Equal(t, http.MethodDelete, r.Method)
				assert.Equal(t, "/api/users/uuid-partial", r.URL.Path)
				return &http.Response{
					StatusCode: testCase.status,
					Body:       io.NopCloser(strings.NewReader(`{"error":"temporary"}`)),
					Header:     make(http.Header),
				}, nil
			})})

			b.rollbackCreatedRemnawaveUser(invite.Code, 990, remnawave.UserRef{UUID: "uuid-partial"})
			stored, err := db.GetInviteByCode(invite.Code)
			require.NoError(t, err)
			if testCase.wantClaimed {
				require.NotNil(t, stored.UsedBy)
				assert.Equal(t, int64(990), *stored.UsedBy)
			} else {
				assert.Nil(t, stored.UsedBy)
			}
		})
	}
}
