package bot

import (
	"os"
	"strconv"
	"testing"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
)

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
		userStates: NewUserStates(),
	}

	ctx := &MockContext{sender: &tele.User{ID: adminID}}
	err = b.processBanUser(ctx, strconv.FormatInt(adminID, 10))
	assert.NoError(t, err)

	// Должно быть сообщение об ошибке, а не бан
	msg, ok := ctx.sentMsg.(string)
	assert.True(t, ok)
	assert.Contains(t, msg, "себя")
}
