package bot

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
)

// captureInlineAnswer перехватывает answerInlineQuery: проверяем то, что
// действительно уходит в Telegram, а не промежуточную структуру.
func captureInlineAnswer(t *testing.T, b *Bot) *map[string]any {
	t.Helper()
	answer := map[string]any{}
	b.bot = newOfflineTelegramBotForTest(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/answerInlineQuery") {
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(body, &answer))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`)),
			Header:     make(http.Header),
		}, nil
	}))
	return &answer
}

func activeReferralInvite(t *testing.T, db *database.DB, creator int64) database.Invite {
	t.Helper()
	// Раздел приглашений доступен только зарегистрированным, и inline-путь
	// проверяет то же самое — без записи в users список окажется пустым.
	if exists, err := db.UserExists(creator); err == nil && !exists {
		_, err := db.CreateUser(creator, "user", "User", strPtrTest(fmt.Sprintf("uuid-%d", creator)), nil, nil, nil)
		require.NoError(t, err)
	}
	invite, err := db.CreateReferralInvite(creator, 400, time.Now().UTC())
	require.NoError(t, err)
	return *invite
}

// TestReferralShareMessage_SelfContained: получатель видит только этот текст,
// поэтому в нём должно быть всё, что нужно для решения.
func TestReferralShareMessage_SelfContained(t *testing.T) {
	b, _ := setupTestBot(t)
	b.config.DefaultSubscriptionPrice = 400
	b.config.TrialTrafficLimitGB = 1
	price := 650
	expiresAt := time.Date(2026, 7, 20, 12, 30, 0, 0, time.UTC)

	message := b.referralShareMessage(&database.Invite{
		Code:              "0123456789abcdef",
		SubscriptionPrice: &price,
		ExpiresAt:         &expiresAt,
	})

	assert.Contains(t, message, "Приглашение в VPN")
	assert.Contains(t, message, "3 дня бесплатно")
	assert.Contains(t, message, "1 ГБ")
	assert.Contains(t, message, "650 ₽")
	assert.Contains(t, message, "20.07.2026 15:30 МСК")
	assert.Contains(t, message, "https://t.me/bot?start=0123456789abcdef")
}

// TestReferralShareMessage_NoOwnerWarnings: текст уходит постороннему человеку,
// и предупреждения, адресованные пригласившему, в чужом чате читаются угрозой.
func TestReferralShareMessage_NoOwnerWarnings(t *testing.T) {
	b, _ := setupTestBot(t)
	expiresAt := time.Now().UTC().Add(24 * time.Hour)

	message := b.referralShareMessage(&database.Invite{Code: "abc", ExpiresAt: &expiresAt})

	assert.NotContains(t, message, "исключён из системы")
	assert.NotContains(t, message, "автоматически отключится")
}

// TestShareableInvites_OnlyOwn: код в inline-запросе приходит от человека, а не
// от кнопки, и чужой подставленный код отдавать нельзя.
func TestShareableInvites_OnlyOwn(t *testing.T) {
	b, db := setupTestBot(t)
	mine := activeReferralInvite(t, db, 100)
	stranger := activeReferralInvite(t, db, 200)
	now := time.Now().UTC()

	t.Run("свой код", func(t *testing.T) {
		invites, err := b.shareableInvites(100, mine.Code, now)
		require.NoError(t, err)
		require.Len(t, invites, 1)
		assert.Equal(t, mine.Code, invites[0].Code)
	})

	t.Run("чужой код", func(t *testing.T) {
		invites, err := b.shareableInvites(100, stranger.Code, now)
		require.NoError(t, err)
		assert.Empty(t, invites)
	})

	t.Run("несуществующий код", func(t *testing.T) {
		invites, err := b.shareableInvites(100, "нет-такого", now)
		require.NoError(t, err)
		assert.Empty(t, invites)
	})

	t.Run("пустой запрос отдаёт свои активные", func(t *testing.T) {
		invites, err := b.shareableInvites(100, "  ", now)
		require.NoError(t, err)
		require.Len(t, invites, 1)
		assert.Equal(t, mine.Code, invites[0].Code)
	})
}

// TestShareableInvites_RevokedIsGone: отозванным приглашением поделиться нельзя.
func TestShareableInvites_RevokedIsGone(t *testing.T) {
	b, db := setupTestBot(t)
	invite := activeReferralInvite(t, db, 100)
	now := time.Now().UTC()
	require.NoError(t, db.RevokeReferralInvite(invite.Code, 100, false, now))

	invites, err := b.shareableInvites(100, invite.Code, now)
	require.NoError(t, err)
	assert.Empty(t, invites)
}

// TestReferralShareButton_SwitchesToChatPicker: кнопка должна открывать выбор
// чата, а не слать callback боту.
func TestReferralShareButton_SwitchesToChatPicker(t *testing.T) {
	markup := ReferralShareKeyboard("abcdef")
	require.Len(t, markup.InlineKeyboard, 1)
	require.Len(t, markup.InlineKeyboard[0], 1)

	button := markup.InlineKeyboard[0][0]
	assert.Equal(t, BtnInviteShare, button.Text)
	assert.Equal(t, "abcdef", button.InlineQuery)
	assert.Empty(t, button.Data)
	// Пустое значение означает «не подставлять в текущий чат»: подставленный
	// туда запрос оставил бы приглашение в том же диалоге с ботом.
	assert.Empty(t, button.InlineQueryChat)
}

// TestReferralInvitesKeyboard_HasShare: в списке приглашений «Поделиться» стоит
// рядом с прежними действиями, не вытесняя их.
func TestReferralInvitesKeyboard_HasShare(t *testing.T) {
	expiresAt := time.Now().UTC().Add(24 * time.Hour)
	markup := ReferralInvitesKeyboard([]database.Invite{{Code: "code1", ExpiresAt: &expiresAt}}, 0, false)

	require.NotEmpty(t, markup.InlineKeyboard)
	row := markup.InlineKeyboard[0]
	require.Len(t, row, 3)
	assert.Equal(t, BtnInviteShare, row[0].Text)
	assert.Equal(t, "code1", row[0].InlineQuery)
	assert.Equal(t, "📨 code1", row[1].Text)
	assert.Equal(t, "🗑 Отозвать", row[2].Text)
}

// TestHandleReferralShareQuery_AnswersWithOwnInvites: карточка на каждое своё
// приглашение, и ответ помечен персональным — иначе Telegram отдаст его другому.
func TestHandleReferralShareQuery_AnswersWithOwnInvites(t *testing.T) {
	b, db := setupTestBot(t)
	first := activeReferralInvite(t, db, 100)
	second := activeReferralInvite(t, db, 100)
	answer := captureInlineAnswer(t, b)

	ctx := b.bot.NewContext(tele.Update{
		Query: &tele.Query{ID: "q1", Sender: &tele.User{ID: 100}},
	})
	require.NoError(t, b.handleReferralShareQuery(ctx))

	results, ok := (*answer)["results"].([]any)
	require.True(t, ok, "ответ должен содержать результаты")
	require.Len(t, results, 2)

	ids := map[string]bool{}
	for _, raw := range results {
		result := raw.(map[string]any)
		assert.Equal(t, "article", result["type"])
		assert.Contains(t, result["message_text"], "Приглашение в VPN")
		ids[result["id"].(string)] = true
	}
	assert.True(t, ids[first.Code])
	assert.True(t, ids[second.Code])

	assert.Equal(t, true, (*answer)["is_personal"])
	assert.Equal(t, float64(shareCacheTime), (*answer)["cache_time"], "без явного значения Telegram кеширует ответ на 5 минут")
}

// TestHandleReferralShareQuery_FiltersByCode: кнопка подставляет конкретный код,
// и в выборе чата должна остаться ровно одна карточка.
func TestHandleReferralShareQuery_FiltersByCode(t *testing.T) {
	b, db := setupTestBot(t)
	first := activeReferralInvite(t, db, 100)
	_ = activeReferralInvite(t, db, 100)
	answer := captureInlineAnswer(t, b)

	ctx := b.bot.NewContext(tele.Update{
		Query: &tele.Query{ID: "q2", Sender: &tele.User{ID: 100}, Text: first.Code},
	})
	require.NoError(t, b.handleReferralShareQuery(ctx))

	results := (*answer)["results"].([]any)
	require.Len(t, results, 1)
	assert.Equal(t, first.Code, results[0].(map[string]any)["id"])
}

// TestHandleReferralShareQuery_EmptyIsStillAnAnswer: без ответа у человека в поле
// ввода навсегда остаётся крутилка, поэтому пустой список — тоже ответ.
func TestHandleReferralShareQuery_EmptyIsStillAnAnswer(t *testing.T) {
	b, db := setupTestBot(t)
	// Зарегистрированный, но пока без единого приглашения: ему подсказка нужна,
	// в отличие от постороннего.
	_, err := db.CreateUser(100, "user", "User", strPtrTest("uuid-100"), nil, nil, nil)
	require.NoError(t, err)
	answer := captureInlineAnswer(t, b)

	ctx := b.bot.NewContext(tele.Update{
		Query: &tele.Query{ID: "q3", Sender: &tele.User{ID: 100}},
	})
	require.NoError(t, b.handleReferralShareQuery(ctx))

	results, ok := (*answer)["results"].([]any)
	require.True(t, ok)
	assert.Empty(t, results)
	assert.Equal(t, "Создать приглашение", (*answer)["switch_pm_text"])
	// Параметр обязателен: без него Telegram отвечает «can't use empty
	// start_parameter», ответ не доходит и крутилка остаётся навсегда.
	assert.Equal(t, StartParamInvites, (*answer)["switch_pm_parameter"])
}

// TestHandleReferralShareQuery_BannedGetsNothing: у забаненного активные
// приглашения остаются в базе, и inline-путь не должен стать обходом бана.
func TestHandleReferralShareQuery_BannedGetsNothing(t *testing.T) {
	b, db := setupTestBot(t)
	invite := activeReferralInvite(t, db, 100)
	require.NoError(t, db.BanUser(100, 1))
	answer := captureInlineAnswer(t, b)

	ctx := b.bot.NewContext(tele.Update{
		Query: &tele.Query{ID: "q4", Sender: &tele.User{ID: 100}, Text: invite.Code},
	})
	require.NoError(t, b.handleReferralShareQuery(ctx))

	results, ok := (*answer)["results"].([]any)
	require.True(t, ok, "ответить надо даже забаненному, иначе у него крутится вечная загрузка")
	assert.Empty(t, results)
	assert.NotContains(t, *answer, "switch_pm_text", "постороннему не показываем даже кнопку")
}

// TestHandleReferralShareQuery_StrangerLearnsNothing: inline-режим виден из
// любого чата, и незарегистрированный не должен узнать из него о сервисе.
func TestHandleReferralShareQuery_StrangerLearnsNothing(t *testing.T) {
	b, _ := setupTestBot(t)
	answer := captureInlineAnswer(t, b)

	ctx := b.bot.NewContext(tele.Update{
		Query: &tele.Query{ID: "q5", Sender: &tele.User{ID: 4242}},
	})
	require.NoError(t, b.handleReferralShareQuery(ctx))

	results, ok := (*answer)["results"].([]any)
	require.True(t, ok)
	assert.Empty(t, results)
	assert.NotContains(t, *answer, "switch_pm_text")
}

// TestShareAnswers_CacheTimeIsSent: ноль выпадает из запроса как omitempty, и
// Telegram применяет свои 300 секунд — за них отозванным кодом ещё делятся.
func TestShareAnswers_CacheTimeIsSent(t *testing.T) {
	for name, response := range map[string]*tele.QueryResponse{
		"пустой": emptyShareResponse(),
		"молчун": silentShareResponse(),
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, 1, response.CacheTime)
			assert.True(t, response.IsPersonal)
		})
	}
}
