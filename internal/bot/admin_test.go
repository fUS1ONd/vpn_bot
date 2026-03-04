package bot

import (
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
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
