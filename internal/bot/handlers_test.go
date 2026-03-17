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

// MockContext реализует интерфейс tele.Context для тестов
type MockContext struct {
	tele.Context
	sender  *tele.User
	message *tele.Message
	sentMsg any
	opts    []any
}

func (c *MockContext) Sender() *tele.User {
	return c.sender
}

func (c *MockContext) Message() *tele.Message {
	return c.message
}

func (c *MockContext) Send(what any, opts ...any) error {
	c.sentMsg = what
	c.opts = opts
	return nil
}

func (c *MockContext) Text() string {
	if c.message != nil {
		return c.message.Text
	}
	return ""
}

// setupTestBot создаёт бота с временной БД для тестов
func setupTestBot(t *testing.T) (*Bot, *database.DB) {
	t.Helper()
	dbFile := "test_handlers.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbFile)
	})

	cfg := &config.Config{AdminID: 999999}
	b := &Bot{
		db:         db,
		config:     cfg,
		userStates: newStateMap(),
	}
	return b, db
}

func getSendOptions(t *testing.T, ctx *MockContext) *tele.SendOptions {
	t.Helper()
	for _, opt := range ctx.opts {
		sendOptions, ok := opt.(*tele.SendOptions)
		if ok {
			return sendOptions
		}
	}
	t.Fatalf("send options not found")
	return nil
}

