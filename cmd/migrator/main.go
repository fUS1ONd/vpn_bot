package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/fus1ond/vpn_bot/internal/config"
	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
	_ "github.com/mattn/go-sqlite3"
)

// OldUser — структура пользователя из старой БД
type OldUser struct {
	TelegramID int64
	Username   string
	Email      string
	UUID       string
	Status     string
}

func main() {
	// Флаги командной строки
	dryRun := flag.Bool("dry-run", false, "Показать что будет мигрировано без изменений")
	live := flag.Bool("live", false, "Выполнить миграцию")
	oldDBPath := flag.String("old-db", "users.db", "Путь к старой БД (users.db)")
	flag.Parse()

	if !*dryRun && !*live {
		fmt.Println("Использование:")
		fmt.Println("  ./migrator --dry-run    # показать что будет мигрировано")
		fmt.Println("  ./migrator --live       # выполнить миграцию")
		fmt.Println("")
		fmt.Println("Опции:")
		fmt.Println("  --old-db PATH    путь к старой БД (по умолчанию: users.db)")
		os.Exit(1)
	}

	// Загрузка конфига
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Ошибка загрузки конфига: %v", err)
	}

	// Открываем старую БД (read-only)
	oldDB, err := sql.Open("sqlite3", *oldDBPath+"?mode=ro")
	if err != nil {
		log.Fatalf("Ошибка открытия старой БД: %v", err)
	}
	defer oldDB.Close()

	// Читаем пользователей из старой БД
	oldUsers, err := readOldUsers(oldDB)
	if err != nil {
		log.Fatalf("Ошибка чтения старой БД: %v", err)
	}

	fmt.Printf("Найдено пользователей в старой БД: %d\n\n", len(oldUsers))

	if *dryRun {
		fmt.Println("=== DRY RUN ===")
		for i, u := range oldUsers {
			fmt.Printf("%d. telegram_id=%d, username=%s, email=%s\n",
				i+1, u.TelegramID, u.Username, u.Email)
		}
		fmt.Println("\nДля выполнения миграции запустите: ./migrator --live")
		return
	}

	// Открываем новую БД
	newDB, err := database.New(cfg.DBPath)
	if err != nil {
		log.Fatalf("Ошибка открытия новой БД: %v", err)
	}
	defer newDB.Close()

	// Создаём клиент Remnawave
	remnawaveClient := remnawave.NewClient(
		cfg.RemnawaveURL,
		cfg.RemnawaveAPIToken,
		cfg.RemnawaveSquadUUID,
	)

	// Открываем лог-файл
	logFile, err := os.Create(fmt.Sprintf("migration_%s.log", time.Now().Format("2006-01-02")))
	if err != nil {
		log.Fatalf("Ошибка создания лог-файла: %v", err)
	}
	defer logFile.Close()

	// Выполняем миграцию
	fmt.Println("=== МИГРАЦИЯ ===")
	successCount := 0
	skipCount := 0
	errorCount := 0

	for _, oldUser := range oldUsers {
		// Пропускаем пользователей с telegram_id = 0 (созданные админом без привязки)
		if oldUser.TelegramID == 0 {
			logLine := fmt.Sprintf("[SKIP] telegram_id=0 (admin-created), email=%s\n", oldUser.Email)
			fmt.Print(logLine)
			logFile.WriteString(logLine)
			skipCount++
			continue
		}

		// Проверяем, не мигрирован ли уже
		existing, _ := newDB.GetUserByTelegramID(oldUser.TelegramID)
		if existing != nil {
			logLine := fmt.Sprintf("[SKIP] telegram_id=%d — already migrated\n", oldUser.TelegramID)
			fmt.Print(logLine)
			logFile.WriteString(logLine)
			skipCount++
			continue
		}

		// Создаём пользователя в Remnawave
		username := oldUser.Username
		if username == "" {
			username = fmt.Sprintf("tg_%d", oldUser.TelegramID)
		}

		remnawaveUser, err := remnawaveClient.CreateUser(oldUser.TelegramID, username)
		if err != nil {
			logLine := fmt.Sprintf("[ERROR] telegram_id=%d — API error: %v\n", oldUser.TelegramID, err)
			fmt.Print(logLine)
			logFile.WriteString(logLine)
			errorCount++
			continue
		}

		// Сохраняем в новую БД (first_name пустой, так как старая БД его не хранила)
		_, err = newDB.CreateUser(oldUser.TelegramID, username, "", remnawaveUser.UUID)
		if err != nil {
			logLine := fmt.Sprintf("[ERROR] telegram_id=%d — DB error: %v\n", oldUser.TelegramID, err)
			fmt.Print(logLine)
			logFile.WriteString(logLine)
			// Удаляем из Remnawave если не смогли сохранить в БД
			_ = remnawaveClient.DeleteUser(remnawaveUser.UUID)
			errorCount++
			continue
		}

		logLine := fmt.Sprintf("[OK] telegram_id=%d, uuid=%s\n", oldUser.TelegramID, remnawaveUser.UUID)
		fmt.Print(logLine)
		logFile.WriteString(logLine)
		successCount++

		// Небольшая задержка чтобы не перегружать API
		time.Sleep(100 * time.Millisecond)
	}

	fmt.Println("\n=== РЕЗУЛЬТАТ ===")
	fmt.Printf("Успешно: %d\n", successCount)
	fmt.Printf("Пропущено: %d\n", skipCount)
	fmt.Printf("Ошибок: %d\n", errorCount)
	fmt.Printf("\nЛог сохранён: migration_%s.log\n", time.Now().Format("2006-01-02"))
}

// readOldUsers читает только активных пользователей из старой БД
func readOldUsers(db *sql.DB) ([]OldUser, error) {
	// Выбираем только пользователей со статусом 'active' (у кого есть доступ к серверам)
	rows, err := db.Query(`
		SELECT telegram_id, COALESCE(username, ''), COALESCE(email, ''), COALESCE(uuid, ''), COALESCE(subscription_status, '')
		FROM users
		WHERE subscription_status = 'active'
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query users: %w", err)
	}
	defer rows.Close()

	var users []OldUser
	for rows.Next() {
		var u OldUser
		if err := rows.Scan(&u.TelegramID, &u.Username, &u.Email, &u.UUID, &u.Status); err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}
		users = append(users, u)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return users, nil
}
