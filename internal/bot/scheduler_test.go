package bot

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
)

// setupSchedulerTestBot создаёт бота для тестов scheduler.
func setupSchedulerTestBot(t *testing.T) (*Bot, *database.DB) {
	t.Helper()
	dbFile := t.TempDir() + "/" + fmt.Sprintf("test_scheduler_%s.db", t.Name())
	db, err := database.New(dbFile)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		_ = os.Remove(dbFile)
	})
	cfg := &config.Config{AdminID: 999}
	b := &Bot{
		db:         db,
		config:     cfg,
		userStates: newStateMap(),
		remnawave:  remnawave.NewClient("https://panel.example.com", "test-token", nil),
	}
	return b, db
}

func newOfflineTelegramBotForTest(t *testing.T, transport http.RoundTripper) *tele.Bot {
	t.Helper()

	bot, err := tele.NewBot(tele.Settings{
		Token:   "test-token",
		URL:     "https://api.telegram.org",
		Offline: true,
		Client: &http.Client{
			Transport: transport,
		},
	})
	require.NoError(t, err)

	return bot
}

// TestHandleAutoKick_404IsNotFatalError проверяет, что при 404 от Remnawave (пользователь
// уже удалён администратором) handleAutoKick всё равно выполняет очистку в БД.
func TestHandleAutoKick_404IsNotFatalError(t *testing.T) {
	b, db := setupSchedulerTestBot(t)

	_, err := db.CreateUser(700, "victim", "Victim", "uuid-700", nil, nil)
	require.NoError(t, err)
	modID := int64(50)
	_, err = db.CreateUser(modID, "mod", "Mod", "uuid-mod", nil, nil)
	require.NoError(t, err)
	expireDays := 30
	inv, err := db.CreateInviteWithExpiry(modID, &expireDays)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, 700))

	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodDelete {
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader(`{"error":"not found"}`)),
					Header:     make(http.Header),
				}, nil
			}
			return nil, fmt.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}),
	})
	b.remnawave = client

	b.handleAutoKick(700, "uuid-700")

	dbUser, err := db.GetUserByTelegramID(700)
	require.NoError(t, err)
	assert.Nil(t, dbUser, "пользователь должен быть удалён из БД даже если Remnawave вернул 404")

	invite, err := db.GetInviteByCode(inv.Code)
	require.NoError(t, err)
	require.NotNil(t, invite)
	assert.NotNil(t, invite.UsedBy, "used_by должен остаться — история активации сохраняется")
	assert.NotNil(t, invite.KickedAt, "kicked_at должен быть проставлен после автокика")
}

func TestHandleAutoKick_DoesNotCleanupOnRemnawaveDeleteError(t *testing.T) {
	b, db := setupSchedulerTestBot(t)

	_, err := db.CreateUser(701, "victim", "Victim", "uuid-701", nil, nil)
	require.NoError(t, err)
	modID := int64(51)
	_, err = db.CreateUser(modID, "mod", "Mod", "uuid-mod", nil, nil)
	require.NoError(t, err)
	expireDays := 30
	inv, err := db.CreateInviteWithExpiry(modID, &expireDays)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, 701))

	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodDelete {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(strings.NewReader(`{"error":"boom"}`)),
					Header:     make(http.Header),
				}, nil
			}
			return nil, fmt.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}),
	})
	b.remnawave = client

	b.handleAutoKick(701, "uuid-701")

	dbUser, err := db.GetUserByTelegramID(701)
	require.NoError(t, err)
	assert.NotNil(t, dbUser, "локальный cleanup нельзя делать, если DeleteUser в Remnawave завершился ошибкой")

	invite, err := db.GetInviteByCode(inv.Code)
	require.NoError(t, err)
	require.NotNil(t, invite)
	assert.Nil(t, invite.KickedAt, "kicked_at нельзя ставить при неуспешном удалении в панели")
}

func TestHandleAutoKick_SkipsAlreadyDeletedInRemnawave(t *testing.T) {
	err := fmt.Errorf("API error 404: not found")
	assert.True(t, isAutoKickNotFoundError(err))

	otherErr := fmt.Errorf("API error 500: internal server error")
	assert.False(t, isAutoKickNotFoundError(otherErr))
}

func TestIsSchedulerForbiddenError(t *testing.T) {
	t.Run("ErrBlockedByUser распознаётся", func(t *testing.T) {
		assert.True(t, isSchedulerForbiddenError(tele.ErrBlockedByUser))
	})
	t.Run("ErrUserIsDeactivated распознаётся", func(t *testing.T) {
		assert.True(t, isSchedulerForbiddenError(tele.ErrUserIsDeactivated))
	})
	t.Run("ErrNotStartedByUser распознаётся", func(t *testing.T) {
		assert.True(t, isSchedulerForbiddenError(tele.ErrNotStartedByUser))
	})
	t.Run("Обычная ошибка НЕ распознаётся", func(t *testing.T) {
		assert.False(t, isSchedulerForbiddenError(fmt.Errorf("network timeout")))
	})
	t.Run("Строковая ошибка с 403 НЕ распознаётся", func(t *testing.T) {
		assert.False(t, isSchedulerForbiddenError(fmt.Errorf("some error with 403 code")))
	})
}

