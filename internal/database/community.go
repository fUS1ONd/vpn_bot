package database

import (
	"database/sql"
	"fmt"
	"time"
)

// Таблица community_members — знания бота о Канале. Их ровно два: пользователь
// в Канале и когда ему в последний раз показывали приписку про Канал.
//
// «В Канале» ставится по двум поводам: бот одобрил заявку либо getChatMember
// подтвердил, что человек в чате уже состоит. Второй повод обязателен — вступают
// и по прямой ссылке владельца, и до появления гейта, а таким участникам бот
// иначе годами предлагает вступить туда, где они давно сидят.
//
// Отдельная таблица, а не notifications_sent: ClearNotifications стирает маркеры
// уведомлений при каждой оплате и бане, а пометка «в Канале» обязана пережить и
// оплату, и деплой — иначе оплативший участник снова получал бы приглашение
// туда, где уже состоит.

// MarkCommunityJoined ставит пометку «в Канале». Повторный вызов не сдвигает
// момент вступления: заявка может быть одобрена не один раз (Telegram не
// запрещает повторные заявки после выхода из Канала).
func (db *DB) MarkCommunityJoined(telegramID int64, joinedAt time.Time) error {
	_, err := db.conn.Exec(
		`INSERT INTO community_members (telegram_id, joined_at)
		 VALUES (?, ?)
		 ON CONFLICT(telegram_id) DO UPDATE SET
		   joined_at = COALESCE(community_members.joined_at, excluded.joined_at)`,
		telegramID, joinedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("failed to mark community membership: %w", err)
	}
	return nil
}

// IsCommunityMember сообщает, знает ли бот этого пользователя как участника
// Канала. Выход из Канала пометку не снимает: приписка зовёт узнать о
// сообществе, а не вернуться в него, и ушедшему сознательно она не нужна.
func (db *DB) IsCommunityMember(telegramID int64) (bool, error) {
	var exists bool
	err := db.conn.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM community_members WHERE telegram_id = ? AND joined_at IS NOT NULL)`,
		telegramID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check community membership: %w", err)
	}
	return exists, nil
}

// CommunityMentionSentAt возвращает момент последнего показа приписки про Канал
// или nil, если её ещё не показывали.
func (db *DB) CommunityMentionSentAt(telegramID int64) (*time.Time, error) {
	var sentAt sql.NullTime
	err := db.conn.QueryRow(
		`SELECT mention_sent_at FROM community_members WHERE telegram_id = ?`,
		telegramID,
	).Scan(&sentAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read community mention timestamp: %w", err)
	}
	if !sentAt.Valid {
		return nil, nil
	}
	return &sentAt.Time, nil
}

// MarkCommunityMentionSent фиксирует показ приписки про Канал — кулдаун общий на
// все места показа и переживает деплой.
func (db *DB) MarkCommunityMentionSent(telegramID int64, sentAt time.Time) error {
	_, err := db.conn.Exec(
		`INSERT INTO community_members (telegram_id, mention_sent_at)
		 VALUES (?, ?)
		 ON CONFLICT(telegram_id) DO UPDATE SET mention_sent_at = excluded.mention_sent_at`,
		telegramID, sentAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("failed to mark community mention: %w", err)
	}
	return nil
}
