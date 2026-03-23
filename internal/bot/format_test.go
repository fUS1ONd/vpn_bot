package bot

import (
	"strings"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
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

// TestFormatSubscriberLabel_ContainsIDOnce проверяет что ID присутствует ровно один раз в label
func TestFormatSubscriberLabel_ContainsIDOnce(t *testing.T) {
	firstName := "Иван"
	username := "ivan"
	sub := database.Subscriber{TelegramID: 300, FirstName: &firstName, Username: &username}

	label := formatSubscriberLabel(sub)
	// ID должен присутствовать ровно один раз — в <code>
	assert.Equal(t, 1, strings.Count(label, "<code>300</code>"), "ID должен быть в <code> ровно один раз")
	// deep link содержит ID в href — это нормально, но не дублирование в тексте
	assert.Equal(t, 1, strings.Count(label, ">300<"), "ID не должен появляться в тексте отдельно")
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

	assert.Contains(t, buttons, BtnAdminModerators)
	assert.Contains(t, buttons, BtnAdminManage)
	assert.Contains(t, buttons, BtnAdminBroadcast)
	assert.Contains(t, buttons, BtnAdminStats)
	assert.Contains(t, buttons, BtnAdminMaintenance)
	assert.Contains(t, buttons, BtnAdminUserMode)
}

// TestFormatInviteEntry_UsedInvite проверяет новый формат строки пользователя в карточке инвайта
func TestFormatInviteEntry_UsedInvite(t *testing.T) {
	usedBy := int64(123456789)
	usedAt := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)

	t.Run("с именем и username — новый формат с |", func(t *testing.T) {
		inv := database.InviteWithUser{
			Code:            "abc123",
			UsedBy:          &usedBy,
			UsedAt:          &usedAt,
			UserUsername:    "ivan",
			UserFirstName:   "Иван",
			CreatorUsername: "admin",
		}
		result := formatInviteEntry(inv)
		assert.Contains(t, result, `<a href="tg://user?id=123456789">Иван</a>`)
		assert.Contains(t, result, "@ivan")
		assert.Contains(t, result, `<code>123456789</code>`)
		assert.Contains(t, result, " | ")
		assert.NotContains(t, result, " • ")
	})

	t.Run("без имени и username — дефолт Пользователь с deeplink", func(t *testing.T) {
		inv := database.InviteWithUser{
			Code:   "abc123",
			UsedBy: &usedBy,
			UsedAt: &usedAt,
		}
		result := formatInviteEntry(inv)
		assert.Contains(t, result, `<a href="tg://user?id=123456789">Пользователь</a>`)
		assert.Contains(t, result, `<code>123456789</code>`)
		assert.NotContains(t, result, " • ")
	})
}

// TestFormatSubscriberLabel_NewFormat проверяет новый формат formatSubscriberLabel
func TestFormatSubscriberLabel_NewFormat(t *testing.T) {
	username := "ivan"
	firstName := "Иван"

	t.Run("с именем и username — разделитель |", func(t *testing.T) {
		sub := database.Subscriber{TelegramID: 123456789, Username: &username, FirstName: &firstName}
		result := formatSubscriberLabel(sub)
		assert.Contains(t, result, `<a href="tg://user?id=123456789">Иван</a>`)
		assert.Contains(t, result, "@ivan")
		assert.Contains(t, result, " | ")
		assert.NotContains(t, result, " • ")
	})

	t.Run("без username — только имя как deeplink", func(t *testing.T) {
		sub := database.Subscriber{TelegramID: 123456789, FirstName: &firstName}
		result := formatSubscriberLabel(sub)
		assert.Contains(t, result, `<a href="tg://user?id=123456789">Иван</a>`)
		assert.NotContains(t, result, "@")
		assert.NotContains(t, result, " • ")
	})

	t.Run("без имени и username — дефолт Пользователь", func(t *testing.T) {
		sub := database.Subscriber{TelegramID: 123456789}
		result := formatSubscriberLabel(sub)
		assert.Contains(t, result, `<a href="tg://user?id=123456789">Пользователь</a>`)
		assert.NotContains(t, result, " • ")
	})
}

// TestAdminListModeratorsFormat проверяет что в списке модераторов нет отдельной строки 🆔
func TestAdminListModeratorsFormat(t *testing.T) {
	// Проверяем что formatUserLabel не содержит 🆔 как отдельную строку
	result := formatUserLabel("Иван", "ivan", 123456789)
	lines := strings.Split(result, "\n")
	// Всё в одной строке
	assert.Len(t, lines, 1)
	assert.Contains(t, result, `<code>123456789</code>`)
	assert.NotContains(t, result, "🆔")
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

	assert.NotContains(t, buttons, BtnAdminModerators)
	assert.Contains(t, buttons, BtnAdminCreateInvite)
	assert.Contains(t, buttons, BtnAdminViewInvites)
	assert.Contains(t, buttons, BtnAdminDeleteInvite)
	assert.Contains(t, buttons, BtnAdminBanUser)
	assert.Contains(t, buttons, BtnAdminSwitchSubscription)
	assert.Contains(t, buttons, BtnAdminUserInfo)
}
