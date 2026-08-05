package bot

import (
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
	"github.com/stretchr/testify/assert"
)

func TestFormatUserStatusShowsUsedTrafficPerMonthWithoutLimit(t *testing.T) {
	user := &remnawave.User{
		Status:            remnawave.StatusActive,
		TrafficLimitBytes: 0,
		SubscriptionURL:   "vless://example",
		ExpireAt:          time.Now().UTC().AddDate(0, 0, 10),
		UserTraffic: &remnawave.Traffic{
			UsedTrafficBytes: 5 * 1024 * 1024 * 1024,
		},
	}

	msg := FormatUserStatus(user, nil, false, nil)

	assert.Contains(t, msg, "<b>Трафик за месяц:</b> 5.00 GB")
	assert.NotContains(t, msg, "<b>Трафик:</b>")
	assert.NotContains(t, msg, " / ")
	assert.NotContains(t, msg, "%)")
	assert.NotContains(t, msg, "<b>Сброс трафика:</b>")
	assert.Contains(t, msg, "<b>Ссылка для ручного подключения</b>")
}

func TestFormatUserStatusGraceShowsPaymentWindow(t *testing.T) {
	price := 400
	msg := FormatUserStatus(&remnawave.User{
		Status:            remnawave.StatusDisabled,
		TrafficLimitBytes: 0,
		SubscriptionURL:   "vless://example",
		ExpireAt:          time.Now().UTC().Add(-12 * time.Hour),
	}, &database.User{
		SubscriptionPrice: &price,
	}, false, nil)

	assert.Contains(t, msg, "⚠️ Подписка истекла")
	assert.Contains(t, msg, "VPN деактивирован")
	assert.Contains(t, msg, "Цена подписки")
	assert.Contains(t, msg, "Осталось для оплаты")
	assert.Contains(t, msg, "аккаунт будет удалён")
}

func TestMsgAccountCreatedHasNoTrafficLimitDetails(t *testing.T) {
	assert.NotContains(t, MsgAccountCreated, "Лимит трафика")
	assert.NotContains(t, MsgAccountCreated, "Сброс трафика")
}

func TestMsgAccountCreatedDoesNotMentionRemovedInstructionsButton(t *testing.T) {
	assert.NotContains(t, MsgAccountCreated, "Инструкции")
}

func TestBuildInfoMessageSubstitutesValuesFromConfig(t *testing.T) {
	cfg := &config.Config{
		PrivacyPolicyURL:  "https://example.com/privacy",
		TermsOfServiceURL: "https://example.com/terms",
		SupportContact:    "@help_bot",
	}

	msg := BuildInfoMessage(cfg)

	// Заголовок и формат сохранены
	assert.Contains(t, msg, "💡 Помощь и контакты")
	assert.Contains(t, msg, "Политика конфиденциальности")
	assert.Contains(t, msg, "Пользовательское соглашение")
	assert.Contains(t, msg, `>читать</a>`)

	// Значения подставлены из config
	assert.Contains(t, msg, "@help_bot")
	assert.Contains(t, msg, `href="https://example.com/privacy"`)
	assert.Contains(t, msg, `href="https://example.com/terms"`)

	// Старые протухшие telegra.ph-ссылки не вшиты в код
	assert.NotContains(t, msg, "telegra.ph/Politika-konfidencialnosti-08-15-17")
	assert.NotContains(t, msg, "telegra.ph/Polzovatelskoe-soglashenie-08-15-10")
}

func TestBuildInfoMessageEscapesURLs(t *testing.T) {
	// URL-поля должны проходить через html.EscapeString, чтобы
	// кавычки внутри значения не ломали структуру HTML-атрибута href.
	cfg := &config.Config{
		PrivacyPolicyURL:  `https://example.com/p?q="bad"`,
		TermsOfServiceURL: "https://example.com/terms",
		SupportContact:    "@help_bot",
	}

	msg := BuildInfoMessage(cfg)

	// Сырая кавычка внутри значения атрибута — недопустимо
	assert.NotContains(t, msg, `q="bad"`)
	// Ожидаем экранирование
	assert.Contains(t, msg, "&#34;bad&#34;")
}
