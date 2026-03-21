package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadRemnawaveSquadUUIDs(t *testing.T) {
	setRequiredEnv(t)

	t.Run("читает список сквадов из нового env", func(t *testing.T) {
		t.Setenv("REMNAWAVE_DEFAULT_SQUAD_UUIDS", "uuid-1, uuid-2 , ,uuid-3")
		t.Setenv("REMNAWAVE_DEFAULT_SQUAD_UUID", "")

		cfg, err := Load()
		require.NoError(t, err)
		require.Equal(t, []string{"uuid-1", "uuid-2", "uuid-3"}, cfg.RemnawaveSquadUUIDs)
	})

	t.Run("использует legacy env как fallback", func(t *testing.T) {
		t.Setenv("REMNAWAVE_DEFAULT_SQUAD_UUIDS", "")
		t.Setenv("REMNAWAVE_DEFAULT_SQUAD_UUID", "legacy-uuid")

		cfg, err := Load()
		require.NoError(t, err)
		require.Equal(t, []string{"legacy-uuid"}, cfg.RemnawaveSquadUUIDs)
	})
}

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("BOT_TOKEN", "test-token")
	t.Setenv("ADMIN_ID", "123456")
	t.Setenv("REMNAWAVE_URL", "https://panel.example.com")
	t.Setenv("REMNAWAVE_API_TOKEN", "test-api-token")
	t.Setenv("DB_PATH", "/tmp/test.db")
}
