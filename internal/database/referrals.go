package database

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	MaxActiveReferralInvites = 3
	MaxDailyReferralInvites  = 15
)

// ReferralCreatorSummary содержит счётчики карточки автора.
type ReferralCreatorSummary struct {
	Active  int
	Used    int
	Expired int
	Revoked int
}

// ReferralOverview содержит агрегаты админского обзора за период.
type ReferralOverview struct {
	Created          int
	Activated        int
	Expired          int
	Revoked          int
	UniqueInvited    int
	FirstPaid        int
	AdminActivations int
}

// ReferralLeaderboardRow содержит строку рейтинга первого касания.
type ReferralLeaderboardRow struct {
	TelegramID int64
	Username   string
	FirstName  string
	Invited    int
	Paid       int
	ActiveNow  int
}

// CreateReferralInvite атомарно создаёт referral-инвайт с ограничениями автора.
func (db *DB) CreateReferralInvite(createdBy int64, price int, now time.Time) (*Invite, error) {
	db.referralMu.Lock()
	defer db.referralMu.Unlock()

	code, err := generateInviteCode()
	if err != nil {
		return nil, fmt.Errorf("failed to generate invite code: %w", err)
	}
	now = now.UTC()
	expiresAt := now.Add(30 * 24 * time.Hour)
	windowStart := now.Add(-24 * time.Hour)

	result, err := db.conn.Exec(
		`INSERT INTO invites
			(code, created_by, expire_days, subscription_price, is_trial, kind, expires_at, created_at)
		 SELECT ?, ?, 30, ?, 1, ?, ?, ?
		 WHERE (SELECT COUNT(*) FROM invites
		        WHERE created_by = ? AND kind = ? AND used_by IS NULL
		          AND revoked_at IS NULL AND expires_at > ?) < ?
		   AND (SELECT COUNT(*) FROM invites
		        WHERE created_by = ? AND kind = ? AND created_at > ?) < ?`,
		code, createdBy, price, InviteKindReferral, expiresAt, now,
		createdBy, InviteKindReferral, now, MaxActiveReferralInvites,
		createdBy, InviteKindReferral, windowStart, MaxDailyReferralInvites,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create referral invite: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to inspect referral invite insert: %w", err)
	}
	if rows == 0 {
		active, countErr := db.CountActiveReferralInvites(createdBy, now)
		if countErr != nil {
			return nil, countErr
		}
		if active >= MaxActiveReferralInvites {
			return nil, ErrActiveInviteLimit
		}
		return nil, ErrDailyInviteLimit
	}
	return db.GetInviteByCode(code)
}

func (db *DB) CountActiveReferralInvites(createdBy int64, now time.Time) (int, error) {
	var count int
	err := db.conn.QueryRow(
		`SELECT COUNT(*) FROM invites
		 WHERE created_by = ? AND kind = ? AND used_by IS NULL
		   AND revoked_at IS NULL AND expires_at > ?`,
		createdBy, InviteKindReferral, now.UTC(),
	).Scan(&count)
	return count, err
}

