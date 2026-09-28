package funnels

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
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

// paymentEntryActions — все входы в оплату: человек открыл экран выбора
// способа, откуда бы ни пришёл.
var paymentEntryActions = []string{
	ActionPayMenu, ActionRenewMenu, ActionPayOpen, ActionAutorenewPayManually,
	ActionPayYooKassaReply, ActionPayCryptoReply,
}

// paymentEntered — источник Шага «вошёл в оплату» любым входом. Как и два
// источника платежей ниже, его переиспользует Воронка непродливших.
var paymentEntered = journalAction(paymentEntryActions...)

// renewalReminded — вход Воронки непродливших: Системное событие напоминания
// о конце оплаченной подписки.
var renewalReminded = journalAction(ActionReminder3d, ActionReminder1d)

// errNoEvents — Действия нет в журнале вовсе. Для Действия, которое Telegram
// присылает только при отдельной настройке бота, это не ноль, а «не знаем».
var errNoEvents = errors.New("action never recorded")

// journalActionOrNoData — как journalAction, но без единого случая Действия во
// всём журнале Шаг получает «нет данных»: выключенная доставка апдейта
// неотличима от нуля иначе.
func journalActionOrNoData(action string) source {
	inPeriod := journalAction(action)
	return func(ctx context.Context, conn *sql.Conn, from, to time.Time) ([]occurrence, error) {
		var recorded bool
		if err := conn.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM events WHERE action = ?)`, action,
		).Scan(&recorded); err != nil {
			return nil, err
		}
		if !recorded {
			return nil, errNoEvents
		}
		return inPeriod(ctx, conn, from, to)
	}
}

// referralInvitesCreated — автор создал referral-приглашение. Служебные
// приглашения владельца — не приглашения друга и в Воронку не идут.
var referralInvitesCreated = tableOccurrences("created_at",
	`SELECT created_by, created_at FROM `+mainSchema+`.invites WHERE kind = ?`,
	database.InviteKindReferral,
)

// firstTouch — условие «приглашение alias — первое, которое использовал его
// гость» (first-touch, тот же порядок, что у статистики приглашений в админке:
// used_at, затем порядок вставки). Вернувшийся после автокика по чужому
// приглашению — не новый друг второго автора. Использованное приглашение без
// used_at (старые записи) — самое раннее, как NULL в ORDER BY.
func firstTouch(alias string) string {
	return `NOT EXISTS (
	  SELECT 1 FROM ` + mainSchema + `.invites earlier
	  WHERE earlier.used_by = ` + alias + `.used_by AND earlier.rowid <> ` + alias + `.rowid
	    AND (earlier.used_at IS NULL
	         OR datetime(earlier.used_at) < datetime(` + alias + `.used_at)
	         OR (datetime(earlier.used_at) = datetime(` + alias + `.used_at) AND earlier.rowid < ` + alias + `.rowid)))`
}

// referralFriendsRegistered — по referral-приглашению автора впервые пришёл
// друг: момент — дата использования приглашения, человек — автор.
var referralFriendsRegistered = tableOccurrences("i.used_at",
	`SELECT i.created_by, i.used_at FROM `+mainSchema+`.invites i
	 WHERE i.kind = ? AND i.used_by IS NOT NULL AND i.used_at IS NOT NULL AND `+firstTouch("i"),
	database.InviteKindReferral,
)

// paidManually — условие «платёж alias — настоящая ручная оплата»: деньги
// приняты (включая упавшую активацию), не тест владельца и не автосписание —
// Воронки про решения людей, а автосписание решением не является.
func paidManually(alias string) string {
	return alias + `.status IN ('confirmed', 'confirmed_not_activated') AND ` + alias + `.is_test = 0
	AND ` + alias + `.confirmed_at IS NOT NULL
	AND ` + notAutorenew(alias)
}

// notAutorenew — условие «платёж alias создан не шагом автосписаний».
func notAutorenew(alias string) string {
	return `NOT EXISTS (SELECT 1 FROM ` + mainSchema + `.autorenew_attempts a WHERE a.payment_id = ` + alias + `.id)`
}

// issuedManually — условие «платёж alias — ручной платёж, выданный кассой»:
// сорвавшееся создание оставляет запись без id у кассы, и это не платёж;
// тестовые платежи и автосписания не считаются.
func issuedManually(alias string) string {
	return alias + `.is_test = 0 AND (` + alias + `.provider_payment_id IS NOT NULL OR ` + alias + `.platega_transaction_id IS NOT NULL)
	AND ` + notAutorenew(alias)
}

// paymentCreated — источник Шага «платёж создан»: новый платёж (момент —
// created_at записи) или живой pending, который бот вернул на выбор способа.
var paymentCreated = unionSources(paymentsIssued, pendingReused)

// paymentsIssued — касса выдала ручной платёж; момент — created_at записи.
var paymentsIssued = wholeSecondsCeil(tableOccurrences("p.created_at",
	`SELECT p.telegram_id, p.created_at FROM `+mainSchema+`.payments p WHERE `+issuedManually("p"),
))

// pendingReused — выбор способа, на который бот вернул уже выданный платёж
// того же способа, а не создал новый (createPaymentForProvider переиспользует
// живой pending моложе суток). Момент — выбор способа: ссылку человек получил
// тогда. Без этого вернувшийся платить по старой ссылке выпадал бы из Воронки
// — его платёж создан раньше входа. Статус записи на момент выбора не
// хранится, поэтому живость восстанавливается по сроку ссылки и по тому, что
// платёж не был подтверждён раньше выбора.
func pendingReused(ctx context.Context, conn *sql.Conn, from, to time.Time) ([]occurrence, error) {
	args := []any{
		from.UTC().Format(journal.TimeLayout), to.UTC().Format(journal.TimeLayout),
		ActionPayMethod, ActionRetryPayment,
	}
	return queryOccurrences(ctx, conn,
		`SELECT e.telegram_id, e.ts FROM events e
		 WHERE e.ts >= ? AND e.ts < ? AND e.action IN (?, ?)
		   AND EXISTS (
		     SELECT 1 FROM `+mainSchema+`.payments p
		     WHERE p.telegram_id = e.telegram_id
		       AND p.provider = `+providerByParam+`
		       AND `+issuedManually("p")+`
		       AND datetime(p.created_at) <= datetime(e.ts)
		       AND datetime(p.created_at) > datetime(e.ts, '-1 day')
		       AND (p.expires_at IS NULL OR datetime(p.expires_at) > datetime(e.ts))
		       AND (p.confirmed_at IS NULL OR datetime(p.confirmed_at) >= datetime(e.ts)))`,
		append(args, providerByParamArgs...)...,
	)
}

// providerByParam — выражение SQL «параметр События e.param → провайдер
// платежа» по PayMethodParams; значения подставляются из providerByParamArgs.
var providerByParam, providerByParamArgs = providerByParamCase()

func providerByParamCase() (string, []any) {
	providers := make([]string, 0, len(PayMethodParams))
	for provider := range PayMethodParams {
		providers = append(providers, provider)
	}
	sort.Strings(providers)

	var sb strings.Builder
	args := make([]any, 0, 2*len(providers))
	sb.WriteString("CASE e.param")
	for _, provider := range providers {
		sb.WriteString(" WHEN ? THEN ?")
		args = append(args, PayMethodParams[provider], provider)
	}
	sb.WriteString(" END")
	return sb.String(), args
}

// unionSources — случаи Шага из нескольких источников вместе.
func unionSources(sources ...source) source {
	return func(ctx context.Context, conn *sql.Conn, from, to time.Time) ([]occurrence, error) {
		var all []occurrence
		for _, src := range sources {
			occurrences, err := src(ctx, conn, from, to)
			if err != nil {
				return nil, err
			}
			all = append(all, occurrences...)
		}
		return all, nil
	}
}

// paymentConfirmed — источник Шага «платёж подтверждён»: ручная оплата принята.
// Момент — confirmed_at.
var paymentConfirmed = wholeSecondsCeil(tableOccurrences("p.confirmed_at",
	`SELECT p.telegram_id, p.confirmed_at FROM `+mainSchema+`.payments p WHERE `+paidManually("p"),
))

// wholeSecondsCeil сдвигает момент, записанный целыми секундами, на конец его
// секунды. Бот пишет created_at и confirmed_at платежа через CURRENT_TIMESTAMP
// и datetime('now'), которые срезают доли, а время Действия — с долями: платёж,
// созданный через 0.2 с после выбора способа в той же секунде, иначе оказался
// бы «раньше» выбора и выпал из когорты. Настоящий момент не позже конца
// секунды, поэтому порядок Шагов сохраняется, а ошибка окна — меньше секунды.
// Воронке приглашения поправка не нужна: там «друг оплатил» идёт за «друг
// зарегистрировался», и оба момента лежат целыми секундами.
func wholeSecondsCeil(src source) source {
	return func(ctx context.Context, conn *sql.Conn, from, to time.Time) ([]occurrence, error) {
		occurrences, err := src(ctx, conn, from, to)
		for i := range occurrences {
			if occurrences[i].At.Nanosecond() == 0 {
				occurrences[i].At = occurrences[i].At.Add(time.Second - time.Nanosecond)
			}
		}
		return occurrences, err
	}
}

// referralFriendsPaid — друг, впервые пришедший по referral-приглашению автора
// (first-touch), впервые оплатил после прихода: момент — подтверждение платежа,
// человек — автор. Только первая оплата: продления давно приглашённого друга
// иначе засчитывали бы автору «друг оплатил» в каждой новой когорте.
var referralFriendsPaid = tableOccurrences("p.confirmed_at",
	`SELECT i.created_by, p.confirmed_at
	 FROM `+mainSchema+`.payments p
	 JOIN `+mainSchema+`.invites i ON i.used_by = p.telegram_id AND i.kind = ?
	 WHERE `+paidManually("p")+`
	   AND i.used_at IS NOT NULL AND `+firstTouch("i")+`
	   AND datetime(p.confirmed_at) >= datetime(i.used_at)
	   AND NOT EXISTS (
	     SELECT 1 FROM `+mainSchema+`.payments earlier
	     WHERE earlier.telegram_id = p.telegram_id AND earlier.id <> p.id
	       AND `+paidManually("earlier")+`
	       AND (datetime(earlier.confirmed_at) < datetime(p.confirmed_at)
	            OR (datetime(earlier.confirmed_at) = datetime(p.confirmed_at) AND earlier.id < p.id)))`,
	database.InviteKindReferral,
)

// usersRegistered — вход онбординга: регистрация в боте, момент — created_at
// пользователя. Секунда не округляется вверх, в отличие от платежей: начало
// секунды не позже настоящего момента, и подключение в ту же секунду остаётся
// «после регистрации».
var usersRegistered = tableOccurrences("u.created_at",
	`SELECT u.telegram_id, u.created_at FROM `+mainSchema+`.users u WHERE u.created_at IS NOT NULL`,
)

// errNoPanel — порт панели не подключён к модулю.
var errNoPanel = errors.New("panel port is not configured")

// firstConnected — Шаг «первое подключение устройства» по данным панели.
// Соединение с базами не нужно: момент знает только панель, и спрашивается она
// при каждом расчёте.
func firstConnected(panel FirstConnections) source {
	return func(ctx context.Context, _ *sql.Conn, from, to time.Time) ([]occurrence, error) {
		if panel == nil {
			return nil, errNoPanel
		}
		connected, err := panel.FirstConnectedAt(ctx)
		if err != nil {
			return nil, err
		}
		var occurrences []occurrence
		for telegramID, at := range connected {
			at = at.UTC()
			if !at.Before(from) && at.Before(to) {
				occurrences = append(occurrences, occurrence{TelegramID: telegramID, At: at})
			}
		}
		return occurrences, nil
	}
}

// tableOccurrences — Шаг из таблиц основной базы. query отбирает пары
// (telegram_id, момент) и заканчивается условием WHERE; границы интервала по
// столбцу момента at дописываются здесь, через datetime(): в основной базе
// время лежит в разных форматах (time.Time драйвера и CURRENT_TIMESTAMP), и
// строковое сравнение между ними неверно. datetime() режет доли секунды,
// поэтому SQL отбирает с запасом до секунды, а точная граница [from, to)
// проверяется в Go.
func tableOccurrences(at, query string, args ...any) source {
	query += ` AND datetime(` + at + `) >= datetime(?) AND datetime(` + at + `) <= datetime(?)`
	return func(ctx context.Context, conn *sql.Conn, from, to time.Time) ([]occurrence, error) {
		bound := append(append([]any{}, args...), sqliteSeconds(from), sqliteSeconds(to))
		all, err := queryOccurrences(ctx, conn, query, bound...)
		if err != nil {
			return nil, err
		}
		within := all[:0]
		for _, o := range all {
			if !o.At.Before(from) && o.At.Before(to) {
				within = append(within, o)
			}
		}
		return within, nil
	}
}

// sqliteSeconds — момент в формате datetime() SQLite.
func sqliteSeconds(at time.Time) string {
	return at.UTC().Format(database.SQLiteSecondsLayout)
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
