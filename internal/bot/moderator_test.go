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

// setupModeratorTestBot создаёт бота с модератором для тестов
func setupModeratorTestBot(t *testing.T) (*Bot, *database.DB, int64, int64) {
	t.Helper()
	dbFile := fmt.Sprintf("test_moderator_%s.db", t.Name())
	db, err := database.New(dbFile)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbFile)
	})

	adminID := int64(999999)
	modID := int64(100)

	cfg := &config.Config{AdminID: adminID}
	b := &Bot{
		db:         db,
		config:     cfg,
		userStates: newStateMap(),
	}

	// Создаём пользователя-модератора
	_, err = db.CreateUser(modID, "moderator", "Модератор", "uuid-mod")
	require.NoError(t, err)
	err = db.AddModerator(modID, adminID)
	require.NoError(t, err)

	return b, db, adminID, modID
}

// --- Тесты клавиатур ---

func TestModeratorMenuKeyboard(t *testing.T) {
	// Клавиатура модератора должна существовать
	kb := ModeratorMenuKeyboard()
	assert.NotNil(t, kb)
}

func TestUserMenuKeyboardForModerator(t *testing.T) {
	// Для модератора должна быть кнопка "Приглашения"
	kb := UserMenuKeyboardModerator()
	assert.NotNil(t, kb)
}

func TestAdminModeratorKeyboard(t *testing.T) {
	// Клавиатура управления модераторами
	kb := AdminModeratorKeyboard()
	assert.NotNil(t, kb)
}

// --- Тесты обработчиков модератора ---

func TestModeratorCreateInvite(t *testing.T) {
	b, db, _, modID := setupModeratorTestBot(t)

	user := &tele.User{ID: modID, Username: "moderator"}
	ctx := &MockContext{
		sender:  user,
		message: &tele.Message{},
	}

	err := b.handleModeratorCreateInvite(ctx)
	assert.NoError(t, err)

	// Проверяем что инвайт создан
	invites, err := db.GetInvitesWithUsersByCreator(modID)
	assert.NoError(t, err)
	assert.Len(t, invites, 1)
	invite, err := db.GetInviteByCode(invites[0].Code)
	require.NoError(t, err)
	require.NotNil(t, invite)
	require.NotNil(t, invite.ExpireDays)
	assert.Equal(t, 30, *invite.ExpireDays)

	// Проверяем что сообщение содержит deep link
	sentStr, ok := ctx.sentMsg.(string)
	assert.True(t, ok)
	assert.Contains(t, sentStr, "Приглашение в VPN")
	assert.Contains(t, sentStr, "t.me/")
}

func TestModeratorViewInvites(t *testing.T) {
	b, db, _, modID := setupModeratorTestBot(t)

	// Создаём несколько инвайтов от модератора
	inv1, err := db.CreateInvite(modID)
	require.NoError(t, err)
	_, err = db.CreateInvite(modID)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv1.Code, 501))

	user := &tele.User{ID: modID, Username: "moderator"}
	ctx := &MockContext{
		sender:  user,
		message: &tele.Message{},
	}

	err = b.handleModeratorViewInvites(ctx)
	assert.NoError(t, err)

	sentStr, ok := ctx.sentMsg.(string)
	assert.True(t, ok)
	assert.Contains(t, sentStr, "Мои приглашения")
	assert.Contains(t, sentStr, "ID: <code>501</code>")
}

func TestModeratorViewInvites_Empty(t *testing.T) {
	b, _, _, modID := setupModeratorTestBot(t)

	user := &tele.User{ID: modID, Username: "moderator"}
	ctx := &MockContext{
		sender:  user,
		message: &tele.Message{},
	}

	err := b.handleModeratorViewInvites(ctx)
	assert.NoError(t, err)

	sentStr, ok := ctx.sentMsg.(string)
	assert.True(t, ok)
	assert.Contains(t, sentStr, "нет")
}

