package bot

import (
	"testing"

	"github.com/fus1ond/vpn_bot/internal/remnawave"
	"github.com/stretchr/testify/assert"
)

func TestFormatUserStatusShowsUsedTrafficPerMonthWithoutLimit(t *testing.T) {
	user := &remnawave.User{
		Status:            remnawave.StatusActive,
		TrafficLimitBytes: 0,
		SubscriptionURL:   "vless://example",
		UserTraffic: &remnawave.Traffic{
			UsedTrafficBytes: 5 * 1024 * 1024 * 1024,
		},
	}

	msg := FormatUserStatus(user)

	assert.Contains(t, msg, "<b>Трафик за месяц:</b> 5.00 GB")
	assert.NotContains(t, msg, "<b>Трафик:</b>")
	assert.NotContains(t, msg, " / ")
	assert.NotContains(t, msg, "%)")
	assert.NotContains(t, msg, "<b>Сброс трафика:</b>")
	assert.Contains(t, msg, "<b>Ссылка подписки:</b>")
}

func TestMsgAccountCreatedHasNoTrafficLimitDetails(t *testing.T) {
	assert.NotContains(t, MsgAccountCreated, "Лимит трафика")
	assert.NotContains(t, MsgAccountCreated, "Сброс трафика")
}

func TestMsgInfoContainsExpectedLinks(t *testing.T) {
	assert.Contains(t, MsgInfo, "💡 Помощь и контакты")
	assert.Contains(t, MsgInfo, "@fus1ond")
	assert.Contains(t, MsgInfo, "https://telegra.ph/Politika-konfidencialnosti-08-15-17")
	assert.Contains(t, MsgInfo, "https://telegra.ph/Polzovatelskoe-soglashenie-08-15-10")
	assert.Contains(t, MsgInfo, `>читать</a>`)
}
