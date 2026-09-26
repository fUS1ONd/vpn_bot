package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// restartedServer поднимает заглушку заново поверх того же файла состояния —
// как после stand-down и stand-up.
func restartedServer(t *testing.T, statePath string) *httptest.Server {
	t.Helper()

	s := newStore()
	require.NoError(t, s.load(statePath))
	srv := httptest.NewServer(newServerWith(s, statePath))
	t.Cleanup(srv.Close)
	return srv
}

// Пользователь, заведённый до перезапуска, виден после него — вместе с
// устройствами, — а новый не получает его id: иначе база бота указывала бы на
// чужого пользователя панели.
func TestStateSurvivesRestart(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "mockpanel.json")

	first := restartedServer(t, statePath)
	uuid, id := createUser(t, first, 555)
	_, _ = post(t, first, "/mock/user", `{"telegramId":555,"devices":2}`)
	first.Close()

	second := restartedServer(t, statePath)
	resp, parsed := get(t, second, "/api/users/"+uuid)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.EqualValues(t, 555, parsed["response"].(map[string]any)["telegramId"])

	_, parsed = get(t, second, "/api/hwid/devices/"+uuid)
	require.Len(t, devicesOf(t, parsed), 2)

	_, nextID := createUser(t, second, 556)
	require.Greater(t, nextID, id)
}

// Отказы не сохраняются: забытый включённым отказ не должен пережить перезапуск.
func TestFailureInjectionNotPersisted(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "mockpanel.json")

	first := restartedServer(t, statePath)
	uuid, _ := createUser(t, first, 555)
	_, _ = post(t, first, "/mock/fail", `{"count":1000,"status":500}`)
	first.Close()

	second := restartedServer(t, statePath)
	resp, _ := get(t, second, "/api/users/"+uuid)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// Первый запуск без файла — пустая панель, а не ошибка.
func TestMissingStateIsEmptyPanel(t *testing.T) {
	s := newStore()
	require.NoError(t, s.load(filepath.Join(t.TempDir(), "absent.json")))
	require.Empty(t, s.users)
}

// Битый файл — ошибка, а не молча пустая панель: та вернула бы 404 без объяснения.
func TestCorruptStateFails(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "mockpanel.json")
	require.NoError(t, os.WriteFile(statePath, []byte("{не json"), 0o600))

	require.Error(t, newStore().load(statePath))
}
