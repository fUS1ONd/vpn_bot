package database

import (
	"database/sql"
	"fmt"
)

// userColumns — список колонок в порядке, в котором его ждёт scanUser.
const userColumns = `telegram_id, username, first_name, remnawave_uuid, remnawave_id,
	subscription_price, moderator_id, invited_by, legacy_paid_migrated, created_at`

// rowScanner объединяет *sql.Row и *sql.Rows — оба умеют Scan.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanUser читает строку users. Все nullable-колонки идут через sql.Null*:
// remnawave_uuid стал nullable вместе с переходом на 3.x, и прямой Scan в string
// падал бы с «converting NULL to string is unsupported» на каждом пользователе,
// зарегистрированном после апгрейда панели.
func scanUser(scanner rowScanner) (*User, error) {
	var user User
	var firstName sql.NullString
	var remnawaveUUID sql.NullString
	var remnawaveID sql.NullInt64
	var subPrice sql.NullInt64
	var modID sql.NullInt64
	var invitedBy sql.NullInt64
	var legacyPaidMigrated sql.NullInt64

	err := scanner.Scan(
		&user.TelegramID, &user.Username, &firstName, &remnawaveUUID, &remnawaveID,
		&subPrice, &modID, &invitedBy, &legacyPaidMigrated, &user.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	if firstName.Valid {
		user.FirstName = firstName.String
	}
	if remnawaveUUID.Valid && remnawaveUUID.String != "" {
		uuid := remnawaveUUID.String
		user.RemnawaveUUID = &uuid
	}
	if remnawaveID.Valid && remnawaveID.Int64 != 0 {
		id := remnawaveID.Int64
		user.RemnawaveID = &id
	}
	if subPrice.Valid {
		v := int(subPrice.Int64)
		user.SubscriptionPrice = &v
	}
	if modID.Valid {
		user.ModeratorID = &modID.Int64
	}
	if invitedBy.Valid {
		user.InvitedBy = &invitedBy.Int64
	}
	user.LegacyPaidMigrated = legacyPaidMigrated.Valid && legacyPaidMigrated.Int64 != 0

	return &user, nil
}

// nullableUUID приводит пустой UUID к NULL. Колонка remnawave_uuid объявлена
// UNIQUE: SQLite допускает сколько угодно NULL, но не две пустые строки, —
// иначе второй же зарегистрировавшийся на 3.x пользователь получил бы
// UNIQUE constraint failed.
func nullableUUID(uuid *string) any {
	if uuid == nil || *uuid == "" {
		return nil
	}
	return *uuid
}

// nullableID приводит нулевой id к NULL по той же причине, что и UUID.
func nullableID(id *int64) any {
	if id == nil || *id == 0 {
		return nil
	}
	return *id
}

// CreateUser создаёт нового пользователя
func (db *DB) CreateUser(telegramID int64, username, firstName string, remnawaveUUID *string, remnawaveID *int64, subscriptionPrice *int, moderatorID *int64) (*User, error) {
	return db.CreateUserWithInviter(telegramID, username, firstName, remnawaveUUID, remnawaveID, subscriptionPrice, moderatorID, moderatorID)
}

// CreateUserWithInviter создаёт пользователя с раздельными архивным moderator_id
// и нейтральным first-touch invited_by.
func (db *DB) CreateUserWithInviter(telegramID int64, username, firstName string, remnawaveUUID *string, remnawaveID *int64, subscriptionPrice *int, moderatorID, invitedBy *int64) (*User, error) {
	_, err := db.conn.Exec(
		`INSERT INTO users (telegram_id, username, first_name, remnawave_uuid, remnawave_id, subscription_price, moderator_id, invited_by, legacy_paid_migrated) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		telegramID, username, firstName, nullableUUID(remnawaveUUID), nullableID(remnawaveID), subscriptionPrice, moderatorID, invitedBy,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	return db.GetUserByTelegramID(telegramID)
}

// GetUserByTelegramID получает пользователя по Telegram ID
func (db *DB) GetUserByTelegramID(telegramID int64) (*User, error) {
	user, err := scanUser(db.conn.QueryRow(
		`SELECT `+userColumns+` FROM users WHERE telegram_id = ?`, telegramID,
	))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	return user, nil
}

// GetUserByRemnawaveUUID получает пользователя по Remnawave UUID.
// Работает только для записей, созданных на панели 2.8.x.
func (db *DB) GetUserByRemnawaveUUID(uuid string) (*User, error) {
	user, err := scanUser(db.conn.QueryRow(
		`SELECT `+userColumns+` FROM users WHERE remnawave_uuid = ?`, uuid,
	))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	return user, nil
}

// GetUserByRemnawaveID получает пользователя по числовому идентификатору панели.
func (db *DB) GetUserByRemnawaveID(id int64) (*User, error) {
	user, err := scanUser(db.conn.QueryRow(
		`SELECT `+userColumns+` FROM users WHERE remnawave_id = ?`, id,
	))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	return user, nil
}

// SetRemnawaveID сохраняет числовой идентификатор пользователя панели.
// Уникальный индекс не даст привязать один и тот же id к двум Telegram ID:
// такая ошибка означает рассинхрон с панелью, и молчать о ней нельзя.
func (db *DB) SetRemnawaveID(telegramID, remnawaveID int64) error {
	_, err := db.conn.Exec(`UPDATE users SET remnawave_id = ? WHERE telegram_id = ?`, remnawaveID, telegramID)
	if err != nil {
		return fmt.Errorf("failed to set remnawave_id for telegram_id=%d: %w", telegramID, err)
	}
	return nil
}

// UsersMissingRemnawaveID возвращает пользователей без связки с числовым id —
// то, что добирают backfill при старте и плановая доливка scheduler.
func (db *DB) UsersMissingRemnawaveID() ([]User, error) {
	return db.queryUsers(`SELECT ` + userColumns + ` FROM users WHERE remnawave_id IS NULL ORDER BY created_at`)
}

// GetAllUsers получает всех пользователей
func (db *DB) GetAllUsers() ([]User, error) {
	return db.queryUsers(`SELECT ` + userColumns + ` FROM users ORDER BY created_at`)
}

func (db *DB) queryUsers(query string, args ...any) ([]User, error) {
	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}
		users = append(users, *user)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return users, nil
}

// UpdateSubscriptionPrice обновляет цену подписки пользователя
func (db *DB) UpdateSubscriptionPrice(telegramID int64, price int) error {
	_, err := db.conn.Exec(`UPDATE users SET subscription_price = ? WHERE telegram_id = ?`, price, telegramID)
	return err
}

// UpdateSubscriptionPriceAndLegacyPaidMigrated обновляет цену и флаг migration в одной транзакции.
func (db *DB) UpdateSubscriptionPriceAndLegacyPaidMigrated(telegramID int64, price int, legacyPaidMigrated *bool) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}

	rollback := func(err error) error {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("%w: rollback failed: %v", err, rbErr)
		}
		return err
	}

	if _, err := tx.Exec(`UPDATE users SET subscription_price = ? WHERE telegram_id = ?`, price, telegramID); err != nil {
		return rollback(err)
	}

	if legacyPaidMigrated != nil {
		intValue := 0
		if *legacyPaidMigrated {
			intValue = 1
		}
		if _, err := tx.Exec(`UPDATE users SET legacy_paid_migrated = ? WHERE telegram_id = ?`, intValue, telegramID); err != nil {
			return rollback(err)
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	return nil
}

// SetLegacyPaidMigrated помечает пользователя как переведённого со старой ручной оплаты.
func (db *DB) SetLegacyPaidMigrated(telegramID int64, value bool) error {
	var intValue int
	if value {
		intValue = 1
	}
	_, err := db.conn.Exec(`UPDATE users SET legacy_paid_migrated = ? WHERE telegram_id = ?`, intValue, telegramID)
	return err
}

// UpdateUsername обновляет username пользователя
func (db *DB) UpdateUsername(telegramID int64, username string) error {
	_, err := db.conn.Exec(
		`UPDATE users SET username = ? WHERE telegram_id = ?`,
		username, telegramID,
	)
	if err != nil {
		return fmt.Errorf("failed to update username: %w", err)
	}
	return nil
}

// UpdateUserInfo обновляет username и first_name пользователя (Upsert для актуализации данных)
func (db *DB) UpdateUserInfo(telegramID int64, username, firstName string) error {
	_, err := db.conn.Exec(
		`UPDATE users SET username = ?, first_name = ? WHERE telegram_id = ?`,
		username, firstName, telegramID,
	)
	if err != nil {
		return fmt.Errorf("failed to update user info: %w", err)
	}
	return nil
}

// DeleteUser удаляет пользователя. Вместе с ним умирают согласие на
// автопродление, Способ автосписания и история попыток: хранить платёжный токен
// человека, которого мы сами отключили, оснований нет, а вернувшийся по новому
// инвайту получит новый триал и новую цену — ожившее по старой цене списание
// стало бы для него сюрпризом.
func (db *DB) DeleteUser(telegramID int64) error {
	if err := db.DeleteAutorenewal(telegramID); err != nil {
		return err
	}
	_, err := db.conn.Exec(`DELETE FROM users WHERE telegram_id = ?`, telegramID)
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}
	return nil
}

// UserExists проверяет существует ли пользователь
func (db *DB) UserExists(telegramID int64) (bool, error) {
	var exists bool
	err := db.conn.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM users WHERE telegram_id = ?)`,
		telegramID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check user existence: %w", err)
	}
	return exists, nil
}

// CountUsers возвращает количество пользователей
func (db *DB) CountUsers() (int, error) {
	var count int
	err := db.conn.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count users: %w", err)
	}
	return count, nil
}
