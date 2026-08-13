package database

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// strPtrTest и i64PtrTest — короткие конструкторы указателей для тестов.
func strPtrTest(s string) *string { return &s }
func i64PtrTest(n int64) *int64   { return &n }

// На 3.x у пользователя нет UUID вовсе. Колонка UNIQUE допускает сколько угодно
// NULL, но не две пустые строки, — значит писать надо NULL, иначе регистрация
// на 3.x сработала бы ровно один раз.
func TestCreateUserWithoutUUIDTwice(t *testing.T) {
	dbFile := "test_users_no_uuid_twice.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	first, err := db.CreateUser(3001, "alice", "Alice", nil, i64PtrTest(11), nil, nil)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.Nil(t, first.RemnawaveUUID)
	require.Equal(t, int64(11), *first.RemnawaveID)

	second, err := db.CreateUser(3002, "bob", "Bob", nil, i64PtrTest(12), nil, nil)
	require.NoError(t, err, "вторая регистрация без UUID тоже должна пройти")
	require.NotNil(t, second)
	require.Nil(t, second.RemnawaveUUID)
}

// Чтение строки с NULL в remnawave_uuid не должно падать на Scan.
func TestReadUserWithNullUUID(t *testing.T) {
	dbFile := "test_users_null_uuid_read.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	_, err = db.CreateUser(3010, "carol", "Carol", nil, i64PtrTest(21), nil, nil)
	require.NoError(t, err)

	user, err := db.GetUserByTelegramID(3010)
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.Nil(t, user.RemnawaveUUID)
	require.NotNil(t, user.RemnawaveID)
	assert.Equal(t, int64(21), *user.RemnawaveID)

	users, err := db.GetAllUsers()
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Nil(t, users[0].RemnawaveUUID)
	assert.Equal(t, int64(21), *users[0].RemnawaveID)
}

// Доливка связки: id проставляется пользователю, созданному ещё на 2.8.x.
func TestSetRemnawaveIDAndLookup(t *testing.T) {
	dbFile := "test_users_set_remnawave_id.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	_, err = db.CreateUser(3020, "dave", "Dave", strPtrTest("uuid-3020"), nil, nil, nil)
	require.NoError(t, err)

	user, err := db.GetUserByTelegramID(3020)
	require.NoError(t, err)
	require.Nil(t, user.RemnawaveID)

	require.NoError(t, db.SetRemnawaveID(3020, 77))

	user, err = db.GetUserByTelegramID(3020)
	require.NoError(t, err)
	require.NotNil(t, user.RemnawaveID)
	assert.Equal(t, int64(77), *user.RemnawaveID)

	byID, err := db.GetUserByRemnawaveID(77)
	require.NoError(t, err)
	require.NotNil(t, byID)
	assert.Equal(t, int64(3020), byID.TelegramID)

	missing, err := db.GetUserByRemnawaveID(78)
	require.NoError(t, err)
	assert.Nil(t, missing)
}

// Один и тот же id панели нельзя привязать к двум Telegram ID: это рассинхрон,
// который должен быть виден ошибкой, а не тихо перезаписывать связку.
func TestSetRemnawaveIDRejectsDuplicate(t *testing.T) {
	dbFile := "test_users_remnawave_id_unique.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	_, err = db.CreateUser(3030, "eve", "Eve", strPtrTest("uuid-3030"), i64PtrTest(90), nil, nil)
	require.NoError(t, err)
	_, err = db.CreateUser(3031, "frank", "Frank", strPtrTest("uuid-3031"), nil, nil, nil)
	require.NoError(t, err)

	err = db.SetRemnawaveID(3031, 90)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "UNIQUE")
}

// Пользователи без связки — то, что добирает backfill и плановая доливка.
func TestUsersMissingRemnawaveID(t *testing.T) {
	dbFile := "test_users_missing_remnawave_id.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	_, err = db.CreateUser(3040, "with", "With", strPtrTest("uuid-3040"), i64PtrTest(101), nil, nil)
	require.NoError(t, err)
	_, err = db.CreateUser(3041, "without", "Without", strPtrTest("uuid-3041"), nil, nil, nil)
	require.NoError(t, err)

	users, err := db.UsersMissingRemnawaveID()
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, int64(3041), users[0].TelegramID)
}
