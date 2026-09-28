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
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
	_ "github.com/mattn/go-sqlite3"
)

// Действия, на которые опираются Шаги. Бот пишет их в журнал теми же
// константами, поэтому id не может разойтись между записью и расчётом.
const (
	ActionInvitesOpen = "invites_open" // открыт раздел приглашений
	// ActionShareSent — человек выбрал приглашение в inline-ответе «Поделиться»,
	// и оно ушло в чат (chosen_inline_result). Telegram присылает выбор, только
	// если у @BotFather включён /setinlinefeedback.
	ActionShareSent = "share_sent"

	// Входы в оплату — Действия, открывающие экран выбора способа оплаты.
	ActionPayMenu              = "pay_menu"           // reply «Оплатить подписку» в главном меню
	ActionRenewMenu            = "renew_menu"         // reply «Продлить подписку» в главном меню
	ActionPayOpen              = "pay_open"           // inline-кнопка оплаты под уведомлением планировщика
	ActionAutorenewPayManually = "ar_pay"             // «Продлить вручную» после неудачного автосписания
	ActionPayYooKassaReply     = "pay_yookassa_reply" // способ со старой reply-клавиатуры: открывает экран заново
	ActionPayCryptoReply       = "pay_crypto_reply"   // то же для крипты

	// Выбор способа оплаты. Способ пишется параметром События — только
	// перечислением PayMethod*, без персональных данных.
	ActionPayMethod    = "pay_method" // способ выбран на экране оплаты
	ActionRetryPayment = "retry_pay"  // повтор создания платежа тем же способом после сбоя

	// Системные события: бот сам отправил человеку сообщение планировщика.
	// Пишутся только когда Telegram сообщение принял; истории этих сообщений
	// нет в таблицах бота — маркер notifications_sent стирается при оплате.
	ActionReminder3d = "remind_3d" // напоминание за 3 дня до конца оплаченной подписки
	ActionReminder1d = "remind_1d" // напоминание за сутки до конца оплаченной подписки
	// ActionExpiredNotice — сообщение «подписка истекла, VPN деактивирован».
	// Id — по сообщению, а не по отключению: в режиме обслуживания или при
	// сбое панели сообщение уходит, а доступ не отключается.
	ActionExpiredNotice = "expired_notice"
)

// Значения параметра выбора способа оплаты. Разбивка Воронки по способам пока
// не показывается, но пишется сразу: история копится без новой записи.
const (
	PayMethodYooKassa = "yookassa" // карта, СБП, SberPay через ЮKassa
	PayMethodCrypto   = "crypto"   // крипта через Platega
)

// PayMethodParams — провайдер платежа → параметр выбора способа оплаты.
// Единственное место этой связи: по ней бот пишет параметр События, а Шаг
// «платёж создан» по параметру находит платёж того же провайдера.
var PayMethodParams = map[string]string{
	paymentprovider.YooKassa: PayMethodYooKassa,
	paymentprovider.Platega:  PayMethodCrypto,
}

// Воронки.
const (
	FunnelInvite     = "invite"
	FunnelPayment    = "payment"
	FunnelOnboarding = "onboarding"
	FunnelNonRenewal = "non_renewal"
)

// Шаги воронок.
const (
	StepInvitesOpened = "invites_opened"
	StepInviteCreated = "invite_created"
	StepInviteSent    = "invite_sent"
	// StepFriendRegistered и StepFriendPaid — Шаги автора: по его приглашению
	// зарегистрировался и впервые оплатил друг.
	StepFriendRegistered = "friend_registered"
	StepFriendPaid       = "friend_paid"

	// Шаги оплаты. Воронка «Непродлившие» переиспользует их вслед за своим входом.
	StepPaymentEntered      = "payment_entered"
	StepPaymentMethodChosen = "payment_method_chosen"
	StepPaymentCreated      = "payment_created"
	StepPaymentConfirmed    = "payment_confirmed"

	// Шаги онбординга: регистрация в боте и первое подключение устройства по
	// данным панели. Завершает Воронку тот же Шаг «платёж подтверждён».
	StepRegistered      = "registered"
	StepDeviceConnected = "device_connected"

	// StepReminded — вход Воронки непродливших: бот напомнил о конце оплаченной
	// подписки за 3 дня или за сутки. Дальше — Шаги оплаты.
	StepReminded = "reminded"
)

