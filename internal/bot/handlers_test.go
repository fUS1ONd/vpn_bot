package bot

import (
	"os"
	"testing"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
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
}
