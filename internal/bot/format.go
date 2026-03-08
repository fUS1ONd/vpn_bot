package bot

import "fmt"

// formatUserLabel возвращает единый формат отображения пользователя:
// Имя (deep link) | @username (если есть) | ID (моно)
func formatUserLabel(firstName, username string, telegramID int64) string {
	name := firstName
	if name == "" {
		name = "Пользователь"
	}

	link := fmt.Sprintf(`<a href="tg://user?id=%d">%s</a>`, telegramID, name)
	id := fmt.Sprintf(`<code>%d</code>`, telegramID)

	if username != "" {
		return link + " | @" + username + " | " + id
	}
	return link + " | " + id
}
