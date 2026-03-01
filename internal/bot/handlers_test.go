package bot

import (
	"os"
	"testing"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	tele "gopkg.in/telebot.v3"
)

// MockContext реализует интерфейс tele.Context для тестов
type MockContext struct {
	tele.Context
	sender  *tele.User
	sentMsg interface{}
	opts    []interface{}
}

func (c *MockContext) Sender() *tele.User {
	return c.sender
}

func (c *MockContext) Send(what interface{}, opts ...interface{}) error {
	c.sentMsg = what
	c.opts = opts
	return nil
}

func TestHandleStart(t *testing.T) {
	// Подготовка временной БД
	dbFile := "test_handlers.db"
	db, err := database.New(dbFile)
	assert.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	// Подготовка бота
	cfg := &config.Config{AdminID: 999999} // ID админа отличается от тестовых юзеров
	b := &Bot{
		db:         db,
		config:     cfg,
		userStates: newStateMap(),
	}

	t.Run("NewUser", func(t *testing.T) {
		user := &tele.User{ID: 111, Username: "newuser"}
		ctx := &MockContext{sender: user}

		err := b.handleStart(ctx)
		assert.NoError(t, err)

		// Проверяем, что бот запросил инвайт
		assert.Equal(t, MsgWelcomeInvite, ctx.sentMsg)
		// Проверяем, что установлено состояние ожидания
		assert.Equal(t, StateWaitInvite, b.userStates.Get(user.ID))
	})

	t.Run("ExistingUser", func(t *testing.T) {
		// Создаем пользователя в БД
		userID := int64(222)
		_, err := db.CreateUser(userID, "olduser", "OldFirstName", "uuid-123")
		assert.NoError(t, err)

		user := &tele.User{ID: userID, Username: "olduser"}
		ctx := &MockContext{sender: user}

		// Симулируем "зависшее" состояние (например, если юзер был добавлен вручную или произошел сбой)
		b.userStates.Set(userID, StateWaitInvite)

		err = b.handleStart(ctx)
		assert.NoError(t, err)

		// Проверяем, что бот отправил приветствие (доступ к меню) вместо запроса инвайта
		assert.Equal(t, MsgWelcomeBack, ctx.sentMsg)

		// Проверяем, что состояние сброшено
		state := b.userStates.Get(userID)
		assert.Equal(t, "", state, "Состояние должно быть сброшено для существующего пользователя")
	})
}