// TestIsTrialUser проверяет определение типа подписки
func TestIsTrialUser(t *testing.T) {
	b, db := setupSchedulerTestBot(t)

	modID := int64(100)
	_, err := db.CreateUser(modID, "mod", "Mod", "uuid-mod", nil, nil)
	require.NoError(t, err)
	db.Conn().Exec(`INSERT INTO moderators (telegram_id, added_by) VALUES (?, ?)`, modID, 999)

	userID := int64(200)
	price := 400
	_, err = db.CreateUser(userID, "user", "User", "uuid-200", &price, &modID)
	require.NoError(t, err)

	expireDays := 30
	inv, err := db.CreateInviteWithExpiry(modID, &expireDays)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, userID))

	t.Run("пользователь без оплаты — триал", func(t *testing.T) {
		assert.True(t, b.isTrialUser(userID))
	})

	t.Run("после оплаты — не триал", func(t *testing.T) {
		payment := &database.Payment{
			TelegramID:    userID,
			Amount:        400,
			PaymentMethod: "sbp",
			Status:        "pending",
		}
		id, err := db.CreatePayment(payment)
		require.NoError(t, err)
		require.NoError(t, db.ConfirmPayment(id))

		assert.False(t, b.isTrialUser(userID))
	})

	t.Run("админский инвайт — не триал", func(t *testing.T) {
		adminUserID := int64(300)
		_, err := db.CreateUser(adminUserID, "admin_user", "Admin User", "uuid-300", nil, nil)
		require.NoError(t, err)

		invAdmin, err := db.CreateInviteWithExpiry(999, nil)
		require.NoError(t, err)
		require.NoError(t, db.ClaimInvite(invAdmin.Code, adminUserID))

		assert.False(t, b.isTrialUser(adminUserID))
	})
}

func TestIsTrialUserTreatsLegacyPaidMigratedUserAsPaid(t *testing.T) {
	b, db := setupSchedulerTestBot(t)

	modID := int64(120)
	_, err := db.CreateUser(modID, "mod", "Mod", "uuid-mod-120", nil, nil)
	require.NoError(t, err)
	db.Conn().Exec(`INSERT INTO moderators (telegram_id, added_by) VALUES (?, ?)`, modID, 999)

	userID := int64(220)
	price := 500
	_, err = db.CreateUser(userID, "legacy_paid", "Legacy Paid", "uuid-220", &price, &modID)
	require.NoError(t, err)

	expireDays := 30
	inv, err := db.CreateInviteWithExpiry(modID, &expireDays)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, userID))
	require.NoError(t, db.SetLegacyPaidMigrated(userID, true))

	assert.False(t, b.isTrialUser(userID))
}

// TestSchedulerTrialKick проверяет кик триального пользователя после expireAt
func TestSchedulerTrialKick(t *testing.T) {
	b, db := setupSchedulerTestBot(t)

	modID := int64(100)
	_, err := db.CreateUser(modID, "mod", "Mod", "uuid-mod", nil, nil)
	require.NoError(t, err)
	db.Conn().Exec(`INSERT INTO moderators (telegram_id, added_by) VALUES (?, ?)`, modID, 999)

	userID := int64(200)
	price := 400
	_, err = db.CreateUser(userID, "user", "User", "uuid-200", &price, &modID)
	require.NoError(t, err)

	expireDays := 3
	inv, err := db.CreateInviteWithExpiry(modID, &expireDays)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, userID))

	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"response":{}}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	b.remnawave = client

	// expireAt вчера — триал истёк
	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	b.processTrialUser(userID, database.User{TelegramID: userID, RemnawaveUUID: "uuid-200"}, yesterday, time.Now().UTC())

	dbUser, err := db.GetUserByTelegramID(userID)
	require.NoError(t, err)
	assert.Nil(t, dbUser, "триальный пользователь должен быть удалён после expireAt")
}

