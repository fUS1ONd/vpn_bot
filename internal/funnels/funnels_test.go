package funnels

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/journal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const ownerID int64 = 999

// fixture — настоящие SQLite-файлы во временной папке: журнал и основная база.
type fixture struct {
	eventsPath string
	mainPath   string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	dir := t.TempDir()
	f := fixture{
		eventsPath: filepath.Join(dir, journal.FileName),
		mainPath:   filepath.Join(dir, "bot.db"),
	}
	db, err := database.New(f.mainPath)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	// Модуль открывает журнал только на чтение и файл не создаёт: бот
	// открывает журнал раньше модуля, так же поступает и фикстура.
	f.record(t)
	return f
}

// record пишет События через настоящий журнал: фикстура лежит в файле ровно так,
// как её положил бы бот.
func (f fixture) record(t *testing.T, events ...journal.Event) {
	t.Helper()
	j, err := journal.Open(f.eventsPath, journal.Options{})
	require.NoError(t, err)
	for _, event := range events {
		if event.Source == "" {
			event.Source = journal.SourceUser
		}
		j.Record(event)
	}
	j.Close()
}

func (f fixture) open(t *testing.T) *Funnels {
	t.Helper()
	return f.openWithPanel(t, nil)
}

// Воронка «Приглашение» считает людей, открывших раздел приглашений за период:
// повторные открытия — один человек, открытия вне периода не считаются,
// владелец не считается никогда.
func TestInviteFunnel_CountsPeopleWhoOpenedSection(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	from := now.Add(-7 * 24 * time.Hour)

	f := newFixture(t)
	f.record(t,
		journal.Event{At: now.Add(-time.Hour), TelegramID: 1, Action: ActionInvitesOpen},
		journal.Event{At: now.Add(-2 * time.Hour), TelegramID: 1, Action: ActionInvitesOpen},
		journal.Event{At: now.Add(-3 * 24 * time.Hour), TelegramID: 2, Action: ActionInvitesOpen},
		journal.Event{At: from.Add(-time.Minute), TelegramID: 3, Action: ActionInvitesOpen},
		journal.Event{At: now.Add(-time.Hour), TelegramID: 4, Action: "pay_open"},
		journal.Event{At: now.Add(-time.Hour), TelegramID: ownerID, Action: ActionInvitesOpen},
	)

	report, err := f.open(t).Report(context.Background(), FunnelInvite, from, now)
	require.NoError(t, err)

	require.NotEmpty(t, report.Steps)
	step := report.Steps[0]
	assert.Equal(t, StepInvitesOpened, step.ID)
	assert.Equal(t, 2, step.People)
	assert.False(t, step.NoData)
}

// exec кладёт фикстуру в основную базу напрямую: так строки лежат ровно в тех
// форматах времени, в каких их пишет бот (time.Time драйвера и CURRENT_TIMESTAMP).
func (f fixture) exec(t *testing.T, query string, args ...any) sql.Result {
	t.Helper()
	db, err := sql.Open("sqlite3", database.DSN(f.mainPath))
	require.NoError(t, err)
	defer db.Close()
	res, err := db.Exec(query, args...)
	require.NoError(t, err)
	return res
}

// sqliteNow — формат CURRENT_TIMESTAMP и datetime('now'): так бот пишет
// used_at приглашения и confirmed_at платежа.
func sqliteNow(at time.Time) string {
	return at.UTC().Format("2006-01-02 15:04:05")
}

var inviteSeq int

// invite кладёт приглашение автора; usedBy = 0 — не использовано.
func (f fixture) invite(t *testing.T, kind string, author int64, createdAt time.Time, usedBy int64, usedAt time.Time) {
	t.Helper()
	inviteSeq++
	code := fmt.Sprintf("code%d", inviteSeq)
	if usedBy == 0 {
		f.exec(t, `INSERT INTO invites (code, created_by, kind, created_at) VALUES (?, ?, ?, ?)`,
			code, author, kind, createdAt.UTC())
		return
	}
	f.exec(t, `INSERT INTO invites (code, created_by, kind, created_at, used_by, used_at) VALUES (?, ?, ?, ?, ?, ?)`,
		code, author, kind, createdAt.UTC(), usedBy, sqliteNow(usedAt))
}

