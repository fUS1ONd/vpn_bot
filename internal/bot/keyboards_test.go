package bot

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

func TestSubscriptionCardKeyboardWithActiveAccess(t *testing.T) {
	kb := SubscriptionCardKeyboard("https://sub.example.com:8443/abc123", true, false)
	require.NotNil(t, kb)
	require.Len(t, kb.InlineKeyboard, 3)

	assert.Equal(t, "https://sub.example.com:8443/abc123", kb.InlineKeyboard[0][0].URL)
	assert.Equal(t, cbDevicesManage, kb.InlineKeyboard[1][0].Unique)
	assert.Equal(t, cbSubRevoke, kb.InlineKeyboard[2][0].Unique)
}

func TestSubscriptionCardKeyboardWithoutActiveAccess(t *testing.T) {
	// Нет доступа — остаются только устройства: ни перехода, ни перевыпуска.
	kb := SubscriptionCardKeyboard("https://sub.example.com/abc123", false, false)
	require.Len(t, kb.InlineKeyboard, 1)
	assert.Equal(t, cbDevicesManage, kb.InlineKeyboard[0][0].Unique)
}

func TestSubscriptionCardKeyboardSkipsInvalidURL(t *testing.T) {
	// Битую ссылку Telegram отверг бы вместе со всем сообщением, поэтому
	// URL-кнопки быть не должно, а остальные кнопки остаются на месте.
	for _, subURL := range []string{"", "vless://example", "не ссылка", "://broken", "https://"} {
		kb := SubscriptionCardKeyboard(subURL, true, false)
		require.Len(t, kb.InlineKeyboard, 2, "subURL=%q", subURL)
		assert.Equal(t, cbDevicesManage, kb.InlineKeyboard[0][0].Unique, "subURL=%q", subURL)
		assert.Equal(t, cbSubRevoke, kb.InlineKeyboard[1][0].Unique, "subURL=%q", subURL)
	}
}

func TestConnectKeyboard(t *testing.T) {
	kb := ConnectKeyboard("https://sub.example.com:8443/abc123")
	require.NotNil(t, kb)
	require.Len(t, kb.InlineKeyboard, 1)
	assert.Equal(t, "https://sub.example.com:8443/abc123", kb.InlineKeyboard[0][0].URL)

	assert.Nil(t, ConnectKeyboard("vless://example"))
	assert.Nil(t, ConnectKeyboard(""))
}

func TestSubscriptionRevokeConfirmKeyboard(t *testing.T) {
	kb := SubscriptionRevokeConfirmKeyboard()
	require.Len(t, kb.InlineKeyboard, 2)
	assert.Equal(t, cbSubRevokeConfirm, kb.InlineKeyboard[0][0].Unique)
	assert.Equal(t, cbSubRevokeCancel, kb.InlineKeyboard[1][0].Unique)
}

func TestDevicesManagementKeyboard(t *testing.T) {
	devices := []remnawave.HwidDevice{
		{Hwid: "hw-a", Platform: "iOS", DeviceModel: "iPhone 14"},
		{Hwid: "hw-b", Platform: "Android", DeviceModel: "Pixel 7"},
	}
	kb := DevicesManagementKeyboard(devices)
	require.NotNil(t, kb)
	// 2 устройства + строка "сбросить все" + строка "назад" = 4 ряда
	require.Len(t, kb.InlineKeyboard, 4)
	require.Equal(t, "dev_del", kb.InlineKeyboard[0][0].Unique)
	require.Equal(t, "0", kb.InlineKeyboard[0][0].Data)
	require.Equal(t, "dev_del", kb.InlineKeyboard[1][0].Unique)
	require.Equal(t, "1", kb.InlineKeyboard[1][0].Data)
	require.Equal(t, "dev_reset_all", kb.InlineKeyboard[2][0].Unique)
	require.Equal(t, cbSubCard, kb.InlineKeyboard[3][0].Unique)
}

