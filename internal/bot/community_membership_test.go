package bot

import (
	"errors"
	"testing"
	"time"

	tele "gopkg.in/telebot.v3"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubChatMember подставляет ответ Telegram о составе Канала и считает вызовы.
func stubChatMember(b *Bot, member *tele.ChatMember, err error) *int {
	calls := 0
	b.chatMemberOf = func(chatID, userID int64) (*tele.ChatMember, error) {
		calls++
		return member, err
	}
	return &calls
}

func TestIsInsideCommunityByStatus(t *testing.T) {
	tests := []struct {
		name   string
		member *tele.ChatMember
		inside bool
	}{
		{"creator", &tele.ChatMember{Role: tele.Creator}, true},
		{"administrator", &tele.ChatMember{Role: tele.Administrator}, true},
		{"member", &tele.ChatMember{Role: tele.Member}, true},
		{"restricted and still in chat", &tele.ChatMember{Role: tele.Restricted, Member: true}, true},
		{"restricted but left", &tele.ChatMember{Role: tele.Restricted}, false},
		{"left", &tele.ChatMember{Role: tele.Left}, false},
		{"kicked", &tele.ChatMember{Role: tele.Kicked}, false},
		{"no answer at all", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.inside, isInsideCommunity(tt.member))
		})
	}
}

// Тот, кто вступил мимо бота (по прямой ссылке владельца или до появления
// гейта), приписки не видит — и больше не будет: живая проверка ставит пометку.
func TestCommunityMentionSkipsUnmarkedMember(t *testing.T) {
	b, db := setupTestBot(t)
	enableCommunity(b)
	makePayingUser(t, b, db, 521, "uuid-mention-outside-bot")
	calls := stubChatMember(b, &tele.ChatMember{Role: tele.Member}, nil)

	assert.Empty(t, b.claimCommunityMention(521))
	assert.Equal(t, 1, *calls)

	member, err := db.IsCommunityMember(521)
	require.NoError(t, err)
	assert.True(t, member, "живая проверка обязана записать участника")

	// Пометка стоит — второй раз Telegram не спрашиваем вовсе.
	require.NoError(t, db.MarkCommunityMentionSent(521, time.Now().UTC().Add(-30*24*time.Hour)))
	assert.Empty(t, b.claimCommunityMention(521))
	assert.Equal(t, 1, *calls)
}

func TestCommunityMentionShownToUserOutsideChannel(t *testing.T) {
	b, db := setupTestBot(t)
	enableCommunity(b)
	makePayingUser(t, b, db, 522, "uuid-mention-left")
	stubChatMember(b, &tele.ChatMember{Role: tele.Left}, nil)

	assert.NotEmpty(t, b.claimCommunityMention(522))

	member, err := db.IsCommunityMember(522)
	require.NoError(t, err)
	assert.False(t, member, "не состоящего в Канале помечать нечем")
}

// Telegram недоступен или бота разжаловали из админов — зовём как раньше:
// молчание оставило бы Канал без новых участников и без единого следа.
func TestCommunityMentionFailsOpenWhenTelegramUnavailable(t *testing.T) {
	b, db := setupTestBot(t)
	enableCommunity(b)
	makePayingUser(t, b, db, 523, "uuid-mention-api-down")
	stubChatMember(b, nil, errors.New("bot is not a member of the supergroup chat"))

	assert.NotEmpty(t, b.claimCommunityMention(523))

	// Кулдаун при этом занят: иначе затяжной сбой приклеил бы приписку к
	// каждому следующему экрану подряд.
	assert.Empty(t, b.claimCommunityMention(523))
}

// Кулдаун занимается до похода в Telegram, поэтому участник Канала стоит ровно
// одного вызова getChatMember в неделю, а не одного на каждое открытие карточки.
func TestCommunityMembershipCheckedOncePerCooldown(t *testing.T) {
	b, db := setupTestBot(t)
	enableCommunity(b)
	makePayingUser(t, b, db, 524, "uuid-mention-one-call")
	calls := stubChatMember(b, &tele.ChatMember{Role: tele.Left}, nil)

	require.NotEmpty(t, b.claimCommunityMention(524))
	assert.Empty(t, b.claimCommunityMention(524))
	assert.Empty(t, b.claimCommunityMention(524))
	assert.Equal(t, 1, *calls)
}

// Шов не настроен (бот собран без telebot — так живут тесты) — считаем состав
// неизвестным и ведём себя как прежде.
func TestCommunityMentionWithoutChatMemberSeam(t *testing.T) {
	b, db := setupTestBot(t)
	enableCommunity(b)
	makePayingUser(t, b, db, 525, "uuid-mention-no-seam")
	require.Nil(t, b.chatMemberOf)

	assert.NotEmpty(t, b.claimCommunityMention(525))
}

// Неплатящего не спрашиваем у Telegram вовсе: право проверяется раньше состава.
func TestCommunityMembershipNotCheckedForNonPaying(t *testing.T) {
	b, db := setupTestBot(t)
	enableCommunity(b)
	_, err := db.CreateUser(526, "trial", "Trial", strPtrTest("uuid-mention-trial-seam"), nil, nil, nil)
	require.NoError(t, err)
	setReferralEligibilityRemote(t, b, "uuid-mention-trial-seam", "ACTIVE", "2098-01-01T00:00:00Z")
	calls := stubChatMember(b, &tele.ChatMember{Role: tele.Left}, nil)

	assert.Empty(t, b.claimCommunityMention(526))
	assert.Equal(t, 0, *calls)
}
