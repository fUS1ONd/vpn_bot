package bot

import (
	"fmt"
	"html"
	"time"

	"github.com/fus1ond/vpn_bot/internal/config"
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

Добро пожаловать! Ваш VPN-доступ активирован.`

	// MsgConnectHint отправляется отдельным сообщением с inline-кнопкой перехода
	// на страницу подписки: Telegram не допускает reply- и inline-разметку в одном
	// сообщении, а приветствие несёт reply-клавиатуру главного меню.
	MsgConnectHint = `<b>🔗 Подключение</b>

Нажмите кнопку ниже — на странице подписки будут приложения для вашей платформы и подключение в один тап.

<b>Ссылка для ручного подключения</b>
(нажмите, чтобы скопировать):
<code>%s</code>`

	MsgNotRegistered = `<b>❌ Вы не зарегистрированы</b>

Для доступа к VPN нужен инвайт-код.
Отправьте /start для регистрации.`

	// MsgRevokeConfirm — экран подтверждения перевыпуска ссылки подписки.
	MsgRevokeConfirm = `<b>🔄 Перевыпуск ссылки</b>

Будет создана новая ссылка подписки, а старая перестанет работать <b>немедленно</b>.

Все подключённые устройства отвалятся, и их придётся настроить заново по новой ссылке.

Перевыпустить?`

	// MsgRevokeDone предваряет обновлённую карточку после успешного перевыпуска.
	MsgRevokeDone = `<b>✅ Ссылка перевыпущена</b>

Старая ссылка больше не работает, устройства сброшены. Подключите их заново.

⚠️ Кнопки в более старых сообщениях «Моя подписка» теперь ведут на нерабочую ссылку — открывайте раздел заново.

`

	MsgSubtitlesProcessing = `⏳ Рендеринг видео...`

	MsgSubtitlesNoAvatar = `⚠️ Не удалось получить фото профиля — возможно, закрыты настройки приватности.

Будет использовано стандартное изображение. Чтобы использовать своё фото, откройте:
Настройки → Конфиденциальность → Фото профиля → Кто видит → Все`

	MsgSubtitlesError = `❌ Не удалось создать видео. Попробуйте позже.`

	MsgSubtitlesTimeout = `⏰ Рендеринг занял слишком долго. Попробуйте позже.`

	MsgSubtitlesUnavailable = `❌ Сервис временно недоступен. Попробуйте позже.`

	// MsgCommunityDeclined уходит в личку тому, чью заявку в Канал бот отклонил.
	// Отказ не приговор: путь внутрь назван прямо, повторная заявка по той же
	// ссылке проходит сразу после оплаты.
	MsgCommunityDeclined = `<b>💬 Сообщество недоступно</b>

Заявка отклонена: в сообщество попадают пользователи с действующей оплаченной подпиской.

Оплатите подписку в боте и подайте заявку по той же ссылке снова — бот одобрит её автоматически.`

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

// Тексты про Канал — самостоятельные блоки без ведущих переводов строки:
// отступ добавляет тот, кто приписывает блок к своему сообщению, иначе формат
// приписки оказался бы размазан по местам показа.
const (
	// communityInfoBlock показывается в «Информации» всем и всегда при
	// включённой фиче: знать о существовании сообщества должен любой, условие
	// входа названо честно.
	communityInfoBlock = "💬 Сообщество: <a href=\"%s\">релизы, баги и идеи</a>\nВступление — по заявке, доступно с оплаченной подпиской."

	// communityMentionBlock — приписка внизу ответов бота для Платящих.
	communityMentionBlock = "💬 <a href=\"%s\">Сообщество</a>: релизы, баги и идеи. Заходите — заявка одобряется автоматически."
)

// buildCommunityText подставляет инвайт-ссылку в шаблон или возвращает пустую
// строку при выключенной фиче — единая форма для всех текстов про Канал.
func buildCommunityText(cfg *config.Config, template string) string {
	if !cfg.CommunityEnabled() {
		return ""
	}
	return fmt.Sprintf(template, html.EscapeString(cfg.CommunityInviteLink))
}

// BuildCommunityInfoBlock возвращает блок про Канал для «Информации».
func BuildCommunityInfoBlock(cfg *config.Config) string {
	return buildCommunityText(cfg, communityInfoBlock)
}

// BuildCommunityMention возвращает приписку про Канал для ответов бота.
func BuildCommunityMention(cfg *config.Config) string {
	return buildCommunityText(cfg, communityMentionBlock)
}

// BuildInfoMessage собирает HTML-текст для кнопки «Информация».
// URL политики и оферты экранируются (httpa-значения берутся из env и
// не должны ломать HTML-структуру), контакт поддержки вставляется как
// есть — это позволяет админу положить туда @username, t.me/..., email
// или уже готовый тег <a href="...">. Ответственность за корректность
// значения SUPPORT_CONTACT лежит на админе.
func BuildInfoMessage(cfg *config.Config) string {
	msg := fmt.Sprintf(`<b>💡 Помощь и контакты</b>

Если есть вопросы — пишите %s

🔒 Политика конфиденциальности: <a href="%s">читать</a>
📜 Пользовательское соглашение: <a href="%s">читать</a>`,
		cfg.SupportContact,
		html.EscapeString(cfg.PrivacyPolicyURL),
		html.EscapeString(cfg.TermsOfServiceURL),
	)
	if block := BuildCommunityInfoBlock(cfg); block != "" {
		msg += "\n\n" + block
	}
	return msg
}

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

// formatSubscriptionLink возвращает блок со ссылкой подписки для ручного
// подключения. Ссылка моноширинная и не кликабельная: тап по ней копирует текст.
func formatSubscriptionLink(subURL string) string {
	return fmt.Sprintf("\n<b>Ссылка для ручного подключения</b>\n(нажмите, чтобы скопировать):\n<code>%s</code>", html.EscapeString(subURL))
}

// SubscriptionLinkVisible сообщает, показывается ли пользователю ссылка подписки.
// Кнопки перехода на страницу подписки и перевыпуска следуют за ссылкой: нет
// ссылки — нет и кнопок (grace-период, исчерпанный трафик триала).
func SubscriptionLinkVisible(remUser *remnawave.User, isTrial bool) bool {
	switch determineSubscriptionType(remUser, isTrial) {
	case subTypeInfinite:
		return true
	case subTypeGrace:
		return false
	default:
		return remUser.Status == remnawave.StatusActive
	}
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

	msg += formatSubscriptionLink(remUser.SubscriptionURL)
	return msg
}

func formatGraceStatus(remUser *remnawave.User, dbUser *database.User) string {
	graceDeadline := remUser.ExpireAt.Add(72 * time.Hour)
	remaining := graceDeadline.Sub(time.Now().UTC())

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
	msg += fmt.Sprintf("\nЕсли не оплатить до %s, аккаунт будет удалён.", graceDeadline.Format("02.01.2006"))
	return msg
}

func formatTrialStatus(remUser *remnawave.User, dbUser *database.User, devicesCount *int) string {
	msg := "<b>👤 Ваш статус</b>\n\n"
	msg += "<b>Тип:</b> ⏳ Пробный период\n"

	// Статус
	statusEmoji, statusText := formatStatusLine(remUser.Status)
	msg += fmt.Sprintf("<b>Статус:</b> %s %s\n", statusEmoji, statusText)

	// Осталось дней
	now := time.Now().UTC()
	remaining := remUser.ExpireAt.Sub(now)
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
		msg += formatSubscriptionLink(remUser.SubscriptionURL)
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
	now := time.Now().UTC()
	remaining := remUser.ExpireAt.Sub(now)
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
		msg += formatSubscriptionLink(remUser.SubscriptionURL)
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
