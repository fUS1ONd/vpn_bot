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

	cfg := &config.Config{
		AdminID:              adminID,
		MinSubscriptionPrice: 400,
	}
	b := &Bot{
		db:         db,
		config:     cfg,
		userStates: newStateMap(),
		remnawave:  remnawave.NewClient("https://panel.example.com", "test-token", nil),
	}

	// Создаём пользователя-модератора
	_, err = db.CreateUser(modID, "moderator", "Модератор", "uuid-mod", nil, nil)
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
	kb := UserMenuKeyboardDynamic(BtnRenew, true, true)
	assert.NotNil(t, kb)
}

func TestBotUserKeyboardForModeratorContainsInfoButton(t *testing.T) {
	b, _, _, modID := setupModeratorTestBot(t)

	kb := b.userKeyboard(modID)
	require.NotNil(t, kb)

	var buttons []string
	for _, row := range kb.ReplyKeyboard {
		for _, btn := range row {
			buttons = append(buttons, btn.Text)
		}
	}

	assert.Contains(t, buttons, BtnInfo)
	assert.Contains(t, buttons, BtnModInvites)
}

func TestAdminModeratorKeyboard(t *testing.T) {
	// Клавиатура управления модераторами
	kb := AdminModeratorKeyboard()
	assert.NotNil(t, kb)
}

// --- Тесты обработчиков модератора ---

func TestModeratorCreateInvite_StartsPriceFlow(t *testing.T) {
	b, _, _, modID := setupModeratorTestBot(t)

	user := &tele.User{ID: modID, Username: "moderator"}
	ctx := &MockContext{
		sender:  user,
		message: &tele.Message{},
	}

	err := b.handleModeratorCreateInvite(ctx)
	assert.NoError(t, err)
	assert.Equal(t, StateWaitModInvitePrice, b.userStates.Get(modID))

	sentStr, ok := ctx.sentMsg.(string)
	assert.True(t, ok)
	assert.Contains(t, sentStr, "Введите цену подписки")
	assert.Contains(t, sentStr, "400")
}

func TestProcessModeratorInvitePrice_CreatesInviteWithPrice(t *testing.T) {
	b, db, _, modID := setupModeratorTestBot(t)

	ctx := &MockContext{
		sender:  &tele.User{ID: modID, Username: "moderator"},
		message: &tele.Message{Text: "500"},
	}

	err := b.processModeratorInvitePrice(ctx, "500")
	require.NoError(t, err)

	invites, err := db.GetInvitesWithUsersByCreator(modID)
	require.NoError(t, err)
	require.Len(t, invites, 1)

	invite, err := db.GetInviteByCode(invites[0].Code)
	require.NoError(t, err)
	require.NotNil(t, invite)
	require.NotNil(t, invite.ExpireDays)
	require.NotNil(t, invite.SubscriptionPrice)
	assert.Equal(t, 30, *invite.ExpireDays)
	assert.Equal(t, 500, *invite.SubscriptionPrice)
	assert.Empty(t, b.userStates.Get(modID))

	sentStr, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, sentStr, "Цена подписки: 500 руб/мес")
}

