package bot

import (
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

// newUserRefBot собирает бота с временной базой и панелью заданной версии.
func newUserRefBot(t *testing.T, dbFile string, version remnawave.APIVersion, handler roundTripFunc) (*Bot, *database.DB) {
	t.Helper()

	db, err := database.New(dbFile)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbFile)
	})

	client := newTestPanelClient()
	client.SetAPIVersion(version)
	client.SetHTTPClient(&http.Client{Transport: handler})

	return &Bot{
		db:         db,
		remnawave:  client,
		config:     &config.Config{AdminID: 1},
		userStates: newStateMap(),
	}, db
}

func panelJSON(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// Связка уже полная — ходить в панель незачем.
func TestUserRefUsesStoredLink(t *testing.T) {
	b, db := newUserRefBot(t, "test_userref_stored.db", remnawave.APIVersionV3,
		func(r *http.Request) (*http.Response, error) {
			t.Fatalf("запрос в панель не нужен: %s", r.URL.Path)
			return nil, nil
		})

	_, err := db.CreateUser(5001, "alice", "Alice", strPtrTest("uuid-5001"), i64PtrTest(31), nil, nil)
	require.NoError(t, err)

	ref, err := b.userRef(5001)
	require.NoError(t, err)
	assert.Equal(t, remnawave.UserRef{UUID: "uuid-5001", ID: 31}, ref)
}

// Бот впервые запустился уже на 3.x: в базе только UUID, резолвить по нему нечего,
// спасает поиск по telegram_id. Найденный id сразу сохраняется.
func TestUserRefRecoversIDOnV3(t *testing.T) {
	var streamCalls int
	b, db := newUserRefBot(t, "test_userref_recover.db", remnawave.APIVersionV3,
		func(r *http.Request) (*http.Response, error) {
			require.Equal(t, "/api/users/stream", r.URL.Path)
			require.Equal(t, "5002", r.URL.Query().Get("telegramId"))
			streamCalls++
			return panelJSON(`{"response":{"users":[{"id":42,"telegramId":5002,"username":"bob"}],"hasMore":false}}`), nil
		})

	_, err := db.CreateUser(5002, "bob", "Bob", strPtrTest("uuid-5002"), nil, nil, nil)
	require.NoError(t, err)

	ref, err := b.userRef(5002)
	require.NoError(t, err)
	assert.Equal(t, int64(42), ref.ID)

	stored, err := db.GetUserByTelegramID(5002)
	require.NoError(t, err)
	require.NotNil(t, stored.RemnawaveID)
	assert.Equal(t, int64(42), *stored.RemnawaveID)

	// Повторный вызов берёт связку из базы, а не ищет заново.
	_, err = b.userRef(5002)
	require.NoError(t, err)
	assert.Equal(t, 1, streamCalls)
}

// На 2.8.x UUID самодостаточен: восстановление id — работа backfill, а не
// каждого обращения к пользователю.
func TestUserRefKeepsUUIDOnV2(t *testing.T) {
	b, db := newUserRefBot(t, "test_userref_v2.db", remnawave.APIVersionV2,
		func(r *http.Request) (*http.Response, error) {
			t.Fatalf("запрос в панель не нужен: %s", r.URL.Path)
			return nil, nil
		})

	_, err := db.CreateUser(5003, "carol", "Carol", strPtrTest("uuid-5003"), nil, nil, nil)
	require.NoError(t, err)

	ref, err := b.userRef(5003)
	require.NoError(t, err)
	assert.Equal(t, remnawave.UserRef{UUID: "uuid-5003"}, ref)
}

func TestUserRefFailsForUnknownUser(t *testing.T) {
	b, _ := newUserRefBot(t, "test_userref_unknown.db", remnawave.APIVersionV3,
		func(r *http.Request) (*http.Response, error) {
			t.Fatalf("запрос в панель не нужен: %s", r.URL.Path)
			return nil, nil
		})

	_, err := b.userRef(5004)
	require.ErrorIs(t, err, errUserNotRegistered)
}

// Пользователь есть в БД, но в панели его нет: ленивое восстановление обязано
// вернуть ErrUserNotFound, чтобы сработали существующие ветки (reconcile, автокик).
func TestUserRefReportsMissingPanelUser(t *testing.T) {
	b, db := newUserRefBot(t, "test_userref_absent.db", remnawave.APIVersionV3,
		func(r *http.Request) (*http.Response, error) {
			return panelJSON(`{"response":{"users":[],"hasMore":false}}`), nil
		})

	_, err := db.CreateUser(5005, "dave", "Dave", strPtrTest("uuid-5005"), nil, nil, nil)
	require.NoError(t, err)

	_, err = b.userRef(5005)
	require.ErrorIs(t, err, remnawave.ErrUserNotFound)
}

// По одному telegram_id нашлось двое: связку не пишем — иначе с некоторой
// вероятностью привяжем к платежу чужой аккаунт.
func TestUserRefRefusesAmbiguousMatch(t *testing.T) {
	b, db := newUserRefBot(t, "test_userref_ambiguous.db", remnawave.APIVersionV3,
		func(r *http.Request) (*http.Response, error) {
			return panelJSON(`{"response":{"users":[{"id":7,"telegramId":5006},{"id":8,"telegramId":5006}],"hasMore":true}}`), nil
		})

	_, err := db.CreateUser(5006, "eve", "Eve", strPtrTest("uuid-5006"), nil, nil, nil)
	require.NoError(t, err)

	_, err = b.userRef(5006)
	require.ErrorIs(t, err, remnawave.ErrMultipleUsersForTelegramID)

	stored, err := db.GetUserByTelegramID(5006)
	require.NoError(t, err)
	assert.Nil(t, stored.RemnawaveID, "при неоднозначности связка не должна записаться")
}

// Неоднозначность должна не просто останавливать запись, а быть видна владельцу:
// разбирать такое всё равно придётся руками.
func TestUserRefAlertsOwnerOnAmbiguousMatch(t *testing.T) {
	b, db := newUserRefBot(t, "test_userref_ambiguous_alert.db", remnawave.APIVersionV3,
		func(r *http.Request) (*http.Response, error) {
			return panelJSON(`{"response":{"users":[{"id":7,"telegramId":5007},{"id":8,"telegramId":5007}],"hasMore":true}}`), nil
		})
	b.config.AdminID = 999
	capture := captureTelegram(t, b)

	_, err := db.CreateUser(5007, "dup", "Dup", strPtrTest("uuid-5007"), nil, nil, nil)
	require.NoError(t, err)

	_, err = b.userRef(5007)
	require.ErrorIs(t, err, remnawave.ErrMultipleUsersForTelegramID)

	alerts := capture.matching("несколько пользователей с telegram_id 5007")
	require.Len(t, alerts, 1)
	assert.Equal(t, "999", alerts[0].ChatID)
}

// Плановая доливка: один и тот же id панели уже привязан к другому Telegram ID.
// Это рассинхрон — логируем, сообщаем владельцу и продолжаем проход, а не падаем.
func TestSchedulerUserRefSurvivesUniqueConflict(t *testing.T) {
	b, db := newUserRefBot(t, "test_userref_scheduler_conflict.db", remnawave.APIVersionV2,
		func(r *http.Request) (*http.Response, error) {
			t.Fatalf("доливка не должна ходить в панель: %s", r.URL.Path)
			return nil, nil
		})
	b.config.AdminID = 999
	capture := captureTelegram(t, b)

	// 5008 уже держит id=71, а панель отдаёт тот же id для 5009.
	_, err := db.CreateUser(5008, "owner", "Owner", strPtrTest("uuid-5008"), i64PtrTest(71), nil, nil)
	require.NoError(t, err)
	_, err = db.CreateUser(5009, "other", "Other", strPtrTest("uuid-5009"), nil, nil, nil)
	require.NoError(t, err)

	stored, err := db.GetUserByTelegramID(5009)
	require.NoError(t, err)

	ref := b.schedulerUserRef(*stored, remnawave.User{ID: 71, TelegramID: i64PtrTest(5009)})

	// Ссылка осталась прежней (только UUID), связка не перезаписана.
	assert.Equal(t, remnawave.UserRef{UUID: "uuid-5009"}, ref)

	after, err := db.GetUserByTelegramID(5009)
	require.NoError(t, err)
	assert.Nil(t, after.RemnawaveID)

	require.Len(t, capture.matching("Рассинхрон связки"), 1)
}

// Доливка в проходе штатно записывает связку, когда конфликта нет.
func TestSchedulerUserRefLinksMissingID(t *testing.T) {
	b, db := newUserRefBot(t, "test_userref_scheduler_link.db", remnawave.APIVersionV2,
		func(r *http.Request) (*http.Response, error) {
			t.Fatalf("доливка не должна ходить в панель: %s", r.URL.Path)
			return nil, nil
		})

	_, err := db.CreateUser(5010, "linkme", "LinkMe", strPtrTest("uuid-5010"), nil, nil, nil)
	require.NoError(t, err)

	stored, err := db.GetUserByTelegramID(5010)
	require.NoError(t, err)

	ref := b.schedulerUserRef(*stored, remnawave.User{ID: 81, TelegramID: i64PtrTest(5010)})
	assert.Equal(t, remnawave.UserRef{UUID: "uuid-5010", ID: 81}, ref)

	after, err := db.GetUserByTelegramID(5010)
	require.NoError(t, err)
	require.NotNil(t, after.RemnawaveID)
	assert.Equal(t, int64(81), *after.RemnawaveID)
}