func TestHandleStart(t *testing.T) {
	b, db := setupTestBot(t)

	t.Run("NewUser", func(t *testing.T) {
		user := &tele.User{ID: 111, Username: "newuser"}
		ctx := &MockContext{
			sender:  user,
			message: &tele.Message{},
		}

		err := b.handleStart(ctx)
		assert.NoError(t, err)

		// Проверяем, что бот запросил инвайт
		assert.Equal(t, MsgWelcomeInvite, ctx.sentMsg)
		// Проверяем, что установлено состояние ожидания
		assert.Equal(t, StateWaitInvite, b.userStates.Get(user.ID))
	})

	t.Run("ExistingUser", func(t *testing.T) {
		userID := int64(222)
		_, err := db.CreateUser(userID, "olduser", "OldFirstName", "uuid-123")
		assert.NoError(t, err)

		user := &tele.User{ID: userID, Username: "olduser"}
		ctx := &MockContext{
			sender:  user,
			message: &tele.Message{},
		}

		// Симулируем "зависшее" состояние
		b.userStates.Set(userID, StateWaitInvite)

		err = b.handleStart(ctx)
		assert.NoError(t, err)

		// Проверяем приветствие
		assert.Equal(t, MsgWelcomeBack, ctx.sentMsg)

		// Проверяем, что состояние сброшено
		state := b.userStates.Get(userID)
		assert.Equal(t, "", state, "Состояние должно быть сброшено для существующего пользователя")
	})

	t.Run("ExistingUserWithPayload_IgnoresCode", func(t *testing.T) {
		// Существующий пользователь с payload — код игнорируется, не расходуется
		userID := int64(333)
		_, err := db.CreateUser(userID, "existing", "Existing", "uuid-333")
		require.NoError(t, err)

		// Создаём инвайт
		invite, err := db.CreateInvite(999999)
		require.NoError(t, err)

		user := &tele.User{ID: userID, Username: "existing"}
		ctx := &MockContext{
			sender:  user,
			message: &tele.Message{Payload: invite.Code},
		}

		err = b.handleStart(ctx)
		assert.NoError(t, err)

		// Проверяем что показали меню, а не активировали код
		assert.Equal(t, MsgWelcomeBack, ctx.sentMsg)

		// Код НЕ должен быть использован
		inv, err := db.GetInviteByCode(invite.Code)
		assert.NoError(t, err)
		assert.Nil(t, inv.UsedBy, "Код не должен быть использован для существующего пользователя")
	})

	t.Run("NewUserWithPayload_EmptyPayload", func(t *testing.T) {
		// Новый пользователь без payload — стандартное поведение
		user := &tele.User{ID: 444, Username: "newuser2"}
		ctx := &MockContext{
			sender:  user,
			message: &tele.Message{Payload: ""},
		}

		err := b.handleStart(ctx)
		assert.NoError(t, err)

		assert.Equal(t, MsgWelcomeInvite, ctx.sentMsg)
		assert.Equal(t, StateWaitInvite, b.userStates.Get(user.ID))
	})

	t.Run("NewUserWithInvalidPayload", func(t *testing.T) {
		// Новый пользователь с невалидным payload — попытка активации,
		// ошибка "код не найден", ставим StateWaitInvite
		user := &tele.User{ID: 555, Username: "newuser3"}
		ctx := &MockContext{
			sender:  user,
			message: &tele.Message{Payload: "invalidcode123"},
		}

		err := b.handleStart(ctx)
		assert.NoError(t, err)

		// Должно быть сообщение об ошибке (код не найден), а не стандартное приветствие
		sentStr, ok := ctx.sentMsg.(string)
		assert.True(t, ok)
		assert.NotEqual(t, MsgWelcomeInvite, sentStr, "Должна быть ошибка, а не стандартное приветствие")
		assert.Contains(t, sentStr, "не найден", "Сообщение должно содержать ошибку о невалидном коде")

		// Должен быть установлен StateWaitInvite чтобы юзер мог ввести код текстом
		assert.Equal(t, StateWaitInvite, b.userStates.Get(user.ID))
	})

	t.Run("BannedUser", func(t *testing.T) {
		userID := int64(666)
		require.NoError(t, db.BanUser(userID, 999999))

		user := &tele.User{ID: userID, Username: "banned"}
		ctx := &MockContext{
			sender:  user,
			message: &tele.Message{},
		}

		err := b.handleStart(ctx)
		require.NoError(t, err)

		msg, ok := ctx.sentMsg.(string)
		require.True(t, ok)
		assert.Contains(t, msg, "заблокирован")
	})

	t.Run("NewUserWithPreviewEnabled", func(t *testing.T) {
		user := &tele.User{ID: 777, Username: "previewuser"}
		ctx := &MockContext{
			sender:  user,
			message: &tele.Message{},
		}

		b.setPreviewMode(true)

		err := b.handleStart(ctx)
		require.NoError(t, err)

		assert.Equal(t, MsgPreviewWelcome, ctx.sentMsg)
		assert.Equal(t, StateNone, b.userStates.Get(user.ID))

		opts := getSendOptions(t, ctx)
		buttons := collectButtons(opts.ReplyMarkup.ReplyKeyboard)
		assert.Contains(t, buttons, BtnActivateCode)
	})

	t.Run("NewUserWithPreviewEnabledAndPayloadStillActivatesInvite", func(t *testing.T) {
		invite, err := db.CreateInvite(999999)
		require.NoError(t, err)

		client := remnawave.NewClient("https://panel.example.com", "test-token", "")
		client.SetHTTPClient(&http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/api/users", r.URL.Path)

				payload, err := json.Marshal(map[string]any{
					"response": map[string]any{
						"uuid":            "uuid-preview-payload",
						"shortUuid":       "short-preview-payload",
						"username":        "preview_payload",
						"status":          remnawave.StatusActive,
						"subscriptionUrl": "vless://preview",
						"createdAt":       time.Now().UTC().Format(time.RFC3339),
						"expireAt":        "2099-01-01T00:00:00Z",
					},
				})
				require.NoError(t, err)

				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(string(payload))),
					Header:     make(http.Header),
				}, nil
			}),
		})
		b.remnawave = client
		b.setPreviewMode(true)

		user := &tele.User{ID: 778, Username: "preview_payload", FirstName: "Preview"}
		ctx := &MockContext{
			sender:  user,
			message: &tele.Message{Payload: invite.Code},
		}

		err = b.handleStart(ctx)
		require.NoError(t, err)

		assert.Equal(t, fmt.Sprintf(MsgAccountCreated, "vless://preview"), ctx.sentMsg)

		createdUser, err := db.GetUserByTelegramID(user.ID)
		require.NoError(t, err)
		require.NotNil(t, createdUser)
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestProcessInviteCode_UsesInviteExpireDays(t *testing.T) {
	t.Run("Бессрочный инвайт", func(t *testing.T) {
		b, db := setupTestBot(t)

		invite, err := db.CreateInviteWithExpiry(999, nil)
		require.NoError(t, err)

		var captured remnawave.CreateUserRequest
		client := remnawave.NewClient("https://panel.example.com", "test-token", "")
		clientHTTP := &http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/api/users", r.URL.Path)
				require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))

				payload, err := json.Marshal(map[string]any{
					"response": map[string]any{
						"uuid":            "uuid-unlimited",
						"shortUuid":       "short-unlimited",
						"username":        "unlimited_user",
						"status":          remnawave.StatusActive,
						"subscriptionUrl": "vless://example",
						"createdAt":       time.Now().UTC().Format(time.RFC3339),
						"expireAt":        "2099-01-01T00:00:00Z",
					},
				})
				require.NoError(t, err)

				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(string(payload))),
					Header:     make(http.Header),
				}, nil
			}),
		}
		client.SetHTTPClient(clientHTTP)
		b.remnawave = client

		ctx := &MockContext{
			sender:  &tele.User{ID: 7001, Username: "unlimited_user", FirstName: "Unlimited"},
			message: &tele.Message{},
		}

		err = b.processInviteCode(ctx, invite.Code)
		require.NoError(t, err)
		assert.Equal(t, "2099-01-01T00:00:00Z", captured.ExpireAt)
	})

	t.Run("Месячный инвайт", func(t *testing.T) {
		b, db := setupTestBot(t)

		days := 30
		invite, err := db.CreateInviteWithExpiry(999, &days)
		require.NoError(t, err)

		var captured remnawave.CreateUserRequest
		client := remnawave.NewClient("https://panel.example.com", "test-token", "")
		clientHTTP := &http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/api/users", r.URL.Path)
				require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))

				payload, err := json.Marshal(map[string]any{
					"response": map[string]any{
						"uuid":            "uuid-limited",
						"shortUuid":       "short-limited",
						"username":        "limited_user",
						"status":          remnawave.StatusActive,
						"subscriptionUrl": "vless://example",
						"createdAt":       time.Now().UTC().Format(time.RFC3339),
						"expireAt":        time.Now().UTC().AddDate(0, 0, 30).Format(time.RFC3339),
					},
				})
				require.NoError(t, err)

				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(string(payload))),
					Header:     make(http.Header),
				}, nil
			}),
		}
		client.SetHTTPClient(clientHTTP)
		b.remnawave = client

		before := time.Now().UTC()
		ctx := &MockContext{
			sender:  &tele.User{ID: 7002, Username: "limited_user", FirstName: "Limited"},
			message: &tele.Message{},
		}

		err = b.processInviteCode(ctx, invite.Code)
		require.NoError(t, err)
		after := time.Now().UTC()

		gotExpireAt, err := time.Parse(time.RFC3339, captured.ExpireAt)
		require.NoError(t, err)
		assert.False(t, gotExpireAt.Before(before.AddDate(0, 0, 30).Add(-2*time.Second)))
		assert.False(t, gotExpireAt.After(after.AddDate(0, 0, 30).Add(2*time.Second)))
	})
}

