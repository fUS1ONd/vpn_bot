package bot

import (
	"fmt"

	"github.com/fus1ond/vpn_bot/internal/remnawave"
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

<b>Лимит трафика:</b> 30 GB / месяц
<b>Сброс трафика:</b> 1-го числа каждого месяца

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

	MsgDonate = `<b>💸 Поддержать проект</b>

Если вам нравится сервис, вы можете поддержать его развитие:

%s`

	MsgInstructions = `<b>📚 Инструкции по настройке</b>

Выберите вашу платформу:`

	MsgInstructionIOS = `<b>Настройка на iOS (iPhone/iPad)</b>

1. Скачайте приложение <b>v2raytun</b> из App Store:
   https://apps.apple.com/app/v2raytun/id6476628951

2. Откройте приложение

3. Нажмите "+" в правом верхнем углу

4. Выберите "Добавить из буфера обмена"

5. Выберите сервер и включите VPN переключателем

<b>Ваша ссылка подписки:</b>
<code>%s</code>`

	MsgInstructionAndroid = `<b>Настройка на Android</b>

1. Скачайте приложение <b>v2raytun</b> из Play Market:
   https://play.google.com/store/apps/details?id=com.v2raytun.android

   Или APK: https://github.com/DigneZzZ/v2raytun/releases/

2. Откройте приложение

3. Нажмите "+" в правом верхнем углу

4. Выберите "Добавить из буфера обмена"

5. Включите VPN переключателем

<b>Ваша ссылка подписки:</b>
<code>%s</code>`

	MsgInstructionWindows = `<b>Настройка на Windows/Linux</b>

1. Скачайте <b>Nekobox</b>:
   https://github.com/MatsuriDayo/nekoray/releases/

2. Установите и откройте программу

3. Нажмите "Программа" -> "Добавить профиль из буфера обмена"

4. Выберите нужный сервер из списка, правой кнопкой мыши "Запустить"

5. Активируйте чекбоксы "Режим tun" и "Режим системного прокси"

<b>Ваша ссылка подписки:</b>
<code>%s</code>`

	MsgInstructionMac = `<b>Настройка на macOS</b>

1. Скачайте <b>Hiddify</b>:
   https://github.com/hiddify/hiddify-next/releases

2. Установите и откройте программу

3. Нажмите "+" и выберите "Добавить из буфера"

4. Вставьте вашу ссылку подписки

5. Выберите сервер и нажмите "Подключить"

<b>Ваша ссылка подписки:</b>
<code>%s</code>`
	MsgSubtitlesWait = `<b>🎤 Субтитры</b>

Отправь голосовое сообщение или видео-кружок, и я добавлю субтитры.`

	MsgSubtitlesProcessing = `⏳ Рендеринг видео...`

	MsgSubtitlesNoAvatar = `❌ Не удалось получить фото профиля.

Установите фото профиля в Telegram и попробуйте снова.`

	MsgSubtitlesError = `❌ Не удалось создать видео. Попробуйте позже.`

	MsgSubtitlesTimeout = `⏰ Рендеринг занял слишком долго. Попробуйте позже.`

	MsgSubtitlesUnavailable = `❌ Сервис временно недоступен. Попробуйте позже.`

	MsgSubtitlesWrongType = `Отправь голосовое сообщение или видео-кружок.
Или нажми 🚫 Отмена.`
)

// Сообщения админа
const (
	MsgAdminWelcome = `<b>👑 Админ-панель</b>

Выберите действие:`

	MsgInviteCreated = `<b>🎟 Инвайт создан!</b>

Код: <code>%s</code>

Отправьте этот код пользователю для регистрации.`

	MsgEnterAddTraffic = `<b>📊 Добавить трафик</b>

Введите данные через пробел:
<code>telegram_id GB</code>

Пример: <code>123456789 10</code>`

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

// FormatUserStatus форматирует статус пользователя
func FormatUserStatus(user *remnawave.User) string {
	statusEmoji := "❌"
	statusText := "Неизвестно"

	switch user.Status {
	case remnawave.StatusActive:
		statusEmoji = "✅"
		statusText = "Активен"
	case remnawave.StatusDisabled:
		statusEmoji = "⛔"
		statusText = "Заблокирован"
	case remnawave.StatusLimited:
		statusEmoji = "⚠️"
		statusText = "Лимит исчерпан"
	case remnawave.StatusExpired:
		statusEmoji = "⏰"
		statusText = "Истёк"
	}

	msg := fmt.Sprintf("<b>👤 Ваш статус</b>\n\n")
	msg += fmt.Sprintf("<b>Статус:</b> %s %s\n", statusEmoji, statusText)

	// Трафик
	if user.UserTraffic != nil {
		usedGB := float64(user.UserTraffic.UsedTrafficBytes) / (1024 * 1024 * 1024)
		limitGB := float64(user.TrafficLimitBytes) / (1024 * 1024 * 1024)
		percent := float64(user.UserTraffic.UsedTrafficBytes) / float64(user.TrafficLimitBytes) * 100

		msg += fmt.Sprintf("\n<b>Трафик:</b>\n")
		msg += fmt.Sprintf("%.2f GB / %.0f GB (%.0f%%)\n", usedGB, limitGB, percent)
	}

	msg += "\n<b>Сброс трафика:</b> 1-го числа месяца\n"

	if user.Status == remnawave.StatusActive {
		msg += fmt.Sprintf("\n<b>Ссылка подписки:</b>\n<code>%s</code>", user.SubscriptionURL)
	}

	return msg
}
