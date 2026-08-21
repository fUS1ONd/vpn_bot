package config

import (
	"fmt"
	"net/url"
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

	// Юридические страницы и контакт поддержки (показываются в кнопке «Информация»).
	// Все три поля обязательны — значения подставляются в HTML-шаблон MsgInfo.
	PrivacyPolicyURL  string // URL страницы политики конфиденциальности
	TermsOfServiceURL string // URL страницы пользовательского соглашения
	SupportContact    string // Контакт поддержки (@username, t.me/..., email и т. п.)

	// Мониторинг
	SDConfigsPath      string // Путь к папке sd_configs для targets.json
	VictoriaMetricsURL string // URL VictoriaMetrics API

	// Render-сервис (субтитры)
	RenderURL    string // URL render-сервиса (опционально)
	RenderAPIKey string // API-ключ для render-сервиса

	// Platega — платёжная система (опционально, отключена если не заданы)
	PlategaMerchantID        string
	PlategaSecret            string
	PlategaCallbackURL       string // Полный URL для callback (https://domain.com/platega/callback)
	MinSubscriptionPrice     int    // Минимальная цена подписки (руб), по умолчанию 400
	DefaultSubscriptionPrice int    // Цена новых referral-инвайтов; snapshot при создании
	AdminTestPaymentPrice    int    // Тестовая цена для ADMIN_ID (руб); 0 выключает тестовую оплату
	TrialTrafficLimitGB      int    // Лимит трафика триала (ГБ), по умолчанию 1
	PlategaFeeSBP            int    // Комиссия Platega СБП (%), по умолчанию 11
	PlategaFeeCard           int    // Комиссия Platega карты (%), по умолчанию 12
	PlategaFeeCrypto         int    // Комиссия Platega крипта (%), по умолчанию 5
	PlategaFeeWithdrawal     int    // Комиссия вывода (%), по умолчанию 2
	YooKassaShopID           string
	YooKassaSecretKey        string
	YooKassaReturnURL        string
	YooKassaFeeBasisPoints   int // Договорная комиссия ЮKassa в сотых долях процента: 3.5% = 350
	CallbackPort             int // Порт для callback-сервера (по умолчанию 8080)

	// «Мой налог» — автопробитие чеков по платежам ЮKassa (опционально).
	// Интеграция включается наличием ИНН и пароля, как Platega и YooKassa.
	MoynalogINN         string
	MoynalogPassword    string
	MoynalogServiceName string // Базовая часть наименования услуги, к ней приписывается метка

	// Канал — форум-супергруппа сообщества (опционально). Фича включается
	// только когда заданы обе переменные: без ID бот не знает, чьи заявки
	// обрабатывать, без ссылки — некуда звать.
	CommunityChatID     int64  // Числовой ID супергруппы (обычно отрицательный, -100…)
	CommunityInviteLink string // Постоянная инвайт-ссылка с одобрением заявок
}

// CommunityEnabled сообщает, подключён ли Канал. Без обеих переменных бот
// работает ровно как раньше: обработчик заявок не регистрируется, упоминания
// Канала не показываются.
func (c *Config) CommunityEnabled() bool {
	return c != nil && c.CommunityChatID != 0 && c.CommunityInviteLink != ""
}

// MoynalogEnabled сообщает, настроена ли интеграция с кабинетом «Мой налог».
func (c *Config) MoynalogEnabled() bool {
	return c != nil && c.MoynalogINN != "" && c.MoynalogPassword != ""
}