func TestSchedulerTrialWaitsForExactExpireAt(t *testing.T) {
	b, db := setupSchedulerTestBot(t)

	modID := int64(110)
	_, err := db.CreateUser(modID, "mod", "Mod", "uuid-mod", nil, nil)
	require.NoError(t, err)
	db.Conn().Exec(`INSERT INTO moderators (telegram_id, added_by) VALUES (?, ?)`, modID, 999)

	userID := int64(210)
	price := 400
	_, err = db.CreateUser(userID, "trial_exact", "Trial", "uuid-210", &price, &modID)
	require.NoError(t, err)

	expireDays := 3
	inv, err := db.CreateInviteWithExpiry(modID, &expireDays)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, userID))

	expireAt := time.Now().UTC().Add(2 * time.Hour)
	b.processTrialUser(userID, database.User{TelegramID: userID, RemnawaveUUID: "uuid-210"}, expireAt, time.Now().UTC())

	dbUser, err := db.GetUserByTelegramID(userID)
	require.NoError(t, err)
	assert.NotNil(t, dbUser, "триальный пользователь не должен кикаться раньше точного expireAt")
}

func TestSchedulerPaidReminderForLegacyPaidMigratedUser(t *testing.T) {
	b, db := setupSchedulerTestBot(t)

	modID := int64(130)
	_, err := db.CreateUser(modID, "mod", "Mod", "uuid-mod-130", nil, nil)
	require.NoError(t, err)
	db.Conn().Exec(`INSERT INTO moderators (telegram_id, added_by) VALUES (?, ?)`, modID, 999)

	userID := int64(230)
	price := 500
	_, err = db.CreateUser(userID, "legacy_paid", "Legacy Paid", "uuid-230", &price, &modID)
	require.NoError(t, err)

	expireDays := 30
	inv, err := db.CreateInviteWithExpiry(modID, &expireDays)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, userID))
	require.NoError(t, db.SetLegacyPaidMigrated(userID, true))

	expireAt := time.Now().UTC().Add(60 * time.Hour)

	b.remnawave = remnawave.NewClient("https://panel.example.com", "test-token", nil)
	b.remnawave.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/users" && r.URL.Query().Get("size") == "1000":
				payload := fmt.Sprintf(`{"response":{"users":[{"uuid":"uuid-230","username":"legacy_paid","status":"ACTIVE","telegramId":230,"expireAt":"%s"}],"total":1}}`,
					expireAt.Format(time.RFC3339))
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			default:
				return nil, fmt.Errorf("unexpected remnawave request: %s %s", r.Method, r.URL.Path)
			}
		}),
	})

	b.bot = newOfflineTelegramBotForTest(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/sendMessage"):
			payload := `{"ok":true,"result":{"message_id":1,"date":1710000000,"chat":{"id":230,"type":"private"},"text":"ok"}}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(payload)),
				Header:     make(http.Header),
			}, nil
		default:
			return nil, fmt.Errorf("unexpected telegram request: %s %s", r.Method, r.URL.Path)
		}
	}))

	b.runSubscriptionSchedulerPass()

	sent, err := db.WasNotificationSent(userID, notificationExpire3d)
	require.NoError(t, err)
	assert.True(t, sent, "migrated-paid пользователь должен получить 3-day reminder")

	trialSent, err := db.WasNotificationSent(userID, notificationTrialExpire1d)
	require.NoError(t, err)
	assert.False(t, trialSent, "migrated-paid пользователь не должен идти по trial-ветке")
}

// TestSchedulerTrialNotKickedIfPaid проверяет, что оплативший триальный не кикается
func TestSchedulerTrialNotKickedIfPaid(t *testing.T) {
	b, db := setupSchedulerTestBot(t)

	modID := int64(100)
	_, err := db.CreateUser(modID, "mod", "Mod", "uuid-mod", nil, nil)
	require.NoError(t, err)
	db.Conn().Exec(`INSERT INTO moderators (telegram_id, added_by) VALUES (?, ?)`, modID, 999)

	userID := int64(201)
	price := 400
	_, err = db.CreateUser(userID, "user_paid_trial", "User", "uuid-201", &price, &modID)
	require.NoError(t, err)

	expireDays := 3
	inv, err := db.CreateInviteWithExpiry(modID, &expireDays)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, userID))

	// Платёж подтверждён только что — isTrialUser вернёт false, но processTrialUser
	// проверяет HasConfirmedPaymentSince и пропустит кик
	payment := &database.Payment{
		TelegramID:    userID,
		Amount:        400,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	id, err := db.CreatePayment(payment)
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(id))

	// expireAt вчера
	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	b.processTrialUser(userID, database.User{TelegramID: userID, RemnawaveUUID: "uuid-201"}, yesterday, time.Now().UTC())

	dbUser, err := db.GetUserByTelegramID(userID)
	require.NoError(t, err)
	assert.NotNil(t, dbUser, "оплативший пользователь не должен быть кикнут")
}

func TestSchedulerTrialNotKickedIfPaymentConfirmedNotActivated(t *testing.T) {
	b, db := setupSchedulerTestBot(t)

	modID := int64(101)
	_, err := db.CreateUser(modID, "mod", "Mod", "uuid-mod", nil, nil)
	require.NoError(t, err)
	db.Conn().Exec(`INSERT INTO moderators (telegram_id, added_by) VALUES (?, ?)`, modID, 999)

	userID := int64(202)
	price := 400
	_, err = db.CreateUser(userID, "user_retry_trial", "User", "uuid-202", &price, &modID)
	require.NoError(t, err)

	expireDays := 3
	inv, err := db.CreateInviteWithExpiry(modID, &expireDays)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, userID))

	payment := &database.Payment{
		TelegramID:    userID,
		Amount:        400,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	id, err := db.CreatePayment(payment)
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(id))
	require.NoError(t, db.UpdatePaymentStatus(id, "confirmed_not_activated"))

	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	b.processTrialUser(userID, database.User{TelegramID: userID, RemnawaveUUID: "uuid-202"}, yesterday, time.Now().UTC())

	dbUser, err := db.GetUserByTelegramID(userID)
	require.NoError(t, err)
	assert.NotNil(t, dbUser, "пользователь с confirmed_not_activated не должен быть кикнут как неоплативший")
}

func TestSchedulerSkipsLegacyUserWithoutInvite(t *testing.T) {
	b, db := setupSchedulerTestBot(t)

	userID := int64(260)
	_, err := db.CreateUser(userID, "legacy_user", "Legacy", "uuid-260", nil, nil)
	require.NoError(t, err)

	invite, err := db.GetInviteByUsedBy(userID)
	require.NoError(t, err)
	require.Nil(t, invite, "legacy-пользователь не должен иметь связанного инвайта")

	var disableCalled bool
	var deleteCalled bool
	expireAt := time.Now().UTC().Add(-2 * time.Hour)

	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/users":
				payload := fmt.Sprintf(`{"response":{"users":[{"uuid":"uuid-260","username":"legacy_user","status":"ACTIVE","telegramId":260,"expireAt":"%s"}],"total":1}}`,
					expireAt.Format(time.RFC3339))
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			case r.Method == http.MethodPatch && r.URL.Path == "/api/users":
				disableCalled = true
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"response":{}}`)),
					Header:     make(http.Header),
				}, nil
			case r.Method == http.MethodDelete && r.URL.Path == "/api/users/uuid-260":
				deleteCalled = true
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"response":{}}`)),
					Header:     make(http.Header),
				}, nil
			default:
				return nil, fmt.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
			}
		}),
	})
	b.remnawave = client

	b.runSubscriptionSchedulerPass()

	dbUser, err := db.GetUserByTelegramID(userID)
	require.NoError(t, err)
	assert.NotNil(t, dbUser, "legacy-пользователь без инвайта не должен обрабатываться scheduler")
	assert.False(t, disableCalled, "scheduler не должен disable-ить legacy-пользователя без инвайта")
	assert.False(t, deleteCalled, "scheduler не должен кикать legacy-пользователя без инвайта")
}

// TestSchedulerPaidDisableAndGraceKick проверяет disable при expireAt и кик через 72ч
func TestSchedulerPaidDisableAndGraceKick(t *testing.T) {
	b, db := setupSchedulerTestBot(t)

	modID := int64(100)
	_, err := db.CreateUser(modID, "mod", "Mod", "uuid-mod", nil, nil)
	require.NoError(t, err)
	db.Conn().Exec(`INSERT INTO moderators (telegram_id, added_by) VALUES (?, ?)`, modID, 999)

	userID := int64(300)
	price := 400
	_, err = db.CreateUser(userID, "paiduser", "PaidUser", "uuid-300", &price, &modID)
	require.NoError(t, err)

	expireDays := 30
	inv, err := db.CreateInviteWithExpiry(modID, &expireDays)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, userID))

	// Делаем пользователя оплаченным (не триал), подтверждение 60 дней назад
	payment := &database.Payment{
		TelegramID:    userID,
		Amount:        400,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	id, err := db.CreatePayment(payment)
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(id))
	_, err = db.Conn().Exec(`UPDATE payments SET confirmed_at = datetime('now', '-60 days') WHERE id = ?`, id)
	require.NoError(t, err)

	t.Run("disable при expireAt", func(t *testing.T) {
		var disableCalled bool
		client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
		client.SetHTTPClient(&http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodPatch {
					disableCalled = true
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(`{"response":{}}`)),
						Header:     make(http.Header),
					}, nil
				}
				return nil, fmt.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
			}),
		})
		b.remnawave = client

		yesterday := time.Now().UTC().AddDate(0, 0, -1)
		b.processPaidUser(userID, database.User{TelegramID: userID, RemnawaveUUID: "uuid-300"}, yesterday, time.Now().UTC())

		assert.True(t, disableCalled, "DisableUser должен быть вызван при expireAt")
	})

	t.Run("кик через 72ч grace", func(t *testing.T) {
		var deleteCalled bool
		client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
		client.SetHTTPClient(&http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/api/users/uuid-300") {
					user := remnawave.User{
						UUID:     "uuid-300",
						Status:   "DISABLED",
						ExpireAt: time.Now().UTC().AddDate(0, 0, -5),
					}
					body, _ := json.Marshal(map[string]interface{}{"response": user})
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(string(body))),
						Header:     make(http.Header),
					}, nil
				}
				if r.Method == http.MethodPatch {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(`{"response":{}}`)),
						Header:     make(http.Header),
					}, nil
				}
				if r.Method == http.MethodDelete {
					deleteCalled = true
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(`{"response":{}}`)),
						Header:     make(http.Header),
					}, nil
				}
				return nil, fmt.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
			}),
		})
		b.remnawave = client

		// expireAt 4 дня назад — grace period (72ч) истёк
		expireAt := time.Now().UTC().Add(-96 * time.Hour)
		b.processPaidUser(userID, database.User{TelegramID: userID, RemnawaveUUID: "uuid-300"}, expireAt, time.Now().UTC())

		assert.True(t, deleteCalled, "пользователь должен быть удалён после grace period")
	})
}

func TestSchedulerPaidWaitsForExactExpireAt(t *testing.T) {
	b, db := setupSchedulerTestBot(t)

	modID := int64(120)
	_, err := db.CreateUser(modID, "mod", "Mod", "uuid-mod", nil, nil)
	require.NoError(t, err)
	db.Conn().Exec(`INSERT INTO moderators (telegram_id, added_by) VALUES (?, ?)`, modID, 999)

	userID := int64(310)
	price := 400
	_, err = db.CreateUser(userID, "paid_exact", "Paid", "uuid-310", &price, &modID)
	require.NoError(t, err)

	expireDays := 30
	inv, err := db.CreateInviteWithExpiry(modID, &expireDays)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, userID))

	payment := &database.Payment{
		TelegramID:    userID,
		Amount:        400,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	id, err := db.CreatePayment(payment)
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(id))
	_, err = db.Conn().Exec(`UPDATE payments SET confirmed_at = datetime('now', '-60 days') WHERE id = ?`, id)
	require.NoError(t, err)

	var disableCalled bool
	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodPatch {
				disableCalled = true
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"response":{}}`)),
					Header:     make(http.Header),
				}, nil
			}
			return nil, fmt.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}),
	})
	b.remnawave = client

	expireAt := time.Now().UTC().Add(2 * time.Hour)
	b.processPaidUser(userID, database.User{TelegramID: userID, RemnawaveUUID: "uuid-310"}, expireAt, time.Now().UTC())

	assert.False(t, disableCalled, "оплаченный пользователь не должен disable-иться раньше точного expireAt")
}

