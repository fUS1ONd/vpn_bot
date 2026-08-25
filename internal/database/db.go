package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DB оборачивает операции с базой данных
type DB struct {
	conn       *sql.DB
	referralMu sync.Mutex
}

// User представляет запись пользователя
type User struct {
	TelegramID int64
	Username   string
	FirstName  string // Имя пользователя из Telegram
	// RemnawaveUUID заполнен только у пользователей, созданных на панели 2.8.x:
	// в 3.x колонка uuid удалена из базы панели, и у новых записей здесь NULL.
	RemnawaveUUID *string
	// RemnawaveID — числовой идентификатор пользователя панели. Он один и тот же
	// в обеих версиях API и единственный на 3.x.
	RemnawaveID        *int64
	SubscriptionPrice  *int   // Цена подписки руб/мес (NULL = не установлена)
	ModeratorID        *int64 // Telegram ID куратора (NULL = админский/снят)
	InvitedBy          *int64 // Telegram ID первого реферального автора
	LegacyPaidMigrated bool   // Старый пользователь с ручной оплатой, переведённый на новую модель
	CreatedAt          time.Time
}

// Invite представляет запись инвайта
type Invite struct {
	Code              string
	CreatedBy         int64
	UsedBy            *int64
	UsedAt            *time.Time // Время активации кода
	ExpireDays        *int       // NULL = бессрочный инвайт
	SubscriptionPrice *int       // Цена подписки при создании инвайта
	KickedAt          *time.Time // Время автокика — инвайт нельзя переиспользовать
	Kind              string     // admin или referral
	IsTrial           bool       // Неизменяемый признак trial-инвайта
	ExpiresAt         *time.Time // Срок действия неиспользованной ссылки
	RevokedAt         *time.Time // Время мягкого отзыва
	RevokedBy         *int64     // Кто отозвал ссылку
	CreatedAt         time.Time
}

const (
	InviteKindAdmin    = "admin"
	InviteKindReferral = "referral"
)