func TestModeratorDeleteInvite(t *testing.T) {
	b, db, _, modID := setupModeratorTestBot(t)

	// Создаём инвайт от модератора
	invite, err := db.CreateInvite(modID)
	require.NoError(t, err)

	user := &tele.User{ID: modID, Username: "moderator"}

	// Запрашиваем удаление
	ctx := &MockContext{
		sender:  user,
		message: &tele.Message{},
	}
	err = b.handleModeratorDeleteInviteRequest(ctx)
	assert.NoError(t, err)
	assert.Equal(t, StateWaitModDeleteInvite, b.userStates.Get(modID))

	// Вводим код
	ctx2 := &MockContext{
		sender:  user,
		message: &tele.Message{Text: invite.Code},
	}
	err = b.processModeratorDeleteInvite(ctx2, invite.Code)
	assert.NoError(t, err)

	// Код должен быть удалён
	inv, err := db.GetInviteByCode(invite.Code)
	assert.NoError(t, err)
	assert.Nil(t, inv)
}

func TestModeratorDeleteInvite_NotOwned(t *testing.T) {
	b, db, adminID, modID := setupModeratorTestBot(t)

	// Создаём инвайт от АДМИНА (не от модератора)
	invite, err := db.CreateInvite(adminID)
	require.NoError(t, err)

	user := &tele.User{ID: modID, Username: "moderator"}
	ctx := &MockContext{
		sender:  user,
		message: &tele.Message{Text: invite.Code},
	}

	err = b.processModeratorDeleteInvite(ctx, invite.Code)
	assert.NoError(t, err)

	// Код НЕ должен быть удалён (чужой)
	inv, err := db.GetInviteByCode(invite.Code)
	assert.NoError(t, err)
	assert.NotNil(t, inv)

	// Сообщение должно содержать ошибку
	sentStr, ok := ctx.sentMsg.(string)
	assert.True(t, ok)
	assert.True(t, strings.Contains(sentStr, "не найден") || strings.Contains(sentStr, "не ваш"),
		"Сообщение должно содержать ошибку об отказе: %s", sentStr)
}

func TestHandleModSubscribers(t *testing.T) {
	b, db, _, modID := setupModeratorTestBot(t)

	// Активный подписчик
	_, err := db.CreateUser(300, "alive", "Alive", "uuid-300")
	require.NoError(t, err)
	inv1, err := db.CreateInviteWithExpiry(modID, intPtrBot(30))
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv1.Code, 300))

	// Удалённый подписчик
	_, err = db.CreateUser(301, "gone", "Gone", "uuid-301")
	require.NoError(t, err)
	inv2, err := db.CreateInviteWithExpiry(modID, intPtrBot(30))
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv2.Code, 301))
	require.NoError(t, db.DeleteUser(301))

	client := remnawave.NewClient("https://panel.example.com", "test-token", "")
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			// batch-запрос вместо поштучных
			if r.Method == http.MethodGet && r.URL.Path == "/api/users" {
				payload, err := json.Marshal(map[string]any{
					"response": map[string]any{
						"users": []map[string]any{
							{
								"uuid":      "uuid-300",
								"username":  "alive",
								"status":    remnawave.StatusActive,
								"expireAt":  time.Now().UTC().AddDate(0, 0, 10).Format(time.RFC3339),
								"createdAt": time.Now().UTC().Format(time.RFC3339),
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
			return nil, fmt.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}),
	})
	b.remnawave = client

	ctx := &MockContext{
		sender:  &tele.User{ID: modID, Username: "moderator"},
		message: &tele.Message{},
	}

	err = b.handleModSubscribers(ctx)
	require.NoError(t, err)

	sentStr, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, sentStr, "Мои подписчики")
	assert.Contains(t, sentStr, "ID: <code>300</code>")
	assert.Contains(t, sentStr, "удалён")
}

func TestHandleModExtend_StartsDialog(t *testing.T) {
	b, db, _, modID := setupModeratorTestBot(t)
	_, err := db.CreateUser(300, "alive", "Alive", "uuid-300")
	require.NoError(t, err)
	inv, err := db.CreateInviteWithExpiry(modID, intPtrBot(30))
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, 300))

	ctx := &MockContext{
		sender:  &tele.User{ID: modID, Username: "moderator"},
		message: &tele.Message{},
	}

	err = b.handleModExtend(ctx)
	require.NoError(t, err)
	assert.Equal(t, StateWaitModExtendID, b.userStates.Get(modID))

	sentStr, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, sentStr, "telegram_id")
}

func intPtrBot(v int) *int {
	return &v
}

// --- Тесты роутинга модератора ---

