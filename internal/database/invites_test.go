package database

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClaimInviteAtomicity проверяет, что двойной claim одного инвайта невозможен
func TestClaimInviteAtomicity(t *testing.T) {
	dbFile := "test_claim_invite.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	// Создаём инвайт
	invite, err := db.CreateInvite(999)
	require.NoError(t, err)

	// Первый claim — должен пройти
	err = db.ClaimInvite(invite.Code, 111)
	assert.NoError(t, err)

	// Второй claim того же кода — должен вернуть ошибку
	err = db.ClaimInvite(invite.Code, 222)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found or already used")
}

// TestReconcileOrphanedInvites проверяет откат инвайтов без соответствующего пользователя
func TestReconcileOrphanedInvites(t *testing.T) {
	dbFile := "test_reconcile.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	// Создаём инвайт и claim-им его (симулируем падение после ClaimInvite до CreateUser)
	invite, err := db.CreateInvite(999)
	require.NoError(t, err)
	err = db.ClaimInvite(invite.Code, 111)
	require.NoError(t, err)

	// Пользователя 111 нет в users — инвайт "завис"

	// ReconcileOrphanedInvites должен откатить его
	count, err := db.ReconcileOrphanedInvites()
	assert.NoError(t, err)
	assert.Equal(t, 1, count, "Должен откатить 1 инвайт")

	// Инвайт снова свободен
	inv, err := db.GetInviteByCode(invite.Code)
	require.NoError(t, err)
	assert.Nil(t, inv.UsedBy, "Инвайт должен быть свободен после reconcile")
}

// TestReconcileOrphanedInvites_SkipsValidClaims проверяет что у реальных пользователей инвайты не трогаются
func TestReconcileOrphanedInvites_SkipsValidClaims(t *testing.T) {
	dbFile := "test_reconcile_skip.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	// Создаём реального пользователя
	_, err = db.CreateUser(111, "user111", "User", "uuid-111")
	require.NoError(t, err)

	// Создаём инвайт и claim-им его — пользователь есть в users
	invite, err := db.CreateInvite(999)
	require.NoError(t, err)
	err = db.ClaimInvite(invite.Code, 111)
	require.NoError(t, err)

	// Reconcile не должен трогать этот инвайт
	count, err := db.ReconcileOrphanedInvites()
	assert.NoError(t, err)
	assert.Equal(t, 0, count, "Валидный инвайт не должен откатываться")

	inv, err := db.GetInviteByCode(invite.Code)
	require.NoError(t, err)
	assert.NotNil(t, inv.UsedBy, "Инвайт должен остаться использованным")
}

// TestReconcileOrphanedInvites_SkipsBannedUserInvites проверяет, что инвайты забаненных
// пользователей не откатываются (claimed давно — не в-процессе регистрации)
func TestReconcileOrphanedInvites_SkipsBannedUserInvites(t *testing.T) {
	dbFile := "test_reconcile_banned.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	// Создаём пользователя, claim-им его инвайт, потом "баним" (удаляем из users)
	_, err = db.CreateUser(111, "user111", "User", "uuid-111")
	require.NoError(t, err)

	invite, err := db.CreateInvite(999)
	require.NoError(t, err)
	err = db.ClaimInvite(invite.Code, 111)
	require.NoError(t, err)

	// Имитируем бан — удаляем пользователя из users
	err = db.DeleteUser(111)
	require.NoError(t, err)

	// Состариваем used_at — симулируем что инвайт был claimed давно (> 1 часа назад)
	_, err = db.Conn().Exec(`UPDATE invites SET used_at = datetime('now', '-2 hours') WHERE code = ?`, invite.Code)
	require.NoError(t, err)

	// Reconcile НЕ должен трогать этот инвайт — он был claimed давно (не в-процессе)
	count, err := db.ReconcileOrphanedInvites()
	assert.NoError(t, err)
	assert.Equal(t, 0, count, "Инвайт забаненного пользователя не должен откатываться")

	inv, err := db.GetInviteByCode(invite.Code)
	require.NoError(t, err)
	assert.NotNil(t, inv.UsedBy, "Исторический инвайт должен остаться использованным")
}

// TestClaimInviteNonExistent проверяет claim несуществующего кода
func TestClaimInviteNonExistent(t *testing.T) {
	dbFile := "test_claim_nonexist.db"
	db, err := New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	err = db.ClaimInvite("nonexistent", 111)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found or already used")
}
