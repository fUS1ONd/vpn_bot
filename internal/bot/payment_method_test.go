package bot

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Кнопка «Способ оплаты» появляется в карточке, только когда способ сохранён.
func TestSubscriptionCardShowsSavedMethodButton(t *testing.T) {
	with := SubscriptionCardKeyboard("https://sub.example.com/a", true, true, true)
	require.True(t, keyboardHasCallback(with, cbPaymentMethod))

	without := SubscriptionCardKeyboard("https://sub.example.com/a", true, true, false)
	require.False(t, keyboardHasCallback(without, cbPaymentMethod))
}

// Отвязка доступна только при сохранённом способе, «🔙 Назад» возвращает карточку.
func TestSavedMethodKeyboard(t *testing.T) {
	withMethod := SavedMethodKeyboard(true)
	require.True(t, keyboardHasCallback(withMethod, cbPaymentMethodUnlink))
	require.True(t, keyboardHasCallback(withMethod, cbSubCard))

	empty := SavedMethodKeyboard(false)
	require.False(t, keyboardHasCallback(empty, cbPaymentMethodUnlink))
	require.True(t, keyboardHasCallback(empty, cbSubCard))
}

// Экран называет сохранённый способ и говорит, что его можно отвязать.
func TestSavedMethodScreen(t *testing.T) {
	b, _ := autorenewUIBot(t)

	empty := b.paymentMethodScreen(autorenewView{})
	require.Contains(t, empty, "Сохранённого способа оплаты нет")

	saved := b.paymentMethodScreen(autorenewView{methodTitle: "•••• 4242", price: 400, consent: true})
	require.Contains(t, saved, "•••• 4242")
	require.Contains(t, saved, "отвязать")
	require.Contains(t, saved, "400 ₽")

	off := b.paymentMethodScreen(autorenewView{methodTitle: "СБП", price: 400})
	require.Contains(t, off, "Автопродление по нему выключено")
}

// Отвязка гасит и Способ, и согласие: согласие без Способа обещало бы списания,
// которых не будет.
func TestUnlinkClearsMethodAndConsent(t *testing.T) {
	b, db := autorenewUIBot(t)
	require.NoError(t, db.SaveAutorenewMethod(1, "pm-1", "•••• 4242"))
	require.NoError(t, db.SetAutorenewEnabled(1, true))

	require.NoError(t, db.SetAutorenewEnabled(1, false))
	require.NoError(t, db.ClearAutorenewMethod(1))

	renewal, err := db.GetAutorenewal(1)
	require.NoError(t, err)
	require.False(t, renewal.HasMethod())
	require.False(t, renewal.Enabled)

	// Списывать по такой записи нечем — в выборку автосписаний она не попадает.
	list, err := db.ListEnabledAutorenewals()
	require.NoError(t, err)
	require.Empty(t, list)

	view := b.autorenewViewFor(1, activeRemUser(10), priced(400))
	require.Equal(t, autorenewNoMethod, view.state)
	require.False(t, view.consent)
}
