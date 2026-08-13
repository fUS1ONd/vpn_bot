package bot

import (
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

// newBackfillFixture — база и клиент панели заданной версии.
func newBackfillFixture(t *testing.T, dbFile string, version remnawave.APIVersion, handler roundTripFunc) (*database.DB, *remnawave.Client) {
	t.Helper()

	db, err := database.New(dbFile)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		os.Remove(dbFile)
	})

	client := remnawave.NewClient("https://panel.example.com", "test-token", nil)
	client.SetAPIVersion(version)
	client.SetHTTPClient(&http.Client{Transport: handler})

	return db, client
}

// Backfill на 2.8.x — одна операция на всю базу: список пользователей панели
// читается разом, никаких запросов на пользователя.
func TestBackfillLinksByUUIDOnV2(t *testing.T) {
	var listCalls int
	db, client := newBackfillFixture(t, "test_backfill_v2.db", remnawave.APIVersionV2,
		func(r *http.Request) (*http.Response, error) {
			require.Equal(t, "/api/users", r.URL.Path)
			listCalls++
			return panelJSON(`{"response":{"total":2,"users":[
				{"uuid":"uuid-6001","id":11,"telegramId":6001},
				{"uuid":"uuid-6002","id":12,"telegramId":6002}
			]}}`), nil
		})

	_, err := db.CreateUser(6001, "alice", "Alice", strPtrTest("uuid-6001"), nil, nil, nil)
	require.NoError(t, err)
	_, err = db.CreateUser(6002, "bob", "Bob", strPtrTest("uuid-6002"), i64PtrTest(12), nil, nil)
	require.NoError(t, err)

	stats, err := BackfillRemnawaveIDs(db, client)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Linked)
	assert.Equal(t, 0, stats.Missing)
	assert.Equal(t, 1, listCalls, "backfill на 2.8.x — один запрос на всю базу")

	alice, err := db.GetUserByTelegramID(6001)
	require.NoError(t, err)
	require.NotNil(t, alice.RemnawaveID)
	assert.Equal(t, int64(11), *alice.RemnawaveID)
}

// Кого панель не знает вовсе — оставляем NULL и перечисляем в логе; это кандидат
// на ручной разбор, а не повод ронять старт.
func TestBackfillLeavesUnknownUsersAlone(t *testing.T) {
	db, client := newBackfillFixture(t, "test_backfill_unknown.db", remnawave.APIVersionV2,
		func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/api/users":
				return panelJSON(`{"response":{"total":0,"users":[]}}`), nil
			case "/api/users/resolve":
				return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody, Header: make(http.Header)}, nil
			}
			t.Fatalf("неожиданный путь: %s", r.URL.Path)
			return nil, nil
		})

	_, err := db.CreateUser(6010, "ghost", "Ghost", strPtrTest("uuid-6010"), nil, nil, nil)
	require.NoError(t, err)

	stats, err := BackfillRemnawaveIDs(db, client)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.Linked)
	assert.Equal(t, 1, stats.Missing)

	user, err := db.GetUserByTelegramID(6010)
	require.NoError(t, err)
	assert.Nil(t, user.RemnawaveID)
}

// Пользователь появился после прохода по списку — добирается точечно через resolve,
// который на 2.8.1 возвращает id по uuid.
func TestBackfillResolvesLeftoverByUUIDOnV2(t *testing.T) {
	db, client := newBackfillFixture(t, "test_backfill_resolve.db", remnawave.APIVersionV2,
		func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/api/users":
				return panelJSON(`{"response":{"total":0,"users":[]}}`), nil
			case "/api/users/resolve":
				return panelJSON(`{"response":{"uuid":"uuid-6020","id":33,"username":"late","shortUuid":"s"}}`), nil
			}
			t.Fatalf("неожиданный путь: %s", r.URL.Path)
			return nil, nil
		})

	_, err := db.CreateUser(6020, "late", "Late", strPtrTest("uuid-6020"), nil, nil, nil)
	require.NoError(t, err)

	stats, err := BackfillRemnawaveIDs(db, client)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Linked)

	user, err := db.GetUserByTelegramID(6020)
	require.NoError(t, err)
	require.NotNil(t, user.RemnawaveID)
	assert.Equal(t, int64(33), *user.RemnawaveID)
}