func TestProcessModeratorInvitePrice_RejectsTooLowPrice(t *testing.T) {
	b, db, _, modID := setupModeratorTestBot(t)
	b.userStates.Set(modID, StateWaitModInvitePrice)

	ctx := &MockContext{
		sender:  &tele.User{ID: modID, Username: "moderator"},
		message: &tele.Message{Text: "399"},
	}

	err := b.processModeratorInvitePrice(ctx, "399")
	require.NoError(t, err)

	invites, err := db.GetInvitesWithUsersByCreator(modID)
	require.NoError(t, err)
	assert.Empty(t, invites)
	assert.Equal(t, StateWaitModInvitePrice, b.userStates.Get(modID))

	sentStr, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, sentStr, "Минимальная цена: 400 руб")
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
	assert.Contains(t, sentStr, "<code>501</code>")
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

	priceTrial := 400
	pricePaid := 500
	priceGrace := 450

	// Триальный подписчик
	_, err := db.CreateUser(300, "trial", "Trial", "uuid-300", &priceTrial, &modID)
	require.NoError(t, err)
	code1, err := db.CreateInviteWithPrice(modID, 30, priceTrial)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(code1, 300))

	// Оплаченный подписчик
	_, err = db.CreateUser(301, "paid", "Paid", "uuid-301", &pricePaid, &modID)
	require.NoError(t, err)
	code2, err := db.CreateInviteWithPrice(modID, 30, pricePaid)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(code2, 301))

	paymentPaid := &database.Payment{
		TelegramID:    301,
		ModeratorID:   &modID,
		Amount:        pricePaid,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	paymentPaidID, err := db.CreatePayment(paymentPaid)
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(paymentPaidID))

	// Grace period
	_, err = db.CreateUser(302, "grace", "Grace", "uuid-302", &priceGrace, &modID)
	require.NoError(t, err)
	code3, err := db.CreateInviteWithPrice(modID, 30, priceGrace)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(code3, 302))

	paymentGrace := &database.Payment{
		TelegramID:    302,
		ModeratorID:   &modID,
		Amount:        priceGrace,
		PaymentMethod: "card",
		Status:        "pending",
	}
	paymentGraceID, err := db.CreatePayment(paymentGrace)
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(paymentGraceID))

	// Удалённый подписчик
	_, err = db.CreateUser(303, "gone", "Gone", "uuid-303", &priceTrial, &modID)
	require.NoError(t, err)
	code4, err := db.CreateInviteWithPrice(modID, 30, priceTrial)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(code4, 303))
	require.NoError(t, db.DeleteUser(303))

	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			// batch-запрос вместо поштучных
			if r.Method == http.MethodGet && r.URL.Path == "/api/users" {
				payload, err := json.Marshal(map[string]any{
					"response": map[string]any{
						"users": []map[string]any{
							{
								"uuid":      "uuid-300",
								"username":  "trial",
								"status":    remnawave.StatusActive,
								"expireAt":  time.Now().UTC().AddDate(0, 0, 10).Format(time.RFC3339),
								"createdAt": time.Now().UTC().Format(time.RFC3339),
							},
							{
								"uuid":      "uuid-301",
								"username":  "paid",
								"status":    remnawave.StatusActive,
								"expireAt":  time.Now().UTC().AddDate(0, 0, 25).Format(time.RFC3339),
								"createdAt": time.Now().UTC().Format(time.RFC3339),
							},
							{
								"uuid":      "uuid-302",
								"username":  "grace",
								"status":    remnawave.StatusDisabled,
								"expireAt":  time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339),
								"createdAt": time.Now().UTC().Format(time.RFC3339),
							},
						},
						"total": 3,
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
	assert.Contains(t, sentStr, "триал")
	assert.Contains(t, sentStr, "оплачено")
	assert.Contains(t, sentStr, "grace")
	assert.Contains(t, sentStr, "цена: 400 руб/мес")
	assert.Contains(t, sentStr, "цена: 500 руб/мес")
	assert.Contains(t, sentStr, "цена: 450 руб/мес")
	assert.Contains(t, sentStr, "удалён")
}

func TestHandleModeratorEarnings(t *testing.T) {
	b, db, _, modID := setupModeratorTestBot(t)
	price := 500

	_, err := db.CreateUser(300, "paid", "Paid", "uuid-300", &price, &modID)
	require.NoError(t, err)
	code, err := db.CreateInviteWithPrice(modID, 30, price)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(code, 300))

	payment := &database.Payment{
		TelegramID:    300,
		ModeratorID:   &modID,
		Amount:        price,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	paymentID, err := db.CreatePayment(payment)
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(paymentID))

	_, err = db.CreateEarning(&database.ModeratorEarning{
		PaymentID:     paymentID,
		ModeratorID:   modID,
		GrossAmount:   500,
		PlategaFee:    55,
		WithdrawalFee: 8,
		NetAmount:     437,
		SharePercent:  15,
		ShareAmount:   65,
	})
	require.NoError(t, err)

	b.remnawave.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet && r.URL.Path == "/api/users" && r.URL.Query().Get("size") == "1000" {
				// Дата окончания должна быть в будущем, иначе пользователь
				// учитывается как истёкший, а не как платящий.
				expireAt := time.Now().UTC().AddDate(0, 0, 10).Format(time.RFC3339)
				payload := fmt.Sprintf(`{"response":{"users":[{"uuid":"uuid-300","telegramId":300,"username":"paid","status":"ACTIVE","expireAt":%q}],"total":1}}`, expireAt)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			}
			return nil, assert.AnError
		}),
	})

	ctx := &MockContext{
		sender:  &tele.User{ID: modID, Username: "moderator"},
		message: &tele.Message{},
	}

	err = b.handleModeratorEarnings(ctx)
	require.NoError(t, err)

	sentStr, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, sentStr, "Мой заработок")
	assert.Contains(t, sentStr, "Финансы за")
	assert.Contains(t, sentStr, "За всё время")
	assert.Contains(t, sentStr, "Текущее состояние подписчиков")
	assert.Contains(t, sentStr, "💳 Платящих: 1")
	assert.NotContains(t, sentStr, "Платящих клиентов")
	assert.Contains(t, sentStr, "500 руб")
	assert.Contains(t, sentStr, "65 руб")
}

