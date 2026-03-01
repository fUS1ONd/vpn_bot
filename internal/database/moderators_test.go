package database

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestDB создаёт временную БД для тестов
func setupTestDB(t *testing.T) *DB {
	t.Helper()
	dbFile := "test_moderators.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbFile)
	})
	return db
}

func TestModeratorsTable(t *testing.T) {
	db := setupTestDB(t)

	// Таблица moderators должна существовать после миграции
	var tableName string
	err := db.conn.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='moderators'`,
	).Scan(&tableName)
	assert.NoError(t, err)
	assert.Equal(t, "moderators", tableName)
}

func TestAddModerator(t *testing.T) {
	db := setupTestDB(t)

	// Создаём пользователя (модератор должен быть зарегистрированным пользователем)
	_, err := db.CreateUser(100, "testmod", "Тест", "uuid-mod-1")
	require.NoError(t, err)

	t.Run("Успешное назначение", func(t *testing.T) {
		err := db.AddModerator(100, 999)
		assert.NoError(t, err)
	})

	t.Run("Повторное назначение — ошибка", func(t *testing.T) {
		err := db.AddModerator(100, 999)
		assert.Error(t, err)
	})

	t.Run("Незарегистрированный пользователь — ошибка", func(t *testing.T) {
		err := db.AddModerator(777, 999)
		assert.Error(t, err)
	})
}

func TestIsModerator(t *testing.T) {
	db := setupTestDB(t)

	_, err := db.CreateUser(200, "user200", "Юзер", "uuid-200")
	require.NoError(t, err)

	t.Run("Не модератор", func(t *testing.T) {
		ok, err := db.IsModerator(200)
		assert.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("Модератор", func(t *testing.T) {
		err := db.AddModerator(200, 999)
		require.NoError(t, err)

		ok, err := db.IsModerator(200)
		assert.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("Несуществующий пользователь", func(t *testing.T) {
		ok, err := db.IsModerator(99999)
		assert.NoError(t, err)
		assert.False(t, ok)
	})
}

func TestGetModerator(t *testing.T) {
	db := setupTestDB(t)

	_, err := db.CreateUser(300, "mod300", "Мод", "uuid-300")
	require.NoError(t, err)

	t.Run("Не найден", func(t *testing.T) {
		mod, err := db.GetModerator(300)
		assert.NoError(t, err)
		assert.Nil(t, mod)
	})

	t.Run("Найден", func(t *testing.T) {
		err := db.AddModerator(300, 999)
		require.NoError(t, err)

		mod, err := db.GetModerator(300)
		assert.NoError(t, err)
		require.NotNil(t, mod)
		assert.Equal(t, int64(300), mod.TelegramID)
		assert.Equal(t, int64(999), mod.AddedBy)
	})
}

func TestGetAllModerators(t *testing.T) {
	db := setupTestDB(t)

	t.Run("Пустой список", func(t *testing.T) {
		mods, err := db.GetAllModerators()
		assert.NoError(t, err)
		assert.Empty(t, mods)
	})

	t.Run("Несколько модераторов", func(t *testing.T) {
		_, err := db.CreateUser(400, "mod1", "Первый", "uuid-400")
		require.NoError(t, err)
		_, err = db.CreateUser(401, "mod2", "Второй", "uuid-401")
		require.NoError(t, err)

		err = db.AddModerator(400, 999)
		require.NoError(t, err)
		err = db.AddModerator(401, 999)
		require.NoError(t, err)

		mods, err := db.GetAllModerators()
		assert.NoError(t, err)
		assert.Len(t, mods, 2)
	})
}

func TestRemoveModerator(t *testing.T) {
	db := setupTestDB(t)

	_, err := db.CreateUser(500, "mod500", "Мод", "uuid-500")
	require.NoError(t, err)
	err = db.AddModerator(500, 999)
	require.NoError(t, err)

	t.Run("Успешное снятие", func(t *testing.T) {
		err := db.RemoveModerator(500)
		assert.NoError(t, err)

		ok, err := db.IsModerator(500)
		assert.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("Снятие несуществующего — ошибка", func(t *testing.T) {
		err := db.RemoveModerator(99999)
		assert.Error(t, err)
	})
}
