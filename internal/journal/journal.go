// Package journal ведёт журнал Событий — что нажал пользователь и что сделал
// сам бот — в собственном файле events.db рядом с основной базой.
//
// Журнал — аналитика, и он не имеет права замедлить или уронить обработку
// нажатия: Record не блокирует и не возвращает ошибок, переполнение буфера —
// отброс со счётчиком в логе, сбой записи — только лог. Почему отдельный файл и
// один писатель — docs/adr/0005-events-journal-separate-db.md.
package journal

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
)

// FileName — имя файла журнала рядом с основной базой.
const FileName = "events.db"

// Источник События: нажатие пользователя или Системное событие бота.
const (
	SourceUser = "user"
	SourceBot  = "bot"
)

// TimeLayout — формат времени События в базе. Фиксированная ширина и UTC:
// строки сравниваются лексикографически в том же порядке, что и моменты времени,
// а формат совпадает с CURRENT_TIMESTAMP таблиц основной базы с точностью до
// миллисекунд. Из-за миллисекунд строковое сравнение ts со столбцами основной
// базы в пределах одной секунды неверно — моменты сравниваются в Go, после Scan.
const TimeLayout = "2006-01-02 15:04:05.000"

const (
	defaultBufferSize    = 4096
	defaultBatchSize     = 100
	defaultFlushInterval = 3 * time.Second
)

// Event — запись о том, что Действие произошло. Содержимого сообщений и
// payload кнопок не несёт никогда; Param — только короткое перечисление без
// персональных данных (например, способ оплаты).
type Event struct {
	At         time.Time // нулевое значение — момент записи
	TelegramID int64
	Action     string // стабильный id Действия
	Source     string // SourceUser или SourceBot
	Param      string // необязательный параметр, пустая строка — NULL
}

// Options — настройки буфера и писателя. Нулевые поля — значения по умолчанию;
// отличные от них нужны тестам.
type Options struct {
	BufferSize    int           // ёмкость буфера; переполнение — отброс
	BatchSize     int           // размер пачки, по которому писатель сбрасывает, не дожидаясь таймера
	FlushInterval time.Duration // сброс накопленного по таймеру
}

// Journal — журнал Событий с буфером и единственным писателем.
type Journal struct {
	conn     *sql.DB
	opts     Options
	events   chan Event
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	// stopMu делает «не остановлен ли журнал → положить в буфер» одной операцией
	// относительно Close: иначе Событие, положенное между проверкой и остановкой
	// писателя, осталось бы в буфере, который уже никто не прочитает.
	stopMu  sync.RWMutex
	stopped bool
	dropped atomic.Int64 // отброшено при переполнении с последнего отчёта в лог
}

// PathNextTo выводит путь журнала из пути основной базы: тот же каталог, а
// значит тот же volume и тот же бэкап.
func PathNextTo(mainDBPath string) string {
	return filepath.Join(filepath.Dir(mainDBPath), FileName)
}

// Open открывает (и при необходимости создаёт) файл журнала и запускает писателя.
func Open(path string, opts Options) (*Journal, error) {
	if opts.BufferSize <= 0 {
		opts.BufferSize = defaultBufferSize
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = defaultBatchSize
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = defaultFlushInterval
	}

	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create journal directory: %w", err)
		}
	}
	// Те же настройки SQLite, что у основной базы: WAL, busy_timeout, immediate-транзакции.
	conn, err := sql.Open("sqlite3", database.DSN(path))
	if err != nil {
		return nil, fmt.Errorf("failed to open journal: %w", err)
	}
	if err := migrate(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to migrate journal: %w", err)
	}

	j := &Journal{
		conn:   conn,
		opts:   opts,
		events: make(chan Event, opts.BufferSize),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go j.run()
	return j, nil
}

// migrate создаёт таблицу Событий. Индексы — под два вида чтения воронок:
// «кто сделал Действие за период» и «что делал человек после входа».
func migrate(conn *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts TIMESTAMP NOT NULL,
			telegram_id INTEGER NOT NULL,
			action TEXT NOT NULL,
			source TEXT NOT NULL,
			param TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_events_action_ts ON events(action, ts)`,
		`CREATE INDEX IF NOT EXISTS idx_events_telegram_id_ts ON events(telegram_id, ts)`,
	}
	for _, statement := range statements {
		if _, err := conn.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

// Record кладёт Событие в буфер и сразу возвращается. Буфер полон — Событие
// отбрасывается (счётчик уходит в лог писателем); журнал остановлен — тоже.
func (j *Journal) Record(event Event) {
	if event.At.IsZero() {
		event.At = time.Now()
	}
	j.stopMu.RLock()
	defer j.stopMu.RUnlock()
	if j.stopped {
		// Нажатие, обработка которого закончилась после остановки: писателя уже
		// нет, событие теряется — как и при падении процесса (ADR-0005).
		return
	}
	select {
	case j.events <- event:
	default:
		j.dropped.Add(1)
	}
}

// Close сбрасывает буфер в базу, останавливает писателя и закрывает файл.
// Повторный вызов безопасен.
func (j *Journal) Close() {
	j.stopOnce.Do(func() {
		j.stopMu.Lock()
		j.stopped = true
		j.stopMu.Unlock()
		close(j.stop)
		<-j.done
		if err := j.conn.Close(); err != nil {
			slog.Error("Failed to close events journal", "error", err)
		}
	})
}

// run — единственный писатель: копит пачку и сбрасывает её по размеру, по
// таймеру и при остановке.
func (j *Journal) run() {
	defer close(j.done)
	ticker := time.NewTicker(j.opts.FlushInterval)
	defer ticker.Stop()

	batch := make([]Event, 0, j.opts.BatchSize)
	flush := func() {
		j.reportDropped()
		if len(batch) == 0 {
			return
		}
		j.write(batch)
		batch = batch[:0]
	}

	for {
		select {
		case event := <-j.events:
			batch = append(batch, event)
			if len(batch) >= j.opts.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-j.stop:
			// Добираем всё, что успело лечь в буфер до остановки.
			for {
				select {
				case event := <-j.events:
					batch = append(batch, event)
				default:
					flush()
					return
				}
			}
		}
	}
}

// reportDropped пишет в лог, сколько Событий отброшено с прошлого отчёта:
// одна строка на сброс, а не на каждое отброшенное нажатие.
func (j *Journal) reportDropped() {
	if dropped := j.dropped.Swap(0); dropped > 0 {
		slog.Warn("Events journal buffer overflow, events dropped", "dropped", dropped)
	}
}

// write записывает пачку одной транзакцией. Ошибка — только лог: пачка
// теряется, бот продолжает работать.
func (j *Journal) write(batch []Event) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("Events journal writer panicked", "recover", r, "events", len(batch))
		}
	}()

	if err := j.insert(batch); err != nil {
		slog.Error("Failed to write events journal batch", "error", err, "events", len(batch))
	}
}

func (j *Journal) insert(batch []Event) error {
	tx, err := j.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // после Commit откат ничего не делает

	stmt, err := tx.Prepare(`INSERT INTO events (ts, telegram_id, action, source, param) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, event := range batch {
		var param any
		if event.Param != "" {
			param = event.Param
		}
		if _, err := stmt.Exec(event.At.UTC().Format(TimeLayout), event.TelegramID, event.Action, event.Source, param); err != nil {
			return err
		}
	}
	return tx.Commit()
}