func TestSchedulerPaidDisableIgnoresPaymentsBeforeExpireAt(t *testing.T) {
	b, db := setupSchedulerTestBot(t)

	modID := int64(130)
	_, err := db.CreateUser(modID, "mod", "Mod", "uuid-mod", nil, nil)
	require.NoError(t, err)
	db.Conn().Exec(`INSERT INTO moderators (telegram_id, added_by) VALUES (?, ?)`, modID, 999)

	userID := int64(320)
	price := 400
	_, err = db.CreateUser(userID, "paid_before_expire", "Paid", "uuid-320", &price, &modID)
	require.NoError(t, err)

	expireDays := 30
	inv, err := db.CreateInviteWithExpiry(modID, &expireDays)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, userID))

	payment := &database.Payment{
		TelegramID:    userID,
		Amount:        400,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	id, err := db.CreatePayment(payment)
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(id))

	expireAt := time.Now().UTC().Add(-30 * time.Minute)
	confirmedAt := expireAt.Add(-2 * time.Hour)
	_, err = db.Conn().Exec(`UPDATE payments SET confirmed_at = ? WHERE id = ?`, confirmedAt, id)
	require.NoError(t, err)

	var disableCalled bool
	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodPatch {
				disableCalled = true
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"response":{}}`)),
					Header:     make(http.Header),
				}, nil
			}
			return nil, fmt.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}),
	})
	b.remnawave = client

	b.processPaidUser(userID, database.User{TelegramID: userID, RemnawaveUUID: "uuid-320"}, expireAt, time.Now().UTC())

	assert.True(t, disableCalled, "старый платёж до expireAt не должен блокировать disable после истечения подписки")
}

func TestSchedulerPaidDisableSkippedIfPaymentConfirmedNotActivated(t *testing.T) {
	b, db := setupSchedulerTestBot(t)

	modID := int64(131)
	_, err := db.CreateUser(modID, "mod", "Mod", "uuid-mod", nil, nil)
	require.NoError(t, err)
	db.Conn().Exec(`INSERT INTO moderators (telegram_id, added_by) VALUES (?, ?)`, modID, 999)

	userID := int64(321)
	price := 400
	_, err = db.CreateUser(userID, "paid_retry_disable", "Paid", "uuid-321", &price, &modID)
	require.NoError(t, err)

	expireDays := 30
	inv, err := db.CreateInviteWithExpiry(modID, &expireDays)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, userID))

	payment := &database.Payment{
		TelegramID:    userID,
		Amount:        400,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	id, err := db.CreatePayment(payment)
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(id))
	require.NoError(t, db.UpdatePaymentStatus(id, "confirmed_not_activated"))

	var disableCalled bool
	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodPatch {
				disableCalled = true
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"response":{}}`)),
					Header:     make(http.Header),
				}, nil
			}
			return nil, fmt.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}),
	})
	b.remnawave = client

	expireAt := time.Now().UTC().Add(-30 * time.Minute)
	b.processPaidUser(userID, database.User{TelegramID: userID, RemnawaveUUID: "uuid-321"}, expireAt, time.Now().UTC())

	assert.False(t, disableCalled, "confirmed_not_activated не должен приводить к disable как будто оплаты не было")
}