func intPtrBot(v int) *int {
	return &v
}

// --- Тесты роутинга модератора ---

func TestHandleTextMessage_ModeratorButtons(t *testing.T) {
	b, _, _, modID := setupModeratorTestBot(t)

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

	t.Run("Кнопка_Назад_из_экрана_подписчиков", func(t *testing.T) {
		b.userStates.Set(modID, StateModSubscribers)
		ctx := &MockContext{
			sender:  user,
			message: &tele.Message{Text: BtnBack},
		}
		err := b.handleTextMessage(ctx)
		assert.NoError(t, err)

		sentStr, ok := ctx.sentMsg.(string)
		assert.True(t, ok)
		assert.Contains(t, sentStr, "Приглашения")
		assert.Empty(t, b.userStates.Get(modID))
	})

	t.Run("Кнопка_Создать_запрашивает_цену", func(t *testing.T) {
		ctx := &MockContext{
			sender:  user,
			message: &tele.Message{Text: BtnModCreate},
		}
		err := b.handleTextMessage(ctx)
		assert.NoError(t, err)
		assert.Equal(t, StateWaitModInvitePrice, b.userStates.Get(modID))
		b.userStates.Delete(modID)
	})

	t.Run("Кнопка_Заработок_открывает_сводку", func(t *testing.T) {
		b.remnawave.SetHTTPClient(&http.Client{
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

		ctx := &MockContext{
			sender:  user,
			message: &tele.Message{Text: BtnModEarnings},
		}
		err := b.handleTextMessage(ctx)
		assert.NoError(t, err)
		sentStr, ok := ctx.sentMsg.(string)
		assert.True(t, ok)
		assert.Contains(t, sentStr, "Мой заработок")
	})
}

// --- Тесты админ-панели модераторов ---

func TestAdminAddModerator(t *testing.T) {
	b, db, adminID, _ := setupModeratorTestBot(t)

	// Создаём нового пользователя для назначения
	_, err := db.CreateUser(200, "newmod", "Новый", "uuid-200", nil, nil)
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

	_, err := db.CreateUser(201, "monthly_user", "Месячный", "uuid-201", nil, nil)
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

func TestProcessModChangePrice_UpdatesTrialSubscriber(t *testing.T) {
	b, db, _, modID := setupModeratorTestBot(t)
	price := 400
	_, err := db.CreateUser(9001, "trial", "Trial", "uuid-9001", &price, &modID)
	require.NoError(t, err)
	code, err := db.CreateInviteWithPrice(modID, 30, price)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(code, 9001))

	b.remnawave.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet && r.URL.Path == "/api/users/uuid-9001" {
				payload := `{"response":{"uuid":"uuid-9001","username":"trial","status":"ACTIVE","expireAt":"2026-04-20T00:00:00Z"}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			}
			return nil, assert.AnError
		}),
	})

	ctxID := &MockContext{sender: &tele.User{ID: modID}, message: &tele.Message{Text: "9001"}}
	require.NoError(t, b.processModChangePriceID(ctxID, "9001"))
	require.Equal(t, StateWaitModChangePriceValue, b.userStates.Get(modID))

	ctxValue := &MockContext{sender: &tele.User{ID: modID}, message: &tele.Message{Text: "550"}}
	require.NoError(t, b.processModChangePriceValue(ctxValue, "550"))

	user, err := db.GetUserByTelegramID(9001)
	require.NoError(t, err)
	require.NotNil(t, user)
	require.NotNil(t, user.SubscriptionPrice)
	assert.Equal(t, 550, *user.SubscriptionPrice)
	assert.Empty(t, b.userStates.Get(modID))

	msg, ok := ctxValue.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, msg, "400 → 550")
}

// --- Тест batch API для handleModSubscribers ---

// TestHandleModSubscribers_UsesBatchAPI проверяет, что handleModSubscribers использует
// один batch-запрос к Remnawave вместо N отдельных GetUser запросов.
func TestHandleModSubscribers_UsesBatchAPI(t *testing.T) {
	b, db, _, modID := setupModeratorTestBot(t)

	// Создаём двух подписчиков
	_, err := db.CreateUser(310, "alice", "Alice", "uuid-310", nil, nil)
	require.NoError(t, err)
	inv1, err := db.CreateInviteWithExpiry(modID, intPtrBot(30))
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv1.Code, 310))

	_, err = db.CreateUser(311, "bob", "Bob", "uuid-311", nil, nil)
	require.NoError(t, err)
	inv2, err := db.CreateInviteWithExpiry(modID, intPtrBot(30))
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv2.Code, 311))

	// Считаем запросы к API
	apiCallPaths := []string{}
	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
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

func TestProcessModChangePriceID_RejectsPaidSubscriber(t *testing.T) {
	b, db, _, modID := setupModeratorTestBot(t)
	price := 500
	_, err := db.CreateUser(400, "paid", "Paid", "uuid-400", &price, &modID)
	require.NoError(t, err)
	code, err := db.CreateInviteWithPrice(modID, 30, price)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(code, 400))

	payment := &database.Payment{
		TelegramID:    400,
		ModeratorID:   &modID,
		Amount:        price,
		PaymentMethod: "sbp",
		Status:        "pending",
	}
	paymentID, err := db.CreatePayment(payment)
	require.NoError(t, err)
	require.NoError(t, db.ConfirmPayment(paymentID))

	ctx := &MockContext{
		sender:  &tele.User{ID: modID},
		message: &tele.Message{Text: "400"},
	}
	require.NoError(t, b.processModChangePriceID(ctx, "400"))

	msg, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, msg, "уже оплатил")
	assert.Empty(t, b.userStates.Get(modID))
}

func TestProcessModChangePriceID_RejectsGraceSubscriber(t *testing.T) {
	b, db, _, modID := setupModeratorTestBot(t)
	price := 500
	_, err := db.CreateUser(401, "grace", "Grace", "uuid-401", &price, &modID)
	require.NoError(t, err)
	code, err := db.CreateInviteWithPrice(modID, 30, price)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(code, 401))

	b.remnawave.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet && r.URL.Path == "/api/users/uuid-401" {
				payload := `{"response":{"uuid":"uuid-401","username":"grace","status":"DISABLED","expireAt":"2026-03-01T00:00:00Z"}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			}
			return nil, assert.AnError
		}),
	})

	ctx := &MockContext{
		sender:  &tele.User{ID: modID},
		message: &tele.Message{Text: "401"},
	}
	require.NoError(t, b.processModChangePriceID(ctx, "401"))

	msg, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, msg, "уже не на пробном периоде")
	assert.Empty(t, b.userStates.Get(modID))
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

	// Модератор должен получить пользовательское меню с кнопкой приглашений.
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
