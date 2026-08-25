package bot

import (
	"log/slog"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
)

// Автопродление (согласие пользователя) и Способ автосписания (инструмент у
// кассы) — две независимые сущности: согласие переживает пропавшую карту, а
// Способ сохраняется и у того, кто согласия не давал.

// autorenewAvailable — один предикат на все места: абзац согласия,
// save_payment_method, кнопки и шаг scheduler гаснут вместе.
func (b *Bot) autorenewAvailable() bool {
	return b.config.AutorenewingEnabled() && b.yookassa != nil
}

// shouldSavePaymentMethod решает, просить ли кассу запомнить способ оплаты.
// Тестовый платёж админа не участвует, крипта — тоже: это параметр ЮKassa.
func (b *Bot) shouldSavePaymentMethod(providerName string, isTest bool) bool {
	return b.autorenewAvailable() && providerName == paymentprovider.YooKassa && !isTest
}

// rememberAutorenewMethod записывает Способ по сверенному ответу кассы.
// Согласие при этом не включается.
func (b *Bot) rememberAutorenewMethod(payment *database.Payment, verified *paymentprovider.Payment) {
	if !b.autorenewAvailable() || payment == nil || verified == nil {
		return
	}
	if payment.IsTest || verified.SavedMethodID == "" {
		return
	}
	if err := b.db.SaveAutorenewMethod(payment.TelegramID, verified.SavedMethodID, verified.SavedMethodTitle); err != nil {
		// Подписка важнее автопродления: Способ вернётся при следующей оплате.
		slog.Error("Не удалось сохранить Способ автосписания",
			"error", err, "telegram_id", payment.TelegramID, "payment_id", payment.ID)
		return
	}
	slog.Info("Способ автосписания сохранён",
		"telegram_id", payment.TelegramID, "payment_id", payment.ID, "method", verified.SavedMethodTitle)
}

// autorenewConsentNote — абзац согласия на экране выбора способа оплаты. Живёт
// до редиректа: касса сохраняет инструмент безусловно, ни о чём не спрашивая,
// и наш экран — единственное место, где человек об этом узнаёт.
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
