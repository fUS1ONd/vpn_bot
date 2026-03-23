package database

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserLegacyPaidMigratedPersists(t *testing.T) {
	dbFile := "test_users_legacy_paid_migrated.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	price := 500
	_, err = db.CreateUser(1010, "legacy_paid", "Legacy Paid", "uuid-1010", &price, nil)
	require.NoError(t, err)

	user, err := db.GetUserByTelegramID(1010)
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.False(t, user.LegacyPaidMigrated)

	err = db.SetLegacyPaidMigrated(1010, true)
	require.NoError(t, err)

	user, err = db.GetUserByTelegramID(1010)
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.True(t, user.LegacyPaidMigrated)

	user, err = db.GetUserByRemnawaveUUID("uuid-1010")
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.True(t, user.LegacyPaidMigrated)

	users, err := db.GetAllUsers()
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.True(t, users[0].LegacyPaidMigrated)
}

func TestUpdateSubscriptionPriceAndLegacyPaidMigrated(t *testing.T) {
	dbFile := "test_users_update_price_and_legacy_flag.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	price := 500
	_, err = db.CreateUser(2020, "legacy_paid", "Legacy Paid", "uuid-2020", &price, nil)
	require.NoError(t, err)

	value := true
	require.NoError(t, db.UpdateSubscriptionPriceAndLegacyPaidMigrated(2020, 650, &value))

	user, err := db.GetUserByTelegramID(2020)
	require.NoError(t, err)
	require.NotNil(t, user)
	require.NotNil(t, user.SubscriptionPrice)
	assert.Equal(t, 650, *user.SubscriptionPrice)
	assert.True(t, user.LegacyPaidMigrated)
}
