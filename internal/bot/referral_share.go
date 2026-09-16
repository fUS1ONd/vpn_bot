package bot

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	tele "gopkg.in/telebot.v3"
)

// referralShareMessage — текст, который уходит в чужой чат. Получатель видит
// только его и ничего больше не знает о сервисе, поэтому здесь нет ни слова,
// обращённого к владельцу приглашения: ни про лимит активных ссылок, ни про
// отключение доступа за неуплату — это предупреждения пригласившему, а не тому,
// кого зовут.
func (b *Bot) referralShareMessage(invite *database.Invite) string {
	price := b.config.DefaultSubscriptionPrice
	if invite != nil && invite.SubscriptionPrice != nil {
		price = *invite.SubscriptionPrice
	}
	code := ""
	if invite != nil {
		code = invite.Code
	}

	var msg strings.Builder
	fmt.Fprintf(&msg, "🔒 Приглашение в VPN\n\n")
	fmt.Fprintf(&msg, "Первые 3 дня бесплатно, %d ГБ трафика на пробу.\n", b.config.TrialTrafficLimitGB)
	fmt.Fprintf(&msg, "Дальше %d ₽ в месяц.\n", price)
	if invite != nil && invite.ExpiresAt != nil {
		fmt.Fprintf(&msg, "\nПриглашение действует до %s.\n", moscowTime(*invite.ExpiresAt))
	}
	fmt.Fprintf(&msg, "\n👉 https://t.me/%s?start=%s", b.getBotUsername(), code)
	return msg.String()
}

// shareableInvites отдаёт приглашения, которыми отправитель вправе поделиться.
// Спрашивающий известен только по inline-запросу, поэтому список строится от
// него: чужой код сюда не попадёт, даже если его подставить в запрос руками.
func (b *Bot) shareableInvites(telegramID int64, query string, now time.Time) ([]database.Invite, error) {
	active, err := b.db.GetActiveReferralInvites(telegramID, now)
	if err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return active, nil
	}
	for _, invite := range active {
		if invite.Code == query {
			return []database.Invite{invite}, nil
		}
	}
	return nil, nil
}

// handleReferralShareQuery отвечает на inline-запрос списком своих приглашений.
//
// Ответ обязателен в любом исходе: без него у человека в поле ввода навсегда
// остаётся крутилка. Поэтому даже ошибка базы заканчивается пустым ответом с
// кнопкой в бота, а не молчанием.
func (b *Bot) handleReferralShareQuery(c tele.Context) error {
	query := c.Query()
	if query == nil || query.Sender == nil {
		return nil
	}

	invites, err := b.shareableInvites(query.Sender.ID, query.Text, time.Now().UTC())
	if err != nil {
		slog.Error("Failed to list invites for inline share", "error", err, "telegram_id", query.Sender.ID)
		return c.Answer(emptyShareResponse())
	}
	if len(invites) == 0 {
		return c.Answer(emptyShareResponse())
	}

	results := make(tele.Results, 0, len(invites))
	for i := range invites {
		invite := invites[i]
		article := &tele.ArticleResult{
			Title:       "📤 Отправить приглашение",
			Text:        b.referralShareMessage(&invite),
			Description: shareResultDescription(&invite),
		}
		// ID нужен свой: по умолчанию telebot считает хеш от содержимого, а на
		// одинаковых по тексту приглашениях он совпадёт, и Telegram покажет одну
		// карточку вместо трёх.
		article.ID = invite.Code
		results = append(results, article)
	}

	// CacheTime = 0 и IsPersonal обязательны вместе: иначе Telegram закеширует
	// ответ и отдаст его другому человеку, набравшему тот же запрос, — а запрос
	// здесь и есть код приглашения.
	return c.Answer(&tele.QueryResponse{
		Results:    results,
		CacheTime:  0,
		IsPersonal: true,
	})
}

// emptyShareResponse — ответ, когда делиться нечем. SwitchPMParameter не задаём
// намеренно: с ним кнопка приведёт в бота с deep link, который незарегистрированный
// человек получит как несуществующий код приглашения.
func emptyShareResponse() *tele.QueryResponse {
	return &tele.QueryResponse{
		Results:      tele.Results{},
		CacheTime:    0,
		IsPersonal:   true,
		SwitchPMText: "Создать приглашение",
	}
}

func shareResultDescription(invite *database.Invite) string {
	if invite == nil {
		return ""
	}
	if invite.ExpiresAt == nil {
		return invite.Code
	}
	return fmt.Sprintf("%s · действует до %s", invite.Code, moscowTime(*invite.ExpiresAt))
}
