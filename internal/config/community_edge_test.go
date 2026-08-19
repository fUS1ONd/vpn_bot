package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Инвайт-ссылка подставляется в <a href="…"> сообщений «Информация» (его видят
// все), карточки подписки и сообщения об оплате. Telegram отвергает
// сообщение целиком, если href без поддерживаемой схемы («Unsupported URL
// protocol»), — опечатка вроде «t.me/+abc» ломает кнопку «Информация» для всей
// базы, а не только фичу Канала. Тот же принцип уже реализован для ссылки
// подписки (isValidSubscriptionURL) и для недонастроенного Канала: ронять старт,
// а не отдавать битый HTML в рантайме.
func TestLoadRejectsCommunityInviteLinkWithoutHTTPScheme(t *testing.T) {
	for _, link := range []string{"t.me/+abc", "tg://join?invite=abc", "просто текст"} {
		t.Run(link, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("COMMUNITY_CHAT_ID", "-1001234567890")
			t.Setenv("COMMUNITY_INVITE_LINK", link)

			_, err := Load()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "COMMUNITY_INVITE_LINK")
		})
	}
}

// ID супергруппы всегда отрицательный (-100…). Положительное значение — потерянный
// при копировании минус: фича считается включённой, обработчик заявок
// регистрируется, но ни одна заявка не совпадёт с CommunityChatID и все они
// повиснут неразобранными. Ровно тот молчаливый отказ, ради которого валидация
// половины настройки и добавлялась.
func TestLoadRejectsPositiveCommunityChatIDSoRequestsDoNotHangUnprocessed(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("COMMUNITY_CHAT_ID", "1001234567890")
	t.Setenv("COMMUNITY_INVITE_LINK", "https://t.me/+abc")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "COMMUNITY_CHAT_ID")
}
