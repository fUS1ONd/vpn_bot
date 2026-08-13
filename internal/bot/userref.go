package bot

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

// errUserNotRegistered — пользователя нет в нашей базе.
var errUserNotRegistered = errors.New("пользователь не зарегистрирован")

// userRef — единственный источник remnawave.UserRef в пакете bot.
//
// Правило: никакой код в bot не собирает UserRef вручную из полей database.User.
// Иначе ленивое восстановление связки оказалось бы реализовано в одном месте из
// тридцати, и на 3.x часть вызовов молча уходила бы с пустым идентификатором.
//
// Читает связку из БД; если числовой id неизвестен, а панель уже 3.x — ищет
// пользователя по telegram_id и сохраняет найденный id. На 2.8.x UUID
// самодостаточен, и восстановление id остаётся работой backfill.
func (b *Bot) userRef(telegramID int64) (remnawave.UserRef, error) {
	user, err := b.db.GetUserByTelegramID(telegramID)
	if err != nil {
		return remnawave.UserRef{}, fmt.Errorf("load user telegram_id=%d: %w", telegramID, err)
	}
	if user == nil {
		return remnawave.UserRef{}, errUserNotRegistered
	}

	return b.userRefForDBUser(*user)
}

// userRefForDBUser собирает ссылку по уже прочитанной записи БД — для мест, где
// пользователь только что загружен (scheduler проходит по всей таблице).
func (b *Bot) userRefForDBUser(user database.User) (remnawave.UserRef, error) {
	ref := storedUserRef(user)

	version, err := b.remnawave.DetectAPIVersion()
	if err != nil {
		// Версия неизвестна — отдаём то, что есть: клиент всё равно откажется
		// отправлять запрос вслепую и вернёт понятную ошибку.
		return ref, nil
	}

	if version != remnawave.APIVersionV3 || ref.ID != 0 {
		return ref, nil
	}

	recovered, err := b.recoverRemnawaveID(user.TelegramID)
	if err != nil {
		return remnawave.UserRef{}, err
	}

	ref.ID = recovered
	return ref, nil
}

// storedUserRef переводит запись БД в ссылку без обращения к панели.
func storedUserRef(user database.User) remnawave.UserRef {
	var ref remnawave.UserRef
	if user.RemnawaveUUID != nil {
		ref.UUID = *user.RemnawaveUUID
	}
	if user.RemnawaveID != nil {
		ref.ID = *user.RemnawaveID
	}
	return ref
}

// recoverRemnawaveID находит числовой id пользователя по Telegram ID и сохраняет
// связку. Все наши записи создавались с telegramId (и регистрация, и migrator),
// поэтому путь рабочий для всей базы.
func (b *Bot) recoverRemnawaveID(telegramID int64) (int64, error) {
	remUser, err := b.remnawave.GetUserByTelegramID(telegramID)
	if err != nil {
		if errors.Is(err, remnawave.ErrMultipleUsersForTelegramID) {
			slog.Error("Panel knows several users with the same telegram id, refusing to link",
				"error", err, "telegram_id", telegramID)
			b.sendAdminAlert(fmt.Sprintf(
				"⚠️ В панели несколько пользователей с telegram_id %d — связка не записана, нужен ручной разбор.",
				telegramID,
			))
		}
		return 0, err
	}
	if remUser == nil {
		return 0, fmt.Errorf("%w: telegram_id=%d", remnawave.ErrUserNotFound, telegramID)
	}
	if remUser.ID == 0 {
		return 0, fmt.Errorf("panel user for telegram_id=%d has no id", telegramID)
	}

	if err := b.db.SetRemnawaveID(telegramID, remUser.ID); err != nil {
		// Связку не сохранили — работать можно, но следующий вызов снова пойдёт
		// искать. Об этом стоит знать: обычно это конфликт уникального индекса,
		// то есть рассинхрон с панелью.
		slog.Error("Failed to persist recovered remnawave_id", "error", err,
			"telegram_id", telegramID, "remnawave_id", remUser.ID)
		b.sendAdminAlert(fmt.Sprintf(
			"⚠️ Не удалось сохранить remnawave_id=%d для telegram_id=%d: %v",
			remUser.ID, telegramID, err,
		))
	}

	return remUser.ID, nil
}

