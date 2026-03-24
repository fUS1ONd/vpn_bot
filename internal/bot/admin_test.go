package bot

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
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

func TestHandleCreateInvite_AdminInviteIsUnlimited(t *testing.T) {
	dbFile := "test_admin_invite_expiry.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	adminID := int64(999999)
	b := &Bot{
		db:         db,
		config:     &config.Config{AdminID: adminID},
		userStates: newStateMap(),
	}

	ctx := &MockContext{
		sender:  &tele.User{ID: adminID, Username: "admin"},
		message: &tele.Message{},
	}

	err = b.handleCreateInvite(ctx)
	require.NoError(t, err)

	invites, err := db.GetAllInvites()
	require.NoError(t, err)
	require.Len(t, invites, 1)
	assert.Nil(t, invites[0].ExpireDays)
}

// TestFormatInvitesListChunking проверяет разбиение длинного списка инвайтов на части
func TestFormatInvitesListChunking(t *testing.T) {
	// Создаём много инвайтов чтобы превысить лимит Telegram (4096 символов)
	var invites []database.InviteWithUser
	for i := 0; i < 100; i++ {
		inv := database.InviteWithUser{
			Code:      "abcdef" + strconv.Itoa(i),
			CreatedBy: 999,
			CreatedAt: time.Now(),
		}
		invites = append(invites, inv)
	}

	chunks := FormatInvitesListChunked(invites, 4000)
	assert.Greater(t, len(chunks), 1, "Длинный список должен быть разбит на несколько частей")

	for _, chunk := range chunks {
		assert.LessOrEqual(t, len(chunk), 4000+200, "Каждая часть не должна сильно превышать лимит")
	}
}

// TestProcessBanUserRejectsSelfBan проверяет, что админ не может забанить самого себя
func TestProcessBanUserRejectsSelfBan(t *testing.T) {
	dbFile := "test_admin_selfban.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	adminID := int64(999999)
	b := &Bot{
		db:         db,
		config:     &config.Config{AdminID: adminID},
		userStates: newStateMap(),
	}

	ctx := &MockContext{sender: &tele.User{ID: adminID}}
	err = b.processBanUser(ctx, strconv.FormatInt(adminID, 10))
	assert.NoError(t, err)

	// Должно быть сообщение об ошибке, а не бан
	msg, ok := ctx.sentMsg.(string)
	assert.True(t, ok)
	assert.Contains(t, msg, "себя")
}

func TestProcessBanUser_PersistsBanAndKeepsInviteHistory(t *testing.T) {
	dbFile := "test_admin_ban_flow.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	adminID := int64(999999)
	targetID := int64(12345)

	_, err = db.CreateUser(targetID, "target", "Target", "uuid-target", nil, nil)
	require.NoError(t, err)
	inv, err := db.CreateInviteWithExpiry(adminID, nil)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, targetID))
	require.NoError(t, db.MarkNotificationSent(targetID, "expire_3d"))

	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodDelete && r.URL.Path == "/api/users/uuid-target" {
				payload, err := json.Marshal(map[string]any{"response": map[string]any{"success": true}})
				require.NoError(t, err)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(string(payload))),
					Header:     make(http.Header),
				}, nil
			}
			return nil, assert.AnError
		}),
	})

	b := &Bot{
		db:         db,
		remnawave:  client,
		config:     &config.Config{AdminID: adminID, PlategaFeeCard: 10, PlategaFeeWithdrawal: 2},
		userStates: newStateMap(),
	}

	ctx := &MockContext{sender: &tele.User{ID: adminID}}
	err = b.processBanUser(ctx, strconv.FormatInt(targetID, 10))
	require.NoError(t, err)

	isBanned, err := db.IsBanned(targetID)
	require.NoError(t, err)
	assert.True(t, isBanned)

	user, err := db.GetUserByTelegramID(targetID)
	require.NoError(t, err)
	assert.Nil(t, user)

	// История инвайта должна сохраниться.
	storedInvite, err := db.GetInviteByCode(inv.Code)
	require.NoError(t, err)
	require.NotNil(t, storedInvite)
	require.NotNil(t, storedInvite.UsedBy)
	assert.Equal(t, targetID, *storedInvite.UsedBy)

	sent, err := db.WasNotificationSent(targetID, "expire_3d")
	require.NoError(t, err)
	assert.False(t, sent)
}

