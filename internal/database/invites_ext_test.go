package database

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestDBInvites создаёт временную БД для тестов инвайтов
func setupTestDBInvites(t *testing.T) *DB {
	t.Helper()
	dbFile := "test_invites_ext.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbFile)
	})
	return db
}

func TestCreateInviteWithExpiry(t *testing.T) {
	db := setupTestDBInvites(t)

	t.Run("Бессрочный инвайт", func(t *testing.T) {
		inv, err := db.CreateInviteWithExpiry(100, nil)
		require.NoError(t, err)
		require.NotNil(t, inv)
		assert.Nil(t, inv.ExpireDays)
	})

	t.Run("Месячный инвайт", func(t *testing.T) {
		days := 30
		inv, err := db.CreateInviteWithExpiry(100, &days)
		require.NoError(t, err)
		require.NotNil(t, inv)
		require.NotNil(t, inv.ExpireDays)
		assert.Equal(t, 30, *inv.ExpireDays)
	})
}

func TestGetInviteByUsedBy(t *testing.T) {
	db := setupTestDBInvites(t)

	invite, err := db.CreateInvite(100)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(invite.Code, 555))

	got, err := db.GetInviteByUsedBy(555)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, invite.Code, got.Code)
	require.NotNil(t, got.UsedBy)
	assert.Equal(t, int64(555), *got.UsedBy)

	notFound, err := db.GetInviteByUsedBy(777)
	require.NoError(t, err)
	assert.Nil(t, notFound)
}

func TestGetSubscribersByModerator(t *testing.T) {
	db := setupTestDBInvites(t)

	// Подписчик, который остался в users
	_, err := db.CreateUser(300, "alive", "Alive", "uuid-300")
	require.NoError(t, err)
	inv1, err := db.CreateInviteWithExpiry(100, intPtr(30))
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv1.Code, 300))

	// Подписчик, удалённый из users
	_, err = db.CreateUser(301, "gone", "Gone", "uuid-301")
	require.NoError(t, err)
	inv2, err := db.CreateInviteWithExpiry(100, intPtr(30))
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv2.Code, 301))
	require.NoError(t, db.DeleteUser(301))

	subs, err := db.GetSubscribersByModerator(100)
	require.NoError(t, err)
	require.Len(t, subs, 2)

	seen := map[int64]Subscriber{}
	for _, sub := range subs {
		seen[sub.TelegramID] = sub
	}

	alive := seen[300]
	require.NotNil(t, alive.Username)
	require.NotNil(t, alive.FirstName)
	require.NotNil(t, alive.RemnawaveUUID)
	assert.Equal(t, "alive", *alive.Username)

	deleted := seen[301]
	assert.Nil(t, deleted.Username)
	assert.Nil(t, deleted.FirstName)
	assert.Nil(t, deleted.RemnawaveUUID)
}

func intPtr(v int) *int {
	return &v
}

func TestGetInvitesWithUsersByCreator(t *testing.T) {
	db := setupTestDBInvites(t)

	// Создаём двух модераторов
	_, err := db.CreateUser(100, "mod1", "Модератор1", "uuid-100")
	require.NoError(t, err)
	_, err = db.CreateUser(200, "mod2", "Модератор2", "uuid-200")
	require.NoError(t, err)

	// Создаём инвайты от разных авторов
	inv1, err := db.CreateInvite(100)
	require.NoError(t, err)
	inv2, err := db.CreateInvite(100)
	require.NoError(t, err)
	_, err = db.CreateInvite(200)
	require.NoError(t, err)

	// Активируем один инвайт от mod1
	_, err = db.CreateUser(300, "user300", "Юзер", "uuid-300")
	require.NoError(t, err)
	err = db.UseInvite(inv1.Code, 300)
	require.NoError(t, err)

	t.Run("Инвайты конкретного модератора", func(t *testing.T) {
		invites, err := db.GetInvitesWithUsersByCreator(100)
		assert.NoError(t, err)
		assert.Len(t, invites, 2)

		// Проверяем что один использован, другой нет
		usedCount := 0
		for _, inv := range invites {
			if inv.UsedBy != nil {
				usedCount++
				assert.Equal(t, "user300", inv.UserUsername)
			}
		}
		assert.Equal(t, 1, usedCount)
	})

	t.Run("Инвайты другого модератора", func(t *testing.T) {
		invites, err := db.GetInvitesWithUsersByCreator(200)
		assert.NoError(t, err)
		assert.Len(t, invites, 1)
	})

	t.Run("Нет инвайтов", func(t *testing.T) {
		invites, err := db.GetInvitesWithUsersByCreator(999)
		assert.NoError(t, err)
		assert.Empty(t, invites)
	})

	_ = inv2 // используется при создании, но не активирован
}

