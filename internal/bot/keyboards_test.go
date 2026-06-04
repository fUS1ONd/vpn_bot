package bot

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

func TestSubscriptionMenuKeyboard(t *testing.T) {
	kb := SubscriptionMenuKeyboard()
	require.NotNil(t, kb)

	var buttons []string
	for _, row := range kb.ReplyKeyboard {
		for _, btn := range row {
			buttons = append(buttons, btn.Text)
		}
	}
	require.Contains(t, buttons, BtnDevices)
	require.Contains(t, buttons, BtnBack)
}

func TestDevicesManagementKeyboard(t *testing.T) {
	devices := []remnawave.HwidDevice{
		{Hwid: "hw-a", Platform: "iOS", DeviceModel: "iPhone 14"},
		{Hwid: "hw-b", Platform: "Android", DeviceModel: "Pixel 7"},
	}
	kb := DevicesManagementKeyboard(devices)
	require.NotNil(t, kb)
	// 2 устройства + строка "сбросить все" + строка "закрыть" = 4 ряда
	require.Len(t, kb.InlineKeyboard, 4)
	require.Equal(t, "dev_del", kb.InlineKeyboard[0][0].Unique)
	require.Equal(t, "0", kb.InlineKeyboard[0][0].Data)
	require.Equal(t, "dev_del", kb.InlineKeyboard[1][0].Unique)
	require.Equal(t, "1", kb.InlineKeyboard[1][0].Data)
	require.Equal(t, "dev_reset_all", kb.InlineKeyboard[2][0].Unique)
	require.Equal(t, "dev_close", kb.InlineKeyboard[3][0].Unique)
}

func TestDevicesManagementKeyboardEmpty(t *testing.T) {
	kb := DevicesManagementKeyboard(nil)
	// нет устройств -> только кнопка "закрыть", без "сбросить все"
	require.Len(t, kb.InlineKeyboard, 1)
	require.Equal(t, "dev_close", kb.InlineKeyboard[0][0].Unique)
}

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
	assert.Contains(t, buttons, BtnAdminUserInfo)
	assert.Contains(t, buttons, BtnAdminBack)
}

func TestAdminKeyboardShowsStatsAndMaintenanceToggle(t *testing.T) {
	t.Run("штатный режим", func(t *testing.T) {
		keyboard := AdminKeyboard(false)

		var buttons []string
		for _, row := range keyboard.ReplyKeyboard {
			for _, btn := range row {
				buttons = append(buttons, btn.Text)
			}
		}

		assert.Contains(t, buttons, BtnAdminStats)
		assert.Contains(t, buttons, BtnAdminMaintenance)
		assert.NotContains(t, buttons, BtnAdminMaintenanceOff)
	})

	t.Run("режим обслуживания", func(t *testing.T) {
		keyboard := AdminKeyboard(true)

		var buttons []string
		for _, row := range keyboard.ReplyKeyboard {
			for _, btn := range row {
				buttons = append(buttons, btn.Text)
			}
		}

		assert.Contains(t, buttons, BtnAdminStats)
		assert.Contains(t, buttons, BtnAdminMaintenanceOff)
		assert.NotContains(t, buttons, BtnAdminMaintenance)
	})
}

func TestAdminSwitchSubmenuContainsExpectedButtons(t *testing.T) {
	keyboard := AdminSwitchSubmenu()

	var buttons []string
	for _, row := range keyboard.ReplyKeyboard {
		for _, btn := range row {
			buttons = append(buttons, btn.Text)
		}
	}

	assert.Contains(t, buttons, BtnAdminSwitchInfinite)
	assert.Contains(t, buttons, BtnAdminChangePrice)
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

func TestAdminChangePriceMigrationKeyboardContainsExpectedButtons(t *testing.T) {
	keyboard := AdminChangePriceMigrationKeyboard()

	var buttons []string
	for _, row := range keyboard.ReplyKeyboard {
		for _, btn := range row {
			buttons = append(buttons, btn.Text)
		}
	}

	assert.Contains(t, buttons, BtnAdminMigrationPaidYes)
	assert.Contains(t, buttons, BtnAdminMigrationPaidNo)
	assert.Contains(t, buttons, BtnCancel)
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