func TestSchedulerGraceKickSkippedIfPaymentConfirmedNotActivated(t *testing.T) {
	b, db := setupSchedulerTestBot(t)

	modID := int64(132)
	_, err := db.CreateUser(modID, "mod", "Mod", "uuid-mod", nil, nil)
	require.NoError(t, err)
	db.Conn().Exec(`INSERT INTO moderators (telegram_id, added_by) VALUES (?, ?)`, modID, 999)

	userID := int64(322)
	price := 400
	_, err = db.CreateUser(userID, "paid_retry_grace", "Paid", "uuid-322", &price, &modID)
	require.NoError(t, err)

	expireDays := 30
	inv, err := db.CreateInviteWithExpiry(modID, &expireDays)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, userID))

	payment := &database.Payment{
		TelegramID:    userID,
		Amount:        400,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	id, err := db.CreatePayment(payment)
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(id))
	require.NoError(t, db.UpdatePaymentStatus(id, "confirmed_not_activated"))

	var deleteCalled bool
	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/api/users/uuid-322") {
				user := remnawave.User{
					UUID:     "uuid-322",
					Status:   "DISABLED",
					ExpireAt: time.Now().UTC().AddDate(0, 0, -5),
				}
				body, _ := json.Marshal(map[string]interface{}{"response": user})
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(string(body))),
					Header:     make(http.Header),
				}, nil
			}
			if r.Method == http.MethodDelete {
				deleteCalled = true
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"response":{}}`)),
					Header:     make(http.Header),
				}, nil
			}
			return nil, fmt.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}),
	})
	b.remnawave = client

	expireAt := time.Now().UTC().Add(-96 * time.Hour)
	b.processPaidUser(userID, database.User{TelegramID: userID, RemnawaveUUID: "uuid-322"}, expireAt, time.Now().UTC())

	assert.False(t, deleteCalled, "confirmed_not_activated не должен приводить к grace kick как будто оплаты не было")
}

func TestSchedulerGraceKickSkippedWhenFreshStatusCheckFails(t *testing.T) {
	b, db := setupSchedulerTestBot(t)

	modID := int64(133)
	_, err := db.CreateUser(modID, "mod", "Mod", "uuid-mod", nil, nil)
	require.NoError(t, err)
	db.Conn().Exec(`INSERT INTO moderators (telegram_id, added_by) VALUES (?, ?)`, modID, 999)

	userID := int64(323)
	price := 400
	_, err = db.CreateUser(userID, "paid_grace_error", "Paid", "uuid-323", &price, &modID)
	require.NoError(t, err)

	expireDays := 30
	inv, err := db.CreateInviteWithExpiry(modID, &expireDays)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, userID))

	payment := &database.Payment{
		TelegramID:    userID,
		Amount:        400,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	id, err := db.CreatePayment(payment)
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(id))
	_, err = db.Conn().Exec(`UPDATE payments SET confirmed_at = datetime('now', '-60 days') WHERE id = ?`, id)
	require.NoError(t, err)

	var deleteCalled bool
	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/api/users/uuid-323") {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(strings.NewReader(`{"error":"panel unavailable"}`)),
					Header:     make(http.Header),
				}, nil
			}
			if r.Method == http.MethodDelete {
				deleteCalled = true
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"response":{}}`)),
					Header:     make(http.Header),
				}, nil
			}
			return nil, fmt.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}),
	})
	b.remnawave = client

	expireAt := time.Now().UTC().Add(-96 * time.Hour)
	b.processPaidUser(userID, database.User{TelegramID: userID, RemnawaveUUID: "uuid-323"}, expireAt, time.Now().UTC())

	assert.False(t, deleteCalled, "при ошибке свежей проверки статуса scheduler не должен идти в auto-kick")

	dbUser, err := db.GetUserByTelegramID(userID)
	require.NoError(t, err)
	assert.NotNil(t, dbUser, "пользователь должен остаться в БД при ошибке проверки панели")
}

