package bot

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

// backfillRequestPause — пауза между точечными запросами в панель. На 3.x проход
// стоит один запрос на пользователя, и вываливать их залпом на панель незачем.
const backfillRequestPause = 100 * time.Millisecond

// BackfillStats — итоги прохода по недостающим связкам.
type BackfillStats struct {
	Linked  int // связка записана
	Missing int // панель такого пользователя не знает
	Failed  int // связку записать не удалось (ошибка панели или конфликт)
}

// BackfillRemnawaveIDs заполняет users.remnawave_id там, где его нет.
//
// На 2.8.x это одна операция на всю базу: список пользователей панели читается
// разом и сопоставляется по UUID. Именно этот проход обязан отработать **до**
// обновления панели — после апгрейда UUID удаляется физически, и сопоставить наши
// записи по нему станет невозможно навсегда.
//
// На 3.x резолв по UUID недоступен, поэтому связка добирается поиском по
// telegram_id — по запросу на пользователя. Такой проход дороже, поэтому
// вызывающая сторона запускает его в фоне.
func BackfillRemnawaveIDs(db *database.DB, client *remnawave.Client) (BackfillStats, error) {
	var stats BackfillStats

	pending, err := db.UsersMissingRemnawaveID()
	if err != nil {
		return stats, fmt.Errorf("load users without remnawave_id: %w", err)
	}
	if len(pending) == 0 {
		return stats, nil
	}

	version, err := client.DetectAPIVersion()
	if err != nil {
		return stats, fmt.Errorf("detect panel version for backfill: %w", err)
	}

	slog.Info("Backfill of remnawave_id started", "pending", len(pending), "contract", version.String())

	if version == remnawave.APIVersionV2 {
		return backfillByUUID(db, client, pending)
	}
	return backfillByTelegramID(db, client, pending)
}

// backfillByUUID сопоставляет наши записи со списком пользователей панели 2.8.x.
func backfillByUUID(db *database.DB, client *remnawave.Client, pending []database.User) (BackfillStats, error) {
	var stats BackfillStats

	panelUsers, err := client.GetAllUsers()
	if err != nil {
		return stats, fmt.Errorf("load panel users for backfill: %w", err)
	}

	idByUUID := make(map[string]int64, len(panelUsers))
	for _, panelUser := range panelUsers {
		if panelUser.UUID != "" && panelUser.ID != 0 {
			idByUUID[panelUser.UUID] = panelUser.ID
		}
	}

	for _, user := range pending {
		if user.RemnawaveUUID == nil {
			// Ни id, ни uuid — сопоставить не по чему.
			logUnlinkedUser(user, "нет ни remnawave_uuid, ни remnawave_id")
			stats.Missing++
			continue
		}

		id, found := idByUUID[*user.RemnawaveUUID]
		if !found {
			// Пользователь мог появиться после прохода по списку — добираем точечно.
			resolved, resolveErr := client.ResolveUserByUUID(*user.RemnawaveUUID)
			switch {
			case resolveErr != nil:
				slog.Warn("Backfill: не удалось резолвить пользователя по uuid",
					"error", resolveErr, "telegram_id", user.TelegramID, "remnawave_uuid", *user.RemnawaveUUID)
				stats.Failed++
				continue
			case resolved == nil || resolved.ID == 0:
				logUnlinkedUser(user, "панель не знает такого uuid")
				stats.Missing++
				continue
			default:
				id = resolved.ID
			}
		}

		if linkRemnawaveID(db, user.TelegramID, id) {
			stats.Linked++
		} else {
			stats.Failed++
		}
	}

	logBackfillResult(stats)
	return stats, nil
}

// backfillByTelegramID добирает связку поиском по Telegram ID — единственный
// доступный путь, если бот впервые запустился уже на панели 3.x.
func backfillByTelegramID(db *database.DB, client *remnawave.Client, pending []database.User) (BackfillStats, error) {
	var stats BackfillStats

	for i, user := range pending {
		if i > 0 {
			time.Sleep(backfillRequestPause)
		}

		panelUser, err := client.GetUserByTelegramID(user.TelegramID)
		switch {
		case errors.Is(err, remnawave.ErrMultipleUsersForTelegramID):
			// Молча выбрать первого — значит с некоторой вероятностью привязать
			// чужой аккаунт к платежу. Пропускаем и идём дальше.
			slog.Error("Backfill: в панели несколько пользователей с одним telegram_id",
				"error", err, "telegram_id", user.TelegramID)
			stats.Failed++
			continue
		case err != nil:
			slog.Warn("Backfill: не удалось найти пользователя по telegram_id",
				"error", err, "telegram_id", user.TelegramID)
			stats.Failed++
			continue
		case panelUser == nil || panelUser.ID == 0:
			logUnlinkedUser(user, "панель не знает такого telegram_id")
			stats.Missing++
			continue
		}

		if linkRemnawaveID(db, user.TelegramID, panelUser.ID) {
			stats.Linked++
		} else {
			stats.Failed++
		}
	}

	logBackfillResult(stats)
	return stats, nil
}

// linkRemnawaveID сохраняет связку и сообщает, получилось ли. Конфликт уникального
// индекса означает рассинхрон с панелью: один id панели на два Telegram ID.
func linkRemnawaveID(db *database.DB, telegramID, remnawaveID int64) bool {
	if err := db.SetRemnawaveID(telegramID, remnawaveID); err != nil {
		slog.Error("Backfill: не удалось сохранить связку", "error", err,
			"telegram_id", telegramID, "remnawave_id", remnawaveID)
		return false
	}
	return true
}

// logUnlinkedUser перечисляет поимённо тех, кого связать не удалось: это кандидаты
// на ручной разбор, а не повод ронять старт.
func logUnlinkedUser(user database.User, reason string) {
	slog.Warn("Backfill: пользователь остался без remnawave_id",
		"telegram_id", user.TelegramID, "username", user.Username, "reason", reason)
}

func logBackfillResult(stats BackfillStats) {
	slog.Info("Backfill of remnawave_id finished",
		"linked", stats.Linked, "missing", stats.Missing, "failed", stats.Failed)
}
