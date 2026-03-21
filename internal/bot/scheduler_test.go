package bot

import (
	"fmt"
	"io"
	"net/http"
	"os"
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

// setupSchedulerTestBot создаёт бота для тестов scheduler.
func setupSchedulerTestBot(t *testing.T) (*Bot, *database.DB) {
	t.Helper()
	dbFile := fmt.Sprintf("test_scheduler_%s.db", t.Name())
	db, err := database.New(dbFile)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbFile)
	})
	cfg := &config.Config{AdminID: 999}
	b := &Bot{
		db:         db,
		config:     cfg,
		userStates: newStateMap(),
	}
	return b, db
}

// TestHandleAutoKick_404IsNotFatalError проверяет, что при 404 от Remnawave (пользователь
// уже удалён администратором) handleAutoKick всё равно выполняет очистку в БД.
func TestHandleAutoKick_404IsNotFatalError(t *testing.T) {
	b, db := setupSchedulerTestBot(t)

	// Создаём пользователя в БД бота
	_, err := db.CreateUser(700, "victim", "Victim", "uuid-700")
	require.NoError(t, err)
	modID := int64(50)
	_, err = db.CreateUser(modID, "mod", "Mod", "uuid-mod")
	require.NoError(t, err)
	expireDays := 30
	inv, err := db.CreateInviteWithExpiry(modID, &expireDays)
	require.NoError(t, err)
	require.NoError(t, db.ClaimInvite(inv.Code, 700))

	// Настраиваем Remnawave — DELETE возвращает 404 (пользователь уже удалён)
	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodDelete {
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader(`{"error":"not found"}`)),
					Header:     make(http.Header),
				}, nil
			}
			return nil, fmt.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}),
	})
	b.remnawave = client

	// Выполняем автокик
	b.handleAutoKick(700, "uuid-700")

	// Несмотря на 404 в Remnawave, пользователь должен быть удалён из БД бота
	dbUser, err := db.GetUserByTelegramID(700)
	require.NoError(t, err)
	assert.Nil(t, dbUser, "пользователь должен быть удалён из БД даже если Remnawave вернул 404")

	// Инвайт должен быть помечен как кикнутый: used_by остаётся (история), kicked_at проставлен
	invite, err := db.GetInviteByCode(inv.Code)
	require.NoError(t, err)
	require.NotNil(t, invite)
	assert.NotNil(t, invite.UsedBy, "used_by должен остаться — история активации сохраняется")
	assert.NotNil(t, invite.KickedAt, "kicked_at должен быть проставлен после автокика")
}

// TestHandleAutoKick_SkipsAlreadyDeletedInRemnawave проверяет, что функция классификации
// 404-ошибки работает корректно.
func TestHandleAutoKick_SkipsAlreadyDeletedInRemnawave(t *testing.T) {
	err := fmt.Errorf("API error 404: not found")
	assert.True(t, isAutoKickNotFoundError(err), "API error 404 должна распознаваться как not found")

	otherErr := fmt.Errorf("API error 500: internal server error")
	assert.False(t, isAutoKickNotFoundError(otherErr), "API error 500 не должна распознаваться как not found")
}

// TestIsSchedulerForbiddenError проверяет что функция корректно распознаёт
// telebot-ошибки типа "бот заблокирован" без хрупкого strings.Contains("403").
func TestIsSchedulerForbiddenError(t *testing.T) {
	t.Run("ErrBlockedByUser распознаётся", func(t *testing.T) {
		assert.True(t, isSchedulerForbiddenError(tele.ErrBlockedByUser))
	})

	t.Run("ErrUserIsDeactivated распознаётся", func(t *testing.T) {
		assert.True(t, isSchedulerForbiddenError(tele.ErrUserIsDeactivated))
	})

	t.Run("ErrNotStartedByUser распознаётся", func(t *testing.T) {
		assert.True(t, isSchedulerForbiddenError(tele.ErrNotStartedByUser))
	})

	t.Run("Обычная ошибка НЕ распознаётся как Forbidden", func(t *testing.T) {
		assert.False(t, isSchedulerForbiddenError(fmt.Errorf("network timeout")))
	})

	t.Run("Строковая ошибка с 403 НЕ распознаётся (хрупкий паттерн устранён)", func(t *testing.T) {
		// Убеждаемся что новый код НЕ полагается на строку "403"
		assert.False(t, isSchedulerForbiddenError(fmt.Errorf("some error with 403 code")))
	})
}

func TestDecideSubscriptionActions(t *testing.T) {
	now := time.Date(2026, time.March, 4, 12, 0, 0, 0, time.UTC)

	t.Run("За 3 дня до истечения", func(t *testing.T) {
		expireAt := time.Date(2026, time.March, 7, 0, 0, 0, 0, time.UTC)
		decision := decideSubscriptionActions(expireAt, now, true, false, false)
		assert.NotEmpty(t, decision.ThreeDaysMessage)
		assert.Empty(t, decision.ExpireTodayMessage)
		assert.False(t, decision.ShouldKick)
	})

	t.Run("Подписка истекла сегодня", func(t *testing.T) {
		expireAt := time.Date(2026, time.March, 4, 0, 0, 0, 0, time.UTC)
		decision := decideSubscriptionActions(expireAt, now, false, true, false)
		assert.Empty(t, decision.ThreeDaysMessage)
		assert.NotEmpty(t, decision.ExpireTodayMessage)
		assert.Contains(t, decision.ExpireTodayMessage, "куратор больше не обслуживает")
		assert.False(t, decision.ShouldKick)
	})

	t.Run("Автокик через 3 дня после истечения", func(t *testing.T) {
		expireAt := time.Date(2026, time.February, 28, 0, 0, 0, 0, time.UTC)
		decision := decideSubscriptionActions(expireAt, now, true, true, true)
		assert.True(t, decision.ShouldKick)
	})

	t.Run("Повторные уведомления не отправляются", func(t *testing.T) {
		expireAt := time.Date(2026, time.March, 7, 0, 0, 0, 0, time.UTC)
		decision := decideSubscriptionActions(expireAt, now, true, true, true)
		assert.Empty(t, decision.ThreeDaysMessage)
		assert.Empty(t, decision.ExpireTodayMessage)
		assert.False(t, decision.ShouldKick)
	})
}