func TestDevicesManagementKeyboardEmpty(t *testing.T) {
	kb := DevicesManagementKeyboard(nil)
	// нет устройств -> только кнопка "назад", без "сбросить все"
	require.Len(t, kb.InlineKeyboard, 1)
	require.Equal(t, cbSubCard, kb.InlineKeyboard[0][0].Unique)
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

func TestInvitesMenuKeyboardContainsExpectedButtons(t *testing.T) {
	keyboard := InvitesMenuKeyboard()

	var buttons []string
	for _, row := range keyboard.ReplyKeyboard {
		for _, btn := range row {
			buttons = append(buttons, btn.Text)
		}
	}

	assert.Contains(t, buttons, BtnInviteCreate)
	assert.Contains(t, buttons, BtnInviteList)
	assert.Contains(t, buttons, BtnInviteBack)
	assert.NotContains(t, buttons, "💰 Мой заработок")
}

func TestAdminReferralsKeyboardContainsStatsButtons(t *testing.T) {
	keyboard := AdminReferralsKeyboard()

	var buttons []string
	for _, row := range keyboard.ReplyKeyboard {
		for _, btn := range row {
			buttons = append(buttons, btn.Text)
		}
	}

	assert.Contains(t, buttons, BtnAdminReferralOverview)
	assert.Contains(t, buttons, BtnAdminReferralLeaders)
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
		assert.NotContains(t, buttons, BtnInvites)
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

	t.Run("пользователь — с кнопкой приглашений", func(t *testing.T) {
		keyboard := UserMenuKeyboardDynamic(BtnRenew, true, true)

		var buttons []string
		for _, row := range keyboard.ReplyKeyboard {
			for _, btn := range row {
				buttons = append(buttons, btn.Text)
			}
		}

		assert.Contains(t, buttons, BtnInvites)
		assert.Contains(t, buttons, BtnRenew)
	})
}

func TestBugServersKeyboard(t *testing.T) {
	hosts := []remnawave.Host{
		{Remark: "🇳🇱 Нидерланды"}, {Remark: "🇩🇪 Германия"},
	}
	// Без выбранных серверов: «Готово» не показывается.
	kb := BugServersKeyboard(hosts, nil)
	require.NotNil(t, kb.InlineKeyboard)
	var labels []string
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			labels = append(labels, btn.Text)
		}
	}
	require.Contains(t, labels, "🇳🇱 Нидерланды")
	require.Contains(t, labels, "🇩🇪 Германия")
	require.Contains(t, labels, BtnBugNoServer)
	require.NotContains(t, labels, "✅ Готово")

	// С выбранным сервером: галочка и кнопка «Готово».
	kb = BugServersKeyboard(hosts, map[string]bool{"🇩🇪 Германия": true})
	labels = nil
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			labels = append(labels, btn.Text)
		}
	}
	require.Contains(t, labels, "✅ 🇩🇪 Германия")
	require.Contains(t, labels, "✅ Готово")
}

func TestBugCategoriesKeyboard(t *testing.T) {
	kb := BugCategoriesKeyboard()
	require.NotNil(t, kb.InlineKeyboard)
	var labels []string
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			labels = append(labels, btn.Text)
		}
	}
	require.Contains(t, labels, "🔌 Не подключается")
}

func TestBugCategoryLabel(t *testing.T) {
	require.Equal(t, "🐢 Медленно работает", bugCategoryLabel("slow"))
	require.Equal(t, "Другое", bugCategoryLabel("unknown"))
}

func TestUserMenuHasBugReport(t *testing.T) {
	kb := UserMenuKeyboardDynamic("", false, false)
	var labels []string
	for _, row := range kb.ReplyKeyboard {
		for _, btn := range row {
			labels = append(labels, btn.Text)
		}
	}
	require.Contains(t, labels, BtnBugReport)
}

func TestPaymentKeyboardsContainExpectedButtons(t *testing.T) {
	methods := PaymentMethodKeyboard(true, true)
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

	assert.Contains(t, methodButtons, BtnPayYooKassa)
	assert.Contains(t, methodButtons, BtnPayCrypto)
	assert.Contains(t, methodButtons, BtnCancel)
	assert.Contains(t, waitButtons, BtnCheckPayment)
	assert.Contains(t, waitButtons, BtnCancel)
}

func TestPaymentMethodKeyboardHidesUnavailableProviders(t *testing.T) {
	for _, tc := range []struct {
		name         string
		yoo, platega bool
		want, absent string
	}{
		{"only YooKassa", true, false, BtnPayYooKassa, BtnPayCrypto},
		{"only Platega", false, true, BtnPayCrypto, BtnPayYooKassa},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyboard := PaymentMethodKeyboard(tc.yoo, tc.platega)
			var labels []string
			for _, row := range keyboard.ReplyKeyboard {
				for _, button := range row {
					labels = append(labels, button.Text)
				}
			}
			assert.Contains(t, labels, tc.want)
			assert.NotContains(t, labels, tc.absent)
		})
	}
}
