package bot

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFormatUserLabel проверяет единый формат отображения пользователя
func TestFormatUserLabel(t *testing.T) {
	t.Run("имя и username — deep link на имя, username, id в моно", func(t *testing.T) {
		result := formatUserLabel("Иван", "ivan", 123456789)
		assert.Equal(t, `<a href="tg://user?id=123456789">Иван</a> | @ivan | <code>123456789</code>`, result)
	})

	t.Run("только имя без username — deep link на имя, id в моно", func(t *testing.T) {
		result := formatUserLabel("Иван", "", 123456789)
		assert.Equal(t, `<a href="tg://user?id=123456789">Иван</a> | <code>123456789</code>`, result)
	})

	t.Run("без имени и без username — deep link на 'Пользователь', id в моно", func(t *testing.T) {
		result := formatUserLabel("", "", 123456789)
		assert.Equal(t, `<a href="tg://user?id=123456789">Пользователь</a> | <code>123456789</code>`, result)
	})

	t.Run("без имени, но с username — deep link на 'Пользователь', username, id в моно", func(t *testing.T) {
		result := formatUserLabel("", "ivan", 123456789)
		assert.Equal(t, `<a href="tg://user?id=123456789">Пользователь</a> | @ivan | <code>123456789</code>`, result)
	})
}

// TestFormatUserLabel_HTMLEscaping проверяет что firstName экранируется в HTML
func TestFormatUserLabel_HTMLEscaping(t *testing.T) {
	t.Run("имя с HTML-тегами экранируется", func(t *testing.T) {
		result := formatUserLabel("<b>Alex</b>", "", 123)
		assert.NotContains(t, result, "<b>Alex</b>")
		assert.Contains(t, result, "&lt;b&gt;Alex&lt;/b&gt;")
	})

	t.Run("имя с амперсандом экранируется", func(t *testing.T) {
		result := formatUserLabel("Tom & Jerry", "", 123)
		assert.NotContains(t, result, "Tom & Jerry")
		assert.Contains(t, result, "Tom &amp; Jerry")
	})
}

// TestAdminKeyboardContainsModeratorsOnTopLevel проверяет что Модераторы на верхнем уровне
func TestAdminKeyboardContainsModeratorsOnTopLevel(t *testing.T) {
	keyboard := AdminKeyboard(false)

	var buttons []string
	for _, row := range keyboard.ReplyKeyboard {
		for _, btn := range row {
			buttons = append(buttons, btn.Text)
		}
	}

	assert.Contains(t, buttons, BtnAdminReferrals)
	assert.Contains(t, buttons, BtnAdminManage)
	assert.Contains(t, buttons, BtnAdminBroadcast)
	assert.Contains(t, buttons, BtnAdminStats)
	assert.Contains(t, buttons, BtnAdminMaintenance)
	assert.Contains(t, buttons, BtnAdminUserMode)
}

// TestAdminManageKeyboardDoesNotContainModerators проверяет что в Управлении нет Модераторов
func TestAdminManageKeyboardDoesNotContainModerators(t *testing.T) {
	keyboard := AdminManageKeyboard()

	var buttons []string
	for _, row := range keyboard.ReplyKeyboard {
		for _, btn := range row {
			buttons = append(buttons, btn.Text)
		}
	}

	assert.NotContains(t, buttons, BtnAdminReferrals)
	assert.Contains(t, buttons, BtnAdminCreateInvite)
	assert.Contains(t, buttons, BtnAdminBanUser)
	assert.Contains(t, buttons, BtnAdminSwitchSubscription)
	assert.Contains(t, buttons, BtnAdminUserInfo)
}
