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