func TestHandleTextMessage_ModeratorButtons(t *testing.T) {
	b, db, _, modID := setupModeratorTestBot(t)

	user := &tele.User{ID: modID, Username: "moderator"}

	t.Run("Кнопка_Приглашения", func(t *testing.T) {
		ctx := &MockContext{
			sender:  user,
			message: &tele.Message{Text: BtnModInvites},
		}
		err := b.handleTextMessage(ctx)
		assert.NoError(t, err)

		// Должно открыться подменю модератора
		sentStr, ok := ctx.sentMsg.(string)
		assert.True(t, ok)
		assert.True(t, len(sentStr) > 0, "Должен быть ответ на кнопку модератора")
	})

	t.Run("Кнопка_В_меню", func(t *testing.T) {
		ctx := &MockContext{
			sender:  user,
			message: &tele.Message{Text: BtnModBack},
		}
		err := b.handleTextMessage(ctx)
		assert.NoError(t, err)
	})

	t.Run("Кнопка_Продлить_ставит_состояние", func(t *testing.T) {
		_, err := db.CreateUser(8080, "sub8080", "Sub", "uuid-8080")
		require.NoError(t, err)
		inv, err := db.CreateInviteWithExpiry(modID, intPtrBot(30))
		require.NoError(t, err)
		require.NoError(t, db.ClaimInvite(inv.Code, 8080))

		ctx := &MockContext{
			sender:  user,
			message: &tele.Message{Text: BtnModExtend},
		}
		err = b.handleTextMessage(ctx)
		assert.NoError(t, err)
		assert.Equal(t, StateWaitModExtendID, b.userStates.Get(modID))
	})
}

// --- Тесты админ-панели модераторов ---

func TestAdminAddModerator(t *testing.T) {
	b, db, adminID, _ := setupModeratorTestBot(t)

	// Создаём нового пользователя для назначения
	_, err := db.CreateUser(200, "newmod", "Новый", "uuid-200")
	require.NoError(t, err)

	admin := &tele.User{ID: adminID, Username: "admin"}

	// Запрашиваем назначение
	ctx := &MockContext{
		sender:  admin,
		message: &tele.Message{},
	}
	err = b.handleAdminAddModeratorRequest(ctx)
	assert.NoError(t, err)
	assert.Equal(t, StateWaitAddModerator, b.userStates.Get(adminID))

	// Вводим telegram_id
	ctx2 := &MockContext{
		sender:  admin,
		message: &tele.Message{Text: "200"},
	}
	err = b.processAddModerator(ctx2, "200")
	assert.NoError(t, err)

	// Проверяем что модератор назначен
	ok, err := db.IsModerator(200)
	assert.NoError(t, err)
	assert.True(t, ok)
}

func TestAdminAddModerator_NotRegistered(t *testing.T) {
	b, _, adminID, _ := setupModeratorTestBot(t)

	admin := &tele.User{ID: adminID, Username: "admin"}
	ctx := &MockContext{
		sender:  admin,
		message: &tele.Message{Text: "99999"},
	}

	err := b.processAddModerator(ctx, "99999")
	assert.NoError(t, err)

	// Должна быть ошибка
	sentStr, ok := ctx.sentMsg.(string)
	assert.True(t, ok)
	assert.Contains(t, sentStr, "не найден")
}

func TestAdminAddModerator_RejectsMonthlyInviteUser(t *testing.T) {
	b, db, adminID, modID := setupModeratorTestBot(t)

	_, err := db.CreateUser(201, "monthly_user", "Месячный", "uuid-201")
	require.NoError(t, err)
	inv, err := db.CreateInviteWithExpiry(modID, intPtrBot(30))
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, 201))

	admin := &tele.User{ID: adminID, Username: "admin"}
	ctx := &MockContext{
		sender:  admin,
		message: &tele.Message{Text: "201"},
	}

	err = b.processAddModerator(ctx, "201")
	require.NoError(t, err)

	msg, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, msg, "месячному инвайту")

	isMod, err := db.IsModerator(201)
	require.NoError(t, err)
	assert.False(t, isMod)
}

