package database

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// InviteWithUser содержит информацию об инвайте вместе с данными пользователя, который его активировал
type InviteWithUser struct {
	Code             string
	CreatedBy        int64
	UsedBy           *int64
	UsedAt           *time.Time
	CreatedAt        time.Time
	UserUsername     string // Username пользователя, который активировал код
	UserFirstName    string // First name пользователя, который активировал код
	CreatorUsername  string // Username автора кода
	CreatorFirstName string // First name автора кода
}

// Subscriber содержит подписчика модератора.
// Поля профиля могут быть nil, если пользователь удалён из users.
type Subscriber struct {
	TelegramID    int64
	Username      *string
	FirstName     *string
	RemnawaveUUID *string
}

// CreateInvite создаёт новый инвайт
func (db *DB) CreateInvite(createdBy int64) (*Invite, error) {
	return db.CreateInviteWithExpiry(createdBy, nil)
}

// CreateInviteWithExpiry создаёт новый инвайт с опциональным сроком действия.
// expireDays = nil означает бессрочный инвайт.
func (db *DB) CreateInviteWithExpiry(createdBy int64, expireDays *int) (*Invite, error) {
	code, err := generateInviteCode()
	if err != nil {
		return nil, fmt.Errorf("failed to generate invite code: %w", err)
	}

	_, err = db.conn.Exec(
		`INSERT INTO invites (code, created_by, expire_days) VALUES (?, ?, ?)`,
		code, createdBy, expireDays,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create invite: %w", err)
	}

	return db.GetInviteByCode(code)
}

// GetInviteByCode получает инвайт по коду
func (db *DB) GetInviteByCode(code string) (*Invite, error) {
	var invite Invite
	var usedBy sql.NullInt64
	var usedAt sql.NullTime
	var expireDays sql.NullInt64
	var subscriptionPrice sql.NullInt64
	var kickedAt sql.NullTime

	err := db.conn.QueryRow(
		`SELECT code, created_by, used_by, used_at, expire_days, subscription_price, kicked_at, created_at FROM invites WHERE code = ?`,
		code,
	).Scan(&invite.Code, &invite.CreatedBy, &usedBy, &usedAt, &expireDays, &subscriptionPrice, &kickedAt, &invite.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get invite: %w", err)
	}

	if usedBy.Valid {
		invite.UsedBy = &usedBy.Int64
	}
	if usedAt.Valid {
		invite.UsedAt = &usedAt.Time
	}
	if expireDays.Valid {
		v := int(expireDays.Int64)
		invite.ExpireDays = &v
	}
	if subscriptionPrice.Valid {
		v := int(subscriptionPrice.Int64)
		invite.SubscriptionPrice = &v
	}
	if kickedAt.Valid {
		invite.KickedAt = &kickedAt.Time
	}

	return &invite, nil
}

