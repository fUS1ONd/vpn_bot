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

func TestLoadPlategaConfig(t *testing.T) {
	setRequiredEnv(t)

	t.Run("использует значения по умолчанию если переменные не заданы", func(t *testing.T) {
		t.Setenv("PLATEGA_MERCHANT_ID", "")
		t.Setenv("PLATEGA_SECRET", "")
		t.Setenv("PLATEGA_CALLBACK_URL", "")
		t.Setenv("MIN_SUBSCRIPTION_PRICE", "")
		t.Setenv("TRIAL_TRAFFIC_LIMIT_GB", "")
		t.Setenv("PLATEGA_FEE_SBP", "")
		t.Setenv("PLATEGA_FEE_CARD", "")
		t.Setenv("PLATEGA_FEE_CRYPTO", "")
		t.Setenv("PLATEGA_FEE_WITHDRAWAL", "")

		cfg, err := Load()
		require.NoError(t, err)
		require.Equal(t, "", cfg.PlategaMerchantID)
		require.Equal(t, "", cfg.PlategaSecret)
		require.Equal(t, "", cfg.PlategaCallbackURL)
		require.Equal(t, 400, cfg.MinSubscriptionPrice)
		require.Equal(t, 1, cfg.TrialTrafficLimitGB)
		require.Equal(t, 11, cfg.PlategaFeeSBP)
		require.Equal(t, 12, cfg.PlategaFeeCard)
		require.Equal(t, 5, cfg.PlategaFeeCrypto)
		require.Equal(t, 2, cfg.PlategaFeeWithdrawal)
	})

	t.Run("читает Platega-переменные из окружения", func(t *testing.T) {
		t.Setenv("PLATEGA_MERCHANT_ID", "merchant-123")
		t.Setenv("PLATEGA_SECRET", "secret-abc")
		t.Setenv("PLATEGA_CALLBACK_URL", "https://example.com/platega/callback")
		t.Setenv("MIN_SUBSCRIPTION_PRICE", "500")
		t.Setenv("PLATEGA_FEE_SBP", "9")

		cfg, err := Load()
		require.NoError(t, err)
		require.Equal(t, "merchant-123", cfg.PlategaMerchantID)
		require.Equal(t, "secret-abc", cfg.PlategaSecret)
		require.Equal(t, "https://example.com/platega/callback", cfg.PlategaCallbackURL)
		require.Equal(t, 500, cfg.MinSubscriptionPrice)
		require.Equal(t, 9, cfg.PlategaFeeSBP)
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