func TestAdminRemoveModerator(t *testing.T) {
	b, db, adminID, modID := setupModeratorTestBot(t)

	// Создаём неиспользованные инвайты от модератора
	_, err := db.CreateInvite(modID)
	require.NoError(t, err)
	_, err = db.CreateInvite(modID)
	require.NoError(t, err)

	admin := &tele.User{ID: adminID, Username: "admin"}
	ctx := &MockContext{
		sender:  admin,
		message: &tele.Message{Text: fmt.Sprintf("%d", modID)},
	}

	err = b.processRemoveModerator(ctx, fmt.Sprintf("%d", modID))
	assert.NoError(t, err)

	// Модератор снят
	ok, err := db.IsModerator(modID)
	assert.NoError(t, err)
	assert.False(t, ok)

	// Неиспользованные инвайты модератора удалены
	invites, err := db.GetInvitesWithUsersByCreator(modID)
	assert.NoError(t, err)
	assert.Empty(t, invites)
}

func TestAdminListModerators(t *testing.T) {
	b, _, adminID, _ := setupModeratorTestBot(t)

	admin := &tele.User{ID: adminID, Username: "admin"}
	ctx := &MockContext{
		sender:  admin,
		message: &tele.Message{},
	}

	err := b.handleAdminListModerators(ctx)
	assert.NoError(t, err)

	sentStr, ok := ctx.sentMsg.(string)
	assert.True(t, ok)
	assert.Contains(t, sentStr, "moderator")
}

// --- Тесты каскадных операций ---

func TestBanModerator_CascadeDelete(t *testing.T) {
	b, db, _, modID := setupModeratorTestBot(t)

	// Создаём инвайты от модератора
	_, err := db.CreateInvite(modID)
	require.NoError(t, err)

	// Проверяем что модератор существует
	ok, err := db.IsModerator(modID)
	require.NoError(t, err)
	require.True(t, ok)

	// Каскадное удаление при бане
	b.cascadeDeleteModerator(modID)

	// Модератор должен быть удалён
	ok, err = db.IsModerator(modID)
	assert.NoError(t, err)
	assert.False(t, ok)

	// Неиспользованные инвайты удалены
	invites, err := db.GetInvitesWithUsersByCreator(modID)
	assert.NoError(t, err)
	assert.Empty(t, invites)
}

// --- Тест batch API для handleModSubscribers ---

// TestHandleModSubscribers_UsesBatchAPI проверяет, что handleModSubscribers использует
// один batch-запрос к Remnawave вместо N отдельных GetUser запросов.
func TestHandleModSubscribers_UsesBatchAPI(t *testing.T) {
	b, db, _, modID := setupModeratorTestBot(t)

	// Создаём двух подписчиков
	_, err := db.CreateUser(310, "alice", "Alice", "uuid-310")
	require.NoError(t, err)
	inv1, err := db.CreateInviteWithExpiry(modID, intPtrBot(30))
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv1.Code, 310))

	_, err = db.CreateUser(311, "bob", "Bob", "uuid-311")
	require.NoError(t, err)
	inv2, err := db.CreateInviteWithExpiry(modID, intPtrBot(30))
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv2.Code, 311))

	// Считаем запросы к API
	apiCallPaths := []string{}
	client := remnawave.NewClient("https://panel.example.com", "test-token", "")
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			apiCallPaths = append(apiCallPaths, r.URL.Path)
			if r.URL.Path == "/api/users" {
				// batch-ответ с обоими пользователями
				payload, _ := json.Marshal(map[string]any{
					"response": map[string]any{
						"users": []map[string]any{
							{"uuid": "uuid-310", "username": "alice", "status": remnawave.StatusActive,
								"expireAt":  time.Now().UTC().AddDate(0, 0, 10).Format(time.RFC3339),
								"createdAt": time.Now().UTC().Format(time.RFC3339)},
							{"uuid": "uuid-311", "username": "bob", "status": remnawave.StatusActive,
								"expireAt":  time.Now().UTC().AddDate(0, 0, 5).Format(time.RFC3339),
								"createdAt": time.Now().UTC().Format(time.RFC3339)},
						},
						"total": 2,
					},
				})
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(string(payload))),
					Header:     make(http.Header),
				}, nil
			}
			return nil, fmt.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}),
	})
	b.remnawave = client

	ctx := &MockContext{
		sender:  &tele.User{ID: modID, Username: "moderator"},
		message: &tele.Message{},
	}

	err = b.handleModSubscribers(ctx)
	require.NoError(t, err)

	// Проверяем: должен быть ТОЛЬКО один запрос к /api/users (batch), а не /api/users/<uuid>
	for _, path := range apiCallPaths {
		assert.NotContains(t, path, "uuid-310", "не должно быть отдельного запроса по uuid")
		assert.NotContains(t, path, "uuid-311", "не должно быть отдельного запроса по uuid")
	}
	assert.Len(t, apiCallPaths, 1, "должен быть ровно один batch-запрос")

	sentStr, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, sentStr, "alice")
	assert.Contains(t, sentStr, "bob")
}

