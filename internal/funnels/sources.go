package funnels

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/journal"
)

// journalAction — Шаг из журнала: случаи Действия (любого из перечисленных) в интервале.
func journalAction(actions ...string) source {
	return func(ctx context.Context, conn *sql.Conn, from, to time.Time) ([]occurrence, error) {
		query := `SELECT telegram_id, ts FROM events WHERE ts >= ? AND ts < ? AND action IN (?` +
			strings.Repeat(", ?", len(actions)-1) + `)`
		args := []any{from.UTC().Format(journal.TimeLayout), to.UTC().Format(journal.TimeLayout)}
		for _, action := range actions {
			args = append(args, action)
		}
		return queryOccurrences(ctx, conn, query, args...)
	}
}

// errNoEvents — Действия нет в журнале вовсе. Для Действия, которое Telegram
// присылает только при отдельной настройке бота, это не ноль, а «не знаем».
var errNoEvents = errors.New("action never recorded")

// journalActionOrNoData — как journalAction, но без единого случая Действия во
// всём журнале Шаг получает «нет данных»: выключенная доставка апдейта
// неотличима от нуля иначе.
func journalActionOrNoData(action string) source {
	inPeriod := journalAction(action)
	return func(ctx context.Context, conn *sql.Conn, from, to time.Time) ([]occurrence, error) {
		var recorded bool
		if err := conn.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM events WHERE action = ?)`, action,
		).Scan(&recorded); err != nil {
			return nil, err
		}
		if !recorded {
			return nil, errNoEvents
		}
		return inPeriod(ctx, conn, from, to)
	}
}

// referralInvitesCreated — автор создал referral-приглашение. Служебные
// приглашения владельца — не приглашения друга и в Воронку не идут.
var referralInvitesCreated = tableOccurrences(
	`SELECT created_by, created_at FROM `+mainSchema+`.invites
	 WHERE kind = ? AND datetime(created_at) >= datetime(?) AND datetime(created_at) <= datetime(?)`,
	database.InviteKindReferral,
)

// referralFriendsRegistered — по referral-приглашению автора зарегистрировался
// друг: момент — дата использования приглашения, человек — автор.
var referralFriendsRegistered = tableOccurrences(
	`SELECT created_by, used_at FROM `+mainSchema+`.invites
	 WHERE kind = ? AND used_by IS NOT NULL AND used_at IS NOT NULL
	   AND datetime(used_at) >= datetime(?) AND datetime(used_at) <= datetime(?)`,
	database.InviteKindReferral,
)

// paidManually — условие «платёж p — настоящая ручная оплата»: деньги приняты
// (включая упавшую активацию), не тест владельца и не автосписание — Воронки
// про решения людей, а автосписание решением не является.
const paidManually = `p.status IN ('confirmed', 'confirmed_not_activated') AND p.is_test = 0
	AND p.confirmed_at IS NOT NULL
	AND NOT EXISTS (SELECT 1 FROM ` + mainSchema + `.autorenew_attempts a WHERE a.payment_id = p.id)`

// referralFriendsPaid — друг, пришедший по referral-приглашению автора, впервые
// оплатил: момент — подтверждение платежа, человек — автор. Только первая
// оплата: продления давно приглашённого друга иначе засчитывали бы автору
// «друг оплатил» в каждой новой когорте.
var referralFriendsPaid = tableOccurrences(
	`SELECT i.created_by, p.confirmed_at
	 FROM `+mainSchema+`.payments p
	 JOIN `+mainSchema+`.invites i ON i.used_by = p.telegram_id AND i.kind = ?
	 WHERE `+paidManually+`
	   AND NOT EXISTS (
	     SELECT 1 FROM `+mainSchema+`.payments earlier
	     WHERE earlier.telegram_id = p.telegram_id AND earlier.id <> p.id
	       AND earlier.status IN ('confirmed', 'confirmed_not_activated') AND earlier.is_test = 0
	       AND earlier.confirmed_at IS NOT NULL
	       AND NOT EXISTS (SELECT 1 FROM `+mainSchema+`.autorenew_attempts a WHERE a.payment_id = earlier.id)
	       AND (datetime(earlier.confirmed_at) < datetime(p.confirmed_at)
	            OR (datetime(earlier.confirmed_at) = datetime(p.confirmed_at) AND earlier.id < p.id)))
	   AND datetime(p.confirmed_at) >= datetime(?) AND datetime(p.confirmed_at) <= datetime(?)`,
	database.InviteKindReferral,
)

// tableOccurrences — Шаг из таблиц основной базы. query отбирает пары
// (telegram_id, момент) и принимает границы интервала двумя параметрами после
// extra, сравнивая их через datetime(): в основной базе время лежит в разных
// форматах (time.Time драйвера и CURRENT_TIMESTAMP), и строковое сравнение
// между ними неверно. datetime() режет доли секунды, поэтому SQL отбирает с
// запасом до секунды, а точная граница [from, to) проверяется в Go.
func tableOccurrences(query string, extra ...any) source {
	return func(ctx context.Context, conn *sql.Conn, from, to time.Time) ([]occurrence, error) {
		args := append(append([]any{}, extra...), sqliteSeconds(from), sqliteSeconds(to))
		all, err := queryOccurrences(ctx, conn, query, args...)
		if err != nil {
			return nil, err
		}
		within := all[:0]
		for _, o := range all {
			if !o.At.Before(from) && o.At.Before(to) {
				within = append(within, o)
			}
		}
		return within, nil
	}
}

// sqliteSeconds — момент в формате datetime() SQLite.
func sqliteSeconds(at time.Time) string {
	return at.UTC().Format("2006-01-02 15:04:05")
}

// queryOccurrences читает пары (telegram_id, момент) из произвольного запроса.
func queryOccurrences(ctx context.Context, conn *sql.Conn, query string, args ...any) ([]occurrence, error) {
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var occurrences []occurrence
	for rows.Next() {
		var o occurrence
		if err := rows.Scan(&o.TelegramID, &o.At); err != nil {
			return nil, err
		}
		o.At = o.At.UTC()
		occurrences = append(occurrences, o)
	}
	return occurrences, rows.Err()
}