func (db *DB) GetActiveReferralInvites(createdBy int64, now time.Time) ([]Invite, error) {
	rows, err := db.conn.Query(
		`SELECT code FROM invites
		 WHERE created_by = ? AND kind = ? AND used_by IS NULL
		   AND revoked_at IS NULL AND expires_at > ?
		 ORDER BY created_at DESC`,
		createdBy, InviteKindReferral, now.UTC(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var codes []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	invites := make([]Invite, 0, len(codes))
	for _, code := range codes {
		invite, err := db.GetInviteByCode(code)
		if err != nil {
			return nil, err
		}
		if invite != nil {
			invites = append(invites, *invite)
		}
	}
	return invites, nil
}

func (db *DB) GetUsedReferralInvitesByCreator(createdBy int64, limit, offset int) ([]InviteWithUser, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := db.conn.Query(
		`SELECT i.code, i.created_by, i.used_by, i.used_at, i.created_at,
		        u.username, u.first_name
		 FROM invites i
		 LEFT JOIN users u ON u.telegram_id = i.used_by
		 WHERE i.created_by = ? AND i.kind = ? AND i.used_by IS NOT NULL
		 ORDER BY i.used_at DESC, i.created_at DESC
		 LIMIT ? OFFSET ?`,
		createdBy, InviteKindReferral, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []InviteWithUser
	for rows.Next() {
		var item InviteWithUser
		var usedBy sql.NullInt64
		var usedAt sql.NullTime
		var username, firstName sql.NullString
		if err := rows.Scan(&item.Code, &item.CreatedBy, &usedBy, &usedAt, &item.CreatedAt, &username, &firstName); err != nil {
			return nil, err
		}
		if usedBy.Valid {
			v := usedBy.Int64
			item.UsedBy = &v
		}
		if usedAt.Valid {
			v := usedAt.Time
			item.UsedAt = &v
		}
		if username.Valid {
			item.UserUsername = username.String
		}
		if firstName.Valid {
			item.UserFirstName = firstName.String
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (db *DB) RevokeReferralInvite(code string, revokedBy int64, admin bool, now time.Time) error {
	query := `UPDATE invites SET revoked_at = ?, revoked_by = ?
		WHERE code = ? AND kind = ? AND used_by IS NULL AND revoked_at IS NULL
		  AND expires_at > ?`
	args := []any{now.UTC(), revokedBy, code, InviteKindReferral, now.UTC()}
	if !admin {
		query += ` AND created_by = ?`
		args = append(args, revokedBy)
	}
	result, err := db.conn.Exec(query, args...)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows > 0 {
		return nil
	}
	invite, err := db.GetInviteByCode(code)
	if err != nil {
		return err
	}
	if invite == nil {
		return ErrInviteNotFound
	}
	if !admin && invite.CreatedBy != revokedBy {
		return ErrInviteNotOwned
	}
	return ErrInviteNotActive
}

func (db *DB) GetReferralCreatorSummary(createdBy int64, now time.Time) (*ReferralCreatorSummary, error) {
	summary := &ReferralCreatorSummary{}
	err := db.conn.QueryRow(
		`SELECT
			COALESCE(SUM(CASE WHEN used_by IS NULL AND revoked_at IS NULL AND expires_at > ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN used_by IS NOT NULL THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN used_by IS NULL AND revoked_at IS NULL AND expires_at <= ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN revoked_at IS NOT NULL THEN 1 ELSE 0 END), 0)
		 FROM invites WHERE created_by = ? AND kind = ?`,
		now.UTC(), now.UTC(), createdBy, InviteKindReferral,
	).Scan(&summary.Active, &summary.Used, &summary.Expired, &summary.Revoked)
	return summary, err
}

// GetFirstReferralInviter возвращает автора, только если самое первое касание
// пользователя было referral. Служебный admin-first-touch навсегда остаётся admin.
func (db *DB) GetFirstReferralInviter(telegramID int64) (*int64, error) {
	var inviter sql.NullInt64
	err := db.conn.QueryRow(
		`SELECT CASE WHEN kind = ? THEN created_by END FROM invites
		 WHERE used_by = ?
		 ORDER BY used_at ASC, created_at ASC, rowid ASC LIMIT 1`,
		InviteKindReferral, telegramID,
	).Scan(&inviter)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !inviter.Valid {
		return nil, nil
	}
	return &inviter.Int64, nil
}

// GetReferralOverview строит агрегаты за [start, end). nil start означает всё время.
func (db *DB) GetReferralOverview(start, end *time.Time) (*ReferralOverview, error) {
	from := time.Unix(0, 0).UTC()
	to := time.Now().UTC().AddDate(100, 0, 0)
	if start != nil {
		from = start.UTC()
	}
	if end != nil {
		to = end.UTC()
	}
	result := &ReferralOverview{}
	err := db.conn.QueryRow(
		`WITH first_invite AS (
			SELECT used_by, created_by, used_at, kind,
			       ROW_NUMBER() OVER (PARTITION BY used_by ORDER BY used_at, created_at, rowid) rn
			FROM invites WHERE used_by IS NOT NULL
		), first_payment AS (
			SELECT telegram_id, MIN(confirmed_at) paid_at FROM payments
			WHERE status IN ('confirmed','confirmed_not_activated') AND is_test = 0
			GROUP BY telegram_id
		)
		SELECT
		 (SELECT COUNT(*) FROM invites WHERE kind = ? AND created_at >= ? AND created_at < ?),
		 (SELECT COUNT(*) FROM invites WHERE kind = ? AND used_at >= ? AND used_at < ?),
		 (SELECT COUNT(*) FROM invites WHERE kind = ? AND used_by IS NULL AND revoked_at IS NULL AND expires_at >= ? AND expires_at < ?),
		 (SELECT COUNT(*) FROM invites WHERE kind = ? AND revoked_at >= ? AND revoked_at < ?),
		 (SELECT COUNT(*) FROM first_invite WHERE rn = 1 AND kind = ? AND used_at >= ? AND used_at < ?),
		 (SELECT COUNT(*) FROM first_invite fi JOIN first_payment fp ON fp.telegram_id = fi.used_by
		   WHERE fi.rn = 1 AND fi.kind = ? AND fp.paid_at >= ? AND fp.paid_at < ? AND fp.paid_at >= fi.used_at),
		 (SELECT COUNT(*) FROM invites WHERE kind = ? AND used_at >= ? AND used_at < ?)`,
		InviteKindReferral, from, to,
		InviteKindReferral, from, to,
		InviteKindReferral, from, to,
		InviteKindReferral, from, to,
		InviteKindReferral, from, to,
		InviteKindReferral, from, to,
		InviteKindAdmin, from, to,
	).Scan(&result.Created, &result.Activated, &result.Expired, &result.Revoked,
		&result.UniqueInvited, &result.FirstPaid, &result.AdminActivations)
	return result, err
}

func (db *DB) GetReferralLeaderboard(start *time.Time, limit, offset int) ([]ReferralLeaderboardRow, error) {
	from := time.Unix(0, 0).UTC()
	if start != nil {
		from = start.UTC()
	}
	if limit <= 0 {
		limit = 10
	}
	rows, err := db.conn.Query(
		`WITH first_invite AS (
			SELECT used_by, created_by, used_at, kind,
			       ROW_NUMBER() OVER (PARTITION BY used_by ORDER BY used_at, created_at, rowid) rn
			FROM invites WHERE used_by IS NOT NULL
		), first_payment AS (
			SELECT telegram_id, MIN(confirmed_at) paid_at FROM payments
			WHERE status IN ('confirmed','confirmed_not_activated') AND is_test = 0
			GROUP BY telegram_id
		)
		SELECT fr.created_by, COALESCE(author.username,''), COALESCE(author.first_name,''),
		       COUNT(*), COUNT(fp.telegram_id),
		       SUM(CASE WHEN invited.telegram_id IS NOT NULL THEN 1 ELSE 0 END)
		FROM first_invite fr
		LEFT JOIN users author ON author.telegram_id = fr.created_by
		LEFT JOIN first_payment fp ON fp.telegram_id = fr.used_by AND fp.paid_at >= fr.used_at
		LEFT JOIN users invited ON invited.telegram_id = fr.used_by
		WHERE fr.rn = 1 AND fr.kind = ? AND fr.used_at >= ?
		GROUP BY fr.created_by
		ORDER BY COUNT(*) DESC, COUNT(fp.telegram_id) DESC, fr.created_by ASC
		LIMIT ? OFFSET ?`,
		InviteKindReferral, from, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ReferralLeaderboardRow
	for rows.Next() {
		var row ReferralLeaderboardRow
		if err := rows.Scan(&row.TelegramID, &row.Username, &row.FirstName, &row.Invited, &row.Paid, &row.ActiveNow); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (db *DB) GetFirstTouchInvitees(createdBy int64, start *time.Time) ([]int64, error) {
	from := time.Unix(0, 0).UTC()
	if start != nil {
		from = start.UTC()
	}
	rows, err := db.conn.Query(
		`WITH first_invite AS (
			SELECT used_by, created_by, used_at, kind,
			       ROW_NUMBER() OVER (PARTITION BY used_by ORDER BY used_at, created_at, rowid) rn
			FROM invites WHERE used_by IS NOT NULL
		)
		SELECT used_by FROM first_invite WHERE rn = 1 AND kind = ? AND created_by = ? AND used_at >= ?`,
		InviteKindReferral, createdBy, from,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
