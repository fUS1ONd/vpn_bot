package bot

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAdminManageKeyboardDoesNotContainAddTrafficButton(t *testing.T) {
	keyboard := AdminManageKeyboard()

	var buttons []string
	for _, row := range keyboard.ReplyKeyboard {
		for _, btn := range row {
			buttons = append(buttons, btn.Text)
		}
	}

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

	var buttons []string
	for _, row := range keyboard.ReplyKeyboard {
		for _, btn := range row {
			buttons = append(buttons, btn.Text)
		}
	}

	assert.Contains(t, buttons, BtnModSubscribers)
	assert.Contains(t, buttons, BtnModExtend)
}

func TestAdminModeratorKeyboardContainsStatsButton(t *testing.T) {
	keyboard := AdminModeratorKeyboard()

	var buttons []string
	for _, row := range keyboard.ReplyKeyboard {
		for _, btn := range row {
			buttons = append(buttons, btn.Text)
		}
	}

	assert.Contains(t, buttons, BtnAdminModStats)
}

func TestInstructionsKeyboardContainsUnifiedDesktopButton(t *testing.T) {
	keyboard := InstructionsKeyboard()

	var buttons []string
	for _, row := range keyboard.ReplyKeyboard {
		for _, btn := range row {
			buttons = append(buttons, btn.Text)
		}
	}

	assert.Contains(t, buttons, BtnInstIOS)
	assert.Contains(t, buttons, BtnInstAndroid)
	assert.Contains(t, buttons, BtnInstDesktop)
	assert.Contains(t, buttons, "💻ПК")
	assert.NotContains(t, buttons, "💻 Windows/Linux")
	assert.NotContains(t, buttons, "🍏 macOS")
}