func TestDeleteUnusedInviteByOwner(t *testing.T) {
	db := setupTestDBInvites(t)

	_, err := db.CreateUser(100, "mod1", "Мод1", "uuid-100")
	require.NoError(t, err)
	_, err = db.CreateUser(200, "mod2", "Мод2", "uuid-200")
	require.NoError(t, err)

	inv1, err := db.CreateInvite(100)
	require.NoError(t, err)
	inv2, err := db.CreateInvite(200)
	require.NoError(t, err)

	t.Run("Удаление своего неиспользованного кода", func(t *testing.T) {
		err := db.DeleteUnusedInviteByOwner(inv1.Code, 100)
		assert.NoError(t, err)

		// Проверяем что код удалён
		inv, err := db.GetInviteByCode(inv1.Code)
		assert.NoError(t, err)
		assert.Nil(t, inv)
	})

	t.Run("Удаление чужого кода — ошибка", func(t *testing.T) {
		err := db.DeleteUnusedInviteByOwner(inv2.Code, 100)
		assert.Error(t, err)
	})

	t.Run("Удаление использованного кода — ошибка", func(t *testing.T) {
		usedInv, err := db.CreateInvite(100)
		require.NoError(t, err)
		_, err = db.CreateUser(300, "user300", "Юзер", "uuid-300")
		require.NoError(t, err)
		err = db.UseInvite(usedInv.Code, 300)
		require.NoError(t, err)

		err = db.DeleteUnusedInviteByOwner(usedInv.Code, 100)
		assert.Error(t, err)
	})

	t.Run("Удаление несуществующего кода — ошибка", func(t *testing.T) {
		err := db.DeleteUnusedInviteByOwner("nonexistent", 100)
		assert.Error(t, err)
	})
}

func TestDeleteUnusedInvitesByCreator(t *testing.T) {
	db := setupTestDBInvites(t)

	_, err := db.CreateUser(100, "mod1", "Мод1", "uuid-100")
	require.NoError(t, err)
	_, err = db.CreateUser(200, "mod2", "Мод2", "uuid-200")
	require.NoError(t, err)

	// Создаём несколько инвайтов от mod1
	inv1, err := db.CreateInvite(100)
	require.NoError(t, err)
	_, err = db.CreateInvite(100) // неиспользованный
	require.NoError(t, err)
	_, err = db.CreateInvite(200) // от другого модератора
	require.NoError(t, err)

	// Активируем один инвайт от mod1
	_, err = db.CreateUser(300, "user300", "Юзер", "uuid-300")
	require.NoError(t, err)
	err = db.UseInvite(inv1.Code, 300)
	require.NoError(t, err)

	t.Run("Удаляет только неиспользованные инвайты конкретного автора", func(t *testing.T) {
		count, err := db.DeleteUnusedInvitesByCreator(100)
		assert.NoError(t, err)
		assert.Equal(t, int64(1), count) // только 1 неиспользованный от mod1

		// Использованный инвайт от mod1 должен остаться
		inv, err := db.GetInviteByCode(inv1.Code)
		assert.NoError(t, err)
		assert.NotNil(t, inv)

		// Инвайт от mod2 не должен быть затронут
		invites, err := db.GetAllInvites()
		assert.NoError(t, err)
		mod2Count := 0
		for _, i := range invites {
			if i.CreatedBy == 200 {
				mod2Count++
			}
		}
		assert.Equal(t, 1, mod2Count)
	})
}
