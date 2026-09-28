package funnels

import (
	"context"
	"database/sql"
	"strings"
	"time"

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
