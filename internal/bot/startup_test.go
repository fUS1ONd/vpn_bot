package bot

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReconcileOrphanedRegistrationsRestoresLocalUserFromRemnawave(t *testing.T) {
	b, db := setupTestBot(t)

	adminID := int64(999999)
	moderatorID := int64(1001)
	userID := int64(2001)
	price := 650

	_, err := db.CreateUser(moderatorID, "mod", "Mod", "uuid-mod", nil, nil)
	require.NoError(t, err)
	require.NoError(t, db.AddModerator(moderatorID, adminID))

	code, err := db.CreateInviteWithPrice(moderatorID, 30, price)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(code, userID))

	b.remnawave.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodGet, r.Method)
			require.Equal(t, "/api/users/by-telegram-id/2001", r.URL.Path)

			payload := `{"response":{"uuid":"uuid-remote-2001","username":"restored_user","telegramId":2001,"status":"ACTIVE","expireAt":"2026-04-20T00:00:00Z"}}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(payload)),
				Header:     make(http.Header),
			}, nil
		}),
	})

	stats, err := ReconcileOrphanedRegistrations(db, b.remnawave)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.RestoredUsers)
	assert.Equal(t, 0, stats.ReleasedInvites)

	user, err := db.GetUserByTelegramID(userID)
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.Equal(t, "uuid-remote-2001", user.RemnawaveUUID)
	assert.Equal(t, "restored_user", user.Username)
	require.NotNil(t, user.SubscriptionPrice)
	assert.Equal(t, price, *user.SubscriptionPrice)
	require.NotNil(t, user.ModeratorID)
	assert.Equal(t, moderatorID, *user.ModeratorID)

	invite, err := db.GetInviteByCode(code)
	require.NoError(t, err)
	require.NotNil(t, invite)
	require.NotNil(t, invite.UsedBy)
	assert.Equal(t, userID, *invite.UsedBy)
}

func TestReconcileOrphanedRegistrationsReleasesInviteWhenRemoteUserMissing(t *testing.T) {
	b, db := setupTestBot(t)

	userID := int64(3001)
	code, err := db.CreateInviteWithPrice(1001, 30, 700)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(code, userID))

	b.remnawave.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodGet, r.Method)
			require.Equal(t, "/api/users/by-telegram-id/3001", r.URL.Path)

			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader(`{"message":"not found"}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})

	stats, err := ReconcileOrphanedRegistrations(db, b.remnawave)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.RestoredUsers)
	assert.Equal(t, 1, stats.ReleasedInvites)

	user, err := db.GetUserByTelegramID(userID)
	require.NoError(t, err)
	assert.Nil(t, user)

	invite, err := db.GetInviteByCode(code)
	require.NoError(t, err)
	require.NotNil(t, invite)
	assert.Nil(t, invite.UsedBy)
}