// schedulerUserRef собирает ссылку в плановом проходе и попутно доливает
// недостающий remnawave_id: пользователь панели уже прочитан, и связка достаётся
// бесплатно, без запроса на каждого.
//
// Конфликт уникального индекса здесь означает рассинхрон (один id панели на два
// Telegram ID). Такой случай логируется и уходит владельцу, но проход по
// остальным пользователям не прерывает.
func (b *Bot) schedulerUserRef(dbUser database.User, panelUser remnawave.User) remnawave.UserRef {
	ref := storedUserRef(dbUser)
	if ref.ID != 0 || panelUser.ID == 0 {
		return ref
	}

	if err := b.db.SetRemnawaveID(dbUser.TelegramID, panelUser.ID); err != nil {
		slog.Error("Scheduler: не удалось долить remnawave_id", "error", err,
			"telegram_id", dbUser.TelegramID, "remnawave_id", panelUser.ID)
		b.sendAdminAlert(fmt.Sprintf(
			"⚠️ Рассинхрон связки: remnawave_id=%d не привязался к telegram_id=%d (%v)",
			panelUser.ID, dbUser.TelegramID, err,
		))
		return ref
	}

	ref.ID = panelUser.ID
	return ref
}

// remnawaveUUIDPtr отдаёт UUID из ответа панели указателем или nil, если панель
// его не вернула (3.x). Слой БД превращает nil в NULL, а пустая строка нарушила бы
// UNIQUE на второй же регистрации.
func remnawaveUUIDPtr(user *remnawave.User) *string {
	if user == nil || user.UUID == "" {
		return nil
	}
	uuid := user.UUID
	return &uuid
}

// remnawaveUser читает пользователя панели по нашему telegram_id через ссылку.
// Самая частая связка «взять ref → сходить в панель», вынесена, чтобы каждый
// вызывающий не повторял её руками.
func (b *Bot) remnawaveUser(telegramID int64) (*remnawave.User, error) {
	ref, err := b.userRef(telegramID)
	if err != nil {
		b.reportPanelAuthError(err, "чтение пользователя панели")
		return nil, err
	}

	user, err := b.remnawave.GetUser(ref)
	if err != nil {
		// Отказ по токену на пользовательском пути тоже стоит показать владельцу:
		// иначе о закрытой панели он узнает только со следующего прохода scheduler.
		b.reportPanelAuthError(err, "чтение пользователя панели")
	}
	return user, err
}

// deleteRemnawaveUser удаляет пользователя из панели по нашему telegram_id.
func (b *Bot) deleteRemnawaveUser(telegramID int64) error {
	ref, err := b.userRef(telegramID)
	if err != nil {
		return err
	}
	return b.remnawave.DeleteUser(ref)
}

// panelAuthAlertKey — единственный ключ дедупликации алерта про токен панели.
const panelAuthAlertKey = "panel_auth"

// reportPanelAuthError сообщает владельцу, что панель отказала по токену: 401 —
// протухший или отозванный, 403 — не хватает scope. Молча деградировать здесь
// нельзя: бот выглядел бы работающим, а панель для него закрыта. Повторы гасятся,
// пока проблема не исчезнет, иначе каждый проход scheduler спамил бы владельца.
func (b *Bot) reportPanelAuthError(err error, context string) {
	if !remnawave.IsAuthError(err) {
		// Проблема ушла — следующий отказ снова достоин сообщения.
		b.panelAuthAlerted.Delete(panelAuthAlertKey)
		return
	}

	slog.Error("Remnawave panel rejected the API token", "error", err, "context", context)

	if _, alreadyReported := b.panelAuthAlerted.LoadOrStore(panelAuthAlertKey, struct{}{}); alreadyReported {
		return
	}

	b.sendAdminAlert(fmt.Sprintf(
		"🔑 Панель Remnawave не приняла API-токен (%s): %v\n\nПроверьте REMNAWAVE_API_TOKEN и его scope (users, hwid, nodes, hosts, чтение system).",
		context, err,
	))
}

// resolveUserRef — версия userRef для inline-обработчиков: они отвечают алертом
// «Сначала активируйте подписку» и различать причины не могут.
func (b *Bot) resolveUserRef(telegramID int64) (remnawave.UserRef, bool) {
	ref, err := b.userRef(telegramID)
	if err != nil {
		if !errors.Is(err, errUserNotRegistered) {
			slog.Error("Failed to resolve user ref", "error", err, "telegram_id", telegramID)
			b.reportPanelAuthError(err, "получение ссылки на пользователя")
		}
		return remnawave.UserRef{}, false
	}
	if ref.IsZero() {
		return remnawave.UserRef{}, false
	}
	return ref, true
}
