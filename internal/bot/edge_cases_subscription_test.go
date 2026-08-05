package bot

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"

	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

// edgeCaseBot — вариант setupRevokeBot с настраиваемым ответом GET /api/users/{uuid}:
// нужен, чтобы проверять подтверждение перевыпуска в состояниях, где кнопки
// перевыпуска на свежей карточке не бывает (grace, исчерпанный трафик).
func edgeCaseBot(t *testing.T, telegramID int64, currentUser map[string]any) (*Bot, *revokeStubCounters) {
	t.Helper()

	b, db := setupTestBot(t)
	_, err := db.CreateUser(telegramID, "user", "User", "uuid-sub", nil, nil)
	require.NoError(t, err)

	counters := &revokeStubCounters{}

	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
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

			switch {
			case r.URL.Path == "/api/hwid/devices/delete-all":
				counters.deleteAll.Add(1)
				return jsonResp(map[string]any{"response": map[string]any{"success": true}})

			case r.URL.Path == "/api/users/uuid-sub/actions/revoke":
				counters.revoke.Add(1)
				revoked := map[string]any{}
				for k, v := range currentUser {
					revoked[k] = v
				}
				revoked["shortUuid"] = "newshort"
				revoked["subscriptionUrl"] = "https://sub.example.com:8443/newshort"
				return jsonResp(map[string]any{"response": revoked})

			case strings.HasPrefix(r.URL.Path, "/api/hwid/devices/"):
				return jsonResp(map[string]any{"response": map[string]any{"devices": []any{}, "total": 0}})

			case r.URL.Path == "/api/users/uuid-sub":
				return jsonResp(map[string]any{"response": currentUser})
			}

			return jsonResp(map[string]any{"response": map[string]any{}})
		}),
	})
	b.remnawave = client

	return b, counters
}

// graceUser — подписка истекла, панель уже перевела пользователя в DISABLED.
// В этом состоянии свежая карточка не содержит ни ссылки, ни кнопки перевыпуска
// (см. SubscriptionLinkVisible и таблицу видимости в спеке).
func graceUser() map[string]any {
	return map[string]any{
		"uuid":            "uuid-sub",
		"shortUuid":       "oldshort",
		"username":        "user",
		"status":          remnawave.StatusDisabled,
		"expireAt":        time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339),
		"subscriptionUrl": "https://sub.example.com:8443/oldshort",
	}
}

// failingEditContext — контекст, у которого редактирование сообщения падает
// (транзиентная ошибка Bot API). Всё остальное ведёт себя как MockContext.
type failingEditContext struct {
	*MockContext
	editErr error
}

func (c *failingEditContext) Edit(what any, opts ...any) error {
	return c.editErr
}

// Инлайн-кнопки живут в чате бесконечно: карточка, отрисованная при активной
// подписке, остаётся нажимаемой и после того, как подписка истекла. По спеке
// перевыпуск в grace-периоде пользователю недоступен (кнопки нет), значит
// подтверждение из устаревшей карточки не должно обходить это правило: сейчас
// оно молча сбрасывает все HWID-привязки и ротирует ссылку в состоянии, где
// пользователь даже не увидит новую ссылку в ответной карточке.
func TestRevokeConfirmFromStaleCardIsRejectedInGraceBecauseButtonMustNotOutliveItsVisibilityRule(t *testing.T) {
	const telegramID = int64(9201)
	b, counters := edgeCaseBot(t, telegramID, graceUser())

	ctx := &MockContext{sender: &tele.User{ID: telegramID}, message: &tele.Message{}}
	require.NoError(t, b.handleSubRevokeConfirm(ctx))

	assert.Equal(t, int32(0), counters.deleteAll.Load(),
		"устройства не должны сбрасываться, если перевыпуск в текущем состоянии недоступен")
	assert.Equal(t, int32(0), counters.revoke.Load(),
		"ссылка не должна перевыпускаться из устаревшей карточки")
}

