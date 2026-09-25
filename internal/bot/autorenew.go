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
//
// Сюда приходит каждый сверенный ответ, а не только первый: повторный вебхук,
// сверка зависших и отменённых. Поэтому два фильтра:
//   - только succeeded: отказ `card_expired` по-прежнему несёт saved=true, и
//     сверка отменённого автосписания вернула бы мёртвую карту;
//   - только платёж, созданный после последнего гашения Способа: иначе
//     отвязанная карта вернулась бы с ответом по старому платежу сразу после
//     «мы больше не храним его».
func (b *Bot) rememberAutorenewMethod(payment *database.Payment, verified *paymentprovider.Payment) {
	if !b.autorenewAvailable() || payment == nil || verified == nil {
		return
	}
	if payment.IsTest || verified.SavedMethodID == "" || verified.Status != paymentprovider.StatusSucceeded {
		return
	}
	renewal, err := b.db.GetAutorenewal(payment.TelegramID)
	if err != nil {
		slog.Error("Не удалось прочитать автопродление перед сохранением Способа",
			"error", err, "telegram_id", payment.TelegramID, "payment_id", payment.ID)
		return
	}
	// created_at платежа хранится с точностью до секунды: платёж той же секунды,
	// что и гашение, считаем старым — лишний раз не вернуть карту дешевле.
	if renewal != nil && renewal.MethodClearedAt != nil && !payment.CreatedAt.After(*renewal.MethodClearedAt) {
		slog.Info("Способ из ответа по платежу старше его гашения не сохраняем",
			"telegram_id", payment.TelegramID, "payment_id", payment.ID)
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