// FirstConnections — порт панели для воронок: момент первого подключения
// устройства по Telegram ID. Кто ни разу не подключался, в ответе отсутствует.
// Ошибка — панель недоступна, и Шаг устройства получает «нет данных».
type FirstConnections interface {
	FirstConnectedAt(ctx context.Context) (map[int64]time.Time, error)
}

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
	// WindowOpen — окно хотя бы одного вошедшего ещё не истекло: он может
	// пройти следующие Шаги позже, и конверсия пока недосчитана.
	WindowOpen bool
}

// StepReport — один Шаг отчёта. Конверсии — доли от 0 до 1; у первого Шага
// обе равны 1, если в когорте есть люди. NoData — источник Шага недоступен:
// это не ноль, а «не знаем». Cascaded — «нет данных» унаследовано от упавшего
// раньше Шага, а не от своего источника: причину нужно искать выше.
type StepReport struct {
	ID           string
	People       int
	FromPrevious float64
	FromFirst    float64
	NoData       bool
	Cascaded     bool
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
	// optional — отказ источника не обрывает цепочку: Шаг получает «нет
	// данных», а следующий считается от последнего посчитанного Шага. Годится
	// только для Шага, без которого следующий имеет смысл сам по себе (оплата
	// без известного подключения — всё ещё оплата новичка); у остальных Шагов
	// подмножество неизвестного не посчитать.
	optional bool
}

// funnel — определение Воронки: Шаги по порядку и окно от входа, в пределах
// которого засчитываются следующие Шаги.
type funnel struct {
	id     string
	window time.Duration
	steps  []step
}

