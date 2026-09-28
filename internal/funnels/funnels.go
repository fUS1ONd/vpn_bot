// Package funnels считает Воронки поверх журнала Событий (events.db) и таблиц
// основной базы бота, подключённой через ATTACH только на чтение.
//
// Модуль не знает про Telegram и не содержит текстов: он отдаёт структуру
// отчёта со стабильными id Воронок и Шагов, а подписи рисует потребитель
// (админка бота, в будущем — веб-панель). Весь SQL, соединяющий журнал с
// таблицами бота, живёт только здесь. Термины — CONTEXT.md, раздел «Аналитика».
package funnels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	_ "github.com/mattn/go-sqlite3"
)

// Действия, на которые опираются Шаги. Бот пишет их в журнал теми же
// константами, поэтому id не может разойтись между записью и расчётом.
const (
	ActionInvitesOpen = "invites_open" // открыт раздел приглашений
)

// Воронки.
const (
	FunnelInvite = "invite"
)

// Шаги воронок.
const (
	StepInvitesOpened = "invites_opened"
)

// mainSchema — имя, под которым основная база подключена к соединению журнала.
const mainSchema = "main_db"

// ErrUnknownFunnel — запрошена Воронка, которой нет в списке.
var ErrUnknownFunnel = errors.New("unknown funnel")

// Funnel — описание Воронки для потребителя: id и упорядоченные id Шагов.
type Funnel struct {
	ID    string
	Steps []string
}

// Report — Воронка, посчитанная по когорте периода [From, To).
type Report struct {
	FunnelID string
	From     time.Time
	To       time.Time
	Steps    []StepReport
}

// StepReport — один Шаг отчёта. Конверсии — доли от 0 до 1; у первого Шага
// обе равны 1, если в когорте есть люди. NoData — источник Шага недоступен:
// это не ноль, а «не знаем».
type StepReport struct {
	ID           string
	People       int
	FromPrevious float64
	FromFirst    float64
	NoData       bool
}

// occurrence — момент, когда человек сделал Шаг.
type occurrence struct {
	TelegramID int64
	At         time.Time
}

// source отдаёт все случаи Шага в интервале [from, to).
type source func(ctx context.Context, conn *sql.Conn, from, to time.Time) ([]occurrence, error)

type step struct {
	id     string
	source source
}

// funnel — определение Воронки: Шаги по порядку и окно от входа, в пределах
// которого засчитываются следующие Шаги.
type funnel struct {
	id     string
	window time.Duration
	steps  []step
}

// definitions — список Воронок в порядке показа.
func definitions() []funnel {
	return []funnel{
		{
			id:     FunnelInvite,
			window: 14 * 24 * time.Hour,
			steps: []step{
				{id: StepInvitesOpened, source: journalAction(ActionInvitesOpen)},
			},
		},
	}
}

// Funnels — модуль расчёта Воронок.
type Funnels struct {
	events   *sql.DB
	mainPath string
	excluded map[int64]struct{}
	funnels  []funnel
}

// New открывает журнал на чтение расчётов. excluded — Telegram ID, которые не
// попадают ни в один Шаг (владелец: его нажатия пишутся в журнал для отладки,
// но искажали бы маленькие числа воронок).
func New(eventsPath, mainDBPath string, excluded []int64) (*Funnels, error) {
	conn, err := sql.Open("sqlite3", database.DSN(eventsPath))
	if err != nil {
		return nil, fmt.Errorf("failed to open events journal: %w", err)
	}
	set := make(map[int64]struct{}, len(excluded))
	for _, id := range excluded {
		set[id] = struct{}{}
	}
	return &Funnels{events: conn, mainPath: mainDBPath, excluded: set, funnels: definitions()}, nil
}

// Close закрывает соединения модуля.
func (f *Funnels) Close() {
	if err := f.events.Close(); err != nil {
		slog.Error("Failed to close funnels connection", "error", err)
	}
}

// List отдаёт доступные Воронки в порядке показа.
func (f *Funnels) List() []Funnel {
	list := make([]Funnel, 0, len(f.funnels))
	for _, fn := range f.funnels {
		steps := make([]string, 0, len(fn.steps))
		for _, s := range fn.steps {
			steps = append(steps, s.id)
		}
		list = append(list, Funnel{ID: fn.id, Steps: steps})
	}
	return list
}

// Report считает Воронку по когорте людей, сделавших первый Шаг в [from, to).
func (f *Funnels) Report(ctx context.Context, funnelID string, from, to time.Time) (Report, error) {
	fn, ok := f.find(funnelID)
	if !ok {
		return Report{}, fmt.Errorf("%w: %s", ErrUnknownFunnel, funnelID)
	}
	from, to = from.UTC(), to.UTC()

	conn, err := f.events.Conn(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("failed to get events connection: %w", err)
	}
	defer conn.Close()

	// ATTACH действует на одно соединение, поэтому расчёт идёт целиком на нём.
	// Основная база не подключилась — Шаги из журнала всё равно считаются, а
	// Шаги из таблиц бота покажут «нет данных».
	detach := f.attachMain(ctx, conn)
	defer detach()

	return f.compute(ctx, conn, fn, from, to), nil
}

