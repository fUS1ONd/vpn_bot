package database

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBanUserAndIsBanned(t *testing.T) {
	dbFile := "test_bans.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	banned, err := db.IsBanned(123)
	require.NoError(t, err)
	assert.False(t, banned)

	require.NoError(t, db.BanUser(123, 999))

	banned, err = db.IsBanned(123)
	require.NoError(t, err)
	assert.True(t, banned)

	// Повторный бан не должен падать
	require.NoError(t, db.BanUser(123, 1000))
	banned, err = db.IsBanned(123)
	require.NoError(t, err)
	assert.True(t, banned)
}
