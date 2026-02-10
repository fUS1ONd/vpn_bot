package bot

import (
	"testing"

	"github.com/stretchr/testify/assert"
	tele "gopkg.in/telebot.v3"
)

// renderMockContext — мок tele.Context для тестов resolveMessageAuthor
type renderMockContext struct {
	tele.Context
	sender *tele.User
	msg    *tele.Message
}

func (c *renderMockContext) Sender() *tele.User {
	return c.sender
}

func (c *renderMockContext) Message() *tele.Message {
	return c.msg
}

func TestResolveMessageAuthor(t *testing.T) {
	t.Run("Обычное сообщение — используется отправитель с username", func(t *testing.T) {
		sender := &tele.User{ID: 100, Username: "sender_user", FirstName: "Sender"}
		ctx := &renderMockContext{
			sender: sender,
			msg:    &tele.Message{},
		}

		author, displayName := resolveMessageAuthor(ctx)

		assert.Equal(t, sender, author)
		assert.Equal(t, "@sender_user", displayName)
	})

	t.Run("Обычное сообщение — отправитель без username, используется FirstName", func(t *testing.T) {
		sender := &tele.User{ID: 101, FirstName: "Иван"}
		ctx := &renderMockContext{
			sender: sender,
			msg:    &tele.Message{},
		}

		author, displayName := resolveMessageAuthor(ctx)

		assert.Equal(t, sender, author)
		assert.Equal(t, "Иван", displayName)
	})

	t.Run("Пересланное сообщение — оригинальный отправитель с username", func(t *testing.T) {
		sender := &tele.User{ID: 100, Username: "forwarder"}
		originalSender := &tele.User{ID: 200, Username: "original_author", FirstName: "Original"}
		ctx := &renderMockContext{
			sender: sender,
			msg: &tele.Message{
				OriginalSender: originalSender,
			},
		}

		author, displayName := resolveMessageAuthor(ctx)

		assert.Equal(t, originalSender, author, "Должен вернуть оригинального отправителя, а не переславшего")
		assert.NotEqual(t, sender, author)
		assert.Equal(t, "@original_author", displayName)
	})

	t.Run("Пересланное сообщение — оригинальный отправитель без username", func(t *testing.T) {
		sender := &tele.User{ID: 100, Username: "forwarder"}
		originalSender := &tele.User{ID: 201, FirstName: "Алексей"}
		ctx := &renderMockContext{
			sender: sender,
			msg: &tele.Message{
				OriginalSender: originalSender,
			},
		}

		author, displayName := resolveMessageAuthor(ctx)

		assert.Equal(t, originalSender, author)
		assert.Equal(t, "Алексей", displayName)
	})

	t.Run("Пересланное сообщение — скрытый аккаунт (OriginalSenderName)", func(t *testing.T) {
		sender := &tele.User{ID: 100, Username: "forwarder"}
		ctx := &renderMockContext{
			sender: sender,
			msg: &tele.Message{
				OriginalSenderName: "Скрытый Пользователь",
			},
		}

		author, displayName := resolveMessageAuthor(ctx)

		assert.Nil(t, author, "Для скрытого аккаунта author должен быть nil")
		assert.Equal(t, "Скрытый Пользователь", displayName)
	})

	t.Run("Пересланное сообщение — есть и OriginalSender, и OriginalSenderName (приоритет OriginalSender)", func(t *testing.T) {
		sender := &tele.User{ID: 100, Username: "forwarder"}
		originalSender := &tele.User{ID: 300, Username: "real_user"}
		ctx := &renderMockContext{
			sender: sender,
			msg: &tele.Message{
				OriginalSender:     originalSender,
				OriginalSenderName: "Скрытое Имя",
			},
		}

		author, displayName := resolveMessageAuthor(ctx)

		assert.Equal(t, originalSender, author, "OriginalSender имеет приоритет над OriginalSenderName")
		assert.Equal(t, "@real_user", displayName)
	})

	t.Run("Обычное сообщение — отправитель без username и без FirstName", func(t *testing.T) {
		sender := &tele.User{ID: 102}
		ctx := &renderMockContext{
			sender: sender,
			msg:    &tele.Message{},
		}

		author, displayName := resolveMessageAuthor(ctx)

		assert.Equal(t, sender, author)
		assert.Equal(t, "", displayName)
	})
}