func (f *Funnels) find(id string) (funnel, bool) {
	for _, fn := range f.funnels {
		if fn.id == id {
			return fn, true
		}
	}
	return funnel{}, false
}

// attachMain подключает основную базу только на чтение: воронки не имеют
// права ничего в ней менять.
func (f *Funnels) attachMain(ctx context.Context, conn *sql.Conn) func() {
	uri := "file:" + f.mainPath + "?mode=ro"
	if _, err := conn.ExecContext(ctx, "ATTACH DATABASE ? AS "+mainSchema, uri); err != nil {
		slog.Warn("Failed to attach main database to funnels", "error", err)
		return func() {}
	}
	return func() {
		// Соединение возвращается в пул: отключаем базу, чтобы следующий
		// расчёт подключил её заново, а не упал на «already in use».
		if _, err := conn.ExecContext(context.Background(), "DETACH DATABASE "+mainSchema); err != nil {
			slog.Warn("Failed to detach main database from funnels", "error", err)
		}
	}
}

// compute проводит когорту по Шагам. Вход — первый случай первого Шага в
// периоде; каждый следующий Шаг — подмножество предыдущего, сделанный не
// раньше предыдущего и не позже конца окна от входа. Источник Шага упал —
// этот и все следующие Шаги «нет данных»: подмножество неизвестного не посчитать.
func (f *Funnels) compute(ctx context.Context, conn *sql.Conn, fn funnel, from, to time.Time) Report {
	report := Report{FunnelID: fn.id, From: from, To: to}

	var entered map[int64]time.Time // вход в Воронку
	var reached map[int64]time.Time // момент прохождения предыдущего Шага
	noData := false

	for index, s := range fn.steps {
		result := StepReport{ID: s.id}
		if noData {
			result.NoData = true
			report.Steps = append(report.Steps, result)
			continue
		}

		sourceTo := to
		if index > 0 {
			sourceTo = to.Add(fn.window)
		}
		occurrences, err := s.source(ctx, conn, from, sourceTo)
		if err != nil {
			slog.Warn("Funnel step source failed", "funnel", fn.id, "step", s.id, "error", err)
			noData = true
			result.NoData = true
			report.Steps = append(report.Steps, result)
			continue
		}

		if index == 0 {
			entered = f.firstOccurrences(occurrences)
			reached = entered
		} else {
			reached = f.nextOccurrences(occurrences, entered, reached, fn.window)
		}
		result.People = len(reached)
		report.Steps = append(report.Steps, result)
	}

	fillConversions(report.Steps)
	return report
}

// firstOccurrences — самый ранний случай на человека, без исключённых.
func (f *Funnels) firstOccurrences(occurrences []occurrence) map[int64]time.Time {
	first := make(map[int64]time.Time)
	for _, o := range occurrences {
		if _, skip := f.excluded[o.TelegramID]; skip {
			continue
		}
		if at, ok := first[o.TelegramID]; !ok || o.At.Before(at) {
			first[o.TelegramID] = o.At
		}
	}
	return first
}

// nextOccurrences оставляет прошедших предыдущий Шаг, кто сделал этот Шаг не
// раньше предыдущего и в пределах окна от входа; момент — самый ранний такой случай.
func (f *Funnels) nextOccurrences(occurrences []occurrence, entered, previous map[int64]time.Time, window time.Duration) map[int64]time.Time {
	next := make(map[int64]time.Time)
	for _, o := range occurrences {
		prevAt, ok := previous[o.TelegramID]
		if !ok || o.At.Before(prevAt) || o.At.After(entered[o.TelegramID].Add(window)) {
			continue
		}
		if at, seen := next[o.TelegramID]; !seen || o.At.Before(at) {
			next[o.TelegramID] = o.At
		}
	}
	return next
}

// fillConversions считает доли к предыдущему и к первому Шагу. Делитель 0 или
// «нет данных» — доля 0: процент от неизвестного не показываем.
func fillConversions(steps []StepReport) {
	for index := range steps {
		current := &steps[index]
		if current.NoData || current.People == 0 {
			continue
		}
		if index == 0 {
			current.FromPrevious, current.FromFirst = 1, 1
			continue
		}
		if previous := steps[index-1]; !previous.NoData && previous.People > 0 {
			current.FromPrevious = float64(current.People) / float64(previous.People)
		}
		if first := steps[0]; !first.NoData && first.People > 0 {
			current.FromFirst = float64(current.People) / float64(first.People)
		}
	}
}
