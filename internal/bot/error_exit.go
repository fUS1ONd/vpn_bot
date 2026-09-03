package bot

import (
	"net/url"
	"strings"

	tele "gopkg.in/telebot.v3"
)

// supportURLPrefix — канонический вид ссылки на телеграм-контакт.
const supportURLPrefix = "https://t.me/"

// retryAction описывает кнопку «🔄 Повторить»: какой callback вызвать и с чем.
// Нулевое значение означает, что повтор бессмыслен и кнопки быть не должно.
type retryAction struct {
	unique string
	data   string
}

// supportURL превращает SUPPORT_CONTACT в ссылку для URL-кнопки.
//
// Значение задаёт владелец и вставляется в «Информацию» как есть, поэтому там
// может оказаться что угодно: @username, t.me/…, email или готовый тег <a href>.
// Кнопку собираем только из того, что уверенно опознали как телеграм-контакт;
// всё остальное остаётся строкой в тексте сообщения. Битое значение не должно
// ронять сообщение об ошибке — это последнее, что у пользователя осталось.
func supportURL(contact string) (string, bool) {
	contact = strings.TrimSpace(contact)
	// Пробелы и угловые скобки означают готовый HTML или фразу вроде
	// «пишите в личку» — ссылки из такого не собрать.
	if contact == "" || strings.ContainsAny(contact, "<> \t\n\r") {
		return "", false
	}

	if name, found := strings.CutPrefix(contact, "@"); found {
		if !isTelegramUsername(name) {
			return "", false
		}
		return supportURLPrefix + name, true
	}

	raw := contact
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	// Хост сверяем явно: URL-кнопка на чужой домен из переменной «контакт
	// поддержки» — не то, чего ждёт пользователь, нажимая «Написать в поддержку».
	switch strings.ToLower(u.Host) {
	case "t.me", "telegram.me":
	default:
		return "", false
	}

	path := strings.Trim(u.Path, "/")
	if path == "" {
		return "", false
	}
	return supportURLPrefix + path, true
}

// isTelegramUsername проверяет @-контакт: буква в начале, дальше буквы, цифры и
// подчёркивания. Границы длины намеренно шире официальных — потерять кнопку
// из-за нестандартного, но живого имени хуже, чем собрать её на всякий случай.
func isTelegramUsername(name string) bool {
	if len(name) < 3 || len(name) > 32 {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case i > 0 && (r >= '0' && r <= '9' || r == '_'):
		default:
			return false
		}
	}
	return true
}

// errorExitKeyboard собирает выходы из ошибки: повтор (там, где он осмыслен) и
// связь с поддержкой. Возвращает nil, если предложить нечего.
func (b *Bot) errorExitKeyboard(retry retryAction) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	if retry.unique != "" {
		rows = append(rows, menu.Row(menu.Data("🔄 Повторить", retry.unique, retry.data)))
	}
	if link, ok := supportURL(b.config.SupportContact); ok {
		rows = append(rows, menu.Row(menu.URL("💬 Написать в поддержку", link)))
	}
	if len(rows) == 0 {
		return nil
	}

	menu.Inline(rows...)
	return menu
}

// errorExitText дописывает контакт поддержки в текст, если кнопку из него
// собрать не удалось. Без этого пользователь остаётся вовсе без адресата.
func (b *Bot) errorExitText(text string) string {
	if _, ok := supportURL(b.config.SupportContact); ok {
		return text
	}
	if contact := strings.TrimSpace(b.config.SupportContact); contact != "" {
		return text + "\n\nЕсли не помогает — напишите в поддержку: " + contact
	}
	return text
}

// sendErrorExit отправляет сообщение об ошибке вместе с выходом из неё.
func (b *Bot) sendErrorExit(c tele.Context, text string, retry retryAction) error {
	return c.Send(b.errorExitText(text), &tele.SendOptions{
		ParseMode:   tele.ModeHTML,
		ReplyMarkup: b.errorExitKeyboard(retry),
	})
}