// ClaimInvite атомарно помечает инвайт как использованный (защита от race condition).
// Отклоняет инвайт если он уже использован или помечен как кикнутый (kicked_at IS NOT NULL).
func (db *DB) ClaimInvite(code string, usedBy int64) error {
	result, err := db.conn.Exec(
		`UPDATE invites SET used_by = ?, used_at = CURRENT_TIMESTAMP
		 WHERE code = ? AND used_by IS NULL AND kicked_at IS NULL`,
		usedBy, code,
	)
	if err != nil {
		return fmt.Errorf("failed to claim invite: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("invite not found or already used")
	}

	return nil
}

// ReconcileOrphanedInvites откатывает инвайты, застрявшие в состоянии "в процессе регистрации":
// claimed недавно (< 1 часа) но без соответствующего пользователя в users.
// Это защита от краша между ClaimInvite и CreateUser.
// Старые claimed-инвайты без пользователя не трогаются — они могут относиться к забаненным пользователям.
// Возвращает количество откаченных инвайтов.
func (db *DB) ReconcileOrphanedInvites() (int, error) {
	result, err := db.conn.Exec(`
		UPDATE invites
		SET used_by = NULL, used_at = NULL
		WHERE used_by IS NOT NULL
		  AND used_by NOT IN (SELECT telegram_id FROM users)
		  AND used_at >= datetime('now', '-1 hour')
	`)
	if err != nil {
		return 0, fmt.Errorf("failed to reconcile orphaned invites: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get affected rows: %w", err)
	}

	return int(rows), nil
}

// UnclaimInvite откатывает claim инвайта (если создание пользователя не удалось)
func (db *DB) UnclaimInvite(code string) error {
	_, err := db.conn.Exec(
		`UPDATE invites SET used_by = NULL, used_at = NULL WHERE code = ?`,
		code,
	)
	if err != nil {
		return fmt.Errorf("failed to unclaim invite: %w", err)
	}
	return nil
}

// UpdateInviteExpireDays обновляет срок действия инвайта по пользователю, который его активировал.
// expireDays = nil означает бессрочный тариф.
func (db *DB) UpdateInviteExpireDays(usedBy int64, expireDays *int) error {
	result, err := db.conn.Exec(
		`UPDATE invites
		 SET expire_days = ?
		 WHERE used_by = ? AND kicked_at IS NULL`,
		expireDays, usedBy,
	)
	if err != nil {
		return fmt.Errorf("failed to update invite expire_days: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("invite not found")
	}

	return nil
}

// UseInvite помечает инвайт как использованный с временем активации
// Deprecated: используй ClaimInvite для атомарной операции
func (db *DB) UseInvite(code string, usedBy int64) error {
	result, err := db.conn.Exec(
		`UPDATE invites SET used_by = ?, used_at = CURRENT_TIMESTAMP WHERE code = ? AND used_by IS NULL`,
		usedBy, code,
	)
	if err != nil {
		return fmt.Errorf("failed to use invite: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("invite not found or already used")
	}

	return nil
}

// IsInviteValid проверяет валиден ли инвайт (существует и не использован)
func (db *DB) IsInviteValid(code string) (bool, error) {
	var exists bool
	err := db.conn.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM invites WHERE code = ? AND used_by IS NULL)`,
		code,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check invite: %w", err)
	}
	return exists, nil
}

// GetAllInvites получает все инвайты
func (db *DB) GetAllInvites() ([]Invite, error) {
	rows, err := db.conn.Query(
		`SELECT code, created_by, used_by, used_at, expire_days, subscription_price, created_at FROM invites ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query invites: %w", err)
	}
	defer rows.Close()

	var invites []Invite
	for rows.Next() {
		var invite Invite
		var usedBy sql.NullInt64
		var usedAt sql.NullTime
		var expireDays sql.NullInt64
		var subscriptionPrice sql.NullInt64

		if err := rows.Scan(&invite.Code, &invite.CreatedBy, &usedBy, &usedAt, &expireDays, &subscriptionPrice, &invite.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan invite: %w", err)
		}

		if usedBy.Valid {
			invite.UsedBy = &usedBy.Int64
		}
		if usedAt.Valid {
			invite.UsedAt = &usedAt.Time
		}
		if expireDays.Valid {
			v := int(expireDays.Int64)
			invite.ExpireDays = &v
		}
		if subscriptionPrice.Valid {
			v := int(subscriptionPrice.Int64)
			invite.SubscriptionPrice = &v
		}

		invites = append(invites, invite)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return invites, nil
}

// GetUnusedInvites получает неиспользованные инвайты
func (db *DB) GetUnusedInvites() ([]Invite, error) {
	rows, err := db.conn.Query(
		`SELECT code, created_by, used_by, used_at, expire_days, subscription_price, created_at FROM invites WHERE used_by IS NULL ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query invites: %w", err)
	}
	defer rows.Close()

	var invites []Invite
	for rows.Next() {
		var invite Invite
		var usedBy sql.NullInt64
		var usedAt sql.NullTime
		var expireDays sql.NullInt64
		var subscriptionPrice sql.NullInt64

		if err := rows.Scan(&invite.Code, &invite.CreatedBy, &usedBy, &usedAt, &expireDays, &subscriptionPrice, &invite.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan invite: %w", err)
		}
		if usedAt.Valid {
			invite.UsedAt = &usedAt.Time
		}
		if expireDays.Valid {
			v := int(expireDays.Int64)
			invite.ExpireDays = &v
		}
		if subscriptionPrice.Valid {
			v := int(subscriptionPrice.Int64)
			invite.SubscriptionPrice = &v
		}

		invites = append(invites, invite)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return invites, nil
}

// DeleteInvite удаляет инвайт
func (db *DB) DeleteInvite(code string) error {
	_, err := db.conn.Exec(`DELETE FROM invites WHERE code = ?`, code)
	if err != nil {
		return fmt.Errorf("failed to delete invite: %w", err)
	}
	return nil
}

// CountUnusedInvites возвращает количество неиспользованных инвайтов
func (db *DB) CountUnusedInvites() (int, error) {
	var count int
	err := db.conn.QueryRow(`SELECT COUNT(*) FROM invites WHERE used_by IS NULL`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count invites: %w", err)
	}
	return count, nil
}

// GetAllInvitesWithUsers получает все инвайты с информацией о пользователях и авторах
func (db *DB) GetAllInvitesWithUsers() ([]InviteWithUser, error) {
	query := `
		SELECT
			i.code, i.created_by, i.used_by, i.used_at, i.created_at,
			u.username, u.first_name,
			c.username, c.first_name
		FROM invites i
		LEFT JOIN users u ON i.used_by = u.telegram_id
		LEFT JOIN users c ON i.created_by = c.telegram_id
		ORDER BY i.created_at DESC
	`

	rows, err := db.conn.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query invites: %w", err)
	}
	defer rows.Close()

	var invites []InviteWithUser
	for rows.Next() {
		var inv InviteWithUser
		var usedBy sql.NullInt64
		var usedAt sql.NullTime
		var username, firstName sql.NullString
		var creatorUsername, creatorFirstName sql.NullString

		err := rows.Scan(
			&inv.Code, &inv.CreatedBy, &usedBy, &usedAt, &inv.CreatedAt,
			&username, &firstName,
			&creatorUsername, &creatorFirstName,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan invite: %w", err)
		}

		if usedBy.Valid {
			inv.UsedBy = &usedBy.Int64
		}
		if usedAt.Valid {
			inv.UsedAt = &usedAt.Time
		}
		if username.Valid {
			inv.UserUsername = username.String
		}
		if firstName.Valid {
			inv.UserFirstName = firstName.String
		}
		if creatorUsername.Valid {
			inv.CreatorUsername = creatorUsername.String
		}
		if creatorFirstName.Valid {
			inv.CreatorFirstName = creatorFirstName.String
		}

		invites = append(invites, inv)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return invites, nil
}

// DeleteUnusedInvite удаляет только неиспользованный инвайт
func (db *DB) DeleteUnusedInvite(code string) error {
	result, err := db.conn.Exec(
		`DELETE FROM invites WHERE code = ? AND used_by IS NULL`,
		code,
	)
	if err != nil {
		return fmt.Errorf("failed to delete invite: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("invite not found or already used")
	}

	return nil
}

// GetInvitesWithUsersByCreator получает инвайты конкретного автора с данными пользователей
func (db *DB) GetInvitesWithUsersByCreator(createdBy int64) ([]InviteWithUser, error) {
	query := `
		SELECT
			i.code, i.created_by, i.used_by, i.used_at, i.created_at,
			u.username, u.first_name
		FROM invites i
		LEFT JOIN users u ON i.used_by = u.telegram_id
		WHERE i.created_by = ?
		ORDER BY i.created_at DESC
	`

	rows, err := db.conn.Query(query, createdBy)
	if err != nil {
		return nil, fmt.Errorf("failed to query invites by creator: %w", err)
	}
	defer rows.Close()

	var invites []InviteWithUser
	for rows.Next() {
		var inv InviteWithUser
		var usedBy sql.NullInt64
		var usedAt sql.NullTime
		var username, firstName sql.NullString

		err := rows.Scan(
			&inv.Code, &inv.CreatedBy, &usedBy, &usedAt, &inv.CreatedAt,
			&username, &firstName,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan invite: %w", err)
		}

		if usedBy.Valid {
			inv.UsedBy = &usedBy.Int64
		}
		if usedAt.Valid {
			inv.UsedAt = &usedAt.Time
		}
		if username.Valid {
			inv.UserUsername = username.String
		}
		if firstName.Valid {
			inv.UserFirstName = firstName.String
		}

		invites = append(invites, inv)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return invites, nil
}

// GetInviteByUsedBy получает инвайт, которым был зарегистрирован пользователь.
func (db *DB) GetInviteByUsedBy(usedBy int64) (*Invite, error) {
	var invite Invite
	var usedByNullable sql.NullInt64
	var usedAt sql.NullTime
	var expireDays sql.NullInt64
	var subscriptionPrice sql.NullInt64
	var kickedAt sql.NullTime

	err := db.conn.QueryRow(
		`SELECT code, created_by, used_by, used_at, expire_days, subscription_price, kicked_at, created_at
		 FROM invites
		 WHERE used_by = ? AND kicked_at IS NULL
		 ORDER BY used_at DESC
		 LIMIT 1`,
		usedBy,
	).Scan(&invite.Code, &invite.CreatedBy, &usedByNullable, &usedAt, &expireDays, &subscriptionPrice, &kickedAt, &invite.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get invite by used_by: %w", err)
	}

	if usedByNullable.Valid {
		invite.UsedBy = &usedByNullable.Int64
	}
	if usedAt.Valid {
		invite.UsedAt = &usedAt.Time
	}
	if expireDays.Valid {
		v := int(expireDays.Int64)
		invite.ExpireDays = &v
	}
	if subscriptionPrice.Valid {
		v := int(subscriptionPrice.Int64)
		invite.SubscriptionPrice = &v
	}
	if kickedAt.Valid {
		invite.KickedAt = &kickedAt.Time
	}

	return &invite, nil
}

// GetSubscribersByModerator возвращает подписчиков, приглашённых модератором.
// Пользователи без записи в users также возвращаются (LEFT JOIN).
func (db *DB) GetSubscribersByModerator(moderatorID int64) ([]Subscriber, error) {
	rows, err := db.conn.Query(
		`SELECT i.used_by, u.username, u.first_name, u.remnawave_uuid
		 FROM invites i
		 LEFT JOIN users u ON i.used_by = u.telegram_id
		 WHERE i.created_by = ? AND i.used_by IS NOT NULL
		 ORDER BY i.used_at DESC, i.created_at DESC`,
		moderatorID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query subscribers by moderator: %w", err)
	}
	defer rows.Close()

	var subscribers []Subscriber
	for rows.Next() {
		var sub Subscriber
		var usedBy sql.NullInt64
		var username sql.NullString
		var firstName sql.NullString
		var remnawaveUUID sql.NullString

		if err := rows.Scan(&usedBy, &username, &firstName, &remnawaveUUID); err != nil {
			return nil, fmt.Errorf("failed to scan subscriber: %w", err)
		}

		if !usedBy.Valid {
			continue
		}
		sub.TelegramID = usedBy.Int64
		if username.Valid {
			v := username.String
			sub.Username = &v
		}
		if firstName.Valid {
			v := firstName.String
			sub.FirstName = &v
		}
		if remnawaveUUID.Valid {
			v := remnawaveUUID.String
			sub.RemnawaveUUID = &v
		}
		subscribers = append(subscribers, sub)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return subscribers, nil
}

// IsSubscriberOfModerator проверяет, что подписчик был приглашён конкретным модератором.
func (db *DB) IsSubscriberOfModerator(moderatorID, subscriberID int64) (bool, error) {
	var exists bool
	err := db.conn.QueryRow(
		`SELECT EXISTS(
			SELECT 1 FROM invites
			WHERE created_by = ? AND used_by = ? AND kicked_at IS NULL
		)`,
		moderatorID, subscriberID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check subscriber owner: %w", err)
	}
	return exists, nil
}

// MarkInviteKickedByTelegramID проставляет kicked_at для инвайта пользователя при автокике.
// Инвайт остаётся «использованным» (used_by не обнуляется) — история активации сохраняется.
// ClaimInvite отклонит такой инвайт при попытке повторного использования.
func (db *DB) MarkInviteKickedByTelegramID(telegramID int64) error {
	_, err := db.conn.Exec(
		`UPDATE invites SET kicked_at = CURRENT_TIMESTAMP WHERE used_by = ?`,
		telegramID,
	)
	if err != nil {
		return fmt.Errorf("failed to mark invite as kicked: %w", err)
	}
	return nil
}

// ResetInviteUsageByTelegramID освобождает использованный инвайт пользователя.
func (db *DB) ResetInviteUsageByTelegramID(telegramID int64) error {
	_, err := db.conn.Exec(
		`UPDATE invites
		 SET used_by = NULL, used_at = NULL
		 WHERE used_by = ?`,
		telegramID,
	)
	if err != nil {
		return fmt.Errorf("failed to reset invite usage: %w", err)
	}
	return nil
}

// DeleteUnusedInviteByOwner удаляет только свой неиспользованный инвайт
func (db *DB) DeleteUnusedInviteByOwner(code string, createdBy int64) error {
	result, err := db.conn.Exec(
		`DELETE FROM invites WHERE code = ? AND used_by IS NULL AND created_by = ?`,
		code, createdBy,
	)
	if err != nil {
		return fmt.Errorf("failed to delete invite: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("invite not found, already used, or not owned by you")
	}

	return nil
}

// DeleteUnusedInvitesByCreator удаляет все неиспользованные инвайты конкретного автора
func (db *DB) DeleteUnusedInvitesByCreator(createdBy int64) (int64, error) {
	result, err := db.conn.Exec(
		`DELETE FROM invites WHERE created_by = ? AND used_by IS NULL`,
		createdBy,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to delete invites: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get affected rows: %w", err)
	}

	return rows, nil
}

// CreateInviteWithPrice создаёт модераторский инвайт с ценой подписки.
// expireDays = срок действия инвайта в днях, price = цена подписки в руб/мес.
func (db *DB) CreateInviteWithPrice(createdBy int64, expireDays int, price int) (string, error) {
	code, err := generateInviteCode()
	if err != nil {
		return "", fmt.Errorf("failed to generate invite code: %w", err)
	}
	_, err = db.conn.Exec(
		`INSERT INTO invites (code, created_by, expire_days, subscription_price) VALUES (?, ?, ?, ?)`,
		code, createdBy, expireDays, price,
	)
	if err != nil {
		return "", fmt.Errorf("failed to create invite with price: %w", err)
	}
	return code, nil
}

// generateInviteCode генерирует случайный 8-символьный код
func generateInviteCode() (string, error) {
	bytes := make([]byte, 4)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
