package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommunityMembershipMark(t *testing.T) {
	db := setupTestDB(t)

	member, err := db.IsCommunityMember(11)
	require.NoError(t, err)
	assert.False(t, member)

	first := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	require.NoError(t, db.MarkCommunityJoined(11, first))

	member, err = db.IsCommunityMember(11)
	require.NoError(t, err)
	assert.True(t, member)

	// Повторное одобрение не двигает момент вступления.
	require.NoError(t, db.MarkCommunityJoined(11, first.Add(24*time.Hour)))
	member, err = db.IsCommunityMember(11)
	require.NoError(t, err)
	assert.True(t, member)
}

func TestCommunityMentionTimestamp(t *testing.T) {
	db := setupTestDB(t)

	sentAt, err := db.CommunityMentionSentAt(12)
	require.NoError(t, err)
	assert.Nil(t, sentAt)

	shown := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	require.NoError(t, db.MarkCommunityMentionSent(12, shown))

	sentAt, err = db.CommunityMentionSentAt(12)
	require.NoError(t, err)
	require.NotNil(t, sentAt)
	assert.WithinDuration(t, shown, sentAt.UTC(), time.Second)

	later := shown.Add(8 * 24 * time.Hour)
	require.NoError(t, db.MarkCommunityMentionSent(12, later))
	sentAt, err = db.CommunityMentionSentAt(12)
	require.NoError(t, err)
	require.NotNil(t, sentAt)
	assert.WithinDuration(t, later, sentAt.UTC(), time.Second)
}

// Пометки Канала живут в своей таблице именно затем, чтобы оплата и бан их не
// стирали: ClearNotifications не должна их трогать.
func TestClearNotificationsKeepsCommunityMarks(t *testing.T) {
	db := setupTestDB(t)
	require.NoError(t, db.MarkCommunityJoined(13, time.Now().UTC()))
	require.NoError(t, db.MarkCommunityMentionSent(13, time.Now().UTC()))
	require.NoError(t, db.MarkNotificationSent(13, "expiring_3d"))

	require.NoError(t, db.ClearNotifications(13))

	member, err := db.IsCommunityMember(13)
	require.NoError(t, err)
	assert.True(t, member)
	sentAt, err := db.CommunityMentionSentAt(13)
	require.NoError(t, err)
	assert.NotNil(t, sentAt)
}