// payment кладёт платёж и возвращает его id.
func (f fixture) payment(t *testing.T, telegramID int64, status string, isTest bool, confirmedAt time.Time) int64 {
	t.Helper()
	res := f.exec(t, `INSERT INTO payments (telegram_id, amount, payment_method, status, provider, is_test, created_at, confirmed_at)
		VALUES (?, 400, 'bank_card', ?, 'yookassa', ?, ?, ?)`,
		telegramID, status, isTest, confirmedAt.UTC().Add(-time.Minute), sqliteNow(confirmedAt))
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

// autorenew помечает платёж как автосписание.
func (f fixture) autorenew(t *testing.T, telegramID, paymentID int64) {
	t.Helper()
	f.exec(t, `INSERT INTO autorenew_attempts (telegram_id, expire_at, attempt_no, outcome, payment_id)
		VALUES (?, '2026-10-01', 1, 'succeeded', ?)`, telegramID, paymentID)
}

func stepPeople(report Report) map[string]int {
	people := make(map[string]int, len(report.Steps))
	for _, step := range report.Steps {
		people[step.ID] = step.People
	}
	return people
}

// Шаг «создал» берётся из таблицы приглашений и считает только referral-
// приглашения: служебное админское приглашение — не приглашение друга.
func TestInviteFunnel_CreatedCountsOnlyReferralInvites(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	from := now.Add(-7 * 24 * time.Hour)
	opened := now.Add(-3 * 24 * time.Hour)

	f := newFixture(t)
	f.record(t,
		journal.Event{At: opened, TelegramID: 1, Action: ActionInvitesOpen},
		journal.Event{At: opened, TelegramID: 2, Action: ActionInvitesOpen},
		journal.Event{At: opened, TelegramID: 3, Action: ActionInvitesOpen},
	)
	f.invite(t, database.InviteKindReferral, 1, opened.Add(time.Minute), 0, time.Time{})
	f.invite(t, database.InviteKindReferral, 1, opened.Add(2*time.Minute), 0, time.Time{})
	f.invite(t, database.InviteKindAdmin, 2, opened.Add(time.Minute), 0, time.Time{})
	// создал раньше, чем открыл раздел в периоде, — не шаг когорты
	f.invite(t, database.InviteKindReferral, 3, opened.Add(-time.Minute), 0, time.Time{})

	report, err := f.open(t).Report(context.Background(), FunnelInvite, from, now)
	require.NoError(t, err)

	assert.Equal(t, 3, stepPeople(report)[StepInvitesOpened])
	assert.Equal(t, 1, stepPeople(report)[StepInviteCreated])
}

// Шаг «отправил» — выбор результата «Поделиться» в журнале, после создания.
func TestInviteFunnel_SentIsChosenShareResult(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	opened := now.Add(-3 * 24 * time.Hour)

	f := newFixture(t)
	f.invite(t, database.InviteKindReferral, 1, opened.Add(time.Minute), 0, time.Time{})
	f.invite(t, database.InviteKindReferral, 2, opened.Add(time.Minute), 0, time.Time{})
	f.record(t,
		journal.Event{At: opened, TelegramID: 1, Action: ActionInvitesOpen},
		journal.Event{At: opened, TelegramID: 2, Action: ActionInvitesOpen},
		journal.Event{At: opened.Add(2 * time.Minute), TelegramID: 1, Action: ActionShareSent},
		// открыл выбор чата, но не выбрал — не отправил
		journal.Event{At: opened.Add(2 * time.Minute), TelegramID: 2, Action: "share_query"},
	)

	report, err := f.open(t).Report(context.Background(), FunnelInvite, now.Add(-7*24*time.Hour), now)
	require.NoError(t, err)

	assert.Equal(t, 2, stepPeople(report)[StepInviteCreated])
	assert.Equal(t, 1, stepPeople(report)[StepInviteSent])
	assert.False(t, report.Steps[2].NoData)
}

// Без единого выбора результата в журнале Шаг «отправил» — «нет данных», а не
// ноль: скорее всего, у @BotFather не включён inline feedback, и Telegram
// просто не присылает выбор. Следующие Шаги — подмножество неизвестного.
func TestInviteFunnel_SentWithoutAnyEventIsNoData(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	opened := now.Add(-3 * 24 * time.Hour)

	f := newFixture(t)
	f.invite(t, database.InviteKindReferral, 1, opened.Add(time.Minute), 0, time.Time{})
	f.record(t, journal.Event{At: opened, TelegramID: 1, Action: ActionInvitesOpen})

	report, err := f.open(t).Report(context.Background(), FunnelInvite, now.Add(-7*24*time.Hour), now)
	require.NoError(t, err)

	require.Len(t, report.Steps, 5)
	assert.Equal(t, 1, report.Steps[1].People)
	assert.False(t, report.Steps[1].NoData)
	for _, step := range report.Steps[2:] {
		assert.True(t, step.NoData, step.ID)
	}
}

// author проводит автора по первым трём Шагам: открыл, создал, отправил.
func (f fixture) author(t *testing.T, id int64, opened time.Time) {
	t.Helper()
	f.invite(t, database.InviteKindReferral, id, opened.Add(time.Minute), 0, time.Time{})
	f.record(t,
		journal.Event{At: opened, TelegramID: id, Action: ActionInvitesOpen},
		journal.Event{At: opened.Add(2 * time.Minute), TelegramID: id, Action: ActionShareSent},
	)
}

// «Друг зарегистрировался» — по приглашению автора кто-то пришёл (дата
// использования), «друг оплатил» — первая настоящая оплата такого друга. Только
// referral-приглашения; тестовые платежи и автосписания не оплата друга.
func TestInviteFunnel_FriendRegisteredAndPaid(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	opened := now.Add(-20 * 24 * time.Hour)
	from := now.Add(-30 * 24 * time.Hour)
	later := opened.Add(time.Hour)

	f := newFixture(t)
	for id := int64(1); id <= 6; id++ {
		f.author(t, id, opened)
	}
	// 1: друг пришёл и оплатил
	f.invite(t, database.InviteKindReferral, 1, opened, 101, later)
	f.payment(t, 101, "confirmed", false, later.Add(time.Hour))
	// 2: друг пришёл, оплата упала на активации — деньги приняты, это оплата
	f.invite(t, database.InviteKindReferral, 2, opened, 102, later)
	f.payment(t, 102, "confirmed_not_activated", false, later.Add(time.Hour))
	// 3: друг пришёл, оплата тестовая
	f.invite(t, database.InviteKindReferral, 3, opened, 103, later)
	f.payment(t, 103, "confirmed", true, later.Add(time.Hour))
	// 4: друг пришёл, платёж не подтверждён
	f.invite(t, database.InviteKindReferral, 4, opened, 104, later)
	f.payment(t, 104, "pending", false, later.Add(time.Hour))
	// 5: пришёл по служебному приглашению — не друг по приглашению
	f.invite(t, database.InviteKindAdmin, 5, opened, 105, later)
	f.payment(t, 105, "confirmed", false, later.Add(time.Hour))
	// 6: новый друг пришёл, но не оплатил; а старый друг, пришедший и впервые
	// оплативший давно, в окне продлился вручную и автосписанием — это не
	// «друг оплатил»: Шаг про первую оплату приглашённого
	f.invite(t, database.InviteKindReferral, 6, opened, 116, later)
	f.invite(t, database.InviteKindReferral, 6, opened.Add(-60*24*time.Hour), 106, opened.Add(-59*24*time.Hour))
	f.payment(t, 106, "confirmed", false, opened.Add(-58*24*time.Hour))
	f.payment(t, 106, "confirmed", false, later)
	f.autorenew(t, 106, f.payment(t, 106, "confirmed", false, later.Add(time.Hour)))

	report, err := f.open(t).Report(context.Background(), FunnelInvite, from, now)
	require.NoError(t, err)

	people := stepPeople(report)
	assert.Equal(t, 6, people[StepInviteSent])
	assert.Equal(t, 5, people[StepFriendRegistered])
	assert.Equal(t, 2, people[StepFriendPaid])
}

// Первая оплата друга — автосписание не бывает первой оплатой, но и
// одно автосписание без ручных оплат не считается: воронка про решение людей.
func TestInviteFunnel_AutorenewIsNotFriendPayment(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	opened := now.Add(-5 * 24 * time.Hour)

	f := newFixture(t)
	f.author(t, 1, opened)
	f.invite(t, database.InviteKindReferral, 1, opened, 101, opened.Add(time.Hour))
	f.autorenew(t, 101, f.payment(t, 101, "confirmed", false, opened.Add(2*time.Hour)))

	report, err := f.open(t).Report(context.Background(), FunnelInvite, now.Add(-7*24*time.Hour), now)
	require.NoError(t, err)

	assert.Equal(t, 1, stepPeople(report)[StepFriendRegistered])
	assert.Equal(t, 0, stepPeople(report)[StepFriendPaid])
}

// Друг автора — тот, кого привело первое использованное им приглашение
// (first-touch, как в статистике приглашений админки). Вернувшийся после
// автокика по чужому приглашению — не новый друг второго автора: ни
// регистрация, ни оплата ему не засчитываются, и одна оплата не делится на двоих.
func TestInviteFunnel_FriendIsFirstTouch(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	opened := now.Add(-5 * 24 * time.Hour)
	later := opened.Add(time.Hour)

	f := newFixture(t)
	f.author(t, 1, opened)
	f.author(t, 2, opened)
	f.author(t, 3, opened)
	// 101 пришёл к автору 1 и оплатил
	f.invite(t, database.InviteKindReferral, 1, opened, 101, later)
	// 102 когда-то пришёл к автору 1, был кикнут и вернулся к автору 2
	f.invite(t, database.InviteKindReferral, 1, opened.Add(-90*24*time.Hour), 102, opened.Add(-89*24*time.Hour))
	f.invite(t, database.InviteKindReferral, 2, opened, 102, later)
	f.payment(t, 102, "confirmed", false, later.Add(time.Hour))
	// 103 впервые пришёл по служебному приглашению, потом — по приглашению автора 3
	f.invite(t, database.InviteKindAdmin, ownerID, opened.Add(-90*24*time.Hour), 103, opened.Add(-89*24*time.Hour))
	f.invite(t, database.InviteKindReferral, 3, opened, 103, later)
	f.payment(t, 103, "confirmed", false, later.Add(time.Hour))
	f.payment(t, 101, "confirmed", false, later.Add(time.Hour))

	report, err := f.open(t).Report(context.Background(), FunnelInvite, now.Add(-7*24*time.Hour), now)
	require.NoError(t, err)

	assert.Equal(t, 1, stepPeople(report)[StepFriendRegistered])
	assert.Equal(t, 1, stepPeople(report)[StepFriendPaid])
}

// Время в основной базе лежит в разных форматах: с долями секунды и смещением
// пояса (time.Time драйвера) и без них (CURRENT_TIMESTAMP). Граница Шага
// сравнивается по моменту, а не по строке: 12:00:00.5 по Москве — это 09:00:00.5
// UTC, и оно позже входа в 09:00:00.2 UTC, хоть строка «12:…» и «больше».
func TestInviteFunnel_MixedTimeFormats(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	opened := time.Date(2026, 9, 25, 9, 0, 0, 200_000_000, time.UTC)
	moscow := time.FixedZone("MSK", 3*60*60)

	f := newFixture(t)
	f.record(t,
		journal.Event{At: opened, TelegramID: 1, Action: ActionInvitesOpen},
		journal.Event{At: opened, TelegramID: 2, Action: ActionInvitesOpen},
	)
	// 1 создал через 0.3 с после входа — время записано по Москве
	f.exec(t, `INSERT INTO invites (code, created_by, kind, created_at) VALUES ('m1', 1, ?, ?)`,
		database.InviteKindReferral, opened.Add(300*time.Millisecond).In(moscow))
	// 2 создал за 0.1 с до входа — в ту же секунду, но раньше: не Шаг когорты
	f.exec(t, `INSERT INTO invites (code, created_by, kind, created_at) VALUES ('m2', 2, ?, ?)`,
		database.InviteKindReferral, opened.Add(-100*time.Millisecond).In(moscow))

	report, err := f.open(t).Report(context.Background(), FunnelInvite, now.Add(-7*24*time.Hour), now)
	require.NoError(t, err)

	assert.Equal(t, 2, stepPeople(report)[StepInvitesOpened])
	assert.Equal(t, 1, stepPeople(report)[StepInviteCreated])
}

// Друг, пришедший позже 14 дней от входа автора, в Воронку не попадает.
func TestInviteFunnel_FriendAfterWindowIsNotCounted(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	f := newFixture(t)
	early := now.Add(-40 * 24 * time.Hour)
	f.author(t, 1, early)
	f.author(t, 2, early)
	f.invite(t, database.InviteKindReferral, 1, early, 101, early.Add(13*24*time.Hour))
	f.invite(t, database.InviteKindReferral, 2, early, 102, early.Add(15*24*time.Hour))

	report, err := f.open(t).Report(context.Background(), FunnelInvite, now.Add(-90*24*time.Hour), now)
	require.NoError(t, err)

	assert.Equal(t, 2, stepPeople(report)[StepInviteSent])
	assert.Equal(t, 1, stepPeople(report)[StepFriendRegistered])
}

// Окно части когорты ещё не истекло — отчёт помечает это, чтобы недосчитанную
// конверсию не приняли за провал. Окно всех вошедших истекло — пометки нет.
func TestInviteFunnel_WindowNotClosed(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

	f := newFixture(t)
	f.record(t,
		journal.Event{At: now.Add(-20 * 24 * time.Hour), TelegramID: 1, Action: ActionInvitesOpen},
		journal.Event{At: now.Add(-3 * 24 * time.Hour), TelegramID: 2, Action: ActionInvitesOpen},
		// владелец не держит окно открытым: его в когорте нет
		journal.Event{At: now.Add(-time.Hour), TelegramID: ownerID, Action: ActionInvitesOpen},
	)
	funnels := f.open(t)
	funnels.now = func() time.Time { return now }

	fresh, err := funnels.Report(context.Background(), FunnelInvite, now.Add(-7*24*time.Hour), now)
	require.NoError(t, err)
	assert.True(t, fresh.WindowOpen, "вошедший 3 дня назад ещё может пройти Шаги")

	// Когорта 30–15 дней назад: вошедший 20 дней назад отгулял все 14 дней.
	closed, err := funnels.Report(context.Background(), FunnelInvite, now.Add(-30*24*time.Hour), now.Add(-15*24*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 1, closed.Steps[0].People)
	assert.False(t, closed.WindowOpen)
}

// Владелец не попадает ни в один Шаг, даже пройдя всю цепочку.
func TestInviteFunnel_ExcludesOwner(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	opened := now.Add(-3 * 24 * time.Hour)

	f := newFixture(t)
	f.author(t, ownerID, opened)
	f.invite(t, database.InviteKindReferral, ownerID, opened, 101, opened.Add(time.Hour))
	f.payment(t, 101, "confirmed", false, opened.Add(2*time.Hour))

	report, err := f.open(t).Report(context.Background(), FunnelInvite, now.Add(-7*24*time.Hour), now)
	require.NoError(t, err)

	for _, step := range report.Steps {
		assert.Zero(t, step.People, step.ID)
	}
	assert.False(t, report.WindowOpen)
}

// Выбор результата где-то в журнале есть, но не в когорте — это ноль, а не
// «нет данных»: feedback работает, просто никто из когорты не отправил.
func TestInviteFunnel_SentZeroWhenFeedbackWorks(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	opened := now.Add(-3 * 24 * time.Hour)

	f := newFixture(t)
	f.invite(t, database.InviteKindReferral, 1, opened.Add(time.Minute), 0, time.Time{})
	f.record(t,
		journal.Event{At: opened, TelegramID: 1, Action: ActionInvitesOpen},
		journal.Event{At: now.Add(-60 * 24 * time.Hour), TelegramID: 7, Action: ActionShareSent},
	)

	report, err := f.open(t).Report(context.Background(), FunnelInvite, now.Add(-7*24*time.Hour), now)
	require.NoError(t, err)

	assert.False(t, report.Steps[2].NoData)
	assert.Equal(t, 0, report.Steps[2].People)
}
