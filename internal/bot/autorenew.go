package bot

import (
	"log/slog"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
)

// Автопродление — согласие пользователя списывать плату за следующий период без
// его участия. Способ автосписания — сохранённый у кассы инструмент, которым
// списание проводится. Это две сущности: согласие может пережить пропавшую
// карту, а Способ может быть сохранён у того, кто согласия не давал.

// autorenewAvailable сообщает, жива ли фича вообще: рубильник включён и касса
// настроена. Один предикат на все места — абзац согласия, save_payment_method,
// кнопки и шаг scheduler гаснут вместе.
func (b *Bot) autorenewAvailable() bool {
	return b.config.AutorenewingEnabled() && b.yookassa != nil
}

// shouldSavePaymentMethod решает, просить ли кассу запомнить способ оплаты.
// Тестовый платёж админа сюда не попадает (Р16): activateSubscription для
// IsTest вообще ничего не делает, и тестовая кнопка не должна вести себя иначе,
// чем боевая. Крипта проходит мимо: save_payment_method — параметр ЮKassa.
func (b *Bot) shouldSavePaymentMethod(providerName string, isTest bool) bool {
	return b.autorenewAvailable() && providerName == paymentprovider.YooKassa && !isTest
}

// rememberAutorenewMethod записывает Способ автосписания по сверенному ответу
// кассы. Согласие при этом не включается: сохранённый способ и согласие — две
// разные сущности, и записать первое не значит получить второе.
func (b *Bot) rememberAutorenewMethod(payment *database.Payment, verified *paymentprovider.Payment) {
	if !b.autorenewAvailable() || payment == nil || verified == nil {
		return
	}
	if payment.IsTest || verified.SavedMethodID == "" {
		return
	}
	if err := b.db.SaveAutorenewMethod(payment.TelegramID, verified.SavedMethodID, verified.SavedMethodTitle); err != nil {
		// Не сохранился Способ — не повод рушить подтверждение оплаты: подписка
		// важнее автопродления, а Способ вернётся при следующей оплате.
		slog.Error("Не удалось сохранить Способ автосписания",
			"error", err, "telegram_id", payment.TelegramID, "payment_id", payment.ID)
		return
	}
	slog.Info("Способ автосписания сохранён",
		"telegram_id", payment.TelegramID, "payment_id", payment.ID, "method", verified.SavedMethodTitle)
}

// autorenewConsentNote — абзац согласия на экране выбора способа оплаты. Живёт
// до редиректа в кассу, а не в сообщении после оплаты: у большинства способов
// касса сохраняет инструмент безусловно и человека ни о чём не спрашивает, так
// что наш экран — единственное место, где он об этом узнаёт.
func (b *Bot) autorenewConsentNote() string {
	if !b.autorenewAvailable() {
		return ""
	}
	note := "\n\n<i>Оплачивая, вы разрешаете сохранить способ оплаты для будущих автосписаний. " +
		"Автопродление выключено по умолчанию — включить или отключить его можно в «👤 Моя подписка»."
	if b.config.TermsOfServiceURL != "" {
		note += " <a href=\"" + b.config.TermsOfServiceURL + "\">Условия</a>."
	}
	return note + "</i>"
}