func TestHandleAdminModStats(t *testing.T) {
	dbFile := "test_admin_mod_stats.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	adminID := int64(999999)
	modID := int64(100)
	subID := int64(200)

	_, err = db.CreateUser(modID, "moderator", "Модератор", "uuid-mod", nil, nil)
	require.NoError(t, err)
	require.NoError(t, db.AddModerator(modID, adminID))

	_, err = db.CreateUser(subID, "sub", "Subscriber", "uuid-sub", nil, nil)
	require.NoError(t, err)
	inv, err := db.CreateInviteWithExpiry(modID, intPtrAdmin(30))
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, subID))

	paymentID, err := db.CreatePayment(&database.Payment{
		TelegramID:    subID,
		ModeratorID:   &modID,
		Amount:        500,
		PaymentMethod: "card",
		Status:        "pending",
	})
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(paymentID))

	_, err = db.CreateEarning(&database.ModeratorEarning{
		PaymentID:     paymentID,
		ModeratorID:   modID,
		GrossAmount:   500,
		PlategaFee:    50,
		WithdrawalFee: 10,
		NetAmount:     440,
		SharePercent:  15,
		ShareAmount:   66,
	})
	require.NoError(t, err)

	rawDB, err := sql.Open("sqlite3", dbFile)
	require.NoError(t, err)
	defer rawDB.Close()

	prevMonth := time.Now().UTC().AddDate(0, -1, 0)
	_, err = rawDB.Exec(
		`UPDATE payments SET confirmed_at = ? WHERE id = ?`,
		time.Date(prevMonth.Year(), prevMonth.Month(), 15, 12, 0, 0, 0, time.UTC),
		paymentID,
	)
	require.NoError(t, err)

	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet && r.URL.Path == "/api/users" {
				payload, err := json.Marshal(map[string]any{
					"response": map[string]any{
						"users": []map[string]any{
							{
								"uuid":       "uuid-sub",
								"telegramId": subID,
								"status":     remnawave.StatusActive,
								"expireAt":   time.Now().UTC().AddDate(0, 0, 15).Format(time.RFC3339),
							},
						},
						"total": 1,
					},
				})
				require.NoError(t, err)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(string(payload))),
					Header:     make(http.Header),
				}, nil
			}
			return nil, assert.AnError
		}),
	})

	b := &Bot{
		db:         db,
		remnawave:  client,
		config:     &config.Config{AdminID: adminID, PlategaFeeCard: 10, PlategaFeeWithdrawal: 2},
		userStates: newStateMap(),
	}

	ctx := &MockContext{
		sender:  &tele.User{ID: adminID, Username: "admin"},
		message: &tele.Message{},
	}

	err = b.handleAdminModStats(ctx)
	require.NoError(t, err)

	require.Len(t, ctx.sentMsgs, 2)

	msg, ok := ctx.sentMsgs[0].(string)
	require.True(t, ok)
	assert.Contains(t, msg, "Статистика:")
	assert.Contains(t, msg, "@moderator")
	assert.Contains(t, msg, "Финансы за")
	assert.Contains(t, msg, "За всё время")
	assert.Contains(t, msg, "Текущее состояние клиентов")
	assert.Contains(t, msg, "Платящих: 1")
	assert.Contains(t, msg, "Платежи: 500 руб")
	assert.Contains(t, msg, "Доля модератора (15%)")

	summary, ok := ctx.sentMsgs[1].(string)
	require.True(t, ok)
	assert.Contains(t, summary, "Итого")
	assert.Contains(t, summary, "66 руб")
}

// TestFormatAdminSwitchTargetLabel_HTMLEscaping проверяет экранирование HTML в имени пользователя
func TestFormatAdminSwitchTargetLabel_HTMLEscaping(t *testing.T) {
	t.Run("имя с HTML-тегами экранируется", func(t *testing.T) {
		user := &database.User{TelegramID: 123, FirstName: "<b>Alex</b>"}
		result := formatAdminSwitchTargetLabel(user)
		assert.NotContains(t, result, "<b>Alex</b>")
		assert.Contains(t, result, "&lt;b&gt;Alex&lt;/b&gt;")
	})

	t.Run("имя с амперсандом экранируется", func(t *testing.T) {
		user := &database.User{TelegramID: 123, FirstName: "Tom & Jerry"}
		result := formatAdminSwitchTargetLabel(user)
		assert.NotContains(t, result, "Tom & Jerry")
		assert.Contains(t, result, "Tom &amp; Jerry")
	})

	t.Run("имя и username — оба экранируются корректно", func(t *testing.T) {
		user := &database.User{TelegramID: 123, FirstName: "Tom & Jerry", Username: "tom"}
		result := formatAdminSwitchTargetLabel(user)
		assert.Contains(t, result, "Tom &amp; Jerry")
		assert.Contains(t, result, "@tom")
	})
}

func TestProcessSwitchSubscriptionID_ValidationErrors(t *testing.T) {
	dbFile := "test_admin_switch_validation.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	adminID := int64(999999)
	targetID := int64(12345)

	_, err = db.CreateUser(targetID, "target", "Target", "uuid-target", nil, nil)
	require.NoError(t, err)

	b := &Bot{
		db:         db,
		config:     &config.Config{AdminID: adminID},
		userStates: newStateMap(),
	}

	t.Run("уже бессрочный", func(t *testing.T) {
		invite, err := db.CreateInviteWithExpiry(adminID, nil)
		require.NoError(t, err)
		require.NoError(t, db.ClaimInvite(invite.Code, targetID))

		ctx := &MockContext{sender: &tele.User{ID: adminID}}
		err = b.processSwitchSubscriptionID(ctx, strconv.FormatInt(targetID, 10))
		require.NoError(t, err)

		msg, ok := ctx.sentMsg.(string)
		require.True(t, ok)
		assert.Contains(t, msg, "уже на бессрочном тарифе")
		assert.Empty(t, b.userStates.Get(adminID))
	})

	t.Run("забанен", func(t *testing.T) {
		otherID := int64(22334)
		_, err := db.CreateUser(otherID, "banned", "Banned", "uuid-banned", nil, nil)
		require.NoError(t, err)
		days := 30
		invite, err := db.CreateInviteWithExpiry(777, &days)
		require.NoError(t, err)
		require.NoError(t, db.ClaimInvite(invite.Code, otherID))
		require.NoError(t, db.BanUser(otherID, adminID))

		ctx := &MockContext{sender: &tele.User{ID: adminID}}
		err = b.processSwitchSubscriptionID(ctx, strconv.FormatInt(otherID, 10))
		require.NoError(t, err)

		msg, ok := ctx.sentMsg.(string)
		require.True(t, ok)
		assert.Contains(t, msg, "пользователь забанен")
		assert.Empty(t, b.userStates.Get(adminID))
	})
}