// Load читает конфигурацию из переменных окружения
func Load() (*Config, error) {
	// Загрузка .env файла если он существует
	_ = godotenv.Load()

	cfg := &Config{
		BotToken:                 os.Getenv("BOT_TOKEN"),
		RemnawaveURL:             os.Getenv("REMNAWAVE_URL"),
		RemnawaveAPIToken:        os.Getenv("REMNAWAVE_API_TOKEN"),
		RemnawaveSquadUUIDs:      getRemnawaveSquadUUIDs(),
		DBPath:                   getEnvOrDefault("DB_PATH", "/app/data/bot.db"),
		SDConfigsPath:            getEnvOrDefault("SD_CONFIGS_PATH", "/app/sd_configs"),
		VictoriaMetricsURL:       getEnvOrDefault("VICTORIA_METRICS_URL", "http://victoriametrics:8428"),
		RenderURL:                os.Getenv("RENDER_URL"),
		RenderAPIKey:             os.Getenv("RENDER_API_KEY"),
		PlategaMerchantID:        os.Getenv("PLATEGA_MERCHANT_ID"),
		PlategaSecret:            os.Getenv("PLATEGA_SECRET"),
		PlategaCallbackURL:       os.Getenv("PLATEGA_CALLBACK_URL"),
		MinSubscriptionPrice:     getEnvOrDefaultInt("MIN_SUBSCRIPTION_PRICE", 400),
		DefaultSubscriptionPrice: getDefaultSubscriptionPrice(),
		AdminTestPaymentPrice:    getOptionalPositiveInt("ADMIN_TEST_PAYMENT_PRICE"),
		TrialTrafficLimitGB:      getEnvOrDefaultInt("TRIAL_TRAFFIC_LIMIT_GB", 1),
		PlategaFeeSBP:            getEnvOrDefaultInt("PLATEGA_FEE_SBP", 11),
		PlategaFeeCard:           getEnvOrDefaultInt("PLATEGA_FEE_CARD", 12),
		PlategaFeeCrypto:         getEnvOrDefaultInt("PLATEGA_FEE_CRYPTO", 5),
		PlategaFeeWithdrawal:     getEnvOrDefaultInt("PLATEGA_FEE_WITHDRAWAL", 2),
		YooKassaShopID:           os.Getenv("YOOKASSA_SHOP_ID"),
		YooKassaSecretKey:        os.Getenv("YOOKASSA_SECRET_KEY"),
		YooKassaReturnURL:        os.Getenv("YOOKASSA_RETURN_URL"),
		YooKassaFeeBasisPoints:   getEnvPercentBasisPoints("YOOKASSA_FEE_PERCENT", 0),
		CallbackPort:             getEnvOrDefaultInt("CALLBACK_PORT", 8080),
		PrivacyPolicyURL:         os.Getenv("PRIVACY_POLICY_URL"),
		TermsOfServiceURL:        os.Getenv("TERMS_OF_SERVICE_URL"),
		SupportContact:           os.Getenv("SUPPORT_CONTACT"),
		MoynalogINN:              strings.TrimSpace(os.Getenv("MOYNALOG_INN")),
		MoynalogPassword:         os.Getenv("MOYNALOG_PASSWORD"),
		MoynalogServiceName:      getEnvOrDefault("MOYNALOG_SERVICE_NAME", "Sarvizza - Подписка на месяц"),
		CommunityChatID:          getOptionalInt64("COMMUNITY_CHAT_ID"),
		CommunityInviteLink:      strings.TrimSpace(os.Getenv("COMMUNITY_INVITE_LINK")),
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
	if cfg.PrivacyPolicyURL == "" {
		return nil, fmt.Errorf("PRIVACY_POLICY_URL is required")
	}
	if cfg.TermsOfServiceURL == "" {
		return nil, fmt.Errorf("TERMS_OF_SERVICE_URL is required")
	}
	if cfg.SupportContact == "" {
		return nil, fmt.Errorf("SUPPORT_CONTACT is required")
	}

	// Половина настройки Канала — почти наверняка опечатка, а не решение
	// обойтись без фичи. Молчаливое выключение владелец заметит нескоро:
	// заявки просто повиснут неразобранными, — поэтому падаем на старте.
	if err := validateCommunityEnv(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func getDefaultSubscriptionPrice() int {
	if value := getOptionalPositiveInt("DEFAULT_SUBSCRIPTION_PRICE"); value > 0 {
		return value
	}
	if value := getOptionalPositiveInt("MIN_SUBSCRIPTION_PRICE"); value > 0 {
		return value
	}
	return 400
}

func getEnvPercentBasisPoints(key string, defaultValue int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultValue
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 {
		return defaultValue
	}
	return int(value*100 + 0.5)
}

// getEnvOrDefaultInt возвращает int-значение переменной окружения или значение по умолчанию
func getEnvOrDefaultInt(key string, defaultValue int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return defaultValue
}

// getOptionalPositiveInt читает необязательную положительную цену. Нулевое,
// отрицательное и некорректное значения выключают соответствующую функцию.
func getOptionalPositiveInt(key string) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

// getOptionalInt64 читает необязательный числовой идентификатор. Пустое и
// некорректное значения дают 0 — то есть «не задано».
func getOptionalInt64(key string) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(key)), 10, 64)
	if err != nil {
		return 0
	}
	return value
}

// validateCommunityEnv ловит недонастроенный Канал: заданный, но нечисловой ID
// и заданную половину пары. Ни то, ни другое не должно выглядеть как «фича
// выключена» — иначе опечатка в ID тихо оставляет заявки без разбора.
func validateCommunityEnv(cfg *Config) error {
	rawChatID := strings.TrimSpace(os.Getenv("COMMUNITY_CHAT_ID"))
	if rawChatID != "" && cfg.CommunityChatID == 0 {
		return fmt.Errorf("invalid COMMUNITY_CHAT_ID: %q is not a chat id", rawChatID)
	}
	if cfg.CommunityChatID != 0 && cfg.CommunityInviteLink == "" {
		return fmt.Errorf("COMMUNITY_INVITE_LINK is required when COMMUNITY_CHAT_ID is set")
	}
	if cfg.CommunityInviteLink != "" && cfg.CommunityChatID == 0 {
		return fmt.Errorf("COMMUNITY_CHAT_ID is required when COMMUNITY_INVITE_LINK is set")
	}
	// ID супергруппы всегда отрицательный. Потерянный при копировании минус —
	// самая вероятная опечатка, и она хуже любой другой: фича считается
	// включённой, обработчик регистрируется, но ни одна заявка не совпадёт с
	// чужим ID и все они повиснут неразобранными.
	if cfg.CommunityChatID > 0 {
		return fmt.Errorf("invalid COMMUNITY_CHAT_ID: supergroup id must be negative, got %d", cfg.CommunityChatID)
	}
	// Ссылка подставляется в <a href="…"> сообщения «Информация», которое видят
	// все. Неподдерживаемую схему Telegram отвергает вместе со всем сообщением,
	// то есть опечатка здесь ломает кнопку далеко за пределами фичи.
	if cfg.CommunityInviteLink != "" {
		parsed, err := url.Parse(cfg.CommunityInviteLink)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("invalid COMMUNITY_INVITE_LINK: %q must be an http(s) URL", cfg.CommunityInviteLink)
		}
	}
	return nil
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
