package bot

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/funnels"
	"github.com/fus1ond/vpn_bot/internal/journal"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const systemEventsUserID int64 = 4242

var systemEventsRef = remnawave.UserRef{UUID: "uuid-4242"}

// setupSystemEventsBot — бот планировщика с платящим пользователем, журналом в
// памяти и подменённым Telegram, который принимает любое сообщение.
func setupSystemEventsBot(t *testing.T) (*Bot, *fakeRecorder, *telegramCapture) {
	t.Helper()
	b, db := setupTestBot(t)
	_, err := db.CreateUser(systemEventsUserID, "u", "U", strPtrTest("uuid-4242"), nil, intPtrTest(400), nil)
	require.NoError(t, err)
	recorder := &fakeRecorder{}
	b.events = recorder
	capture := captureTelegram(t, b)
	return b, recorder, capture
}

// systemActions — id Действий Системных событий пользователя по порядку.
func systemActions(t *testing.T, recorder *fakeRecorder, telegramID int64) []string {
	t.Helper()
	var actions []string
	for _, event := range recorder.recorded() {
		if event.TelegramID != telegramID {
			continue
		}
		assert.Equal(t, journal.SourceBot, event.Source, event.Action)
		assert.Empty(t, event.Param, event.Action)
		assert.False(t, event.At.IsZero(), event.Action)
		actions = append(actions, event.Action)
	}
	return actions
}

// Напоминание за 3 дня ушло — в журнале Системное событие от бота. Повторный
// проход напоминание не повторяет, и События тоже нет.
func TestScheduler_Reminder3dRecordsSystemEvent(t *testing.T) {
	b, recorder, capture := setupSystemEventsBot(t)
	expireAt := time.Now().UTC().Add(60 * time.Hour)

	b.processPaidUser(systemEventsUserID, systemEventsRef, expireAt, time.Now().UTC())
	b.processPaidUser(systemEventsUserID, systemEventsRef, expireAt, time.Now().UTC())

	require.Len(t, capture.matching("заканчивается через 3 дня"), 1)
	assert.Equal(t, []string{funnels.ActionReminder3d}, systemActions(t, recorder, systemEventsUserID))
}

// Напоминание за сутки — своё Действие.
func TestScheduler_Reminder1dRecordsSystemEvent(t *testing.T) {
	b, recorder, capture := setupSystemEventsBot(t)
	expireAt := time.Now().UTC().Add(12 * time.Hour)

	b.processPaidUser(systemEventsUserID, systemEventsRef, expireAt, time.Now().UTC())

	require.Len(t, capture.matching("менее чем через 24 часа"), 1)
	assert.Equal(t, []string{funnels.ActionReminder1d}, systemActions(t, recorder, systemEventsUserID))
}

// Подписка истекла: сообщение об отключении ушло — Системное событие
// «отключение». Кик после grace в журнал не пишется: он уже лежит в таблице
// приглашений.
func TestScheduler_DisableRecordedAndAutoKickIsNot(t *testing.T) {
	b, recorder, capture := setupSystemEventsBot(t)
	expireAt := time.Now().UTC().Add(-time.Hour)

	b.processPaidUser(systemEventsUserID, systemEventsRef, expireAt, time.Now().UTC())
	require.Len(t, capture.matching("Ваша подписка истекла"), 1)
	assert.Equal(t, []string{funnels.ActionExpiredNotice}, systemActions(t, recorder, systemEventsUserID))

	// Перед киком бот перечитывает пользователя в панели: он отключён.
	panel := newTestPanelClient()
	panel.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"response":{"uuid":"uuid-4242","username":"u","status":"DISABLED","expireAt":"` +
			expireAt.Format(time.RFC3339) + `"}}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(body))}, nil
	})})
	b.remnawave = panel

	b.processPaidUser(systemEventsUserID, systemEventsRef, expireAt, expireAt.Add(73*time.Hour))
	require.Len(t, capture.matching("Ваш доступ удалён"), 1, "кик после grace должен состояться")
	assert.Equal(t, []string{funnels.ActionExpiredNotice}, systemActions(t, recorder, systemEventsUserID))
}

// Напоминание о конце триала и кик по его окончании Системными событиями не
// пишутся: спека просит только напоминания об оплаченной подписке и отключение.
func TestScheduler_TrialNoticesAreNotRecorded(t *testing.T) {
	b, recorder, capture := setupSystemEventsBot(t)
	expireAt := time.Now().UTC().Add(12 * time.Hour)

	b.processTrialUser(systemEventsUserID, systemEventsRef, expireAt, time.Now().UTC())
	require.Len(t, capture.matching("пробный период заканчивается"), 1)

	b.processTrialUser(systemEventsUserID, systemEventsRef, expireAt, expireAt.Add(time.Hour))
	require.Len(t, capture.matching("пробный период закончился"), 1, "кик триала должен состояться")

	assert.Empty(t, systemActions(t, recorder, systemEventsUserID))
}

// Telegram сообщение не принял (бот заблокирован) — напоминания не было, и
// События тоже нет.
func TestScheduler_UndeliveredNoticeIsNotRecorded(t *testing.T) {
	b, recorder, _ := setupSystemEventsBot(t)
	b.bot = newOfflineTelegramBotForTest(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader(`{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`)),
			Header:     make(http.Header),
		}, nil
	}))

	b.processPaidUser(systemEventsUserID, systemEventsRef, time.Now().UTC().Add(60*time.Hour), time.Now().UTC())
	b.processPaidUser(systemEventsUserID, systemEventsRef, time.Now().UTC().Add(-time.Hour), time.Now().UTC())

	assert.Empty(t, systemActions(t, recorder, systemEventsUserID))
}

// В режиме обслуживания отключения нет, но сообщение об истечении уходит — и
// Системное событие о нём пишется: Действие названо по сообщению.
func TestScheduler_ExpiredNoticeInMaintenanceIsRecorded(t *testing.T) {
	expireAt := time.Now().UTC().Add(-time.Hour)
	stub := &arEdgeStub{expireAt: expireAt}
	b, db, capture := setupAutorenewEdgeBot(t, stub)
	require.NoError(t, db.SetAutorenewEnabled(arEdgeUserID, false))
	b.setMaintenanceMode(true)
	recorder := &fakeRecorder{}
	b.events = recorder

	b.processPaidUser(arEdgeUserID, edgeRef, expireAt, time.Now().UTC())

	require.Len(t, capture.matching("Ваша подписка истекла"), 1)
	assert.Zero(t, stub.panelDisables(), "в режиме обслуживания не отключаем")
	assert.Equal(t, []string{funnels.ActionExpiredNotice}, systemActions(t, recorder, arEdgeUserID))
}

// Напоминание, подавленное автопродлением, не отправлено — и не записано.
func TestScheduler_ReminderSuppressedByAutorenewIsNotRecorded(t *testing.T) {
	expireAt := time.Now().UTC().Add(12 * time.Hour)
	b, _, capture := setupAutorenewEdgeBot(t, &arEdgeStub{expireAt: expireAt})
	require.True(t, b.autorenewSuppressesExpiryNotice(arEdgeUserID, expireAt))
	recorder := &fakeRecorder{}
	b.events = recorder

	b.processPaidUser(arEdgeUserID, edgeRef, expireAt, time.Now().UTC())

	assert.Empty(t, capture.matching("менее чем через 24 часа"))
	assert.Empty(t, recorder.recorded())
}