func TestHandleInstructionDesktopUsesUnifiedPCMessage(t *testing.T) {
	b, _ := setupTestBot(t)
	ctx := &MockContext{
		sender:  &tele.User{ID: 777, Username: "desktop"},
		message: &tele.Message{},
	}

	err := b.handleInstructionDesktop(ctx)
	require.NoError(t, err)

	msg, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, msg, "<b>Настройка на ПК</b>")
	assert.Contains(t, msg, "https://www.happ.su/main/ru")
	assert.Contains(t, msg, "\"URL подписки\"")
	assert.Contains(t, msg, "\"TUN\"")
	assert.Contains(t, msg, "Сначала активируйте подписку")
}

func TestHandleInstructionDesktopUsesPreviewPlaceholderForGuest(t *testing.T) {
	b, _ := setupTestBot(t)
	b.setPreviewMode(true)

	ctx := &MockContext{
		sender:  &tele.User{ID: 779, Username: "previewdesktop"},
		message: &tele.Message{},
	}

	err := b.handleInstructionDesktop(ctx)
	require.NoError(t, err)

	msg, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, msg, PreviewSubscriptionPlaceholder)
}

func TestHandleInfoSendsHelpMessage(t *testing.T) {
	b, _ := setupTestBot(t)
	ctx := &MockContext{
		sender:  &tele.User{ID: 888, Username: "reader"},
		message: &tele.Message{},
	}

	err := b.handleInfo(ctx)
	require.NoError(t, err)

	msg, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Equal(t, MsgInfo, msg)
}

func TestHandleTextMessage_InfoButtonRoutesToHelpMessage(t *testing.T) {
	b, _ := setupTestBot(t)
	ctx := &MockContext{
		sender:  &tele.User{ID: 999, Username: "reader"},
		message: &tele.Message{Text: BtnInfo},
	}

	err := b.handleTextMessage(ctx)
	require.NoError(t, err)

	msg, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Equal(t, MsgInfo, msg)
}

func TestHandleStatusReturnsPreviewMessageForGuest(t *testing.T) {
	b, _ := setupTestBot(t)
	b.setPreviewMode(true)

	ctx := &MockContext{
		sender:  &tele.User{ID: 1001, Username: "guest"},
		message: &tele.Message{},
	}

	err := b.handleStatus(ctx)
	require.NoError(t, err)

	msg, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Equal(t, MsgPreviewStatus, msg)
}

func TestHandleConnectReturnsPreviewMessageForGuest(t *testing.T) {
	b, _ := setupTestBot(t)
	b.setPreviewMode(true)

	ctx := &MockContext{
		sender:  &tele.User{ID: 1002, Username: "guest"},
		message: &tele.Message{},
	}

	err := b.handleConnect(ctx)
	require.NoError(t, err)

	msg, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Equal(t, MsgPreviewConnect, msg)
}

func TestHandleTextMessage_ActivateCodeButtonEntersInviteState(t *testing.T) {
	b, _ := setupTestBot(t)
	b.setPreviewMode(true)

	ctx := &MockContext{
		sender:  &tele.User{ID: 1003, Username: "guest"},
		message: &tele.Message{Text: BtnActivateCode},
	}

	err := b.handleTextMessage(ctx)
	require.NoError(t, err)

	assert.Equal(t, MsgWelcomeInvite, ctx.sentMsg)
	assert.Equal(t, StateWaitInvite, b.userStates.Get(ctx.sender.ID))
}
