package bot

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
)

func TestNextMonthExpireAt(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		status string
		expire time.Time
		want   time.Time
	}{
		{
			name:   "активная подписка в будущем — плюсуем к expireAt",
			status: remnawave.StatusActive,
			expire: time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC),
			want:   time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC),
		},
		{
			name:   "истёкшая (не ACTIVE) — считаем от now",
			status: remnawave.StatusExpired,
			expire: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
			want:   time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
		},
		{
			name:   "disabled grace — считаем от now",
			status: remnawave.StatusDisabled,
			expire: time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC),
			want:   time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
		},
		{
			name:   "ACTIVE но дата в прошлом — считаем от now",
			status: remnawave.StatusActive,
			expire: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
			want:   time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
		},
		{
			name:   "LIMITED (исчерпан трафик триала) в будущем — плюсуем к expireAt, не теряем остаток",
			status: remnawave.StatusLimited,
			expire: time.Date(2026, 3, 12, 12, 0, 0, 0, time.UTC),
			want:   time.Date(2026, 4, 12, 12, 0, 0, 0, time.UTC),
		},
		{
			name:   "LIMITED и дата в прошлом — считаем от now",
			status: remnawave.StatusLimited,
			expire: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
			want:   time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remUser := &remnawave.User{Status: tt.status, ExpireAt: tt.expire}
			got := nextMonthExpireAt(remUser, now)
			if !got.Equal(tt.want) {
				t.Errorf("nextMonthExpireAt() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseAdminExtendTargetID(t *testing.T) {
	// валидный
	c := &MockContext{args: []string{"12345"}}
	id, ok := parseAdminExtendTargetID(c)
	if !ok || id != 12345 {
		t.Errorf("ожидали (12345,true), got (%d,%v)", id, ok)
	}
	// пустой
	c2 := &MockContext{args: nil}
	if _, ok := parseAdminExtendTargetID(c2); ok {
		t.Error("ожидали ok=false для пустых args")
	}
	// невалидный
	c3 := &MockContext{args: []string{"abc"}}
	if _, ok := parseAdminExtendTargetID(c3); ok {
		t.Error("ожидали ok=false для нечислового args")
	}
}

// TestHandleAdminExtendConfirm_NotAdmin проверяет, что не-админ не может продлить подписку.
func TestHandleAdminExtendConfirm_NotAdmin(t *testing.T) {
	dbFile := "test_admin_extend_not_admin.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	adminID := int64(999999)
	targetID := int64(12345)

	var patchCount int
	client := newTestPanelClient()
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodPatch {
				patchCount++
			}
			return nil, assert.AnError
		}),
	})

	b := &Bot{
		db:         db,
		remnawave:  client,
		config:     &config.Config{AdminID: adminID},
		userStates: newStateMap(),
	}

	// Отправитель — НЕ админ.
	ctx := &MockContext{sender: &tele.User{ID: 111}, args: []string{strconv.FormatInt(targetID, 10)}}
	err = b.handleAdminExtendConfirm(ctx)
	require.NoError(t, err)

	assert.Equal(t, 0, patchCount, "EnableUser не должен вызываться для не-админа")
	assert.True(t, ctx.responded)
}

// TestHandleAdminExtendConfirm_InvalidTargetID проверяет обработку некорректного targetID.
func TestHandleAdminExtendConfirm_InvalidTargetID(t *testing.T) {
	dbFile := "test_admin_extend_invalid_id.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	adminID := int64(999999)

	var patchCount int
	client := newTestPanelClient()
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodPatch {
				patchCount++
			}
			return nil, assert.AnError
		}),
	})

	b := &Bot{
		db:         db,
		remnawave:  client,
		config:     &config.Config{AdminID: adminID},
		userStates: newStateMap(),
	}

	ctx := &MockContext{sender: &tele.User{ID: adminID}, args: nil}
	err = b.handleAdminExtendConfirm(ctx)
	require.NoError(t, err)

	assert.Equal(t, 0, patchCount)
	assert.True(t, ctx.responded)
	assert.Equal(t, "Некорректный запрос", ctx.alertText)
}

