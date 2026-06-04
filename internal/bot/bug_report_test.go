package bot

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildBugReportMessage(t *testing.T) {
	r := bugReport{
		telegramID:   12345,
		username:     "ivan",
		firstName:    "Иван",
		server:       "🇳🇱 Нидерланды",
		category:     "🔌 Не подключается",
		comment:      "второй день не коннектится",
		subscription: "оплачена до 12.06.26",
	}
	msg := buildBugReportMessage(r)
	require.Contains(t, msg, "Багрепорт")
	require.Contains(t, msg, "Иван")
	require.Contains(t, msg, "@ivan")
	require.Contains(t, msg, "tg://user?id=12345")
	require.Contains(t, msg, "🇳🇱 Нидерланды")
	require.Contains(t, msg, "Не подключается")
	require.Contains(t, msg, "второй день не коннектится")
	require.Contains(t, msg, "оплачена до 12.06.26")
}

func TestBuildBugReportMessage_NoServerNoComment(t *testing.T) {
	r := bugReport{telegramID: 1, category: "🐢 Медленно"}
	msg := buildBugReportMessage(r)
	require.Contains(t, msg, "не указан")
	require.NotContains(t, msg, "💬")
}

func TestTruncateComment(t *testing.T) {
	require.Equal(t, "abc", truncateComment("abc"))
	long := strings.Repeat("я", 2000)
	got := truncateComment(long)
	require.LessOrEqual(t, len([]rune(got)), 1001)
	require.True(t, strings.HasSuffix(got, "…"))
}

func TestBugReportCooldown(t *testing.T) {
	b := &Bot{}
	require.False(t, b.bugReportOnCooldown(42))
	b.markBugReportSent(42)
	require.True(t, b.bugReportOnCooldown(42))
	require.False(t, b.bugReportOnCooldown(99))
}
