package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config содержит всю конфигурацию приложения
type Config struct {
	// Telegram
	BotToken string
	AdminID  int64

	// Remnawave
	RemnawaveURL        string
	RemnawaveAPIToken   string
	RemnawaveSquadUUIDs []string // Опционально, UUID внутренних сквадов по умолчанию

	// База данных
	DBPath string

	// Донат
	DonateText string

	// Мониторинг
	SDConfigsPath      string // Путь к папке sd_configs для targets.json
	VictoriaMetricsURL string // URL VictoriaMetrics API

	// Render-сервис (субтитры)
	RenderURL    string // URL render-сервиса (опционально)
	RenderAPIKey string // API-ключ для render-сервиса
}

// Load читает конфигурацию из переменных окружения
func Load() (*Config, error) {
	// Загрузка .env файла если он существует
	_ = godotenv.Load()

	cfg := &Config{
		BotToken:            os.Getenv("BOT_TOKEN"),
		RemnawaveURL:        os.Getenv("REMNAWAVE_URL"),
		RemnawaveAPIToken:   os.Getenv("REMNAWAVE_API_TOKEN"),
		RemnawaveSquadUUIDs: getRemnawaveSquadUUIDs(),
		DBPath:              getEnvOrDefault("DB_PATH", "/app/data/bot.db"),
		DonateText:          os.Getenv("DONATE_TEXT"),
		SDConfigsPath:       getEnvOrDefault("SD_CONFIGS_PATH", "/app/sd_configs"),
		VictoriaMetricsURL:  getEnvOrDefault("VICTORIA_METRICS_URL", "http://victoriametrics:8428"),
		RenderURL:           os.Getenv("RENDER_URL"),
		RenderAPIKey:        os.Getenv("RENDER_API_KEY"),
	}

	// Парсинг AdminID
	adminID, err := strconv.ParseInt(os.Getenv("ADMIN_ID"), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid ADMIN_ID: %w", err)
	}
	cfg.AdminID = adminID

	// Валидация обязательных полей
	if cfg.BotToken == "" {
		return nil, fmt.Errorf("BOT_TOKEN is required")
	}
	if cfg.RemnawaveURL == "" {
		return nil, fmt.Errorf("REMNAWAVE_URL is required")
	}
	if cfg.RemnawaveAPIToken == "" {
		return nil, fmt.Errorf("REMNAWAVE_API_TOKEN is required")
	}

	return cfg, nil
}

// getEnvOrDefault возвращает значение переменной окружения или значение по умолчанию
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getRemnawaveSquadUUIDs читает список default squads из нового env
// и использует legacy env как fallback для обратной совместимости.
func getRemnawaveSquadUUIDs() []string {
	if squadUUIDs := parseCSVEnv(os.Getenv("REMNAWAVE_DEFAULT_SQUAD_UUIDS")); len(squadUUIDs) > 0 {
		return squadUUIDs
	}

	return parseCSVEnv(os.Getenv("REMNAWAVE_DEFAULT_SQUAD_UUID"))
}

func parseCSVEnv(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		result = append(result, value)
	}

	if len(result) == 0 {
		return nil
	}

	return result
}
