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
	t.Setenv("PRIVACY_POLICY_URL", "https://example.com/privacy")
	t.Setenv("TERMS_OF_SERVICE_URL", "https://example.com/terms")
	t.Setenv("SUPPORT_CONTACT", "@support_user")
}

func TestLoadInfoEnvRequired(t *testing.T) {
	// Проверяем, что каждая из трёх новых переменных обязательна
	// и её отсутствие приводит к понятной ошибке при старте.
	cases := []struct {
		name    string
		envKey  string
		wantErr string
	}{
		{"без PRIVACY_POLICY_URL", "PRIVACY_POLICY_URL", "PRIVACY_POLICY_URL is required"},
		{"без TERMS_OF_SERVICE_URL", "TERMS_OF_SERVICE_URL", "TERMS_OF_SERVICE_URL is required"},
		{"без SUPPORT_CONTACT", "SUPPORT_CONTACT", "SUPPORT_CONTACT is required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv(tc.envKey, "")

			_, err := Load()
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestLoadInfoEnvRead(t *testing.T) {
	// Проверяем, что значения читаются из окружения без изменений.
	setRequiredEnv(t)
	t.Setenv("PRIVACY_POLICY_URL", "https://legal.example.com/privacy")
	t.Setenv("TERMS_OF_SERVICE_URL", "https://legal.example.com/terms")
	t.Setenv("SUPPORT_CONTACT", "@fus1ond")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "https://legal.example.com/privacy", cfg.PrivacyPolicyURL)
	require.Equal(t, "https://legal.example.com/terms", cfg.TermsOfServiceURL)
	require.Equal(t, "@fus1ond", cfg.SupportContact)
}
