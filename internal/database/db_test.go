package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) *DB {
	db, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

// === User Tests ===

func TestCreateUser(t *testing.T) {
	db := setupTestDB(t)

	user, err := db.CreateUser(123456, "test@example.com", "uuid-123", "testuser")
	require.NoError(t, err)
	require.NotNil(t, user)

	assert.Equal(t, int64(123456), user.TelegramID)
	assert.Equal(t, "test@example.com", user.Email)
	assert.Equal(t, "uuid-123", user.UUID)
	assert.Equal(t, "testuser", user.Username.String)
	assert.Equal(t, StatusNone, user.SubscriptionStatus)
	assert.False(t, user.TrialUsed)
	assert.Equal(t, int64(0), user.RuExtraTraffic)
}

func TestCreateUserDuplicate(t *testing.T) {
	db := setupTestDB(t)

	_, err := db.CreateUser(123456, "test@example.com", "uuid-123", "")
	require.NoError(t, err)

	// Duplicate telegram_id
	_, err = db.CreateUser(123456, "other@example.com", "uuid-456", "")
	assert.Error(t, err)

	// Duplicate email
	_, err = db.CreateUser(789, "test@example.com", "uuid-789", "")
	assert.Error(t, err)
}

func TestGetUserByTelegramID(t *testing.T) {
	db := setupTestDB(t)

	created, err := db.CreateUser(123456, "test@example.com", "uuid-123", "")
	require.NoError(t, err)

	user, err := db.GetUserByTelegramID(123456)
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.Equal(t, created.ID, user.ID)

	// Non-existent user
	user, err = db.GetUserByTelegramID(999999)
	require.NoError(t, err)
	assert.Nil(t, user)
}

func TestGetUserByEmail(t *testing.T) {
	db := setupTestDB(t)

	_, err := db.CreateUser(123456, "test@example.com", "uuid-123", "")
	require.NoError(t, err)

	user, err := db.GetUserByEmail("test@example.com")
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.Equal(t, int64(123456), user.TelegramID)
}

func TestGetUserByUUID(t *testing.T) {
	db := setupTestDB(t)

	_, err := db.CreateUser(123456, "test@example.com", "uuid-123", "")
	require.NoError(t, err)

	user, err := db.GetUserByUUID("uuid-123")
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.Equal(t, "test@example.com", user.Email)
}

func TestUpdateUserSubscription(t *testing.T) {
	db := setupTestDB(t)

	user, err := db.CreateUser(123456, "test@example.com", "uuid-123", "")
	require.NoError(t, err)

	endTime := time.Now().Add(30 * 24 * time.Hour)
	err = db.UpdateUserSubscription(user.ID, StatusActive, &endTime)
	require.NoError(t, err)

	updated, err := db.GetUserByID(user.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusActive, updated.SubscriptionStatus)
	assert.NotNil(t, updated.SubscriptionEndAt)
}

func TestMarkTrialUsed(t *testing.T) {
	db := setupTestDB(t)

	user, err := db.CreateUser(123456, "test@example.com", "uuid-123", "")
	require.NoError(t, err)
	assert.False(t, user.TrialUsed)

	err = db.MarkTrialUsed(user.ID)
	require.NoError(t, err)

	updated, err := db.GetUserByID(user.ID)
	require.NoError(t, err)
	assert.True(t, updated.TrialUsed)
}

func TestAddRuExtraTraffic(t *testing.T) {
	db := setupTestDB(t)

	user, err := db.CreateUser(123456, "test@example.com", "uuid-123", "")
	require.NoError(t, err)

	// Add 10GB
	err = db.AddRuExtraTraffic(user.ID, 10*1024*1024*1024)
	require.NoError(t, err)

	updated, err := db.GetUserByID(user.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(10*1024*1024*1024), updated.RuExtraTraffic)

	// Add another 5GB
	err = db.AddRuExtraTraffic(user.ID, 5*1024*1024*1024)
	require.NoError(t, err)

	updated, err = db.GetUserByID(user.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(15*1024*1024*1024), updated.RuExtraTraffic)
}

func TestGetActiveUsers(t *testing.T) {
	db := setupTestDB(t)

	// Create users with different statuses
	user1, _ := db.CreateUser(1, "user1@test.com", "uuid-1", "")
	db.UpdateUserSubscription(user1.ID, StatusActive, nil)

	user2, _ := db.CreateUser(2, "user2@test.com", "uuid-2", "")
	db.UpdateUserSubscription(user2.ID, StatusTrial, nil)

	user3, _ := db.CreateUser(3, "user3@test.com", "uuid-3", "")
	db.UpdateUserSubscription(user3.ID, StatusExpired, nil)

	db.CreateUser(4, "user4@test.com", "uuid-4", "") // status = none

	users, err := db.GetActiveUsers()
	require.NoError(t, err)
	assert.Len(t, users, 2)
}

func TestUserExists(t *testing.T) {
	db := setupTestDB(t)

	exists, err := db.UserExists(123456)
	require.NoError(t, err)
	assert.False(t, exists)

	_, err = db.CreateUser(123456, "test@example.com", "uuid-123", "")
	require.NoError(t, err)

	exists, err = db.UserExists(123456)
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestDeleteUser(t *testing.T) {
	db := setupTestDB(t)

	user, err := db.CreateUser(123456, "test@example.com", "uuid-123", "")
	require.NoError(t, err)

	err = db.DeleteUser(user.ID)
	require.NoError(t, err)

	deleted, err := db.GetUserByID(user.ID)
	require.NoError(t, err)
	assert.Nil(t, deleted)
}

// === Payment Tests ===

func TestCreatePayment(t *testing.T) {
	db := setupTestDB(t)

	user, _ := db.CreateUser(123456, "test@example.com", "uuid-123", "")

	payment, err := db.CreatePayment(user.ID, "pay-123", "robokassa", 200, PaymentTypeMonthly, nil)
	require.NoError(t, err)
	require.NotNil(t, payment)

	assert.Equal(t, user.ID, payment.UserID)
	assert.Equal(t, "pay-123", payment.PaymentID)
	assert.Equal(t, "robokassa", payment.Provider)
	assert.Equal(t, 200, payment.Amount)
	assert.Equal(t, PaymentStatusPending, payment.Status)
	assert.Equal(t, PaymentTypeMonthly, payment.Type)
}

func TestConfirmPayment(t *testing.T) {
	db := setupTestDB(t)

	user, _ := db.CreateUser(123456, "test@example.com", "uuid-123", "")
	_, err := db.CreatePayment(user.ID, "pay-123", "robokassa", 200, PaymentTypeMonthly, nil)
	require.NoError(t, err)

	err = db.ConfirmPayment("pay-123")
	require.NoError(t, err)

	payment, err := db.GetPaymentByPaymentID("pay-123")
	require.NoError(t, err)
	assert.Equal(t, PaymentStatusSucceeded, payment.Status)
	assert.NotNil(t, payment.ConfirmedAt)
}

func TestFailPayment(t *testing.T) {
	db := setupTestDB(t)

	user, _ := db.CreateUser(123456, "test@example.com", "uuid-123", "")
	_, err := db.CreatePayment(user.ID, "pay-123", "robokassa", 200, PaymentTypeMonthly, nil)
	require.NoError(t, err)

	err = db.FailPayment("pay-123")
	require.NoError(t, err)

	payment, err := db.GetPaymentByPaymentID("pay-123")
	require.NoError(t, err)
	assert.Equal(t, PaymentStatusFailed, payment.Status)
}

func TestGetPaymentsByUserID(t *testing.T) {
	db := setupTestDB(t)

	user, _ := db.CreateUser(123456, "test@example.com", "uuid-123", "")

	db.CreatePayment(user.ID, "pay-1", "robokassa", 200, PaymentTypeMonthly, nil)
	db.CreatePayment(user.ID, "pay-2", "robokassa", 100, PaymentTypeTraffic10G, nil)

	payments, err := db.GetPaymentsByUserID(user.ID)
	require.NoError(t, err)
	assert.Len(t, payments, 2)
}

func TestGetUserTotalPaid(t *testing.T) {
	db := setupTestDB(t)

	user, _ := db.CreateUser(123456, "test@example.com", "uuid-123", "")

	db.CreatePayment(user.ID, "pay-1", "robokassa", 200, PaymentTypeMonthly, nil)
	db.ConfirmPayment("pay-1")

	db.CreatePayment(user.ID, "pay-2", "robokassa", 100, PaymentTypeTraffic10G, nil)
	db.ConfirmPayment("pay-2")

	db.CreatePayment(user.ID, "pay-3", "robokassa", 200, PaymentTypeMonthly, nil)
	// Not confirmed

	total, err := db.GetUserTotalPaid(user.ID)
	require.NoError(t, err)
	assert.Equal(t, 300, total)
}

// === Promo Code Tests ===

func TestCreatePromoCode(t *testing.T) {
	db := setupTestDB(t)

	validUntil := time.Now().Add(30 * 24 * time.Hour)
	promo, err := db.CreatePromoCode("WELCOME10", PromoTypeDiscount, 10, 100, &validUntil)
	require.NoError(t, err)
	require.NotNil(t, promo)

	assert.Equal(t, "WELCOME10", promo.Code)
	assert.Equal(t, PromoTypeDiscount, promo.Type)
	assert.Equal(t, 10, promo.Value)
	assert.Equal(t, 100, promo.MaxUses)
	assert.Equal(t, 0, promo.UsedCount)
}

func TestValidatePromoCode(t *testing.T) {
	db := setupTestDB(t)

	user, _ := db.CreateUser(123456, "test@example.com", "uuid-123", "")

	// Valid promo
	validUntil := time.Now().Add(24 * time.Hour)
	db.CreatePromoCode("VALID", PromoTypeDiscount, 10, 10, &validUntil)

	promo, err := db.ValidatePromoCode("VALID", user.ID)
	require.NoError(t, err)
	require.NotNil(t, promo)

	// Expired promo
	expiredTime := time.Now().Add(-24 * time.Hour)
	db.CreatePromoCode("EXPIRED", PromoTypeDiscount, 10, 10, &expiredTime)

	_, err = db.ValidatePromoCode("EXPIRED", user.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expired")

	// Non-existent promo
	_, err = db.ValidatePromoCode("NOTFOUND", user.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestUsePromoCode(t *testing.T) {
	db := setupTestDB(t)

	user, _ := db.CreateUser(123456, "test@example.com", "uuid-123", "")

	promo, _ := db.CreatePromoCode("TEST", PromoTypeDiscount, 10, 2, nil)

	// First use
	err := db.UsePromoCode(user.ID, promo.ID)
	require.NoError(t, err)

	updated, _ := db.GetPromoCodeByID(promo.ID)
	assert.Equal(t, 1, updated.UsedCount)

	// Same user cannot use again
	_, err = db.ValidatePromoCode("TEST", user.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already used")

	// Different user can use
	user2, _ := db.CreateUser(789, "user2@test.com", "uuid-789", "")
	err = db.UsePromoCode(user2.ID, promo.ID)
	require.NoError(t, err)

	updated, _ = db.GetPromoCodeByID(promo.ID)
	assert.Equal(t, 2, updated.UsedCount)

	// Max uses reached
	user3, _ := db.CreateUser(111, "user3@test.com", "uuid-111", "")
	_, err = db.ValidatePromoCode("TEST", user3.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "limit reached")
}

func TestGetActivePromoCodes(t *testing.T) {
	db := setupTestDB(t)

	future := time.Now().Add(24 * time.Hour)
	past := time.Now().Add(-24 * time.Hour)

	db.CreatePromoCode("ACTIVE1", PromoTypeDiscount, 10, 10, &future)
	db.CreatePromoCode("ACTIVE2", PromoTypeFreeDays, 7, 5, nil) // No expiry
	db.CreatePromoCode("EXPIRED", PromoTypeDiscount, 10, 10, &past)

	// Create exhausted promo
	exhausted, _ := db.CreatePromoCode("EXHAUSTED", PromoTypeDiscount, 10, 1, nil)
	user, _ := db.CreateUser(123, "test@test.com", "uuid-test", "")
	db.UsePromoCode(user.ID, exhausted.ID)

	active, err := db.GetActivePromoCodes()
	require.NoError(t, err)
	assert.Len(t, active, 2)
}

func TestDeletePromoCode(t *testing.T) {
	db := setupTestDB(t)

	promo, _ := db.CreatePromoCode("DELETE", PromoTypeDiscount, 10, 10, nil)

	user, _ := db.CreateUser(123, "test@test.com", "uuid-test", "")
	db.UsePromoCode(user.ID, promo.ID)

	err := db.DeletePromoCode(promo.ID)
	require.NoError(t, err)

	deleted, err := db.GetPromoCodeByID(promo.ID)
	require.NoError(t, err)
	assert.Nil(t, deleted)

	// Check that uses are also deleted
	uses, err := db.GetUserPromoUses(user.ID)
	require.NoError(t, err)
	assert.Empty(t, uses)
}
