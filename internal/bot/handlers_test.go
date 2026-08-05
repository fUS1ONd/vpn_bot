package bot

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/platega"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
)

// MockContext реализует интерфейс tele.Context для тестов
type MockContext struct {
	tele.Context
	sender     *tele.User
	message    *tele.Message
	sentMsg    any
	sentMsgs   []any
	opts       []any
	args       []string
	editedMsg  any
	editedOpts []any
	responded  bool
	alertText  string
}

func (c *MockContext) Sender() *tele.User {
	return c.sender
}

func (c *MockContext) Message() *tele.Message {
	return c.message
}

func (c *MockContext) Send(what any, opts ...any) error {
	c.sentMsg = what
	c.sentMsgs = append(c.sentMsgs, what)
	c.opts = opts
	return nil
}

func (c *MockContext) Text() string {
	if c.message != nil {
		return c.message.Text
	}
	return ""
}

// Args возвращает аргументы callback-данных (после Unique), используется в inline-хендлерах.
func (c *MockContext) Args() []string {
	return c.args
}

// Edit имитирует редактирование сообщения по callback-у.
func (c *MockContext) Edit(what any, opts ...any) error {
	c.editedMsg = what
	c.editedOpts = opts
	return nil
}

// Respond имитирует ответ на callback-запрос (закрытие "часиков" в клиенте).
func (c *MockContext) Respond(resp ...*tele.CallbackResponse) error {
	c.responded = true
	return nil
}

// RespondAlert имитирует alert-ответ на callback-запрос.
func (c *MockContext) RespondAlert(text string) error {
	c.responded = true
	c.alertText = text
	return nil
}

// setupTestBot создаёт бота с временной БД для тестов
func setupTestBot(t *testing.T) (*Bot, *database.DB) {
	t.Helper()
	testName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	dbFile := t.TempDir() + "/test_handlers_" + testName + ".db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		_ = os.Remove(dbFile)
	})

	cfg := &config.Config{
		AdminID:             999999,
		TrialTrafficLimitGB: 1,
		PrivacyPolicyURL:    "https://example.com/privacy",
		TermsOfServiceURL:   "https://example.com/terms",
		SupportContact:      "@test_support",
	}
	b := &Bot{
		db:         db,
		config:     cfg,
		userStates: newStateMap(),
		remnawave:  remnawave.NewClient("https://panel.example.com", "test-token", nil),
		shutdownCh: make(chan struct{}),
	}
	return b, db
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
		_, err := db.CreateUser(userID, "olduser", "OldFirstName", "uuid-123", nil, nil)
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
		_, err := db.CreateUser(userID, "existing", "Existing", "uuid-333", nil, nil)
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
}

