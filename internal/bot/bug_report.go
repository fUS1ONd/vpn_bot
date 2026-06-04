package bot

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// bugReport — собранные данные одного багрепорта для отправки админу.
type bugReport struct {
	telegramID   int64
	username     string
	firstName    string
	server       string // Remark хоста или "" если не указан
	category     string
	comment      string // "" если пропущен
	subscription string // человекочитаемый статус подписки
}

// buildBugReportMessage формирует HTML-сообщение багрепорта для админа.
func buildBugReportMessage(r bugReport) string {
	var b strings.Builder
	b.WriteString("🛠 <b>Багрепорт</b>\n\n")
	fmt.Fprintf(&b, "📅 %s\n", time.Now().Format("02.01.06 15:04"))

	userLink := fmt.Sprintf("<a href=\"tg://user?id=%d\">%d</a>", r.telegramID, r.telegramID)
	name := html.EscapeString(r.firstName)
	if r.username != "" {
		fmt.Fprintf(&b, "👤 %s (@%s) · %s\n", name, html.EscapeString(r.username), userLink)
	} else {
		fmt.Fprintf(&b, "👤 %s · %s\n", name, userLink)
	}

	server := r.server
	if server == "" {
		server = "не указан"
	}
	fmt.Fprintf(&b, "📡 Сервер: %s\n", html.EscapeString(server))
	fmt.Fprintf(&b, "❗ Проблема: %s\n", html.EscapeString(r.category))

	if r.comment != "" {
		fmt.Fprintf(&b, "💬 «%s»\n", html.EscapeString(r.comment))
	}
	if r.subscription != "" {
		fmt.Fprintf(&b, "\nПодписка: %s", html.EscapeString(r.subscription))
	}
	return b.String()
}

// truncateComment обрезает комментарий пользователя до разумной длины.
func truncateComment(s string) string {
	const max = 1000
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
