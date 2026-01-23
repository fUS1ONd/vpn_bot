// Скрипт для заполнения поля used_at в таблице invites
//
// ЗАЧЕМ ЭТО НУЖНО:
// В версии бота до 2026-01-23 таблица invites не имела поля used_at (дата активации кода).
// После обновления бота поле добавляется автоматически через миграцию, но для уже
// использованных кодов значение будет NULL.
//
// Этот скрипт подтягивает дату создания пользователя из Remnawave API и записывает
// её как дату активации кода. Логика: дата создания пользователя в Remnawave ≈ дата
// активации инвайт-кода (пользователь создаётся сразу после ввода кода).
//
// ИСПОЛЬЗОВАНИЕ:
//
//	# Предпросмотр — показывает что будет обновлено, без изменений в БД
//	go run cmd/backfill-used-at/main.go --dry-run
//
//	# Выполнить обновление
//	go run cmd/backfill-used-at/main.go --live
//
// ТРЕБУЕМЫЕ ПЕРЕМЕННЫЕ ОКРУЖЕНИЯ:
//   - DB_PATH — путь к базе данных бота
//   - REMNAWAVE_URL — URL панели Remnawave
//   - REMNAWAVE_API_TOKEN — токен API Remnawave
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

func main() {
	// Флаги командной строки
	dryRun := flag.Bool("dry-run", false, "Предпросмотр без изменений в БД")
	live := flag.Bool("live", false, "Выполнить обновление")
	flag.Parse()

	// Проверка флагов
	if !*dryRun && !*live {
		fmt.Println("Использование:")
		fmt.Println("  --dry-run  Предпросмотр без изменений")
		fmt.Println("  --live     Выполнить обновление")
		os.Exit(1)
	}

	if *dryRun && *live {
		log.Fatal("Нельзя указать одновременно --dry-run и --live")
	}

	// Загружаем конфигурацию из окружения
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "data/bot.db"
	}

	remnawaveURL := os.Getenv("REMNAWAVE_URL")
	if remnawaveURL == "" {
		log.Fatal("REMNAWAVE_URL не задан")
	}

	remnawaveToken := os.Getenv("REMNAWAVE_API_TOKEN")
	if remnawaveToken == "" {
		log.Fatal("REMNAWAVE_API_TOKEN не задан")
	}

	// Подключаемся к БД
	db, err := database.New(dbPath)
	if err != nil {
		log.Fatalf("Ошибка подключения к БД: %v", err)
	}
	defer db.Close()

	// Создаём клиент Remnawave
	client := remnawave.NewClient(remnawaveURL, remnawaveToken, "")

	// Получаем инвайты без даты активации
	invites, err := getInvitesWithoutUsedAt(db)
	if err != nil {
		log.Fatalf("Ошибка получения инвайтов: %v", err)
	}

	if len(invites) == 0 {
		fmt.Println("✅ Все использованные инвайты уже имеют дату активации")
		return
	}

	fmt.Printf("Найдено %d инвайтов без даты активации\n\n", len(invites))

	// Обрабатываем каждый инвайт
	var updated, skipped, errors int

	for _, inv := range invites {
		// Получаем пользователя из БД
		user, err := db.GetUserByTelegramID(*inv.UsedBy)
		if err != nil || user == nil {
			fmt.Printf("⚠️  Код %s: пользователь %d не найден в БД\n", inv.Code, *inv.UsedBy)
			skipped++
			continue
		}

		// Получаем данные из Remnawave
		remnawaveUser, err := client.GetUser(user.RemnawaveUUID)
		if err != nil {
			fmt.Printf("❌ Код %s: ошибка Remnawave API: %v\n", inv.Code, err)
			errors++
			continue
		}

		// Выводим информацию
		fmt.Printf("🔹 Код: %s\n", inv.Code)
		fmt.Printf("   Пользователь: %d (@%s)\n", *inv.UsedBy, user.Username)
		fmt.Printf("   Дата создания в Remnawave: %s\n", remnawaveUser.CreatedAt.Format("02.01.2006 15:04:05"))

		// Обновляем в БД если не dry-run
		if *live {
			if err := updateInviteUsedAt(db, inv.Code, remnawaveUser.CreatedAt); err != nil {
				fmt.Printf("   ❌ Ошибка обновления: %v\n", err)
				errors++
				continue
			}
			fmt.Printf("   ✅ Обновлено\n")
		} else {
			fmt.Printf("   ℹ️  Будет обновлено (dry-run)\n")
		}

		updated++
		fmt.Println()
	}

	// Итоги
	fmt.Println("─────────────────────────────")
	fmt.Printf("Обработано: %d\n", updated)
	fmt.Printf("Пропущено: %d\n", skipped)
	fmt.Printf("Ошибок: %d\n", errors)

	if *dryRun {
		fmt.Println("\n⚠️  Это был предпросмотр. Для применения изменений запустите с --live")
	}
}

// inviteInfo — информация об инвайте для обработки
type inviteInfo struct {
	Code   string
	UsedBy *int64
}

// getInvitesWithoutUsedAt получает использованные инвайты без даты активации
func getInvitesWithoutUsedAt(db *database.DB) ([]inviteInfo, error) {
	rows, err := db.Conn().Query(`
		SELECT code, used_by
		FROM invites
		WHERE used_by IS NOT NULL AND used_at IS NULL
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var invites []inviteInfo
	for rows.Next() {
		var inv inviteInfo
		if err := rows.Scan(&inv.Code, &inv.UsedBy); err != nil {
			return nil, err
		}
		invites = append(invites, inv)
	}

	return invites, rows.Err()
}

// updateInviteUsedAt обновляет дату активации инвайта
func updateInviteUsedAt(db *database.DB, code string, usedAt time.Time) error {
	_, err := db.Conn().Exec(
		`UPDATE invites SET used_at = ? WHERE code = ?`,
		usedAt, code,
	)
	return err
}