func TestUserKeyboardHidesPaymentButtonInMaintenanceMode(t *testing.T) {
	b, db := setupTestBot(t)

	userID := int64(777)
	price := 500
	_, err := db.CreateUser(userID, "paid", "Paid", "uuid-paid", &price, nil)
	require.NoError(t, err)

	b.platega = platega.NewClient("merchant", "secret")
	b.remnawave.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet && r.URL.Path == "/api/users/by-telegram-id/777" {
				payload := `{"response":{"uuid":"uuid-paid","username":"paid","status":"ACTIVE","expireAt":"2026-04-15T00:00:00Z"}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			}
			return nil, assert.AnError
		}),
	})

	kb := b.userKeyboard(userID)
	var normalButtons []string
	for _, row := range kb.ReplyKeyboard {
		for _, btn := range row {
			normalButtons = append(normalButtons, btn.Text)
		}
	}
	assert.Contains(t, normalButtons, BtnRenew)

	b.setMaintenanceMode(true)

	kb = b.userKeyboard(userID)
	var maintenanceButtons []string
	for _, row := range kb.ReplyKeyboard {
		for _, btn := range row {
			maintenanceButtons = append(maintenanceButtons, btn.Text)
		}
	}
	assert.NotContains(t, maintenanceButtons, BtnPay)
	assert.NotContains(t, maintenanceButtons, BtnRenew)
}

func TestUserKeyboardHidesPaymentButtonWithoutPlatega(t *testing.T) {
	b, db := setupTestBot(t)

	userID := int64(778)
	price := 500
	_, err := db.CreateUser(userID, "paid", "Paid", "uuid-paid", &price, nil)
	require.NoError(t, err)

	kb := b.userKeyboard(userID)
	var buttons []string
	for _, row := range kb.ReplyKeyboard {
		for _, btn := range row {
			buttons = append(buttons, btn.Text)
		}
	}

	assert.NotContains(t, buttons, BtnPay)
	assert.NotContains(t, buttons, BtnRenew)
}

func TestAdminTestPaymentPriceShowsPaymentButtonAndUsesConfiguredAmount(t *testing.T) {
	b, db := setupTestBot(t)
	adminID := b.config.AdminID
	_, err := db.CreateUser(adminID, "admin", "Admin", "uuid-admin", nil, nil)
	require.NoError(t, err)

	b.config.AdminTestPaymentPrice = 10
	b.platega = platega.NewClient("merchant", "secret")
	b.remnawave.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, assert.AnError
	})})

	kb := b.userKeyboard(adminID)
	var buttons []string
	for _, row := range kb.ReplyKeyboard {
		for _, btn := range row {
			buttons = append(buttons, btn.Text)
		}
	}
	assert.Contains(t, buttons, BtnPay)

	ctx := &MockContext{sender: &tele.User{ID: adminID}, message: &tele.Message{}}
	require.NoError(t, b.handlePayButton(ctx))
	msg, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, msg, "10 руб")
	assert.Equal(t, StateWaitPaymentMethod, b.userStates.Get(adminID))
}

func TestAdminTestPaymentPriceDisabledHidesPaymentButton(t *testing.T) {
	b, db := setupTestBot(t)
	adminID := b.config.AdminID
	_, err := db.CreateUser(adminID, "admin", "Admin", "uuid-admin", nil, nil)
	require.NoError(t, err)
	b.platega = platega.NewClient("merchant", "secret")

	kb := b.userKeyboard(adminID)
	for _, row := range kb.ReplyKeyboard {
		for _, btn := range row {
			assert.NotEqual(t, BtnPay, btn.Text)
			assert.NotEqual(t, BtnRenew, btn.Text)
		}
	}
}

func TestUserKeyboardShowsRenewForLegacyPaidMigratedUser(t *testing.T) {
	b, db := setupTestBot(t)

	modID := int64(781)
	userID := int64(780)
	price := 500
	_, err := db.CreateUser(modID, "moderator", "Moderator", "uuid-mod-781", nil, nil)
	require.NoError(t, err)

	_, err = db.CreateUser(userID, "legacy_paid", "Legacy Paid", "uuid-legacy-paid", &price, &modID)
	require.NoError(t, err)

	expireDays := 30
	invite, err := db.CreateInviteWithExpiry(modID, &expireDays)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(invite.Code, userID))

	require.NoError(t, db.SetLegacyPaidMigrated(userID, true))

	b.platega = platega.NewClient("merchant", "secret")
	b.remnawave.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/users/by-telegram-id/780":
				payload := `{"response":{"uuid":"uuid-legacy-paid","username":"legacy_paid","status":"ACTIVE","expireAt":"2026-04-15T00:00:00Z"}}`
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

	kb := b.userKeyboard(userID)
	var buttons []string
	for _, row := range kb.ReplyKeyboard {
		for _, btn := range row {
			buttons = append(buttons, btn.Text)
		}
	}

	assert.Contains(t, buttons, BtnRenew)
	assert.NotContains(t, buttons, BtnPay)
}

func TestHandleStatusShowsDevices(t *testing.T) {
	b, db := setupTestBot(t)

	userID := int64(779)
	price := 500
	_, err := db.CreateUser(userID, "paid", "Paid", "uuid-devices", &price, nil)
	require.NoError(t, err)

	b.remnawave.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/users/uuid-devices":
				payload := `{"response":{"uuid":"uuid-devices","username":"paid","status":"ACTIVE","expireAt":"2026-04-15T00:00:00Z","subscriptionUrl":"vless://example","hwidDeviceLimit":3,"userTraffic":{"usedTrafficBytes":2147483648}}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			case r.Method == http.MethodGet && r.URL.Path == "/api/hwid/devices/uuid-devices":
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

	ctx := &MockContext{
		sender:  &tele.User{ID: userID},
		message: &tele.Message{},
	}

	err = b.handleStatus(ctx)
	require.NoError(t, err)

	msg, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, msg, "<b>Устройства:</b> 2 / 3")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestProcessInviteCode_UsesExpectedTrialPeriod(t *testing.T) {
	t.Run("Бессрочный инвайт", func(t *testing.T) {
		b, db := setupTestBot(t)

		invite, err := db.CreateInviteWithExpiry(999, nil)
		require.NoError(t, err)

		var captured remnawave.CreateUserRequest
		client := remnawave.NewClient("https://panel.example.com", "test-token", []string{"uuid-1", "uuid-2"})
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
		assert.Equal(t, int64(0), captured.TrafficLimitBytes) // Бессрочный инвайт — безлимит
		assert.Equal(t, []string{"uuid-1", "uuid-2"}, captured.ActiveInternalSquads)
	})

	t.Run("Триальный инвайт всегда создаёт доступ на 72 часа", func(t *testing.T) {
		b, db := setupTestBot(t)

		days := 30
		invite, err := db.CreateInviteWithExpiry(999, &days)
		require.NoError(t, err)

		var captured remnawave.CreateUserRequest
		client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
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
		assert.False(t, gotExpireAt.Before(before.Add(72*time.Hour).Add(-2*time.Second)))
		assert.False(t, gotExpireAt.After(after.Add(72*time.Hour).Add(2*time.Second)))
		// Триальный инвайт получает лимит трафика.
		assert.Equal(t, int64(1*1024*1024*1024), captured.TrafficLimitBytes)
	})
}

func TestProcessInviteCode_SetsFirstTouchWithoutModeratorRuntime(t *testing.T) {
	t.Run("referral-инвайт копирует invited_by и цену", func(t *testing.T) {
		b, db := setupTestBot(t)

		modID := int64(1234)
		_, err := db.CreateUser(modID, "mod", "Mod", "uuid-mod", nil, nil)
		require.NoError(t, err)
		require.NoError(t, db.AddModerator(modID, b.config.AdminID))

		price := 450
		inviteCode, err := db.CreateInviteWithPrice(modID, 30, price)
		require.NoError(t, err)

		client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
		client.SetHTTPClient(&http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				payload, err := json.Marshal(map[string]any{
					"response": map[string]any{
						"uuid":            "uuid-moderator-user",
						"shortUuid":       "short-moderator-user",
						"username":        "trial_user",
						"status":          remnawave.StatusActive,
						"subscriptionUrl": "vless://example",
						"createdAt":       time.Now().UTC().Format(time.RFC3339),
						"expireAt":        time.Now().UTC().Add(72 * time.Hour).Format(time.RFC3339),
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

		ctx := &MockContext{
			sender:  &tele.User{ID: 7101, Username: "trial_user", FirstName: "Trial"},
			message: &tele.Message{},
		}

		err = b.processInviteCode(ctx, inviteCode)
		require.NoError(t, err)

		user, err := db.GetUserByTelegramID(7101)
		require.NoError(t, err)
		require.NotNil(t, user)
		require.NotNil(t, user.SubscriptionPrice)
		assert.Nil(t, user.ModeratorID)
		require.NotNil(t, user.InvitedBy)
		assert.Equal(t, price, *user.SubscriptionPrice)
		assert.Equal(t, modID, *user.InvitedBy)
	})

	t.Run("админский срочный инвайт не заполняет moderator_id", func(t *testing.T) {
		b, db := setupTestBot(t)

		price := 500
		inviteCode, err := db.CreateInviteWithPrice(b.config.AdminID, 30, price)
		require.NoError(t, err)

		client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
		client.SetHTTPClient(&http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				payload, err := json.Marshal(map[string]any{
					"response": map[string]any{
						"uuid":            "uuid-admin-user",
						"shortUuid":       "short-admin-user",
						"username":        "admin_trial_user",
						"status":          remnawave.StatusActive,
						"subscriptionUrl": "vless://example",
						"createdAt":       time.Now().UTC().Format(time.RFC3339),
						"expireAt":        time.Now().UTC().Add(72 * time.Hour).Format(time.RFC3339),
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

		ctx := &MockContext{
			sender:  &tele.User{ID: 7102, Username: "admin_trial_user", FirstName: "Admin Trial"},
			message: &tele.Message{},
		}

		err = b.processInviteCode(ctx, inviteCode)
		require.NoError(t, err)

		user, err := db.GetUserByTelegramID(7102)
		require.NoError(t, err)
		require.NotNil(t, user)
		require.NotNil(t, user.SubscriptionPrice)
		assert.Equal(t, price, *user.SubscriptionPrice)
		assert.Nil(t, user.ModeratorID)
		require.NotNil(t, user.InvitedBy)
		assert.Equal(t, b.config.AdminID, *user.InvitedBy)
	})
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
	assert.Equal(t, BuildInfoMessage(b.config), msg)
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
	assert.Equal(t, BuildInfoMessage(b.config), msg)
}

func TestHandleTextMessage_AdminUserInfoCardCancelReturnsToManage(t *testing.T) {
	b, _ := setupTestBot(t)
	adminID := b.config.AdminID
	b.userStates.Set(adminID, StateAdminUserInfoCard)

	ctx := &MockContext{
		sender:  &tele.User{ID: adminID, Username: "admin"},
		message: &tele.Message{Text: BtnCancel},
	}

	require.NoError(t, b.handleTextMessage(ctx))

	msg, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Equal(t, "Отменено", msg)
	assert.Equal(t, StateNone, b.userStates.Get(adminID))

	require.Len(t, ctx.opts, 1)
	sendOpts, ok := ctx.opts[0].(*tele.SendOptions)
	require.True(t, ok)
	markup := sendOpts.ReplyMarkup
	require.NotNil(t, markup)

	var buttons []string
	for _, row := range markup.ReplyKeyboard {
		for _, btn := range row {
			buttons = append(buttons, btn.Text)
		}
	}
	assert.Contains(t, buttons, BtnAdminUserInfo)
}

func TestHandleTextMessage_AdminUserInfoCardStateClearedOnOtherInput(t *testing.T) {
	b, _ := setupTestBot(t)
	adminID := b.config.AdminID
	b.userStates.Set(adminID, StateAdminUserInfoCard)

	ctx := &MockContext{
		sender:  &tele.User{ID: adminID, Username: "admin"},
		message: &tele.Message{Text: BtnAdminStats},
	}

	require.NoError(t, b.handleTextMessage(ctx))
	assert.Equal(t, StateNone, b.userStates.Get(adminID))
}

func TestHandleTextMessage_PaymentFlowResetsOnMainMenuButtons(t *testing.T) {
	b, _ := setupTestBot(t)
	userID := int64(12345)
	b.userStates.Set(userID, StateWaitPaymentMethod)

	ctx := &MockContext{
		sender:  &tele.User{ID: userID, Username: "payer"},
		message: &tele.Message{Text: BtnInfo},
	}

	err := b.handleTextMessage(ctx)
	require.NoError(t, err)

	msg, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Equal(t, BuildInfoMessage(b.config), msg)
	assert.Equal(t, StateNone, b.userStates.Get(userID), "при выходе в главное меню state оплаты должен сбрасываться")
}