func TestProcessSwitchSubscription_ConfirmFlow(t *testing.T) {
	dbFile := "test_admin_switch_confirm.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	adminID := int64(999999)
	modID := int64(100)
	targetID := int64(12345)

	_, err = db.CreateUser(modID, "moderator", "Moderator", "uuid-mod", nil, nil)
	require.NoError(t, err)
	_, err = db.CreateUser(targetID, "target", "Target", "uuid-target", nil, nil)
	require.NoError(t, err)

	days := 30
	invite, err := db.CreateInviteWithExpiry(modID, &days)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(invite.Code, targetID))
	require.NoError(t, db.MarkNotificationSent(targetID, "expire_3d"))

	var patchCount int
	var lastPatchReq remnawave.UpdateUserRequest

	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/users/uuid-target":
				payload := `{"response":{"uuid":"uuid-target","username":"target","status":"EXPIRED","expireAt":"2026-04-15T00:00:00Z"}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			case r.Method == http.MethodPatch && r.URL.Path == "/api/users":
				patchCount++
				require.NoError(t, json.NewDecoder(r.Body).Decode(&lastPatchReq))
				payload := `{"response":{"uuid":"uuid-target"}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			default:
				return nil, assert.AnError
			}
		}),
	})

	b := &Bot{
		db:         db,
		remnawave:  client,
		config:     &config.Config{AdminID: adminID, PlategaFeeCard: 10, PlategaFeeWithdrawal: 2},
		userStates: newStateMap(),
	}

	ctxID := &MockContext{sender: &tele.User{ID: adminID}}
	err = b.processSwitchSubscriptionID(ctxID, strconv.FormatInt(targetID, 10))
	require.NoError(t, err)

	msgID, ok := ctxID.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, msgID, "Перевод на бессрочный тариф")
	assert.Contains(t, msgID, "@target")
	assert.Contains(t, msgID, "@moderator")
	assert.Contains(t, msgID, "15.04.2026")
	require.Equal(t, StateWaitSwitchSubscriptionConfirm, b.userStates.Get(adminID))

	ctxConfirm := &MockContext{sender: &tele.User{ID: adminID}}
	err = b.processSwitchSubscriptionConfirm(ctxConfirm, BtnConfirmYes)
	require.NoError(t, err)

	// EnableUser (PATCH) + UpdateUser (PATCH) = 2 вызова
	require.Equal(t, 2, patchCount)
	require.Equal(t, "uuid-target", lastPatchReq.UUID)
	require.NotNil(t, lastPatchReq.ExpireAt)
	assert.Equal(t, "2099-01-01T00:00:00Z", *lastPatchReq.ExpireAt)

	gotInvite, err := db.GetInviteByUsedBy(targetID)
	require.NoError(t, err)
	require.NotNil(t, gotInvite)
	assert.Nil(t, gotInvite.ExpireDays)

	sent, err := db.WasNotificationSent(targetID, "expire_3d")
	require.NoError(t, err)
	assert.False(t, sent)

	msgConfirm, ok := ctxConfirm.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, msgConfirm, "переведён на бессрочный тариф")
	assert.Empty(t, b.userStates.Get(adminID))
}

