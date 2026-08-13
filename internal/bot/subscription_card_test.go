package bot

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"

	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

// revokeStubCounters считает вызовы панели, чтобы проверять порядок и факт вызова.
type revokeStubCounters struct {
	deleteAll atomic.Int32
	revoke    atomic.Int32
}

// setupRevokeBot поднимает бота с пользователем и подменённым HTTP-клиентом панели.
// deleteAllErr/revokeErr включают падение соответствующего вызова.
func setupRevokeBot(t *testing.T, telegramID int64, deleteAllErr, revokeErr bool) (*Bot, *revokeStubCounters) {
	t.Helper()

	b, db := setupTestBot(t)
	_, err := db.CreateUser(telegramID, "user", "User", strPtrTest("uuid-sub"), nil, nil, nil)
	require.NoError(t, err)

	counters := &revokeStubCounters{}

	client := newTestPanelClient()
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			jsonResp := func(payload any) (*http.Response, error) {
				body, err := json.Marshal(payload)
				require.NoError(t, err)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(string(body))),
					Header:     make(http.Header),
				}, nil
			}
			errResp := func() (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(strings.NewReader(`{"message":"boom"}`)),
					Header:     make(http.Header),
				}, nil
			}

			switch {
			case r.URL.Path == "/api/hwid/devices/delete-all":
				counters.deleteAll.Add(1)
				if deleteAllErr {
					return errResp()
				}
				return jsonResp(map[string]any{"response": map[string]any{"success": true}})

			case r.URL.Path == "/api/users/uuid-sub/actions/revoke":
				counters.revoke.Add(1)
				if revokeErr {
					return errResp()
				}
				return jsonResp(map[string]any{"response": map[string]any{
					"uuid":            "uuid-sub",
					"shortUuid":       "newshort",
					"username":        "user",
					"status":          "ACTIVE",
					"expireAt":        time.Now().UTC().AddDate(0, 0, 20).Format(time.RFC3339),
					"subscriptionUrl": "https://sub.example.com:8443/newshort",
				}})

			case strings.HasPrefix(r.URL.Path, "/api/hwid/devices/"):
				return jsonResp(map[string]any{"response": map[string]any{"devices": []any{}}})

			case r.URL.Path == "/api/users/uuid-sub":
				return jsonResp(map[string]any{"response": map[string]any{
					"uuid":            "uuid-sub",
					"shortUuid":       "oldshort",
					"username":        "user",
					"status":          "ACTIVE",
					"expireAt":        time.Now().UTC().AddDate(0, 0, 20).Format(time.RFC3339),
					"subscriptionUrl": "https://sub.example.com:8443/oldshort",
				}})
			}

			return jsonResp(map[string]any{"response": map[string]any{}})
		}),
	})
	b.remnawave = client

	return b, counters
}

func TestApplyRevokeSuccess(t *testing.T) {
	const telegramID = int64(4001)
	b, counters := setupRevokeBot(t, telegramID, false, false)

	remUser, err := b.applyRevoke(telegramID)
	require.NoError(t, err)
	assert.Equal(t, "https://sub.example.com:8443/newshort", remUser.SubscriptionURL)
	assert.Equal(t, int32(1), counters.deleteAll.Load())
	assert.Equal(t, int32(1), counters.revoke.Load())
}

func TestApplyRevokeSkipsRevokeWhenDevicesResetFails(t *testing.T) {
	const telegramID = int64(4002)
	b, counters := setupRevokeBot(t, telegramID, true, false)

	_, err := b.applyRevoke(telegramID)
	require.ErrorIs(t, err, errRevokeDevicesFailed)
	// Ссылка не должна меняться, если устройства сбросить не удалось.
	assert.Equal(t, int32(0), counters.revoke.Load())
	assert.Contains(t, revokeErrorAlert(err), "Ссылка не изменилась")
}

func TestApplyRevokeReportsRevokeFailure(t *testing.T) {
	const telegramID = int64(4003)
	b, _ := setupRevokeBot(t, telegramID, false, true)

	_, err := b.applyRevoke(telegramID)
	require.ErrorIs(t, err, errRevokeFailed)
	assert.Contains(t, revokeErrorAlert(err), "Устройства сброшены")
}

func TestApplyRevokeCooldownBlocksRepeat(t *testing.T) {
	const telegramID = int64(4004)
	b, counters := setupRevokeBot(t, telegramID, false, false)

	_, err := b.applyRevoke(telegramID)
	require.NoError(t, err)

	_, err = b.applyRevoke(telegramID)
	require.ErrorIs(t, err, errRevokeCooldown)
	// Повтор внутри окна не должен дёргать панель второй раз.
	assert.Equal(t, int32(1), counters.deleteAll.Load())
	assert.Equal(t, int32(1), counters.revoke.Load())
}

func TestApplyRevokeCooldownExpires(t *testing.T) {
	const telegramID = int64(4005)
	b, counters := setupRevokeBot(t, telegramID, false, false)

	_, err := b.applyRevoke(telegramID)
	require.NoError(t, err)

	b.subRevokeCooldown.Store(telegramID, time.Now().Add(-subRevokeCooldownWindow-time.Second))

	_, err = b.applyRevoke(telegramID)
	require.NoError(t, err)
	assert.Equal(t, int32(2), counters.revoke.Load())
}

