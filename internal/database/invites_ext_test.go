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