// TestHandleAdminExtendConfirm_Success проверяет успешное продление: PATCH с trafficLimitBytes=0
// и корректным expireAt, очистку уведомлений и сообщение пользователю.
func TestHandleAdminExtendConfirm_Success(t *testing.T) {
	dbFile := "test_admin_extend_success.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	adminID := int64(999999)
	targetID := int64(12345)

	_, err = db.CreateUser(targetID, "target", "Target", strPtrTest("uuid-target"), nil, nil, nil)
	require.NoError(t, err)
	require.NoError(t, db.MarkNotificationSent(targetID, "expire_3d"))

	// Подписка активна и истекает через 10 дней от текущего момента —
	// nextMonthExpireAt должна прибавить месяц именно к этой дате.
	currentExpireAt := time.Now().UTC().AddDate(0, 0, 10)
	wantExpireAt := currentExpireAt.AddDate(0, 1, 0)

	var patchCount int
	var lastPatchReq remnawave.UpdateUserRequest

	client := newTestPanelClient()
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/users/uuid-target":
				payload := fmt.Sprintf(`{"response":{"uuid":"uuid-target","telegramId":12345,"username":"target","status":"ACTIVE","expireAt":"%s"}}`, currentExpireAt.Format(time.RFC3339))
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			case r.Method == http.MethodGet && r.URL.Path == "/api/users/by-telegram-id/12345":
				payload := fmt.Sprintf(`{"response":[{"uuid":"uuid-target","telegramId":12345,"username":"target","status":"ACTIVE","expireAt":"%s"}]}`, wantExpireAt.Format(time.RFC3339))
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			case r.Method == http.MethodPatch && r.URL.Path == "/api/users":
				patchCount++
				require.NoError(t, json.NewDecoder(r.Body).Decode(&lastPatchReq))
				payload := `{"response":{"uuid":"uuid-target"}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			default:
				return nil, assert.AnError
			}
		}),
	})

	b := &Bot{
		db:         db,
		remnawave:  client,
		config:     &config.Config{AdminID: adminID},
		userStates: newStateMap(),
	}

	ctx := &MockContext{sender: &tele.User{ID: adminID}, args: []string{strconv.FormatInt(targetID, 10)}}
	err = b.handleAdminExtendConfirm(ctx)
	require.NoError(t, err)

	require.Equal(t, 1, patchCount)
	require.NotNil(t, lastPatchReq.TrafficLimitBytes)
	assert.Equal(t, int64(0), *lastPatchReq.TrafficLimitBytes)
	require.NotNil(t, lastPatchReq.ExpireAt)
	assert.Equal(t, wantExpireAt.Format(time.RFC3339), *lastPatchReq.ExpireAt)

	sent, err := db.WasNotificationSent(targetID, "expire_3d")
	require.NoError(t, err)
	assert.False(t, sent, "уведомления должны быть очищены после продления")

	editedMsg, ok := ctx.editedMsg.(string)
	require.True(t, ok)
	assert.Contains(t, editedMsg, "Подписка продлена")
	assert.True(t, ctx.responded)
}

// TestHandleAdminExtendConfirm_DoubleClickCooldown проверяет, что повторное
// подтверждение продления сразу после успешного (дабл-клик/ретрай Telegram) не
// продлевает подписку второй раз — второй PATCH не должен отправиться.
func TestHandleAdminExtendConfirm_DoubleClickCooldown(t *testing.T) {
	dbFile := "test_admin_extend_cooldown.db"
	db, err := database.New(dbFile)
	require.NoError(t, err)
	defer func() {
		db.Close()
		os.Remove(dbFile)
	}()

	adminID := int64(999999)
	targetID := int64(12345)

	_, err = db.CreateUser(targetID, "target", "Target", strPtrTest("uuid-target"), nil, nil, nil)
	require.NoError(t, err)

	currentExpireAt := time.Now().UTC().AddDate(0, 0, 10)

	var patchCount int

	client := newTestPanelClient()
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/users/uuid-target":
				payload := fmt.Sprintf(`{"response":{"uuid":"uuid-target","telegramId":12345,"username":"target","status":"ACTIVE","expireAt":"%s"}}`, currentExpireAt.Format(time.RFC3339))
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			case r.Method == http.MethodPatch && r.URL.Path == "/api/users":
				patchCount++
				payload := `{"response":{"uuid":"uuid-target"}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload)),
					Header:     make(http.Header),
				}, nil
			default:
				return nil, assert.AnError
			}
		}),
	})

	b := &Bot{
		db:         db,
		remnawave:  client,
		config:     &config.Config{AdminID: adminID},
		userStates: newStateMap(),
	}

	ctx1 := &MockContext{sender: &tele.User{ID: adminID}, args: []string{strconv.FormatInt(targetID, 10)}}
	require.NoError(t, b.handleAdminExtendConfirm(ctx1))
	require.Equal(t, 1, patchCount, "первое подтверждение должно продлить подписку")

	ctx2 := &MockContext{sender: &tele.User{ID: adminID}, args: []string{strconv.FormatInt(targetID, 10)}}
	require.NoError(t, b.handleAdminExtendConfirm(ctx2))
	assert.Equal(t, 1, patchCount, "повторное подтверждение в течение кулдауна не должно продлевать ещё раз")
	assert.Equal(t, "Подписка уже продлена, повторное нажатие проигнорировано", ctx2.alertText)
}

// TestHandleAdminExtendCancel проверяет отмену продления.
func TestHandleAdminExtendCancel(t *testing.T) {
	adminID := int64(999999)
	b := &Bot{
		config: &config.Config{AdminID: adminID},
	}

	ctx := &MockContext{sender: &tele.User{ID: adminID}}
	err := b.handleAdminExtendCancel(ctx)
	require.NoError(t, err)

	editedMsg, ok := ctx.editedMsg.(string)
	require.True(t, ok)
	assert.Equal(t, "Продление отменено.", editedMsg)
	assert.True(t, ctx.responded)
}
