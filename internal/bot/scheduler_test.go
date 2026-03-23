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
	dbFile := fmt.Sprintf("test_scheduler_%s.db", t.Name())
	db, err := database.New(dbFile)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbFile)
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

// TestSchedulerMaintenanceMode проверяет, что в maintenance mode кики и disable не выполняются
func TestSchedulerMaintenanceMode(t *testing.T) {
	b, db := setupSchedulerTestBot(t)
	b.maintenanceMode = true

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