// Экран подтверждения — единственный новый хендлер без фоллбэка на Send:
// handleSubRevoke только логирует ошибку c.Edit и закрывает «часики». При
// транзиентной ошибке Bot API пользователь жмёт «Перевыпустить ссылку» и не
// видит ни экрана подтверждения, ни объяснения — кнопка выглядит мёртвой.
func TestRevokeConfirmScreenTellsUserSomethingWhenEditFailsSoTheButtonIsNotSilentlyDead(t *testing.T) {
	const telegramID = int64(9202)
	b, _ := edgeCaseBot(t, telegramID, map[string]any{
		"uuid":            "uuid-sub",
		"shortUuid":       "oldshort",
		"username":        "user",
		"status":          remnawave.StatusActive,
		"expireAt":        time.Now().UTC().AddDate(0, 0, 20).Format(time.RFC3339),
		"subscriptionUrl": "https://sub.example.com:8443/oldshort",
	})

	inner := &MockContext{sender: &tele.User{ID: telegramID}, message: &tele.Message{}}
	ctx := &failingEditContext{MockContext: inner, editErr: errors.New("telegram: Bad Gateway (502)")}

	require.NoError(t, b.handleSubRevoke(ctx))

	assert.True(t, inner.sentMsg != nil || inner.alertText != "",
		"после неудачного Edit пользователь должен получить экран подтверждения новым сообщением или алерт с ошибкой")
}

// Ниже — зелёные тесты: документируют уже работающее поведение, чтобы оно не
// сломалось при правках. Находками не являются.

// Telegram доставляет апдейты параллельно, поэтому дабл-клик по «Да, перевыпустить»
// приходит двумя горутинами одновременно: мьютекс и кулдаун обязаны пропустить
// ровно один перевыпуск, иначе второй сбросит только что подключённые устройства.
func TestConcurrentRevokeConfirmHitsPanelOnceBecauseMutexAndCooldownSerializeDoubleClick(t *testing.T) {
	const telegramID = int64(9203)
	b, counters := edgeCaseBot(t, telegramID, map[string]any{
		"uuid":            "uuid-sub",
		"shortUuid":       "oldshort",
		"username":        "user",
		"status":          remnawave.StatusActive,
		"expireAt":        time.Now().UTC().AddDate(0, 0, 20).Format(time.RFC3339),
		"subscriptionUrl": "https://sub.example.com:8443/oldshort",
	})

	var wg sync.WaitGroup
	var succeeded atomic.Int32
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := b.applyRevoke(telegramID); err == nil {
				succeeded.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), succeeded.Load())
	assert.Equal(t, int32(1), counters.deleteAll.Load())
	assert.Equal(t, int32(1), counters.revoke.Load())
}

// Счётчик устройств — вспомогательный вызов панели: его падение не должно
// лишать пользователя статуса и ссылки подписки.
func TestSubscriptionCardStillShowsLinkWhenDeviceCountRequestFailsBecauseCountIsOptionalDecoration(t *testing.T) {
	const telegramID = int64(9204)

	b, db := setupTestBot(t)
	_, err := db.CreateUser(telegramID, "user", "User", "uuid-sub", nil, nil)
	require.NoError(t, err)

	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if strings.HasPrefix(r.URL.Path, "/api/hwid/devices/") {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(strings.NewReader(`{"message":"boom"}`)),
					Header:     make(http.Header),
				}, nil
			}
			body, err := json.Marshal(map[string]any{"response": map[string]any{
				"uuid":            "uuid-sub",
				"shortUuid":       "oldshort",
				"username":        "user",
				"status":          remnawave.StatusActive,
				"expireAt":        time.Now().UTC().AddDate(0, 0, 20).Format(time.RFC3339),
				"subscriptionUrl": "https://sub.example.com:8443/oldshort",
			}})
			require.NoError(t, err)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(string(body))),
				Header:     make(http.Header),
			}, nil
		}),
	})
	b.remnawave = client

	ctx := &MockContext{sender: &tele.User{ID: telegramID}, message: &tele.Message{}}
	require.NoError(t, b.handleStatus(ctx))

	msg, ok := ctx.sentMsg.(string)
	require.True(t, ok)
	assert.Contains(t, msg, "oldshort")
	assert.NotContains(t, msg, "<b>Устройства:</b>")
}
