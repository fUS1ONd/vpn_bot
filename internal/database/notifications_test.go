package database

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotificationsLifecycle(t *testing.T) {
	dbFile := "test_notifications.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	sent, err := db.WasNotificationSent(123, "expire_3d")
	require.NoError(t, err)
	assert.False(t, sent)

	require.NoError(t, db.MarkNotificationSent(123, "expire_3d"))

	sent, err = db.WasNotificationSent(123, "expire_3d")
	require.NoError(t, err)
	assert.True(t, sent)

	// Повторная запись не должна падать
	require.NoError(t, db.MarkNotificationSent(123, "expire_3d"))

	require.NoError(t, db.ClearNotifications(123))
	sent, err = db.WasNotificationSent(123, "expire_3d")
	require.NoError(t, err)
	assert.False(t, sent)
}
