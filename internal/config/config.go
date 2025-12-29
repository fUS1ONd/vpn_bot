package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// ServerConfig holds configuration for a single 3X-UI server
type ServerConfig struct {
	BaseURL    string
	WebPath    string
	Username   string
	Password   string
	InboundID  int
	LimitBytes int64
	PublicKey  string
	SNI        string
	SID        string
}

// Config holds all application configuration
type Config struct {
	BotToken         string
	AdminID          int64
	SubPort          int
	SubscriptionHost string // Host for subscription links (domain or IP)
	DBPath           string
	ServerA          ServerConfig
	ServerB          ServerConfig
}

// Load reads configuration from environment variables
func Load() (*Config, error) {
	// Load .env file if it exists (ignore error if not found)
	_ = godotenv.Load()

	cfg := &Config{
		BotToken:         os.Getenv("BOT_TOKEN"),
		SubscriptionHost: os.Getenv("SUBSCRIPTION_HOST"),
		DBPath:           getEnvOrDefault("DB_PATH", "/app/data/users.db"),
	}

	// Parse AdminID
	adminID, err := strconv.ParseInt(os.Getenv("ADMIN_ID"), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid ADMIN_ID: %w", err)
	}
	cfg.AdminID = adminID

	// Parse SubPort
	subPort, err := strconv.Atoi(getEnvOrDefault("SUB_PORT", "8000"))
	if err != nil {
		return nil, fmt.Errorf("invalid SUB_PORT: %w", err)
	}
	cfg.SubPort = subPort

	// Load Server A config
	cfg.ServerA, err = loadServerConfig("SERVER_A")
	if err != nil {
		return nil, fmt.Errorf("failed to load Server A config: %w", err)
	}

	// Load Server B config
	cfg.ServerB, err = loadServerConfig("SERVER_B")
	if err != nil {
		return nil, fmt.Errorf("failed to load Server B config: %w", err)
	}

	// Validate required fields
	if cfg.BotToken == "" {
		return nil, fmt.Errorf("BOT_TOKEN is required")
	}

	return cfg, nil
}

// loadServerConfig loads configuration for a specific server
func loadServerConfig(prefix string) (ServerConfig, error) {
	inboundID, err := strconv.Atoi(getEnvOrDefault(prefix+"_INBOUND_ID", "1"))
	if err != nil {
		return ServerConfig{}, fmt.Errorf("invalid %s_INBOUND_ID: %w", prefix, err)
	}

	limitBytes, err := strconv.ParseInt(getEnvOrDefault(prefix+"_LIMIT_BYTES", "0"), 10, 64)
	if err != nil {
		return ServerConfig{}, fmt.Errorf("invalid %s_LIMIT_BYTES: %w", prefix, err)
	}

	return ServerConfig{
		BaseURL:    os.Getenv(prefix + "_BASE_URL"),
		WebPath:    os.Getenv(prefix + "_WEB_PATH"),
		Username:   os.Getenv(prefix + "_USERNAME"),
		Password:   os.Getenv(prefix + "_PASSWORD"),
		InboundID:  inboundID,
		LimitBytes: limitBytes,
		PublicKey:  os.Getenv(prefix + "_PUBLIC_KEY"),
		SNI:        os.Getenv(prefix + "_SNI"),
		SID:        os.Getenv(prefix + "_SID"),
	}, nil
}

// getEnvOrDefault returns environment variable value or default if not set
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