// New создаёт новое подключение к БД и инициализирует схему
func New(dbPath string) (*DB, error) {
	// Создаём директорию если не существует
	dir := filepath.Dir(dbPath)
	if dir != "" && dir != "." && dir != ":memory:" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create database directory: %w", err)
		}
	}

	// Открываем подключение к БД
	conn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Включаем WAL mode для корректной работы при concurrent writes
	// (callback-сервер + scheduler + Telegram handler)
	if _, err := conn.Exec("PRAGMA journal_mode = WAL"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
	}

	// Включаем foreign keys
	if _, err := conn.Exec("PRAGMA foreign_keys = ON"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	// Запускаем миграции
	if err := migrate(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return &DB{conn: conn}, nil
}

// migrate выполняет миграции БД
func migrate(conn *sql.DB) error {
	migrations := []string{
		// Таблица пользователей — связка Telegram <-> Remnawave
		// remnawave_uuid объявлен без NOT NULL: у пользователя, созданного на
		// панели 3.x, UUID нет вовсе.
		`CREATE TABLE IF NOT EXISTS users (
			telegram_id INTEGER PRIMARY KEY,
			username TEXT,
			remnawave_uuid TEXT UNIQUE,
			remnawave_id INTEGER,
			invited_by INTEGER,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,

		// Таблица инвайтов
		`CREATE TABLE IF NOT EXISTS invites (
				code TEXT PRIMARY KEY,
				created_by INTEGER NOT NULL,
				used_by INTEGER,
				used_at TIMESTAMP,
				expire_days INTEGER,
				is_trial INTEGER NOT NULL DEFAULT 0,
				kind TEXT NOT NULL DEFAULT 'admin',
				expires_at TIMESTAMP,
				revoked_at TIMESTAMP,
				revoked_by INTEGER,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			)`,

		// Таблица модераторов
		`CREATE TABLE IF NOT EXISTS moderators (
			telegram_id INTEGER PRIMARY KEY,
			added_by INTEGER NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,

		// Таблица банов (перманентные блокировки)
		`CREATE TABLE IF NOT EXISTS banned_users (
			telegram_id INTEGER PRIMARY KEY,
			banned_by INTEGER NOT NULL,
			reason TEXT,
			banned_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,

		// Таблица отправленных уведомлений по подписке
		`CREATE TABLE IF NOT EXISTS notifications_sent (
			telegram_id INTEGER NOT NULL,
			type TEXT NOT NULL,
			sent_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (telegram_id, type)
		)`,

		// Таблица платежей
		`CREATE TABLE IF NOT EXISTS payments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			telegram_id INTEGER NOT NULL,
			moderator_id INTEGER,
			amount INTEGER NOT NULL,
			payment_method TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			platega_transaction_id TEXT UNIQUE,
			provider TEXT NOT NULL DEFAULT 'platega',
			provider_payment_id TEXT,
			provider_request_key TEXT,
			provider_fee_percent INTEGER,
			is_test INTEGER NOT NULL DEFAULT 0,
			redirect_url TEXT,
			expires_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			confirmed_at TIMESTAMP
		)`,

		// Таблица начислений модераторов
		`CREATE TABLE IF NOT EXISTS moderator_earnings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			payment_id INTEGER NOT NULL REFERENCES payments(id),
			moderator_id INTEGER NOT NULL,
			gross_amount INTEGER NOT NULL,
			platega_fee INTEGER NOT NULL,
			withdrawal_fee INTEGER NOT NULL,
			net_amount INTEGER NOT NULL,
			share_percent INTEGER NOT NULL,
			share_amount INTEGER NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,

		// Таблица чеков «Мой налог». payment_id как PRIMARY KEY даёт связь 1:1
		// и физический барьер против второго чека по одному платежу.
		`CREATE TABLE IF NOT EXISTS receipts (
			payment_id INTEGER PRIMARY KEY,
			marker TEXT NOT NULL,
			receipt_uuid TEXT,
			state TEXT NOT NULL,
			operation_time TIMESTAMP NOT NULL,
			amount INTEGER NOT NULL,
			attempts INTEGER NOT NULL DEFAULT 0,
			last_error TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP
		)`,

		// Автопродление. enabled и payment_method_id независимы намеренно:
		// согласие без Способа и Способ без согласия оба нормальны, CHECK их
		// связывать нельзя. period_months сегодня всегда 1.
		`CREATE TABLE IF NOT EXISTS autorenewals (
			telegram_id INTEGER PRIMARY KEY,
			enabled INTEGER NOT NULL DEFAULT 0,
			payment_method_id TEXT,
			method_title TEXT,
			period_months INTEGER NOT NULL DEFAULT 1,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP
		)`,

		// Попытки автосписания. Ключ — барьер против дубля, привязка к
		// expire_at означает, что сдвинувшийся expireAt открывает новый цикл.
		`CREATE TABLE IF NOT EXISTS autorenew_attempts (
			telegram_id INTEGER NOT NULL,
			expire_at TIMESTAMP NOT NULL,
			attempt_no INTEGER NOT NULL,
			outcome TEXT NOT NULL,
			payment_id INTEGER,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (telegram_id, expire_at, attempt_no)
		)`,

		// Знания бота о Канале: одобренная заявка и последний показ приписки.
		// Отдельно от notifications_sent — эти пометки не должна стирать оплата.
		`CREATE TABLE IF NOT EXISTS community_members (
			telegram_id INTEGER PRIMARY KEY,
			joined_at TIMESTAMP,
			mention_sent_at TIMESTAMP
		)`,

		// Индексы. Индексы по users создаются после возможной перестройки таблицы:
		// DROP TABLE уносит их вместе со старой таблицей.
		`CREATE INDEX IF NOT EXISTS idx_invites_used_by ON invites(used_by)`,
		`CREATE INDEX IF NOT EXISTS idx_payments_telegram_id ON payments(telegram_id)`,
		`CREATE INDEX IF NOT EXISTS idx_payments_status ON payments(status)`,
		`CREATE INDEX IF NOT EXISTS idx_payments_platega_tx ON payments(platega_transaction_id)`,
		`CREATE INDEX IF NOT EXISTS idx_earnings_moderator ON moderator_earnings(moderator_id)`,
		`CREATE INDEX IF NOT EXISTS idx_earnings_payment ON moderator_earnings(payment_id)`,
		`CREATE INDEX IF NOT EXISTS idx_receipts_state ON receipts(state)`,
		`CREATE INDEX IF NOT EXISTS idx_autorenew_attempts_user ON autorenew_attempts(telegram_id)`,
	}

	for _, m := range migrations {
		if _, err := conn.Exec(m); err != nil {
			return fmt.Errorf("migration failed: %w\nSQL: %s", err, m)
		}
	}

	// Безопасные миграции ALTER TABLE (игнорируем ошибку "duplicate column")
	alterMigrations := []string{
		// Миграция: добавление поля first_name в таблицу users
		`ALTER TABLE users ADD COLUMN first_name TEXT`,
		// Миграция: добавление поля used_at в таблицу invites
		`ALTER TABLE invites ADD COLUMN used_at TIMESTAMP`,
		// Миграция: добавление срока действия инвайта в днях (NULL = бессрочно)
		`ALTER TABLE invites ADD COLUMN expire_days INTEGER`,
		// Миграция: метка автокика — инвайт нельзя использовать повторно, но история сохраняется
		`ALTER TABLE invites ADD COLUMN kicked_at TIMESTAMP`,
		// Миграция: цена подписки пользователя (руб/мес, NULL = не установлена)
		`ALTER TABLE users ADD COLUMN subscription_price INTEGER`,
		// Миграция: telegram_id модератора-куратора (NULL = админский или снят модератор)
		`ALTER TABLE users ADD COLUMN moderator_id INTEGER`,
		// Миграция: флаг старой ручной оплаты для перевода legacy-пользователей на новую модель
		`ALTER TABLE users ADD COLUMN legacy_paid_migrated INTEGER NOT NULL DEFAULT 0`,
		// Первый автор referral-приглашения; moderator_id остаётся архивным полем.
		`ALTER TABLE users ADD COLUMN invited_by INTEGER`,
		// Числовой идентификатор пользователя панели — единственный на 3.x.
		`ALTER TABLE users ADD COLUMN remnawave_id INTEGER`,
		// Миграция: цена подписки при создании инвайта
		`ALTER TABLE invites ADD COLUMN subscription_price INTEGER`,
		// Миграция: неизменяемый исторический флаг trial-инвайта
		`ALTER TABLE invites ADD COLUMN is_trial INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE invites ADD COLUMN kind TEXT NOT NULL DEFAULT 'admin'`,
		`ALTER TABLE invites ADD COLUMN expires_at TIMESTAMP`,
		`ALTER TABLE invites ADD COLUMN revoked_at TIMESTAMP`,
		`ALTER TABLE invites ADD COLUMN revoked_by INTEGER`,
		// Нейтральные поля провайдера; старый platega_transaction_id остаётся для rollback-совместимости.
		`ALTER TABLE payments ADD COLUMN provider TEXT NOT NULL DEFAULT 'platega'`,
		`ALTER TABLE payments ADD COLUMN provider_payment_id TEXT`,
		`ALTER TABLE payments ADD COLUMN provider_request_key TEXT`,
		`ALTER TABLE payments ADD COLUMN provider_fee_percent INTEGER`,
		// Тестовые платежи администратора не влияют на доступ и финансовые отчёты.
		`ALTER TABLE payments ADD COLUMN is_test INTEGER NOT NULL DEFAULT 0`,
		// Длительность оплаченного периода; сегодня всегда 1.
		`ALTER TABLE payments ADD COLUMN period_months INTEGER NOT NULL DEFAULT 1`,
	}
	for _, m := range alterMigrations {
		// Игнорируем ошибки ALTER TABLE - колонка может уже существовать
		conn.Exec(m)
	}
	// Перестройка users идёт после alterMigrations (чтобы remnawave_id уже
	// существовал и переносился) и до создания индексов по users.
	if err := rebuildUsersTable(conn); err != nil {
		return err
	}
	if _, err := conn.Exec(`CREATE INDEX IF NOT EXISTS idx_users_remnawave_uuid ON users(remnawave_uuid)`); err != nil {
		return fmt.Errorf("failed to create users uuid index: %w", err)
	}
	// Частичный уникальный индекс: NULL-ов может быть много, а один и тот же id
	// панели не должен оказаться привязан к двум Telegram ID.
	if _, err := conn.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_remnawave_id ON users(remnawave_id) WHERE remnawave_id IS NOT NULL`); err != nil {
		return fmt.Errorf("failed to create users remnawave_id index: %w", err)
	}

	if _, err := conn.Exec(`UPDATE payments SET provider = 'platega' WHERE provider IS NULL OR provider = ''`); err != nil {
		return fmt.Errorf("failed to backfill payments.provider: %w", err)
	}
	if _, err := conn.Exec(`UPDATE payments SET provider_payment_id = platega_transaction_id WHERE provider_payment_id IS NULL AND platega_transaction_id IS NOT NULL`); err != nil {
		return fmt.Errorf("failed to backfill payments.provider_payment_id: %w", err)
	}
	if _, err := conn.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_payments_provider_payment_id ON payments(provider, provider_payment_id) WHERE provider_payment_id IS NOT NULL AND provider_payment_id != ''`); err != nil {
		return fmt.Errorf("failed to create provider payment index: %w", err)
	}
	// До поддержки дробных процентов snapshot хранился в целых процентах.
	// Конвертируем только старые значения <100; уже новые basis points не затрагиваем.
	if _, err := conn.Exec(`UPDATE payments SET provider_fee_percent = provider_fee_percent * 100 WHERE provider_fee_percent IS NOT NULL AND provider_fee_percent > 0 AND provider_fee_percent < 100`); err != nil {
		return fmt.Errorf("failed to migrate provider fee snapshots: %w", err)
	}

	// Бэкофилл для старых записей: всё, что изначально было trial (expire_days IS NOT NULL),
	// должно остаться trial в исторической статистике даже после последующих изменений expire_days.
	if _, err := conn.Exec(`UPDATE invites SET is_trial = 1 WHERE is_trial = 0 AND expire_days IS NOT NULL`); err != nil {
		return fmt.Errorf("failed to backfill invites.is_trial: %w", err)
	}
	// Исторические trial-инвайты становятся referral. Остальные старые инвайты
	// остаются служебными admin-инвайтами.
	if _, err := conn.Exec(`UPDATE invites SET kind = 'referral' WHERE is_trial = 1 OR expire_days IS NOT NULL OR subscription_price IS NOT NULL`); err != nil {
		return fmt.Errorf("failed to backfill invites.kind: %w", err)
	}
	if _, err := conn.Exec(`UPDATE invites SET kind = 'admin' WHERE kind IS NULL OR kind = ''`); err != nil {
		return fmt.Errorf("failed to backfill admin invites.kind: %w", err)
	}
	// В старых схемах used_at появился позже used_by. Без детерминированного
	// fallback такие активации выпадали бы даже из статистики «за всё время».
	if _, err := conn.Exec(`UPDATE invites
		SET used_at = COALESCE(created_at, CURRENT_TIMESTAMP)
		WHERE used_by IS NOT NULL AND used_at IS NULL`); err != nil {
		return fmt.Errorf("failed to backfill invites.used_at: %w", err)
	}
	// Старые неиспользованные referral-ссылки аннулируются при переходе. Новые
	// referral-ссылки всегда имеют expires_at и этим запросом не затрагиваются.
	if _, err := conn.Exec(`UPDATE invites
		SET revoked_at = CURRENT_TIMESTAMP, revoked_by = created_by
		WHERE kind = 'referral' AND used_by IS NULL AND expires_at IS NULL AND revoked_at IS NULL`); err != nil {
		return fmt.Errorf("failed to revoke legacy referral invites: %w", err)
	}
	// Если от старой moderator-модели не сохранилась строка инвайта, создаём
	// использованную архивную запись. Иначе fallback жил бы только в users и
	// исчезал после автокика, ломая first-touch и историческую статистику.
	if _, err := conn.Exec(`INSERT INTO invites
		(code, created_by, used_by, used_at, expire_days, subscription_price, is_trial, kind, created_at)
		SELECT printf('legacy-referral-%d-%d', u.moderator_id, u.telegram_id), u.moderator_id, u.telegram_id,
		       COALESCE(u.created_at, CURRENT_TIMESTAMP), 30, u.subscription_price, 1, 'referral',
		       COALESCE(u.created_at, CURRENT_TIMESTAMP)
		FROM users u
		WHERE u.moderator_id IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM invites i WHERE i.used_by = u.telegram_id)`); err != nil {
		return fmt.Errorf("failed to preserve legacy moderator attribution: %w", err)
	}
	// First-touch определяется самым ранним использованным инвайтом любого вида:
	// служебный admin-first-touch никогда не может быть перезаписан referral.
	if _, err := conn.Exec(`UPDATE users
		SET invited_by = (
			SELECT i.created_by FROM invites i
			WHERE i.used_by = users.telegram_id
			ORDER BY i.used_at ASC, i.created_at ASC, i.rowid ASC LIMIT 1
		)
		WHERE invited_by IS NULL AND 'referral' = (
			SELECT i.kind FROM invites i WHERE i.used_by = users.telegram_id
			ORDER BY i.used_at ASC, i.created_at ASC, i.rowid ASC LIMIT 1
		)`); err != nil {
		return fmt.Errorf("failed to backfill users.invited_by from invites: %w", err)
	}
	if _, err := conn.Exec(`CREATE INDEX IF NOT EXISTS idx_invites_creator_state ON invites(created_by, kind, used_by, expires_at, revoked_at)`); err != nil {
		return fmt.Errorf("failed to create invite state index: %w", err)
	}
	if _, err := conn.Exec(`CREATE INDEX IF NOT EXISTS idx_invites_creator_created ON invites(created_by, kind, created_at)`); err != nil {
		return fmt.Errorf("failed to create invite creation index: %w", err)
	}
	if _, err := conn.Exec(`CREATE INDEX IF NOT EXISTS idx_invites_first_touch ON invites(used_by, used_at)`); err != nil {
		return fmt.Errorf("failed to create invite first-touch index: %w", err)
	}
	// Ручные чеки владельца связываются с платежами один раз миграцией, а не
	// запросом к ФНС при каждом старте.
	if err := seedManualReceipts(conn); err != nil {
		return err
	}

	return nil
}

// Close закрывает соединение с БД
func (db *DB) Close() error {
	return db.conn.Close()
}

// Conn возвращает базовое соединение sql.DB (для тестов)
func (db *DB) Conn() *sql.DB {
	return db.conn
}
