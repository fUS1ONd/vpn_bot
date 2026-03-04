package database

import "fmt"

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

// ClearNotifications очищает все маркеры отправленных уведомлений пользователя.
func (db *DB) ClearNotifications(telegramID int64) error {
	_, err := db.conn.Exec(
		`DELETE FROM notifications_sent WHERE telegram_id = ?`,
		telegramID,
	)
	if err != nil {
		return fmt.Errorf("failed to clear notifications: %w", err)
	}
	return nil
}