func TestHandleAdminStats_ShowsFinanceAndConversion(t *testing.T) {
	dbFile := "test_admin_stats.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	adminID := int64(999999)
	modID := int64(100)
	payingID := int64(200)
	trialID := int64(201)
	graceID := int64(202)
	infiniteID := int64(203)

	_, err = db.CreateUser(modID, "moderator", "Модератор", "uuid-mod", nil, nil)
	require.NoError(t, err)
	require.NoError(t, db.AddModerator(modID, adminID))

	price := 500
	_, err = db.CreateUser(payingID, "paid", "Paid", "uuid-paid", &price, &modID)
	require.NoError(t, err)
	_, err = db.CreateUser(trialID, "trial", "Trial", "uuid-trial", &price, &modID)
	require.NoError(t, err)
	_, err = db.CreateUser(graceID, "grace", "Grace", "uuid-grace", &price, &modID)
	require.NoError(t, err)
	_, err = db.CreateUser(infiniteID, "inf", "Infinite", "uuid-inf", nil, nil)
	require.NoError(t, err)

	finiteInvite, err := db.CreateInviteWithExpiry(modID, intPtrAdmin(30))
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(finiteInvite.Code, payingID))

	finiteInvite2, err := db.CreateInviteWithExpiry(modID, intPtrAdmin(30))
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(finiteInvite2.Code, trialID))

	finiteInvite3, err := db.CreateInviteWithExpiry(modID, intPtrAdmin(30))
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(finiteInvite3.Code, graceID))

	infiniteInvite, err := db.CreateInviteWithExpiry(adminID, nil)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(infiniteInvite.Code, infiniteID))

	paymentID, err := db.CreatePayment(&database.Payment{
		TelegramID:    payingID,
		ModeratorID:   &modID,
		Amount:        500,
		PaymentMethod: "card",
		Status:        "pending",
	})
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(paymentID))

	_, err = db.CreateEarning(&database.ModeratorEarning{
		PaymentID:     paymentID,
		ModeratorID:   modID,
		GrossAmount:   500,
		PlategaFee:    50,
		WithdrawalFee: 10,
		NetAmount:     440,
		SharePercent:  15,
		ShareAmount:   66,
	})
	require.NoError(t, err)

	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet && r.URL.Path == "/api/users" {
				payload, err := json.Marshal(map[string]any{
					"response": map[string]any{
						"users": []map[string]any{
							{
								"uuid":       "uuid-paid",
								"telegramId": payingID,
								"status":     remnawave.StatusActive,
								"expireAt":   time.Now().UTC().AddDate(0, 0, 20).Format(time.RFC3339),
							},
							{
								"uuid":       "uuid-trial",
								"telegramId": trialID,
								"status":     remnawave.StatusActive,
								"expireAt":   time.Now().UTC().AddDate(0, 0, 2).Format(time.RFC3339),
							},
							{
								"uuid":       "uuid-grace",
								"telegramId": graceID,
								"status":     remnawave.StatusDisabled,
								"expireAt":   time.Now().UTC().Add(-12 * time.Hour).Format(time.RFC3339),
							},
							{
								"uuid":       "uuid-inf",
								"telegramId": infiniteID,
								"status":     remnawave.StatusActive,
								"expireAt":   time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
							},
						},
						"total": 4,
					},
				})
				require.NoError(t, err)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(string(payload))),
					Header:     make(http.Header),
				}, nil
			}
			return nil, assert.AnError
		}),
	})

	b := &Bot{
		db:         db,
		remnawave:  client,
		config:     &config.Config{AdminID: adminID, PlategaFeeCard: 10, PlategaFeeWithdrawal: 2},
		userStates: newStateMap(),
	}

	ctx := &MockContext{sender: &tele.User{ID: adminID}}
	err = b.handleAdminStats(ctx)
	require.NoError(t, err)

	msg, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, msg, "Общая статистика")
	assert.Contains(t, msg, "Финансы за")
	assert.Contains(t, msg, "Воронка за")
	assert.Contains(t, msg, "Текущее состояние пользователей")
	assert.Contains(t, msg, "Платежей за месяц: 1")
	assert.Contains(t, msg, "Сумма платежей (грязная): 500 руб")
	// Комиссии считаются через calculateMonthlyPaymentFinance (целочисленное деление):
	// 500 card(10%): platega=50, afterPlatega=450, withdrawal=450*2/100=9, net=441, share=66 (из earnings), owner=375
	assert.Contains(t, msg, "Комиссии Platega: -50 руб")
	assert.Contains(t, msg, "Комиссия вывода (2%): -9 руб")
	assert.Contains(t, msg, "Чистый доход: 441 руб")
	assert.Contains(t, msg, "Выплаты модераторам: -66 руб")
	assert.Contains(t, msg, "Доход владельца: 375 руб")
	assert.Contains(t, msg, "Всего в системе: 4")
	assert.Contains(t, msg, "💳 Платящих: 1")
	assert.Contains(t, msg, "⏳ Триал: 1")
	assert.Contains(t, msg, "⚠️ Grace period: 1")
	assert.Contains(t, msg, "♾️ Бессрочных: 1")
	assert.Contains(t, msg, "Конверсия триал → оплата: 33%")
	assert.NotContains(t, msg, "👥 <b>Пользователи</b>")
}