// Бот выкачен на уже обновлённую панель: резолв по UUID невозможен, спасает
// поиск по telegram_id — по запросу на пользователя.
func TestBackfillRecoversByTelegramIDOnV3(t *testing.T) {
	db, client := newBackfillFixture(t, "test_backfill_v3.db", remnawave.APIVersionV3,
		func(r *http.Request) (*http.Response, error) {
			require.Equal(t, "/api/users/stream", r.URL.Path)
			switch r.URL.Query().Get("telegramId") {
			case "6030":
				return panelJSON(`{"response":{"users":[{"id":44,"telegramId":6030}],"hasMore":false}}`), nil
			default:
				return panelJSON(`{"response":{"users":[],"hasMore":false}}`), nil
			}
		})

	_, err := db.CreateUser(6030, "carol", "Carol", strPtrTest("uuid-6030"), nil, nil, nil)
	require.NoError(t, err)
	_, err = db.CreateUser(6031, "ghost", "Ghost", strPtrTest("uuid-6031"), nil, nil, nil)
	require.NoError(t, err)

	stats, err := BackfillRemnawaveIDs(db, client)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Linked)
	assert.Equal(t, 1, stats.Missing)

	carol, err := db.GetUserByTelegramID(6030)
	require.NoError(t, err)
	require.NotNil(t, carol.RemnawaveID)
	assert.Equal(t, int64(44), *carol.RemnawaveID)
}

// Дубликат telegramId в панели: связку не пишем, проход по остальным продолжаем.
func TestBackfillSkipsAmbiguousMatchesButContinues(t *testing.T) {
	db, client := newBackfillFixture(t, "test_backfill_ambiguous.db", remnawave.APIVersionV3,
		func(r *http.Request) (*http.Response, error) {
			switch r.URL.Query().Get("telegramId") {
			case "6040":
				return panelJSON(`{"response":{"users":[{"id":51,"telegramId":6040},{"id":52,"telegramId":6040}],"hasMore":true}}`), nil
			default:
				return panelJSON(`{"response":{"users":[{"id":53,"telegramId":6041}],"hasMore":false}}`), nil
			}
		})

	_, err := db.CreateUser(6040, "dup", "Dup", strPtrTest("uuid-6040"), nil, nil, nil)
	require.NoError(t, err)
	_, err = db.CreateUser(6041, "fine", "Fine", strPtrTest("uuid-6041"), nil, nil, nil)
	require.NoError(t, err)

	stats, err := BackfillRemnawaveIDs(db, client)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Linked)
	assert.Equal(t, 1, stats.Failed)

	dup, err := db.GetUserByTelegramID(6040)
	require.NoError(t, err)
	assert.Nil(t, dup.RemnawaveID, "при неоднозначности связка не пишется")

	fine, err := db.GetUserByTelegramID(6041)
	require.NoError(t, err)
	require.NotNil(t, fine.RemnawaveID)
	assert.Equal(t, int64(53), *fine.RemnawaveID)
}

// Нечего делать — в панель не ходим вовсе.
func TestBackfillIsNoopWhenAllLinked(t *testing.T) {
	db, client := newBackfillFixture(t, "test_backfill_noop.db", remnawave.APIVersionV2,
		func(r *http.Request) (*http.Response, error) {
			t.Fatalf("запрос в панель не нужен: %s", r.URL.Path)
			return nil, nil
		})

	_, err := db.CreateUser(6050, "linked", "Linked", strPtrTest("uuid-6050"), i64PtrTest(61), nil, nil)
	require.NoError(t, err)

	stats, err := BackfillRemnawaveIDs(db, client)
	require.NoError(t, err)
	assert.Equal(t, BackfillStats{}, stats)
}
