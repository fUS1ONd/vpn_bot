package bot

import (
	"encoding/json"
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

	_, err = db.CreateUser(targetID, "target", "Target", "uuid-target")
	require.NoError(t, err)
	inv, err := db.CreateInviteWithExpiry(adminID, nil)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, targetID))
	require.NoError(t, db.MarkNotificationSent(targetID, "expire_3d"))

	client := remnawave.NewClient("https://panel.example.com", "test-token", "")
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
		config:     &config.Config{AdminID: adminID},
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

	_, err = db.CreateUser(modID, "moderator", "Модератор", "uuid-mod")
	require.NoError(t, err)
	require.NoError(t, db.AddModerator(modID, adminID))

	_, err = db.CreateUser(subID, "sub", "Subscriber", "uuid-sub")
	require.NoError(t, err)
	inv, err := db.CreateInviteWithExpiry(modID, intPtrAdmin(30))
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, subID))

	client := remnawave.NewClient("https://panel.example.com", "test-token", "")
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
		config:     &config.Config{AdminID: adminID},
		userStates: newStateMap(),
	}

	ctx := &MockContext{
		sender:  &tele.User{ID: adminID, Username: "admin"},
		message: &tele.Message{},
	}

	err = b.handleAdminModStats(ctx)
	require.NoError(t, err)

	msg, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, msg, "Статистика модераторов")
	assert.Contains(t, msg, "@moderator")
	assert.Contains(t, msg, "Активных: 1")
}

func TestHandleAdminPreviewToggle(t *testing.T) {
	b, _ := setupTestBot(t)
	adminID := b.config.AdminID

	ctx := &MockContext{
		sender:  &tele.User{ID: adminID, Username: "admin"},
		message: &tele.Message{Text: BtnAdminPreview(false)},
	}

	err := b.handleTextMessage(ctx)
	require.NoError(t, err)
	assert.True(t, b.isPreviewModeEnabled())

	msg, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, msg, "Preview")
	assert.Contains(t, msg, "включен")

	opts := getSendOptions(t, ctx)
	buttons := collectButtons(opts.ReplyMarkup.ReplyKeyboard)
	assert.Contains(t, buttons, BtnAdminPreview(true))
}

func TestHandleAdminPreviewToggleRejectsNonAdmin(t *testing.T) {
	b, _ := setupTestBot(t)

	ctx := &MockContext{
		sender:  &tele.User{ID: 4242, Username: "user"},
		message: &tele.Message{Text: BtnAdminPreview(false)},
	}

	err := b.handleAdminPreviewToggle(ctx)
	require.NoError(t, err)
	assert.False(t, b.isPreviewModeEnabled())
	assert.Nil(t, ctx.sentMsg)
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

	_, err = db.CreateUser(targetID, "target", "Target", "uuid-target")
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
		_, err := db.CreateUser(otherID, "banned", "Banned", "uuid-banned")
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

	_, err = db.CreateUser(modID, "moderator", "Moderator", "uuid-mod")
	require.NoError(t, err)
	_, err = db.CreateUser(targetID, "target", "Target", "uuid-target")
	require.NoError(t, err)

	days := 30
	invite, err := db.CreateInviteWithExpiry(modID, &days)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(invite.Code, targetID))
	require.NoError(t, db.MarkNotificationSent(targetID, "expire_3d"))

	var gotEnable bool
	var gotPatch bool
	var patchReq remnawave.UpdateUserRequest

	client := remnawave.NewClient("https://panel.example.com", "test-token", "")
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
			case r.Method == http.MethodPost && r.URL.Path == "/api/users/uuid-target/actions/enable":
				gotEnable = true
				payload := `{"response":{"success":true}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			case r.Method == http.MethodPatch && r.URL.Path == "/api/users":
				gotPatch = true
				require.NoError(t, json.NewDecoder(r.Body).Decode(&patchReq))
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
		config:     &config.Config{AdminID: adminID},
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

	require.True(t, gotEnable)
	require.True(t, gotPatch)
	require.Equal(t, "uuid-target", patchReq.UUID)
	require.NotNil(t, patchReq.ExpireAt)
	assert.Equal(t, "2099-01-01T00:00:00Z", *patchReq.ExpireAt)

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

func intPtrAdmin(v int) *int {
	return &v
}
