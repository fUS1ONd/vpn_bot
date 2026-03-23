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

func TestModeratorMenuKeyboardContainsNewButtons(t *testing.T) {
	keyboard := ModeratorMenuKeyboard()

	var buttons []string
	for _, row := range keyboard.ReplyKeyboard {
		for _, btn := range row {
			buttons = append(buttons, btn.Text)
		}
	}

	assert.Contains(t, buttons, BtnModSubscribers)
	assert.Contains(t, buttons, BtnModEarnings)
	assert.NotContains(t, buttons, "⏳ Продлить подписку")
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

func TestUserMenuKeyboardDynamicContainsPayButton(t *testing.T) {
	t.Run("с кнопкой оплаты", func(t *testing.T) {
		keyboard := UserMenuKeyboardDynamic(BtnPay, true, false)

		var buttons []string
		for _, row := range keyboard.ReplyKeyboard {
			for _, btn := range row {
				buttons = append(buttons, btn.Text)
			}
		}

		assert.Contains(t, buttons, BtnStatus)
		assert.Contains(t, buttons, BtnPay)
		assert.Contains(t, buttons, BtnServers)
		assert.Contains(t, buttons, BtnInfo)
		assert.NotContains(t, buttons, BtnModInvites)
	})

	t.Run("без кнопки оплаты", func(t *testing.T) {
		keyboard := UserMenuKeyboardDynamic("", false, false)

		var buttons []string
		for _, row := range keyboard.ReplyKeyboard {
			for _, btn := range row {
				buttons = append(buttons, btn.Text)
			}
		}

		assert.Contains(t, buttons, BtnStatus)
		assert.NotContains(t, buttons, BtnPay)
		assert.NotContains(t, buttons, BtnRenew)
		assert.Contains(t, buttons, BtnServers)
	})

	t.Run("модератор — с кнопкой приглашений", func(t *testing.T) {
		keyboard := UserMenuKeyboardDynamic(BtnRenew, true, true)

		var buttons []string
		for _, row := range keyboard.ReplyKeyboard {
			for _, btn := range row {
				buttons = append(buttons, btn.Text)
			}
		}

		assert.Contains(t, buttons, BtnModInvites)
		assert.Contains(t, buttons, BtnRenew)
	})
}

func TestPaymentKeyboardsContainExpectedButtons(t *testing.T) {
	methods := PaymentMethodKeyboard()
	wait := PaymentWaitKeyboard()

	var methodButtons []string
	for _, row := range methods.ReplyKeyboard {
		for _, btn := range row {
			methodButtons = append(methodButtons, btn.Text)
		}
	}

	var waitButtons []string
	for _, row := range wait.ReplyKeyboard {
		for _, btn := range row {
			waitButtons = append(waitButtons, btn.Text)
		}
	}

	assert.Contains(t, methodButtons, BtnPaySBP)
	assert.Contains(t, methodButtons, BtnPayCard)
	assert.Contains(t, methodButtons, BtnPayCrypto)
	assert.Contains(t, methodButtons, BtnCancel)
	assert.Contains(t, waitButtons, BtnCheckPayment)
	assert.Contains(t, waitButtons, BtnCancel)
}