func TestApplyRevokeUnknownUser(t *testing.T) {
	b, counters := setupRevokeBot(t, 4006, false, false)

	_, err := b.applyRevoke(999999)
	require.ErrorIs(t, err, errRevokeUserNotFound)
	assert.Equal(t, int32(0), counters.deleteAll.Load())
}

func TestHandleSubRevokeConfirmRendersNewLink(t *testing.T) {
	const telegramID = int64(4007)
	b, _ := setupRevokeBot(t, telegramID, false, false)

	ctx := &MockContext{sender: &tele.User{ID: telegramID}, message: &tele.Message{}}
	require.NoError(t, b.handleSubRevokeConfirm(ctx))

	msg, ok := ctx.editedMsg.(string)
	require.True(t, ok)
	assert.Contains(t, msg, "Ссылка перевыпущена")
	assert.Contains(t, msg, "newshort")
	assert.NotContains(t, msg, "oldshort")
}

func TestHandleSubRevokeConfirmAlertsOnFailure(t *testing.T) {
	const telegramID = int64(4008)
	b, _ := setupRevokeBot(t, telegramID, true, false)

	ctx := &MockContext{sender: &tele.User{ID: telegramID}, message: &tele.Message{}}
	require.NoError(t, b.handleSubRevokeConfirm(ctx))

	assert.Nil(t, ctx.editedMsg)
	assert.Contains(t, ctx.alertText, "Ссылка не изменилась")
}

func TestSubscriptionLinkVisible(t *testing.T) {
	future := time.Now().UTC().AddDate(0, 0, 10)

	t.Run("активная подписка", func(t *testing.T) {
		assert.True(t, SubscriptionLinkVisible(&remnawave.User{
			Status: remnawave.StatusActive, ExpireAt: future,
		}, false))
	})

	t.Run("безлимитная — всегда", func(t *testing.T) {
		assert.True(t, SubscriptionLinkVisible(&remnawave.User{
			Status: remnawave.StatusDisabled, ExpireAt: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC),
		}, false))
	})

	t.Run("grace-период", func(t *testing.T) {
		assert.False(t, SubscriptionLinkVisible(&remnawave.User{
			Status: remnawave.StatusDisabled, ExpireAt: time.Now().UTC().Add(-time.Hour),
		}, false))
	})

	t.Run("триал с исчерпанным трафиком", func(t *testing.T) {
		assert.False(t, SubscriptionLinkVisible(&remnawave.User{
			Status: remnawave.StatusLimited, ExpireAt: future,
		}, true))
	})
}

func TestRevokeCooldownAlertNamesRemainingTime(t *testing.T) {
	const telegramID = int64(4009)
	b, _ := setupRevokeBot(t, telegramID, false, false)

	_, err := b.applyRevoke(telegramID)
	require.NoError(t, err)

	_, err = b.applyRevoke(telegramID)
	require.ErrorIs(t, err, errRevokeCooldown)

	alert := revokeErrorAlert(err)
	assert.Contains(t, alert, "Повторить можно через")
	assert.Contains(t, alert, "сек.")
}

// Панель могла выполнить перевыпуск и не донести ответ (таймаут, обрыв).
// Утверждать «переподключитесь по прежней ссылке» в этом случае нельзя:
// перечитываем состояние и, если ссылка сменилась, считаем перевыпуск успешным.
func TestApplyRevokeTreatsChangedURLAsSuccessWhenResponseLost(t *testing.T) {
	const telegramID = int64(4010)
	b, db := setupTestBot(t)
	_, err := db.CreateUser(telegramID, "user", "User", strPtrTest("uuid-sub"), nil, nil, nil)
	require.NoError(t, err)

	revoked := false
	client := newTestPanelClient()
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			jsonResp := func(payload any) (*http.Response, error) {
				body, err := json.Marshal(payload)
				require.NoError(t, err)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(string(body))),
					Header:     make(http.Header),
				}, nil
			}

			switch r.URL.Path {
			case "/api/hwid/devices/delete-all":
				return jsonResp(map[string]any{"response": map[string]any{"success": true}})

			case "/api/users/uuid-sub/actions/revoke":
				// Панель выполнила перевыпуск, но ответ до бота не дошёл.
				revoked = true
				return nil, assert.AnError

			case "/api/users/uuid-sub":
				short := "oldshort"
				if revoked {
					short = "newshort"
				}
				return jsonResp(map[string]any{"response": map[string]any{
					"uuid":            "uuid-sub",
					"shortUuid":       short,
					"username":        "user",
					"status":          "ACTIVE",
					"expireAt":        time.Now().UTC().AddDate(0, 0, 20).Format(time.RFC3339),
					"subscriptionUrl": "https://sub.example.com:8443/" + short,
				}})
			}
			return jsonResp(map[string]any{"response": map[string]any{"devices": []any{}}})
		}),
	})
	b.remnawave = client

	remUser, err := b.applyRevoke(telegramID)
	require.NoError(t, err)
	assert.Equal(t, "https://sub.example.com:8443/newshort", remUser.SubscriptionURL)
}

func TestRevokeDoneWarnsAboutStaleCards(t *testing.T) {
	assert.Contains(t, MsgRevokeDone, "более старых сообщениях")
}
