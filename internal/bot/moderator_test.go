package bot

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
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

	// Проверяем что сообщение содержит deep link
	sentStr, ok := ctx.sentMsg.(string)
	assert.True(t, ok)
	assert.Contains(t, sentStr, "Приглашение в VPN")
	assert.Contains(t, sentStr, "t.me/")
}

func TestModeratorViewInvites(t *testing.T) {
	b, db, _, modID := setupModeratorTestBot(t)

	// Создаём несколько инвайтов от модератора
	_, err := db.CreateInvite(modID)
	require.NoError(t, err)
	_, err = db.CreateInvite(modID)
	require.NoError(t, err)

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
