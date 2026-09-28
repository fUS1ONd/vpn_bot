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
var referralInvitesCreated = tableOccurrences("created_at",
	`SELECT created_by, created_at FROM `+mainSchema+`.invites WHERE kind = ?`,
	database.InviteKindReferral,
)

// firstTouch — условие «приглашение alias — первое, которое использовал его
// гость» (first-touch, тот же порядок, что у статистики приглашений в админке:
// used_at, затем порядок вставки). Вернувшийся после автокика по чужому
// приглашению — не новый друг второго автора. Использованное приглашение без
// used_at (старые записи) — самое раннее, как NULL в ORDER BY.
func firstTouch(alias string) string {
	return `NOT EXISTS (
	  SELECT 1 FROM ` + mainSchema + `.invites earlier
	  WHERE earlier.used_by = ` + alias + `.used_by AND earlier.rowid <> ` + alias + `.rowid
	    AND (earlier.used_at IS NULL
	         OR datetime(earlier.used_at) < datetime(` + alias + `.used_at)
	         OR (datetime(earlier.used_at) = datetime(` + alias + `.used_at) AND earlier.rowid < ` + alias + `.rowid)))`
}

// referralFriendsRegistered — по referral-приглашению автора впервые пришёл
// друг: момент — дата использования приглашения, человек — автор.
var referralFriendsRegistered = tableOccurrences("i.used_at",
	`SELECT i.created_by, i.used_at FROM `+mainSchema+`.invites i
	 WHERE i.kind = ? AND i.used_by IS NOT NULL AND i.used_at IS NOT NULL AND `+firstTouch("i"),
	database.InviteKindReferral,
)

// paidManually — условие «платёж alias — настоящая ручная оплата»: деньги
// приняты (включая упавшую активацию), не тест владельца и не автосписание —
// Воронки про решения людей, а автосписание решением не является.
func paidManually(alias string) string {
	return alias + `.status IN ('confirmed', 'confirmed_not_activated') AND ` + alias + `.is_test = 0
	AND ` + alias + `.confirmed_at IS NOT NULL
	AND NOT EXISTS (SELECT 1 FROM ` + mainSchema + `.autorenew_attempts a WHERE a.payment_id = ` + alias + `.id)`
}

// referralFriendsPaid — друг, впервые пришедший по referral-приглашению автора
// (first-touch), впервые оплатил после прихода: момент — подтверждение платежа,
// человек — автор. Только первая оплата: продления давно приглашённого друга
// иначе засчитывали бы автору «друг оплатил» в каждой новой когорте.
var referralFriendsPaid = tableOccurrences("p.confirmed_at",
	`SELECT i.created_by, p.confirmed_at
	 FROM `+mainSchema+`.payments p
	 JOIN `+mainSchema+`.invites i ON i.used_by = p.telegram_id AND i.kind = ?
	 WHERE `+paidManually("p")+`
	   AND i.used_at IS NOT NULL AND `+firstTouch("i")+`
	   AND datetime(p.confirmed_at) >= datetime(i.used_at)
	   AND NOT EXISTS (
	     SELECT 1 FROM `+mainSchema+`.payments earlier
	     WHERE earlier.telegram_id = p.telegram_id AND earlier.id <> p.id
	       AND `+paidManually("earlier")+`
	       AND (datetime(earlier.confirmed_at) < datetime(p.confirmed_at)
	            OR (datetime(earlier.confirmed_at) = datetime(p.confirmed_at) AND earlier.id < p.id)))`,
	database.InviteKindReferral,
)

// tableOccurrences — Шаг из таблиц основной базы. query отбирает пары
// (telegram_id, момент) и заканчивается условием WHERE; границы интервала по
// столбцу момента at дописываются здесь, через datetime(): в основной базе
// время лежит в разных форматах (time.Time драйвера и CURRENT_TIMESTAMP), и
// строковое сравнение между ними неверно. datetime() режет доли секунды,
// поэтому SQL отбирает с запасом до секунды, а точная граница [from, to)
// проверяется в Go.
func tableOccurrences(at, query string, args ...any) source {
	query += ` AND datetime(` + at + `) >= datetime(?) AND datetime(` + at + `) <= datetime(?)`
	return func(ctx context.Context, conn *sql.Conn, from, to time.Time) ([]occurrence, error) {
		bound := append(append([]any{}, args...), sqliteSeconds(from), sqliteSeconds(to))
		all, err := queryOccurrences(ctx, conn, query, bound...)
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