// definitions — список Воронок в порядке показа. panel — порт панели для Шага
// устройства; nil — панель не подключена, и Шаг всегда «нет данных».
func definitions(panel FirstConnections) []funnel {
	return []funnel{
		{
			id:     FunnelInvite,
			window: 14 * 24 * time.Hour,
			steps: []step{
				{id: StepInvitesOpened, source: journalAction(ActionInvitesOpen)},
				{id: StepInviteCreated, source: referralInvitesCreated},
				{id: StepInviteSent, source: journalActionOrNoData(ActionShareSent)},
				{id: StepFriendRegistered, source: referralFriendsRegistered},
				{id: StepFriendPaid, source: referralFriendsPaid},
			},
		},
		{
			id:     FunnelPayment,
			window: 24 * time.Hour,
			steps: []step{
				{id: StepPaymentEntered, source: paymentEntered},
				{id: StepPaymentMethodChosen, source: journalAction(ActionPayMethod, ActionRetryPayment)},
				{id: StepPaymentCreated, source: paymentCreated},
				{id: StepPaymentConfirmed, source: paymentConfirmed},
			},
		},
		{
			id:     FunnelOnboarding,
			window: 14 * 24 * time.Hour,
			steps: []step{
				{id: StepRegistered, source: usersRegistered},
				{id: StepDeviceConnected, source: firstConnected(panel), optional: true},
				{id: StepPaymentConfirmed, source: paymentConfirmed},
			},
		},
		{
			id:     FunnelNonRenewal,
			window: 14 * 24 * time.Hour,
			steps: []step{
				{id: StepReminded, source: renewalReminded},
				{id: StepPaymentEntered, source: paymentEntered},
				{id: StepPaymentCreated, source: paymentCreated},
				{id: StepPaymentConfirmed, source: paymentConfirmed},
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
	now      func() time.Time // часы для признака «окно ещё не закрыто»
}

// New открывает журнал на чтение расчётов. excluded — Telegram ID, которые не
// попадают ни в один Шаг (владелец: его нажатия пишутся в журнал для отладки,
// но искажали бы маленькие числа воронок). panel — порт панели для Шага
// первого подключения; nil допустим, Шаг тогда «нет данных».
func New(eventsPath, mainDBPath string, excluded []int64, panel FirstConnections) (*Funnels, error) {
	// Журнал — только на чтение: пишет в него один бот. Режим WAL у файла уже
	// выставлен журналом, читателю его не выставлять, а без _txlock=immediate
	// чтение не берёт блокировку записи.
	conn, err := sql.Open("sqlite3", "file:"+sqliteURIPath(eventsPath)+"?mode=ro&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("failed to open events journal: %w", err)
	}
	set := make(map[int64]struct{}, len(excluded))
	for _, id := range excluded {
		set[id] = struct{}{}
	}
	return &Funnels{events: conn, mainPath: mainDBPath, excluded: set, funnels: definitions(panel), now: time.Now}, nil
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
	uri := "file:" + sqliteURIPath(f.mainPath) + "?mode=ro"
	if _, err := conn.ExecContext(ctx, "ATTACH DATABASE ? AS "+mainSchema, uri); err != nil {
		slog.Warn("Failed to attach main database to funnels", "error", err)
		return func() {}
	}
	return func() {
		// Соединение возвращается в пул: отключаем базу, чтобы следующий
		// расчёт подключил её заново, а не упал на «already in use».
		if _, err := conn.ExecContext(context.Background(), "DETACH DATABASE "+mainSchema); err != nil {
			slog.Warn("Failed to detach main database from funnels, dropping connection", "error", err)
			// Соединение с неотключённой базой в пул не возвращаем: ATTACH на нём
			// падал бы всегда, и Шаги из таблиц бота навсегда стали бы «нет
			// данных». ErrBadConn из Raw велит пулу закрыть соединение.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}
}

// compute проводит когорту по Шагам. Вход — первый случай первого Шага в
// периоде; каждый следующий Шаг — подмножество предыдущего, сделанный не
// раньше предыдущего и не позже конца окна от входа. Источник Шага упал —
// этот и все следующие Шаги «нет данных»: подмножество неизвестного не посчитать.
// Исключение — необязательный Шаг (step.optional): «нет данных» только у него,
// а следующий Шаг считается от последнего посчитанного.
func (f *Funnels) compute(ctx context.Context, conn *sql.Conn, fn funnel, from, to time.Time) Report {
	report := Report{FunnelID: fn.id, From: from, To: to}

	var entered map[int64]time.Time // вход в Воронку
	var reached map[int64]time.Time // момент прохождения предыдущего Шага
	noData := false

	for index, s := range fn.steps {
		result := StepReport{ID: s.id}
		if noData {
			result.NoData = true
			result.Cascaded = true
			report.Steps = append(report.Steps, result)
			continue
		}

		sourceTo := to
		if index > 0 {
			sourceTo = to.Add(fn.window)
		}
		occurrences, err := s.source(ctx, conn, from, sourceTo)
		if err != nil {
			if errors.Is(err, errNoEvents) {
				slog.Info("Funnel step has no events yet", "funnel", fn.id, "step", s.id)
			} else {
				slog.Warn("Funnel step source failed", "funnel", fn.id, "step", s.id, "error", err)
			}
			// Первый Шаг необязательным не бывает: без входа нет когорты.
			noData = index == 0 || !s.optional
			result.NoData = true
			report.Steps = append(report.Steps, result)
			continue
		}

		if index == 0 {
			entered = f.firstOccurrences(occurrences)
			reached = entered
			report.WindowOpen = windowOpen(entered, fn.window, f.now())
		} else {
			reached = f.nextOccurrences(occurrences, entered, reached, fn.window)
		}
		result.People = len(reached)
		report.Steps = append(report.Steps, result)
	}

	fillConversions(report.Steps)
	return report
}

// windowOpen — не истекло ли окно хоть у одного вошедшего к моменту now.
func windowOpen(entered map[int64]time.Time, window time.Duration, now time.Time) bool {
	for _, at := range entered {
		if at.Add(window).After(now) {
			return true
		}
	}
	return false
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

// sqliteURIPath экранирует в пути символы, которые SQLite в URI-имени файла
// понял бы как начало параметров, фрагмента или escape-последовательности.
func sqliteURIPath(path string) string {
	return strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(path)
}