// --- Тест утечки состояния при ошибке продления ---

// TestProcessModExtendConfirm_ClearsStateOnExtendError проверяет, что при ошибке
// продления подписки состояние и сессия очищаются, иначе следующее сообщение
// модератора снова попадёт в обработчик подтверждения.
func TestProcessModExtendConfirm_ClearsStateOnExtendError(t *testing.T) {
	b, db, _, modID := setupModeratorTestBot(t)

	// Создаём подписчика
	_, err := db.CreateUser(400, "sub400", "Sub", "uuid-400")
	require.NoError(t, err)
	inv, err := db.CreateInviteWithExpiry(modID, intPtrBot(30))
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, 400))

	// Настраиваем Remnawave — первый вызов (preview) успешен, второй (extend) — ошибка
	callCount := 0
	client := remnawave.NewClient("https://panel.example.com", "test-token", "")
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			callCount++
			if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "uuid-400") {
				payload, _ := json.Marshal(map[string]any{
					"response": map[string]any{
						"uuid":      "uuid-400",
						"username":  "sub400",
						"status":    remnawave.StatusActive,
						"expireAt":  time.Now().UTC().AddDate(0, 0, 10).Format(time.RFC3339),
						"createdAt": time.Now().UTC().Format(time.RFC3339),
					},
				})
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(string(payload))),
					Header:     make(http.Header),
				}, nil
			}
			// PATCH — симулируем ошибку API при продлении
			if r.Method == http.MethodPatch {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(strings.NewReader(`{"error":"internal"}`)),
					Header:     make(http.Header),
				}, nil
			}
			return nil, fmt.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}),
	})
	b.remnawave = client

	// Шаг 1: ввод ID подписчика
	ctxID := &MockContext{
		sender:  &tele.User{ID: modID},
		message: &tele.Message{Text: "400"},
	}
	err = b.processModExtendID(ctxID, "400")
	require.NoError(t, err)
	require.Equal(t, StateWaitModExtendConfirm, b.userStates.Get(modID))

	// Шаг 2: подтверждение — получаем ошибку API
	ctxConfirm := &MockContext{
		sender:  &tele.User{ID: modID},
		message: &tele.Message{Text: "да"},
	}
	err = b.processModExtendConfirm(ctxConfirm, "да")
	require.NoError(t, err)

	// После ошибки продления: состояние и сессия должны быть очищены
	assert.Empty(t, b.userStates.Get(modID), "состояние должно быть очищено после ошибки")
	_, sessionExists := b.getModExtendSession(modID)
	assert.False(t, sessionExists, "сессия должна быть очищена после ошибки")
}

// --- Тесты handleStart с меню модератора ---

func TestHandleStart_ModeratorGetsModeratorMenu(t *testing.T) {
	b, _, _, modID := setupModeratorTestBot(t)

	user := &tele.User{ID: modID, Username: "moderator", FirstName: "Модератор"}
	ctx := &MockContext{
		sender:  user,
		message: &tele.Message{},
	}

	err := b.handleStart(ctx)
	assert.NoError(t, err)

	// Модератор должен получить UserMenuKeyboardModerator (с кнопкой приглашений)
	assert.Equal(t, MsgWelcomeBack, ctx.sentMsg)

	// Проверяем что в opts есть клавиатура с кнопкой приглашений
	require.NotEmpty(t, ctx.opts)
	found := false
	for _, opt := range ctx.opts {
		if sendOpts, ok := opt.(*tele.SendOptions); ok {
			if sendOpts.ReplyMarkup != nil {
				found = true
			}
		}
	}
	assert.True(t, found, "Должна быть клавиатура в ответе")
}