func TestHandleAdminStats_IncludesAdminPaymentsAndModeratorPayouts(t *testing.T) {
	dbFile := "test_admin_stats_regression.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	adminID := int64(999999)
	modID := int64(100)
	moderatorPaymentUserID := int64(200)
	adminPaymentUserID := int64(201)
	previousMonthPaymentUserID := int64(202)
	notActivatedPaymentUserID := int64(203)

	_, err = db.CreateUser(modID, "moderator", "Модератор", "uuid-mod", nil, nil)
	require.NoError(t, err)
	require.NoError(t, db.AddModerator(modID, adminID))

	_, err = db.CreateUser(moderatorPaymentUserID, "paid-mod", "Paid Mod", "uuid-paid-mod", nil, &modID)
	require.NoError(t, err)
	_, err = db.CreateUser(adminPaymentUserID, "paid-admin", "Paid Admin", "uuid-paid-admin", nil, nil)
	require.NoError(t, err)
	_, err = db.CreateUser(previousMonthPaymentUserID, "paid-prev", "Paid Prev", "uuid-paid-prev", nil, &modID)
	require.NoError(t, err)
	_, err = db.CreateUser(notActivatedPaymentUserID, "paid-pending", "Paid Pending", "uuid-paid-pending", nil, &modID)
	require.NoError(t, err)

	moderatorPaymentID, err := db.CreatePayment(&database.Payment{
		TelegramID:    moderatorPaymentUserID,
		ModeratorID:   &modID,
		Amount:        500,
		PaymentMethod: "card",
		Status:        "pending",
	})
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(moderatorPaymentID))

	_, err = db.CreateEarning(&database.ModeratorEarning{
		PaymentID:     moderatorPaymentID,
		ModeratorID:   modID,
		GrossAmount:   500,
		PlategaFee:    60,
		WithdrawalFee: 8,
		NetAmount:     432,
		SharePercent:  15,
		ShareAmount:   66,
	})
	require.NoError(t, err)

	adminPaymentID, err := db.CreatePayment(&database.Payment{
		TelegramID:    adminPaymentUserID,
		Amount:        1000,
		PaymentMethod: "sbp",
		Status:        "pending",
	})
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(adminPaymentID))

	previousMonthPaymentID, err := db.CreatePayment(&database.Payment{
		TelegramID:    previousMonthPaymentUserID,
		ModeratorID:   &modID,
		Amount:        700,
		PaymentMethod: "sbp",
		Status:        "pending",
	})
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(previousMonthPaymentID))

	notActivatedPaymentID, err := db.CreatePayment(&database.Payment{
		TelegramID:    notActivatedPaymentUserID,
		ModeratorID:   &modID,
		Amount:        800,
		PaymentMethod: "sbp",
		Status:        "pending",
	})
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(notActivatedPaymentID))

	now := time.Now().UTC()
	previousMonth := time.Date(now.Year(), now.Month(), 1, 12, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	currentMonthConfirmedAt := time.Date(now.Year(), now.Month(), 10, 12, 0, 0, 0, time.UTC)
	_, err = db.Conn().Exec(`UPDATE payments SET confirmed_at = ? WHERE id = ?`, currentMonthConfirmedAt, moderatorPaymentID)
	require.NoError(t, err)
	_, err = db.Conn().Exec(`UPDATE payments SET confirmed_at = ? WHERE id = ?`, currentMonthConfirmedAt, adminPaymentID)
	require.NoError(t, err)
	_, err = db.Conn().Exec(`UPDATE payments SET confirmed_at = ? WHERE id = ?`, previousMonth, previousMonthPaymentID)
	require.NoError(t, err)
	_, err = db.Conn().Exec(`UPDATE payments SET status = 'confirmed_not_activated', confirmed_at = ? WHERE id = ?`, currentMonthConfirmedAt, notActivatedPaymentID)
	require.NoError(t, err)

	_, err = db.CreateEarning(&database.ModeratorEarning{
		PaymentID:     previousMonthPaymentID,
		ModeratorID:   modID,
		GrossAmount:   700,
		PlategaFee:    70,
		WithdrawalFee: 12,
		NetAmount:     618,
		SharePercent:  15,
		ShareAmount:   92,
	})
	require.NoError(t, err)
	_, err = db.CreateEarning(&database.ModeratorEarning{
		PaymentID:     notActivatedPaymentID,
		ModeratorID:   modID,
		GrossAmount:   800,
		PlategaFee:    80,
		WithdrawalFee: 14,
		NetAmount:     706,
		SharePercent:  15,
		ShareAmount:   105,
	})
	require.NoError(t, err)

	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet && r.URL.Path == "/api/users" && r.URL.Query().Get("size") == "1000" {
				payload := `{"response":{"users":[],"total":0}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			}
			return nil, assert.AnError
		}),
	})

	b := &Bot{
		db:         db,
		remnawave:  client,
		config:     &config.Config{AdminID: adminID, PlategaFeeSBP: 10, PlategaFeeCard: 12, PlategaFeeWithdrawal: 2},
		userStates: newStateMap(),
	}

	ctx := &MockContext{
		sender:  &tele.User{ID: adminID, Username: "admin"},
		message: &tele.Message{},
	}

	err = b.handleAdminStats(ctx)
	require.NoError(t, err)

	msg, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	// Финансовая статистика считается по всем confirmed-платежам месяца через GetConfirmedPaymentsByMonth.
	// previousMonthPaymentID подтверждён в прошлом месяце — не входит.
	// В текущем месяце: moderatorPayment (500 card) + adminPayment (1000 sbp) + notActivatedPayment (800 sbp) = 3 платежа.
	// moderatorPayment (500, card 12%): platega=60, withdrawal=8, net=432, share=66
	// adminPayment (1000, sbp 10%): platega=100, withdrawal=18, net=882, share=0 (нет earnings)
	// notActivatedPayment (800, sbp 10%): platega=80, withdrawal=14, net=706, share=105
	// Итого: gross=2300, platega=240, withdrawal=40, net=2020, share=171, owner=1849
	assert.Contains(t, msg, "Платежей за месяц: 3")
	assert.Contains(t, msg, "Сумма платежей (грязная): 2300 руб")
	assert.Contains(t, msg, "Комиссии Platega: -240 руб")
	assert.Contains(t, msg, "Комиссия вывода (2%): -40 руб")
	assert.Contains(t, msg, "Чистый доход: 2020 руб")
	assert.Contains(t, msg, "Выплаты модераторам: -171 руб")
	assert.Contains(t, msg, "Доход владельца: 1849 руб")
}

func TestProcessAdminUserInfo_ShowsFullCard(t *testing.T) {
	dbFile := "test_admin_user_info.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	adminID := int64(999999)
	modID := int64(100)
	targetID := int64(12345)
	price := 500

	_, err = db.CreateUser(modID, "petr", "Пётр", "uuid-mod", nil, nil)
	require.NoError(t, err)
	require.NoError(t, db.AddModerator(modID, adminID))

	_, err = db.CreateUser(targetID, "ivan", "Иван", "uuid-target", &price, &modID)
	require.NoError(t, err)
	invite, err := db.CreateInviteWithExpiry(modID, intPtrAdmin(30))
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(invite.Code, targetID))
	paymentID, err := db.CreatePayment(&database.Payment{
		TelegramID:    targetID,
		ModeratorID:   &modID,
		Amount:        price,
		PaymentMethod: "card",
		Status:        "pending",
	})
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(paymentID))

	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/users/uuid-target":
				payload := `{"response":{"uuid":"uuid-target","username":"ivan","status":"ACTIVE","expireAt":"2026-04-15T00:00:00Z","hwidDeviceLimit":3,"userTraffic":{"usedTrafficBytes":13421772800,"lifetimeUsedTrafficBytes":13421772800}}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			case r.Method == http.MethodGet && r.URL.Path == "/api/hwid/devices/uuid-target":
				payload := `{"response":{"total":2,"devices":[{"hwid":"a"},{"hwid":"b"}]}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			default:
				return nil, assert.AnError
			}
		}),
	})

	b := &Bot{
		db:         db,
		remnawave:  client,
		config:     &config.Config{AdminID: adminID},
		userStates: newStateMap(),
	}

	ctx := &MockContext{sender: &tele.User{ID: adminID}}
	err = b.processAdminUserInfo(ctx, strconv.FormatInt(targetID, 10))
	require.NoError(t, err)

	msg, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, msg, "Информация о пользователе")
	assert.Contains(t, msg, "@ivan")
	assert.Contains(t, msg, "@petr")
	assert.Contains(t, msg, "500 руб/мес")
	assert.Contains(t, msg, "15.04.2026")
	assert.Contains(t, msg, "12.50 GB")
	assert.Contains(t, msg, "2 / 3")
	assert.Contains(t, msg, "💳 Подписка")
	assert.Contains(t, msg, "Статус: Активен")
}

func TestAdminChangePriceFlow_UpdatesPaidUser(t *testing.T) {
	dbFile := "test_admin_change_price.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	adminID := int64(999999)
	targetID := int64(12345)
	oldPrice := 500

	_, err = db.CreateUser(targetID, "paid", "Paid", "uuid-target", &oldPrice, nil)
	require.NoError(t, err)
	invite, err := db.CreateInviteWithExpiry(adminID, intPtrAdmin(30))
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(invite.Code, targetID))

	paymentID, err := db.CreatePayment(&database.Payment{
		TelegramID:    targetID,
		Amount:        oldPrice,
		PaymentMethod: "card",
		Status:        "pending",
	})
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(paymentID))

	b := &Bot{
		db:         db,
		config:     &config.Config{AdminID: adminID, MinSubscriptionPrice: 400},
		userStates: newStateMap(),
	}

	ctxID := &MockContext{sender: &tele.User{ID: adminID}}
	require.NoError(t, b.processAdminChangePriceID(ctxID, strconv.FormatInt(targetID, 10)))
	require.Equal(t, StateWaitAdminChangePriceValue, b.userStates.Get(adminID))

	msgID, ok := ctxID.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, msgID, "Текущая цена")
	assert.Contains(t, msgID, "500 руб/мес")

	ctxValue := &MockContext{sender: &tele.User{ID: adminID}}
	require.NoError(t, b.processAdminChangePriceValue(ctxValue, "650"))

	updatedUser, err := db.GetUserByTelegramID(targetID)
	require.NoError(t, err)
	require.NotNil(t, updatedUser.SubscriptionPrice)
	assert.Equal(t, 650, *updatedUser.SubscriptionPrice)

	updatedInvite, err := db.GetInviteByUsedBy(targetID)
	require.NoError(t, err)
	require.NotNil(t, updatedInvite.SubscriptionPrice)
	assert.Equal(t, 650, *updatedInvite.SubscriptionPrice)

	msgValue, ok := ctxValue.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, msgValue, "изменена: 500 → 650 руб/мес")
	assert.Empty(t, b.userStates.Get(adminID))
}

func TestAdminChangePriceFlow_PromptsForLegacyPaidMigration(t *testing.T) {
	dbFile := "test_admin_change_price_migration.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	adminID := int64(999999)
	modID := int64(54321)
	targetID := int64(12346)

	_, err = db.CreateUser(modID, "moderator", "Moderator", "uuid-mod", nil, nil)
	require.NoError(t, err)
	require.NoError(t, db.AddModerator(modID, adminID))

	_, err = db.CreateUser(targetID, "legacy", "Legacy", "uuid-target", nil, nil)
	require.NoError(t, err)

	expireDays := 30
	inv, err := db.CreateInviteWithExpiry(modID, &expireDays)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, targetID))

	expireAt := time.Date(2026, time.April, 15, 0, 0, 0, 0, time.UTC)
	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/users/uuid-target":
				payload := fmt.Sprintf(
					`{"response":{"uuid":"uuid-target","username":"legacy","status":"ACTIVE","expireAt":"%s"}}`,
					expireAt.Format(time.RFC3339),
				)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			default:
				return nil, fmt.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		}),
	})

	b := &Bot{
		db:         db,
		remnawave:  client,
		config:     &config.Config{AdminID: adminID, MinSubscriptionPrice: 400},
		userStates: newStateMap(),
	}

	ctxID := &MockContext{sender: &tele.User{ID: adminID}}
	require.NoError(t, b.processAdminChangePriceID(ctxID, strconv.FormatInt(targetID, 10)))
	require.Equal(t, StateWaitAdminChangePriceValue, b.userStates.Get(adminID))

	ctxValue := &MockContext{sender: &tele.User{ID: adminID}}
	require.NoError(t, b.processAdminChangePriceValue(ctxValue, "650"))

	msgValue, ok := ctxValue.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, msgValue, "Текущий период уже оплачен вручную")
	assert.Contains(t, msgValue, "15.04.2026")
	assert.Equal(t, StateWaitAdminChangePriceMigrationConfirm, b.userStates.Get(adminID))

	updatedUser, err := db.GetUserByTelegramID(targetID)
	require.NoError(t, err)
	assert.Nil(t, updatedUser.SubscriptionPrice, "цена не должна применяться до ответа на migration-вопрос")

	ctxConfirm := &MockContext{sender: &tele.User{ID: adminID}}
	require.NoError(t, b.processAdminChangePriceMigrationConfirm(ctxConfirm, BtnAdminMigrationPaidYes))

	updatedUser, err = db.GetUserByTelegramID(targetID)
	require.NoError(t, err)
	require.NotNil(t, updatedUser.SubscriptionPrice)
	assert.Equal(t, 650, *updatedUser.SubscriptionPrice)
	assert.True(t, updatedUser.LegacyPaidMigrated)

	updatedInvite, err := db.GetInviteByUsedBy(targetID)
	require.NoError(t, err)
	require.NotNil(t, updatedInvite.SubscriptionPrice)
	assert.Equal(t, 650, *updatedInvite.SubscriptionPrice)

	assert.Empty(t, b.userStates.Get(adminID))
}

func TestAdminChangePriceFlow_FailsClosedWhenMigrationLookupFails(t *testing.T) {
	dbFile := "test_admin_change_price_migration_lookup_fail.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	adminID := int64(999995)
	modID := int64(54325)
	targetID := int64(12350)

	_, err = db.CreateUser(modID, "moderator", "Moderator", "uuid-mod-fail", nil, nil)
	require.NoError(t, err)
	require.NoError(t, db.AddModerator(modID, adminID))

	_, err = db.CreateUser(targetID, "legacy_fail", "Legacy Fail", "uuid-target-fail", nil, nil)
	require.NoError(t, err)

	expireDays := 30
	inv, err := db.CreateInviteWithExpiry(modID, &expireDays)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, targetID))

	b := &Bot{
		db:         db,
		remnawave:  remnawave.NewClient("https://panel.example.com", "test-token", nil),
		config:     &config.Config{AdminID: adminID, MinSubscriptionPrice: 400},
		userStates: newStateMap(),
	}
	b.remnawave.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("network down")
		}),
	})

	ctxID := &MockContext{sender: &tele.User{ID: adminID}}
	err = b.processAdminChangePriceID(ctxID, strconv.FormatInt(targetID, 10))
	require.NoError(t, err)

	msg, ok := ctxID.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, msg, "Ошибка проверки пользователя, попробуйте позже")
	assert.Empty(t, b.userStates.Get(adminID))

	user, err := db.GetUserByTelegramID(targetID)
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.Nil(t, user.SubscriptionPrice)
}

func TestAdminChangePriceFlow_MigrationNoLeavesTrial(t *testing.T) {
	dbFile := "test_admin_change_price_migration_no.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	adminID := int64(999997)
	modID := int64(54323)
	targetID := int64(12348)

	_, err = db.CreateUser(modID, "moderator", "Moderator", "uuid-mod-no", nil, nil)
	require.NoError(t, err)
	require.NoError(t, db.AddModerator(modID, adminID))

	_, err = db.CreateUser(targetID, "legacy_no", "Legacy No", "uuid-target-no", nil, nil)
	require.NoError(t, err)

	expireDays := 30
	inv, err := db.CreateInviteWithExpiry(modID, &expireDays)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, targetID))

	expireAt := time.Date(2026, time.April, 20, 0, 0, 0, 0, time.UTC)
	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/users/uuid-target-no":
				payload := fmt.Sprintf(
					`{"response":{"uuid":"uuid-target-no","username":"legacy_no","status":"ACTIVE","expireAt":"%s"}}`,
					expireAt.Format(time.RFC3339),
				)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			default:
				return nil, fmt.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		}),
	})

	b := &Bot{
		db:         db,
		remnawave:  client,
		config:     &config.Config{AdminID: adminID, MinSubscriptionPrice: 400},
		userStates: newStateMap(),
	}

	ctxID := &MockContext{sender: &tele.User{ID: adminID}}
	require.NoError(t, b.processAdminChangePriceID(ctxID, strconv.FormatInt(targetID, 10)))
	require.Equal(t, StateWaitAdminChangePriceValue, b.userStates.Get(adminID))

	ctxValue := &MockContext{sender: &tele.User{ID: adminID}}
	require.NoError(t, b.processAdminChangePriceValue(ctxValue, "700"))

	msgValue, ok := ctxValue.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, msgValue, "Текущий период уже оплачен вручную")
	require.Equal(t, StateWaitAdminChangePriceMigrationConfirm, b.userStates.Get(adminID))

	ctxConfirm := &MockContext{sender: &tele.User{ID: adminID}}
	require.NoError(t, b.processAdminChangePriceMigrationConfirm(ctxConfirm, BtnAdminMigrationPaidNo))

	updatedUser, err := db.GetUserByTelegramID(targetID)
	require.NoError(t, err)
	require.NotNil(t, updatedUser.SubscriptionPrice)
	assert.Equal(t, 700, *updatedUser.SubscriptionPrice)
	assert.False(t, updatedUser.LegacyPaidMigrated)

	assert.Empty(t, b.userStates.Get(adminID))
}

func TestAdminChangePriceFlow_MigrationCancelClearsSession(t *testing.T) {
	dbFile := "test_admin_change_price_migration_cancel.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	adminID := int64(999996)
	modID := int64(54324)
	targetID := int64(12349)

	_, err = db.CreateUser(modID, "moderator", "Moderator", "uuid-mod-cancel", nil, nil)
	require.NoError(t, err)
	require.NoError(t, db.AddModerator(modID, adminID))

	_, err = db.CreateUser(targetID, "legacy_cancel", "Legacy Cancel", "uuid-target-cancel", nil, nil)
	require.NoError(t, err)

	expireDays := 30
	inv, err := db.CreateInviteWithExpiry(modID, &expireDays)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, targetID))

	expireAt := time.Date(2026, time.April, 25, 0, 0, 0, 0, time.UTC)
	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/users/uuid-target-cancel":
				payload := fmt.Sprintf(
					`{"response":{"uuid":"uuid-target-cancel","username":"legacy_cancel","status":"ACTIVE","expireAt":"%s"}}`,
					expireAt.Format(time.RFC3339),
				)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			default:
				return nil, fmt.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		}),
	})

	b := &Bot{
		db:         db,
		remnawave:  client,
		config:     &config.Config{AdminID: adminID, MinSubscriptionPrice: 400},
		userStates: newStateMap(),
	}

	ctxID := &MockContext{sender: &tele.User{ID: adminID}}
	require.NoError(t, b.processAdminChangePriceID(ctxID, strconv.FormatInt(targetID, 10)))
	require.Equal(t, StateWaitAdminChangePriceValue, b.userStates.Get(adminID))

	ctxValue := &MockContext{sender: &tele.User{ID: adminID}}
	require.NoError(t, b.processAdminChangePriceValue(ctxValue, "710"))
	require.Equal(t, StateWaitAdminChangePriceMigrationConfirm, b.userStates.Get(adminID))

	ctxCancel := &MockContext{sender: &tele.User{ID: adminID}}
	require.NoError(t, b.processAdminChangePriceMigrationConfirm(ctxCancel, BtnCancel))

	updatedUser, err := db.GetUserByTelegramID(targetID)
	require.NoError(t, err)
	assert.Nil(t, updatedUser.SubscriptionPrice)
	assert.Empty(t, b.userStates.Get(adminID))
}

func TestAdminChangePriceFlow_DoesNotPromptForFreshTrial(t *testing.T) {
	dbFile := "test_admin_change_price_fresh_trial.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	adminID := int64(999998)
	modID := int64(54322)
	targetID := int64(12347)
	invitePrice := 500

	_, err = db.CreateUser(modID, "moderator", "Moderator", "uuid-mod-2", nil, nil)
	require.NoError(t, err)
	require.NoError(t, db.AddModerator(modID, adminID))

	_, err = db.CreateUser(targetID, "fresh", "Fresh", "uuid-target-2", &invitePrice, nil)
	require.NoError(t, err)

	expireDays := 30
	inviteCode, err := db.CreateInviteWithPrice(modID, expireDays, invitePrice)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inviteCode, targetID))

	initialUser, err := db.GetUserByTelegramID(targetID)
	require.NoError(t, err)
	require.NotNil(t, initialUser.SubscriptionPrice)
	assert.Equal(t, invitePrice, *initialUser.SubscriptionPrice)

	expireAt := time.Date(2026, time.April, 15, 0, 0, 0, 0, time.UTC)
	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/users/uuid-target-2":
				payload := fmt.Sprintf(
					`{"response":{"uuid":"uuid-target-2","username":"fresh","status":"ACTIVE","expireAt":"%s"}}`,
					expireAt.Format(time.RFC3339),
				)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			default:
				return nil, fmt.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		}),
	})

	b := &Bot{
		db:         db,
		remnawave:  client,
		config:     &config.Config{AdminID: adminID, MinSubscriptionPrice: 400},
		userStates: newStateMap(),
	}

	ctxID := &MockContext{sender: &tele.User{ID: adminID}}
	require.NoError(t, b.processAdminChangePriceID(ctxID, strconv.FormatInt(targetID, 10)))
	require.Equal(t, StateWaitAdminChangePriceValue, b.userStates.Get(adminID))

	ctxValue := &MockContext{sender: &tele.User{ID: adminID}}
	require.NoError(t, b.processAdminChangePriceValue(ctxValue, "650"))

	msgValue, ok := ctxValue.sentMsg.(string)
	require.True(t, ok)
	assert.NotContains(t, msgValue, "Текущий период уже оплачен вручную")
	assert.NotEqual(t, StateWaitAdminChangePriceMigrationConfirm, b.userStates.Get(adminID))

	updatedUser, err := db.GetUserByTelegramID(targetID)
	require.NoError(t, err)
	require.NotNil(t, updatedUser.SubscriptionPrice)
	assert.Equal(t, 650, *updatedUser.SubscriptionPrice)
	assert.False(t, updatedUser.LegacyPaidMigrated)
}

func TestProcessAdminUserInfo_ShowsNonSuccessStatusForGraceUser(t *testing.T) {
	dbFile := "test_admin_user_info_grace.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	adminID := int64(999999)
	targetID := int64(22334)
	price := 500

	_, err = db.CreateUser(targetID, "grace", "Grace", "uuid-grace-user", &price, nil)
	require.NoError(t, err)

	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/users/uuid-grace-user":
				payload := `{"response":{"uuid":"uuid-grace-user","username":"grace","status":"DISABLED","expireAt":"2026-03-01T00:00:00Z","hwidDeviceLimit":2,"userTraffic":{"usedTrafficBytes":1073741824,"lifetimeUsedTrafficBytes":1073741824}}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			case r.Method == http.MethodGet && r.URL.Path == "/api/hwid/devices/uuid-grace-user":
				payload := `{"response":{"total":1,"devices":[{"hwid":"a"}]}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			default:
				return nil, assert.AnError
			}
		}),
	})

	b := &Bot{
		db:         db,
		remnawave:  client,
		config:     &config.Config{AdminID: adminID},
		userStates: newStateMap(),
	}

	ctx := &MockContext{sender: &tele.User{ID: adminID}}
	err = b.processAdminUserInfo(ctx, strconv.FormatInt(targetID, 10))
	require.NoError(t, err)

	msg, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, msg, "Статус: Grace period")
	assert.NotContains(t, msg, "✅ Статус: Grace period")
	assert.Contains(t, msg, "⛔")
}

func intPtrAdmin(v int) *int {
	return &v
}
