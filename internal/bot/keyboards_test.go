package bot

import (
	"testing"

	"github.com/stretchr/testify/assert"
	tele "gopkg.in/telebot.v3"
)

func collectButtons(keyboardButtons [][]tele.ReplyButton) []string {
	var buttons []string
	for _, row := range keyboardButtons {
		for _, btn := range row {
			buttons = append(buttons, btn.Text)
		}
	}
	return buttons
}

func TestAdminManageKeyboardDoesNotContainAddTrafficButton(t *testing.T) {
	keyboard := AdminManageKeyboard()

	buttons := collectButtons(keyboard.ReplyKeyboard)

	assert.NotContains(t, buttons, "📊 Добавить трафик")
	assert.Contains(t, buttons, BtnAdminCreateInvite)
	assert.Contains(t, buttons, BtnAdminViewInvites)
	assert.Contains(t, buttons, BtnAdminDeleteInvite)
	assert.Contains(t, buttons, BtnAdminBanUser)
	assert.Contains(t, buttons, BtnAdminSwitchSubscription)
	assert.Contains(t, buttons, BtnAdminBack)
}

func TestModeratorMenuKeyboardContainsSubscriptionButtons(t *testing.T) {
	keyboard := ModeratorMenuKeyboard()

	buttons := collectButtons(keyboard.ReplyKeyboard)

	assert.Contains(t, buttons, BtnModSubscribers)
	assert.Contains(t, buttons, BtnModExtend)
}

func TestAdminModeratorKeyboardContainsStatsButton(t *testing.T) {
	keyboard := AdminModeratorKeyboard()

	buttons := collectButtons(keyboard.ReplyKeyboard)

	assert.Contains(t, buttons, BtnAdminModStats)
}

func TestInstructionsKeyboardContainsUnifiedDesktopButton(t *testing.T) {
	keyboard := InstructionsKeyboard()

	buttons := collectButtons(keyboard.ReplyKeyboard)

	assert.Contains(t, buttons, BtnInstIOS)
	assert.Contains(t, buttons, BtnInstAndroid)
	assert.Contains(t, buttons, BtnInstDesktop)
	assert.Contains(t, buttons, "💻ПК")
	assert.NotContains(t, buttons, "💻 Windows/Linux")
	assert.NotContains(t, buttons, "🍏 macOS")
}

func TestUserMenuKeyboardContainsInfoButton(t *testing.T) {
	keyboard := UserMenuKeyboard()

	buttons := collectButtons(keyboard.ReplyKeyboard)

	assert.Contains(t, buttons, BtnInfo)
	assert.Contains(t, buttons, BtnDonate)
	assert.NotContains(t, buttons, BtnActivateCode)
}

func TestUserMenuKeyboardModeratorContainsInfoButton(t *testing.T) {
	keyboard := UserMenuKeyboardModerator()

	buttons := collectButtons(keyboard.ReplyKeyboard)

	assert.Contains(t, buttons, BtnInfo)
	assert.Contains(t, buttons, BtnModInvites)
	assert.NotContains(t, buttons, BtnActivateCode)
}

func TestPreviewUserMenuKeyboardContainsActivateButton(t *testing.T) {
	keyboard := PreviewUserMenuKeyboard()

	buttons := collectButtons(keyboard.ReplyKeyboard)

	assert.Contains(t, buttons, BtnActivateCode)
	assert.Contains(t, buttons, BtnStatus)
	assert.Contains(t, buttons, BtnConnect)
}

func TestAdminKeyboardContainsPreviewToggleButton(t *testing.T) {
	keyboard := AdminKeyboard(false)

	buttons := collectButtons(keyboard.ReplyKeyboard)

	assert.Contains(t, buttons, BtnAdminPreview(false))
	assert.Contains(t, buttons, BtnAdminUserMode)
}