// TestSchedulerMaintenanceMode проверяет, что в maintenance mode кики и disable не выполняются
func TestSchedulerMaintenanceMode(t *testing.T) {
	b, db := setupSchedulerTestBot(t)
	b.setMaintenanceMode(true)

	modID := int64(100)
	_, err := db.CreateUser(modID, "mod", "Mod", "uuid-mod", nil, nil)
	require.NoError(t, err)
	db.Conn().Exec(`INSERT INTO moderators (telegram_id, added_by) VALUES (?, ?)`, modID, 999)

	t.Run("триал: не кикает в maintenance mode", func(t *testing.T) {
		userID := int64(400)
		price := 400
		_, err := db.CreateUser(userID, "trial_maint", "Trial", "uuid-400", &price, &modID)
		require.NoError(t, err)

		expireDays := 3
		inv, err := db.CreateInviteWithExpiry(modID, &expireDays)
		require.NoError(t, err)
		require.NoError(t, db.ClaimInvite(inv.Code, userID))

		yesterday := time.Now().UTC().AddDate(0, 0, -1)
		b.processTrialUser(userID, database.User{TelegramID: userID, RemnawaveUUID: "uuid-400"}, yesterday, time.Now().UTC())

		dbUser, err := db.GetUserByTelegramID(userID)
		require.NoError(t, err)
		assert.NotNil(t, dbUser, "в maintenance mode пользователь не должен быть кикнут")
	})

	t.Run("grace: не кикает в maintenance mode", func(t *testing.T) {
		userID := int64(500)
		price := 400
		_, err := db.CreateUser(userID, "paid_maint", "Paid", "uuid-500", &price, &modID)
		require.NoError(t, err)

		expireDays := 30
		inv, err := db.CreateInviteWithExpiry(modID, &expireDays)
		require.NoError(t, err)
		require.NoError(t, db.ClaimInvite(inv.Code, userID))

		// Оплата 60 дней назад
		payment := &database.Payment{
			TelegramID:    userID,
			Amount:        400,
			PaymentMethod: "sbp",
			Status:        "pending",
		}
		pid, err := db.CreatePayment(payment)
		require.NoError(t, err)
		require.NoError(t, db.ConfirmPayment(pid))
		_, err = db.Conn().Exec(`UPDATE payments SET confirmed_at = datetime('now', '-60 days') WHERE id = ?`, pid)
		require.NoError(t, err)

		expireAt := time.Now().UTC().Add(-96 * time.Hour)
		b.processPaidUser(userID, database.User{TelegramID: userID, RemnawaveUUID: "uuid-500"}, expireAt, time.Now().UTC())

		dbUser, err := db.GetUserByTelegramID(userID)
		require.NoError(t, err)
		assert.NotNil(t, dbUser, "в maintenance mode пользователь не должен быть кикнут при grace")
	})
}

