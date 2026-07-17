package bot

import (
	"fmt"
	"html"
	"time"
)

// formatUserLabel возвращает единый формат отображения пользователя:
// Имя (deep link) | @username (если есть) | ID (моно)
func formatUserLabel(firstName, username string, telegramID int64) string {
	name := firstName
	if name == "" {
		name = "Пользователь"
	}

	link := fmt.Sprintf(`<a href="tg://user?id=%d">%s</a>`, telegramID, html.EscapeString(name))
	id := fmt.Sprintf(`<code>%d</code>`, telegramID)

	if username != "" {
		return link + " | @" + username + " | " + id
	}
	return link + " | " + id
}

func formatPriceLabel(price *int) string {
	if price == nil {
		return "не установлена"
	}
	return fmt.Sprintf("%d руб/мес", *price)
}

func (b *Bot) minSubscriptionPrice() int {
	if b.config != nil && b.config.MinSubscriptionPrice > 0 {
		return b.config.MinSubscriptionPrice
	}
	return 400
}

func daysUntil(target, now time.Time) int {
	days := int(target.Sub(now).Hours()/24) + 1
	if days < 0 {
		return 0
	}
	return days
}

func monthNameRu(month time.Month) string {
	names := map[time.Month]string{
		time.January: "январь", time.February: "февраль", time.March: "март",
		time.April: "апрель", time.May: "май", time.June: "июнь",
		time.July: "июль", time.August: "август", time.September: "сентябрь",
		time.October: "октябрь", time.November: "ноябрь", time.December: "декабрь",
	}
	return names[month]
}
