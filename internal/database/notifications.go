package database

import (
	"database/sql"
	"fmt"
	"time"
)

// WasNotificationSent проверяет, отправлялось ли уведомление указанного типа.
func (db *DB) WasNotificationSent(telegramID int64, notificationType string) (bool, error) {
	var exists bool
	err := db.conn.QueryRow(
		`SELECT EXISTS(
			SELECT 1 FROM notifications_sent
			WHERE telegram_id = ? AND type = ?
		)`,
		telegramID, notificationType,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check notification status: %w", err)
	}
	return exists, nil
}

// NotificationSentAt возвращает момент последней отправки уведомления или nil,
// если оно не отправлялось. Нужен там, где важна не сама отправка, а её давность:
// например, суточная сводка должна пережить перезапуск бота.
func (db *DB) NotificationSentAt(telegramID int64, notificationType string) (*time.Time, error) {
	var sentAt sql.NullTime
	err := db.conn.QueryRow(
		`SELECT sent_at FROM notifications_sent WHERE telegram_id = ? AND type = ?`,
		telegramID, notificationType,
	).Scan(&sentAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read notification timestamp: %w", err)
	}
	if !sentAt.Valid {
		return nil, nil
	}
	return &sentAt.Time, nil
}

// MarkNotificationSent фиксирует факт отправки уведомления.
func (db *DB) MarkNotificationSent(telegramID int64, notificationType string) error {
	_, err := db.conn.Exec(
		`INSERT INTO notifications_sent (telegram_id, type)
		 VALUES (?, ?)
		 ON CONFLICT(telegram_id, type) DO UPDATE SET
		   sent_at = CURRENT_TIMESTAMP`,
		telegramID, notificationType,
	)
	if err != nil {
		return fmt.Errorf("failed to mark notification sent: %w", err)
	}
	return nil
}

// receiptNotificationPrefix — маркеры сообщений о чеках. Они лежат в той же
// таблице, но принадлежат владельцу как получателю алертов, а не пользователю как
// подписчику: владелец — тоже пользователь бота и платит тестовыми платежами, и
// без этой оговорки любой его платёж стирал бы память о застрявших чеках, а сводка
// начинала бы отсчёт суток заново.
const receiptNotificationPrefix = "receipt"

// ClearNotifications очищает маркеры уведомлений о подписке. Маркеры по чекам
// (см. receiptNotificationPrefix) переживают оплату — их снимает только разбор
// самого чека.
func (db *DB) ClearNotifications(telegramID int64) error {
	_, err := db.conn.Exec(
		`DELETE FROM notifications_sent WHERE telegram_id = ? AND type NOT LIKE ? || '%'`,
		telegramID, receiptNotificationPrefix,
	)
	if err != nil {
		return fmt.Errorf("failed to clear notifications: %w", err)
	}
	return nil
}