// TestSchedulerRetryConfirmedNotActivated проверяет retry подтверждённых, но не активированных платежей
func TestSchedulerRetryConfirmedNotActivated(t *testing.T) {
	b, db := setupSchedulerTestBot(t)

	userID := int64(600)
	_, err := db.CreateUser(userID, "retry_user", "Retry", "uuid-600", nil, nil)
	require.NoError(t, err)

	payment := &database.Payment{
		TelegramID:    userID,
		Amount:        400,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	id, err := db.CreatePayment(payment)
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(id))
	confirmedAt := time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC)
	_, err = db.Conn().Exec(`UPDATE payments SET confirmed_at = ? WHERE id = ?`, confirmedAt, id)
	require.NoError(t, err)
	require.NoError(t, db.UpdatePaymentStatus(id, "confirmed_not_activated"))

	var enableCalled bool
	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/api/users/uuid-600") {
				user := remnawave.User{
					UUID:     "uuid-600",
					Status:   "EXPIRED",
					ExpireAt: time.Now().UTC().AddDate(0, 0, -1),
				}
				body, _ := json.Marshal(map[string]interface{}{"response": user})
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(string(body))),
					Header:     make(http.Header),
				}, nil
			}
			if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/api/users/by-telegram-id") {
				user := remnawave.User{
					UUID:     "uuid-600",
					Status:   "ACTIVE",
					ExpireAt: time.Now().UTC().AddDate(0, 1, 0),
				}
				body, _ := json.Marshal(map[string]interface{}{"response": user})
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(string(body))),
					Header:     make(http.Header),
				}, nil
			}
			if r.Method == http.MethodPatch {
				enableCalled = true
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"response":{}}`)),
					Header:     make(http.Header),
				}, nil
			}
			return nil, fmt.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}),
	})
	b.remnawave = client

	b.retryConfirmedNotActivated()

	assert.True(t, enableCalled, "EnableUser должен быть вызван при retry confirmed_not_activated")

	p, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	assert.Equal(t, "confirmed", p.Status, "статус должен стать confirmed после retry")
	require.NotNil(t, p.ConfirmedAt)
	assert.True(t, p.ConfirmedAt.Equal(confirmedAt), "confirmed_at должен сохраниться после retry")
}

func TestSchedulerPassDoesNotPunishConfirmedNotActivatedWhenRetryStillFails(t *testing.T) {
	b, db := setupSchedulerTestBot(t)

	modID := int64(140)
	_, err := db.CreateUser(modID, "mod", "Mod", "uuid-mod", nil, nil)
	require.NoError(t, err)
	db.Conn().Exec(`INSERT INTO moderators (telegram_id, added_by) VALUES (?, ?)`, modID, 999)

	userID := int64(630)
	price := 400
	_, err = db.CreateUser(userID, "retry_pass_user", "Retry", "uuid-630", &price, &modID)
	require.NoError(t, err)

	expireDays := 30
	inv, err := db.CreateInviteWithExpiry(modID, &expireDays)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, userID))

	payment := &database.Payment{
		TelegramID:    userID,
		Amount:        400,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	id, err := db.CreatePayment(payment)
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(id))
	require.NoError(t, db.UpdatePaymentStatus(id, "confirmed_not_activated"))

	expireAt := time.Now().UTC().Add(-2 * time.Hour)
	_, err = db.Conn().Exec(`UPDATE payments SET confirmed_at = ? WHERE id = ?`, time.Now().UTC(), id)
	require.NoError(t, err)

	var retryPatchCalled bool
	var disableCalled bool
	var deleteCalled bool

	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/api/users/uuid-630"):
				user := remnawave.User{
					UUID:     "uuid-630",
					Status:   "EXPIRED",
					ExpireAt: expireAt,
				}
				body, _ := json.Marshal(map[string]interface{}{"response": user})
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(string(body))),
					Header:     make(http.Header),
				}, nil
			case r.Method == http.MethodGet && r.URL.Path == "/api/users":
				payload := fmt.Sprintf(`{"response":{"users":[{"uuid":"uuid-630","username":"retry_pass_user","status":"EXPIRED","telegramId":630,"expireAt":"%s"}],"total":1}}`,
					expireAt.Format(time.RFC3339))
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			case r.Method == http.MethodPatch && r.URL.Path == "/api/users":
				bodyBytes, err := io.ReadAll(r.Body)
				require.NoError(t, err)

				var req remnawave.UpdateUserRequest
				require.NoError(t, json.Unmarshal(bodyBytes, &req))
				require.NotNil(t, req.Status)

				switch *req.Status {
				case remnawave.StatusActive:
					retryPatchCalled = true
					return &http.Response{
						StatusCode: http.StatusInternalServerError,
						Body:       io.NopCloser(strings.NewReader(`{"error":"temporary failure"}`)),
						Header:     make(http.Header),
					}, nil
				case remnawave.StatusDisabled:
					disableCalled = true
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(`{"response":{}}`)),
						Header:     make(http.Header),
					}, nil
				default:
					return nil, fmt.Errorf("unexpected patch status: %s", *req.Status)
				}
			case r.Method == http.MethodDelete && r.URL.Path == "/api/users/uuid-630":
				deleteCalled = true
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"response":{}}`)),
					Header:     make(http.Header),
				}, nil
			default:
				return nil, fmt.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
			}
		}),
	})
	b.remnawave = client

	b.runSubscriptionSchedulerPass()

	assert.True(t, retryPatchCalled, "scheduler должен попробовать retry активации confirmed_not_activated")
	assert.False(t, disableCalled, "после неуспешного retry scheduler не должен disable-ить уже оплатившего пользователя")
	assert.False(t, deleteCalled, "после неуспешного retry scheduler не должен кикать уже оплатившего пользователя")

	dbUser, err := db.GetUserByTelegramID(userID)
	require.NoError(t, err)
	assert.NotNil(t, dbUser, "пользователь должен остаться в БД после scheduler pass")

	storedPayment, err := db.GetPaymentByID(id)
	require.NoError(t, err)
	require.NotNil(t, storedPayment)
	assert.Equal(t, "confirmed_not_activated", storedPayment.Status, "платёж должен остаться в retry-статусе после неуспешной активации")
}
