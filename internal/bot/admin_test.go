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

func intPtrAdmin(v int) *int {
	return &v
}
