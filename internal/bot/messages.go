package bot

import (
	"fmt"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

// subscriptionType определяет тип подписки для отображения в UI
type subscriptionType int

const (
	subTypeTrial    subscriptionType = iota // Триал (пробный период)
	subTypePaid                             // Оплаченная подписка
	subTypeGrace                            // Grace period (disabled + не кикнут)
	subTypeInfinite                         // Бесконечная (expireAt >= 2099)
)

// Сообщения пользователя
const (
	MsgWelcomeInvite = `<b>🔒 Доступ по приглашению</b>

Этот VPN-бот работает по системе инвайтов.

Введите код приглашения:`

	MsgWelcomeBack = `<b>С возвращением!</b>

Выберите действие:`

	MsgAccountCreated = `<b>✅ Аккаунт создан!</b>

Добро пожаловать! Ваш VPN-доступ активирован.

<b>Ссылка для подключения:</b>
<code>%s</code>

Скопируйте ссылку и вставьте в приложение.
Нажмите "📚 Инструкции" для настройки.`

	MsgNotRegistered = `<b>❌ Вы не зарегистрированы</b>

Для доступа к VPN нужен инвайт-код.
Отправьте /start для регистрации.`

	MsgSubscriptionLink = `<b>🌐 Ссылка для подключения</b>

<code>%s</code>

Скопируйте ссылку и вставьте в приложение VPN-клиента.`

	MsgInfo = `<b>💡 Помощь и контакты</b>

Если есть вопросы — пишите @fus1ond

🔒 Политика конфиденциальности: <a href="https://telegra.ph/Politika-konfidencialnosti-08-15-17">читать</a>
📜 Пользовательское соглашение: <a href="https://telegra.ph/Polzovatelskoe-soglashenie-08-15-10">читать</a>`

	MsgInstructions = `<b>📚 Инструкции по настройке</b>

Выберите вашу платформу:`

	MsgInstructionIOS = `<b>Настройка на iOS (iPhone/iPad)</b>

1. Скачайте приложение <b>Happ</b> из App Store:
   https://apps.apple.com/ru/app/happ-proxy-utility-plus/id6746188973

2. Откройте приложение

3. Нажмите "+" в правом верхнем углу

4. Выберите "Вставить из буфера обмена"

5. Выберите сервер и включите VPN переключателем

<b>Ваша ссылка подписки:</b>
<code>%s</code>`

	MsgInstructionAndroid = `<b>Настройка на Android</b>

1. Скачайте приложение <b>Happ</b> из Play Market:
   https://play.google.com/store/apps/details?id=com.happproxy

   Или APK: https://github.com/Happ-proxy/happ-android/releases/latest/download/Happ.apk

2. Откройте приложение

3. Нажмите "+" в правом верхнем углу

4. Выберите "Добавить из буфера обмена"

5. Включите VPN переключателем

<b>Ваша ссылка подписки:</b>
<code>%s</code>`

	MsgInstructionDesktop = `<b>Настройка на ПК</b>

1. Скачайте <b>HAPP</b>:
   https://www.happ.su/main/ru

2. Установите и откройте клиент

3. Нажмите "Добавить"(визуально плюсик)

4. Вставьте в поле "URL подписки" вашу ссылку (внизу данного поста)

5. Переключите тумблер на "TUN" и подключитесь

<b>Ваша ссылка подписки:</b>
<code>%s</code>`
	MsgSubtitlesProcessing = `⏳ Рендеринг видео...`

	MsgSubtitlesNoAvatar = `⚠️ Не удалось получить фото профиля — возможно, закрыты настройки приватности.

Будет использовано стандартное изображение. Чтобы использовать своё фото, откройте:
Настройки → Конфиденциальность → Фото профиля → Кто видит → Все`

	MsgSubtitlesError = `❌ Не удалось создать видео. Попробуйте позже.`

	MsgSubtitlesTimeout = `⏰ Рендеринг занял слишком долго. Попробуйте позже.`

	MsgSubtitlesUnavailable = `❌ Сервис временно недоступен. Попробуйте позже.`

	MsgGraceWarning = `⚠️ <b>Ваша подписка истекла. VPN деактивирован.</b>

Осталось <b>%s</b> чтобы оплатить и восстановить доступ.
Если не оплатить до <b>%s</b>, аккаунт будет удалён.`
)

// Сообщения админа
const (
	MsgAdminWelcome = `<b>👑 Админ-панель</b>

Выберите действие:`

	MsgInviteCreated = `🔒 Приглашение в VPN

Нажмите на ссылку ниже, чтобы активировать доступ:
👉 https://t.me/%s?start=%s`

	MsgEnterBanUser = `<b>🚫 Забанить пользователя</b>

Введите telegram_id пользователя:`

	MsgAdminBroadcastMenu = `<b>📢 Рассылка</b>

Активных пользователей: %d

Рассылка отправляется только пользователям со статусом ACTIVE в Remnawave.`

	MsgAdminEnterBroadcast = `<b>📢 Рассылка</b>

Получателей: <b>%d</b> активных пользователей

Введите сообщение для рассылки.
Поддерживается HTML-форматирование.`

	MsgAdminBroadcastResult = `<b>📢 Рассылка завершена!</b>

✅ Успешно: %d
❌ Ошибок: %d`
)

// determineSubscriptionType определяет тип подписки на основе данных из Remnawave и БД
func determineSubscriptionType(remUser *remnawave.User, isTrial bool) subscriptionType {
	if remUser.ExpireAt.Year() >= 2099 {
		return subTypeInfinite
	}
	// Grace period: disabled, но подписка истекла
	if remUser.Status == remnawave.StatusDisabled && !remUser.ExpireAt.After(time.Now().UTC()) {
		return subTypeGrace
	}
	if isTrial {
		return subTypeTrial
	}
	return subTypePaid
}

// FormatUserStatus форматирует статус пользователя с учётом типа подписки
func FormatUserStatus(remUser *remnawave.User, dbUser *database.User, isTrial bool, devicesCount *int) string {
	subType := determineSubscriptionType(remUser, isTrial)

	var msg string

	switch subType {
	case subTypeInfinite:
		msg = formatInfiniteStatus(remUser, devicesCount)
	case subTypeGrace:
		msg = formatGraceStatus(remUser, dbUser)
	case subTypeTrial:
		msg = formatTrialStatus(remUser, dbUser, devicesCount)
	case subTypePaid:
		msg = formatPaidStatus(remUser, dbUser, devicesCount)
	}

	return msg
}

func formatInfiniteStatus(remUser *remnawave.User, devicesCount *int) string {
	msg := "<b>👤 Ваш статус</b>\n\n"
	msg += "<b>Тип:</b> ♾️ Безлимитная подписка\n"
	msg += "<b>Статус:</b> ✅ Активен\n"

	if remUser.UserTraffic != nil {
		usedGB := float64(remUser.UserTraffic.UsedTrafficBytes) / (1024 * 1024 * 1024)
		msg += fmt.Sprintf("\n<b>Трафик за месяц:</b> %.2f GB\n", usedGB)
	}
	msg += formatDevicesLine(remUser, devicesCount)

	msg += fmt.Sprintf("\n<b>Ссылка подписки:</b>\n<code>%s</code>", remUser.SubscriptionURL)
	return msg
}

func formatGraceStatus(remUser *remnawave.User, dbUser *database.User) string {
	graceDeadline := remUser.ExpireAt.Add(72 * time.Hour)
	remaining := time.Until(graceDeadline)

	var remainStr string
	days := int(remaining.Hours() / 24)
	if days > 0 {
		remainStr = fmt.Sprintf("%d дн.", days)
	} else {
		hours := int(remaining.Hours())
		if hours > 0 {
			remainStr = fmt.Sprintf("%d ч.", hours)
		} else {
			remainStr = "менее часа"
		}
	}

	msg := "<b>⚠️ Подписка истекла</b>\n\n"
	msg += "<b>Статус:</b> ⛔ VPN деактивирован\n"
	msg += fmt.Sprintf("<b>Осталось для оплаты:</b> %s (до %s)\n", remainStr, graceDeadline.Format("02.01.2006"))

	if dbUser != nil && dbUser.SubscriptionPrice != nil {
		msg += fmt.Sprintf("\n<b>Цена подписки:</b> %d руб/мес\n", *dbUser.SubscriptionPrice)
	}

	msg += "\nОплатите подписку, чтобы восстановить доступ."
	return msg
}

func formatTrialStatus(remUser *remnawave.User, dbUser *database.User, devicesCount *int) string {
	msg := "<b>👤 Ваш статус</b>\n\n"
	msg += "<b>Тип:</b> ⏳ Пробный период\n"

	// Статус
	statusEmoji, statusText := formatStatusLine(remUser.Status)
	msg += fmt.Sprintf("<b>Статус:</b> %s %s\n", statusEmoji, statusText)

	// Осталось дней
	remaining := time.Until(remUser.ExpireAt)
	days := int(remaining.Hours() / 24)
	if days > 0 {
		msg += fmt.Sprintf("<b>Осталось:</b> %d дн. (до %s)\n", days, remUser.ExpireAt.Format("02.01.2006"))
	} else if remaining > 0 {
		hours := int(remaining.Hours())
		msg += fmt.Sprintf("<b>Осталось:</b> %d ч.\n", hours)
	}

	// Трафик с лимитом
	if remUser.UserTraffic != nil {
		usedGB := float64(remUser.UserTraffic.UsedTrafficBytes) / (1024 * 1024 * 1024)
		if remUser.TrafficLimitBytes > 0 {
			limitGB := float64(remUser.TrafficLimitBytes) / (1024 * 1024 * 1024)
			msg += fmt.Sprintf("<b>Трафик:</b> %.2f / %.2f GB\n", usedGB, limitGB)
		} else {
			msg += fmt.Sprintf("<b>Трафик за месяц:</b> %.2f GB\n", usedGB)
		}
	}
	msg += formatDevicesLine(remUser, devicesCount)

	if dbUser != nil && dbUser.SubscriptionPrice != nil {
		msg += fmt.Sprintf("\n<b>Цена подписки:</b> %d руб/мес\n", *dbUser.SubscriptionPrice)
	}

	// Ссылка подписки
	if remUser.Status == remnawave.StatusActive {
		msg += fmt.Sprintf("\n<b>Ссылка подписки:</b>\n<code>%s</code>", remUser.SubscriptionURL)
	}

	// Подсказка
	if remUser.UserTraffic != nil && remUser.TrafficLimitBytes > 0 &&
		remUser.UserTraffic.UsedTrafficBytes >= remUser.TrafficLimitBytes {
		msg += "\n\n⚠️ Лимит трафика исчерпан. VPN не работает.\nОплатите подписку для безлимитного доступа."
	} else {
		msg += "\n\n💡 Оплатите подписку, чтобы снять лимит трафика\nи получить безлимитный доступ."
	}

	return msg
}

func formatPaidStatus(remUser *remnawave.User, dbUser *database.User, devicesCount *int) string {
	msg := "<b>👤 Ваш статус</b>\n\n"
	msg += "<b>Тип:</b> 💳 Подписка\n"

	statusEmoji, statusText := formatStatusLine(remUser.Status)
	msg += fmt.Sprintf("<b>Статус:</b> %s %s\n", statusEmoji, statusText)

	// Осталось дней
	remaining := time.Until(remUser.ExpireAt)
	days := int(remaining.Hours() / 24)
	if days > 0 {
		msg += fmt.Sprintf("<b>Осталось:</b> %d дн. (до %s)\n", days, remUser.ExpireAt.Format("02.01.2006"))
	} else if remaining > 0 {
		hours := int(remaining.Hours())
		msg += fmt.Sprintf("<b>Осталось:</b> %d ч.\n", hours)
	}

	// Трафик (безлимит для оплаченных)
	if remUser.UserTraffic != nil {
		usedGB := float64(remUser.UserTraffic.UsedTrafficBytes) / (1024 * 1024 * 1024)
		msg += fmt.Sprintf("<b>Трафик за месяц:</b> %.2f GB\n", usedGB)
	}
	msg += formatDevicesLine(remUser, devicesCount)

	if dbUser != nil && dbUser.SubscriptionPrice != nil {
		msg += fmt.Sprintf("\n<b>Цена продления:</b> %d руб/мес\n", *dbUser.SubscriptionPrice)
	}

	// Ссылка подписки
	if remUser.Status == remnawave.StatusActive {
		msg += fmt.Sprintf("\n<b>Ссылка подписки:</b>\n<code>%s</code>", remUser.SubscriptionURL)
	}

	return msg
}

func formatDevicesLine(remUser *remnawave.User, devicesCount *int) string {
	if devicesCount == nil {
		return ""
	}

	if remUser.HwidDeviceLimit > 0 {
		return fmt.Sprintf("<b>Устройства:</b> %d / %d\n", *devicesCount, remUser.HwidDeviceLimit)
	}

	return fmt.Sprintf("<b>Устройства:</b> %d\n", *devicesCount)
}

// formatStatusLine возвращает эмоджи и текст для статуса
func formatStatusLine(status string) (string, string) {
	switch status {
	case remnawave.StatusActive:
		return "✅", "Активен"
	case remnawave.StatusDisabled:
		return "⛔", "Заблокирован"
	case remnawave.StatusLimited:
		return "⚠️", "Лимит исчерпан"
	case remnawave.StatusExpired:
		return "⏰", "Истёк"
	default:
		return "❌", "Неизвестно"
	}
}
