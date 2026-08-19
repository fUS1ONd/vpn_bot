package bot

import (
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommunityMentionByUserClass(t *testing.T) {
	t.Run("paying user sees the mention", func(t *testing.T) {
		b, db := setupTestBot(t)
		enableCommunity(b)
		makePayingUser(t, b, db, 501, "uuid-mention-paid")

		mention := b.claimCommunityMention(501)
		assert.Contains(t, mention, "Сообщество")
		assert.Contains(t, mention, b.config.CommunityInviteLink)
	})

	t.Run("non-paying user never sees it", func(t *testing.T) {
		b, db := setupTestBot(t)
		enableCommunity(b)
		_, err := db.CreateUser(502, "trial", "Trial", strPtrTest("uuid-mention-trial"), nil, nil, nil)
		require.NoError(t, err)
		setReferralEligibilityRemote(t, b, "uuid-mention-trial", "ACTIVE", "2098-01-01T00:00:00Z")

		assert.Empty(t, b.claimCommunityMention(502))
	})

	t.Run("member of the channel never sees it", func(t *testing.T) {
		b, db := setupTestBot(t)
		enableCommunity(b)
		makePayingUser(t, b, db, 503, "uuid-mention-member")
		require.NoError(t, db.MarkCommunityJoined(503, time.Now().UTC()))

		assert.Empty(t, b.claimCommunityMention(503))
	})

	t.Run("feature disabled changes nothing", func(t *testing.T) {
		b, db := setupTestBot(t)
		makePayingUser(t, b, db, 504, "uuid-mention-off")

		assert.Empty(t, b.claimCommunityMention(504))
	})
}

// Кулдаун один на все места показа и живёт в базе — переживает деплой.
func TestCommunityMentionSharesOneWeeklyCooldown(t *testing.T) {
	b, db := setupTestBot(t)
	enableCommunity(b)
	makePayingUser(t, b, db, 505, "uuid-mention-cooldown")

	require.NotEmpty(t, b.claimCommunityMention(505))
	assert.Empty(t, b.claimCommunityMention(505), "второй показ подряд запрещён")

	// Показ шестидневной давности кулдаун ещё держит.
	require.NoError(t, db.MarkCommunityMentionSent(505, time.Now().UTC().Add(-6*24*time.Hour)))
	assert.Empty(t, b.claimCommunityMention(505))

	// Восемь дней — можно снова.
	require.NoError(t, db.MarkCommunityMentionSent(505, time.Now().UTC().Add(-8*24*time.Hour)))
	assert.NotEmpty(t, b.claimCommunityMention(505))
}

// Одобренная заявка гасит приписку навсегда.
func TestCommunityMentionStopsAfterJoining(t *testing.T) {
	b, db := setupTestBot(t)
	enableCommunity(b)
	makePayingUser(t, b, db, 506, "uuid-mention-joins")

	require.NotEmpty(t, b.claimCommunityMention(506))
	require.True(t, b.resolveJoinRequest(506).approve)
	b.markCommunityJoined(506)

	require.NoError(t, db.MarkCommunityMentionSent(506, time.Now().UTC().Add(-30*24*time.Hour)))
	assert.Empty(t, b.claimCommunityMention(506))
}

func TestInfoMessageMentionsCommunityForEveryone(t *testing.T) {
	cfg := &config.Config{
		PrivacyPolicyURL:  "https://example.com/privacy",
		TermsOfServiceURL: "https://example.com/terms",
		SupportContact:    "@support",
	}
	withoutCommunity := BuildInfoMessage(cfg)
	assert.NotContains(t, withoutCommunity, "Сообщество")

	cfg.CommunityChatID = -1001234567890
	cfg.CommunityInviteLink = "https://t.me/+testinvite"
	withCommunity := BuildInfoMessage(cfg)
	assert.Contains(t, withCommunity, "Сообщество")
	assert.Contains(t, withCommunity, "https://t.me/+testinvite")
	assert.Contains(t, withCommunity, "оплаченной подпиской")
	// Прежний текст остаётся на месте целиком.
	assert.Contains(t, withCommunity, withoutCommunity)
}

func TestCommunityMentionTextNamesAutomaticApproval(t *testing.T) {
	cfg := &config.Config{CommunityChatID: -100, CommunityInviteLink: "https://t.me/+link"}
	mention := BuildCommunityMention(cfg)

	assert.Contains(t, mention, "Сообщество")
	assert.Contains(t, mention, "https://t.me/+link")
	assert.Contains(t, mention, "автоматически")
	assert.Empty(t, BuildCommunityMention(&config.Config{}))
}
