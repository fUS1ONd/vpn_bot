package bot

import (
	"fmt"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
)

// Message templates
const (
	MsgTorrentWarning = `⚠️ <b>ВНИМАНИЕ</b>

Пожалуйста, если вы скачиваете торренты, <b>ОБЯЗАТЕЛЬНО отключайте раздачу</b>.

За раздачу торрентов я буду вынужден ограничить доступ.`

	MsgRefundPolicy = `<b>Политика возврата средств</b>

Настоящим уведомляется, что в соответствии с условиями пользовательского соглашения, <b>услуга вывода информации через защищённый канал связи (VPN) является цифровым контентом, предоставляемым в электронном виде, и подлежит немедленному использованию с момента активации</b>.

В соответствии с Законом Российской Федерации от 7 февраля 1992 года № 2300-1 «О защите прав потребителей», <b>право на возврат денежных средств за цифровой контент исключено</b>, так как предоставляемый сервис относится к услугам, начинающим действие с момента подтверждения заказа и не подлежащим возврату.

Возврат средств <b>не осуществляется</b> в следующих случаях:
• После активации пробного периода (триала)
• После активации платной подписки
• За использованный трафик
• При отсутствии документов, подтверждающих факт оплаты

Все платежи являются окончательными и возврату не подлежат.`

	MsgWelcome = `<b>VPN-бот</b>

Быстрый и безопасный VPN для обхода блокировок.

<b>Тарифы:</b>
- Подписка: 200 руб/мес
- Доп. трафик RU: 100 руб за 10GB

<b>Триал:</b> 3 дня + 1GB бесплатно

Выберите действие:`

	MsgWelcomeBack = `<b>С возвращением!</b>

Выберите действие:`

	MsgTrialOffer = `<b>Бесплатный триал</b>

Попробуйте VPN бесплатно:
- 3 дня доступа
- 1GB трафика на RU сервере
- Безлимит на EU сервере

Нажмите кнопку ниже для активации:`

	MsgTrialActivated = `<b>Триал активирован!</b>

Ваша подписка активна до: <b>%s</b>

<b>Ссылка для подключения:</b>
<code>%s</code>

Скопируйте ссылку и вставьте в приложение.
Нажмите "Инструкции" для настройки.`

	MsgTrialUsed = `<b>Триал уже использован</b>

Вы уже использовали бесплатный триал.
Для продолжения работы оплатите подписку.

<b>Стоимость:</b> 200 руб/мес`

	MsgPaymentRequired = `<b>Требуется оплата</b>

Для подключения к VPN необходимо оплатить подписку.

<b>Стоимость:</b> 200 руб/мес

Нажмите кнопку ниже для оплаты:`

	MsgSubscriptionExpired = `<b>Подписка истекла</b>

Ваша подписка закончилась %s.
Для продолжения работы оплатите подписку.

<b>Стоимость:</b> 200 руб/мес`

	MsgEnterPromo = `<b>Введите промокод</b>

Отправьте промокод в ответном сообщении:`

	MsgPromoSuccess = `<b>Промокод применен!</b>

%s`

	MsgPromoError = `<b>Ошибка</b>

%s`

	MsgSupport = `<b>Поддержка</b>

Если у вас возникли проблемы с подключением или оплатой, напишите нам:

@fUS1ONd

Мы ответим в течение 24 часов.`

	MsgSeller = `<b>Реквизиты продавца</b>

<b>Полное наименование:</b> Кривоносов Константин Юрьевич

<b>ИНН:</b> 490101303391

<b>ОГРН/ОГРНИП:</b> Не применимо

<b>Контактный телефон:</b> +7 918 019-36-60

<b>Контактный e-mail:</b> koskriv2006@gmail.com

Электронная почта и телефон также доступны на сайте.`

	MsgBuyTraffic = `<b>Докупить трафик</b>

На RU сервере действует лимит 30GB/мес.
Вы можете докупить дополнительный трафик.

<b>Стоимость:</b> 100 руб за 10GB

<i>На EU сервере трафик безлимитный.</i>`

	MsgInstructions = `<b>Инструкции по настройке</b>

Выберите вашу платформу:`

	MsgInstructionIOS = `<b>Настройка на iOS (iPhone/iPad)</b>

1. Скачайте приложение <b>Streisand</b> из App Store:
   https://apps.apple.com/app/streisand/id6450534064

2. Откройте приложение

3. Нажмите "+" в правом верхнем углу

4. Выберите "Добавить из буфера обмена"

5. Вставьте вашу ссылку подписки

6. Включите VPN переключателем

<b>Ваша ссылка подписки:</b>
<code>%s</code>`

	MsgInstructionAndroid = `<b>Настройка на Android</b>

1. Скачайте приложение <b>v2rayNG</b>:
   https://play.google.com/store/apps/details?id=com.v2ray.ang

   Или APK: https://github.com/2dust/v2rayNG/releases

2. Откройте приложение

3. Нажмите "+" в правом верхнем углу

4. Выберите "Импорт из буфера обмена"

5. Вставьте вашу ссылку подписки

6. Выберите сервер и нажмите кнопку подключения

<b>Ваша ссылка подписки:</b>
<code>%s</code>`

	MsgInstructionWindows = `<b>Настройка на Windows</b>

1. Скачайте <b>Hiddify</b>:
   https://github.com/hiddify/hiddify-next/releases

2. Установите и откройте программу

3. Нажмите "+" и выберите "Добавить из буфера"

4. Вставьте вашу ссылку подписки

5. Выберите сервер и нажмите "Подключить"

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

	MsgNoSubscription = `<b>Нет активной подписки</b>

У вас пока нет активной подписки.
Нажмите "Подключить VPN" для начала.`

	MsgPaymentLink = `<b>Оплата подписки</b>

Для оплаты перейдите по ссылке:
%s

После оплаты подписка активируется автоматически.`

	MsgPaymentSuccess = `<b>Оплата успешна!</b>

Ваша подписка активирована до: <b>%s</b>

<b>Ссылка для подключения:</b>
<code>%s</code>`

	MsgTrafficAdded = `<b>Трафик добавлен!</b>

На ваш аккаунт добавлено 10GB трафика для RU сервера.`
)

// FormatStatus formats user status message
func FormatStatus(user *database.User, subLink string, trafficUsedRU, trafficLimitRU int64) string {
	statusEmoji := ""
	statusText := ""

	switch user.SubscriptionStatus {
	case database.StatusActive:
		statusEmoji = "OK"
		statusText = "Активна"
	case database.StatusTrial:
		statusEmoji = "OK"
		statusText = "Триал"
	case database.StatusExpired:
		statusEmoji = "X"
		statusText = "Истекла"
	default:
		statusEmoji = "-"
		statusText = "Нет подписки"
	}

	msg := fmt.Sprintf("<b>Ваш статус</b>\n\n")
	msg += fmt.Sprintf("<b>Статус:</b> %s %s\n", statusEmoji, statusText)

	if user.SubscriptionEndAt != nil {
		daysLeft := FormatDaysLeft(user.SubscriptionEndAt)
		msg += fmt.Sprintf("<b>Действует до:</b> %s (%s)\n", user.SubscriptionEndAt.Format("02.01.2006 15:04"), daysLeft)
	}

	msg += "\n<b>Трафик RU сервер:</b>\n"
	usedGB := float64(trafficUsedRU) / (1024 * 1024 * 1024)
	limitGB := float64(trafficLimitRU) / (1024 * 1024 * 1024)

	if trafficLimitRU > 0 {
		percent := float64(trafficUsedRU) / float64(trafficLimitRU) * 100
		msg += fmt.Sprintf("%.2f GB / %.0f GB (%.0f%%)\n", usedGB, limitGB, percent)
	} else {
		msg += fmt.Sprintf("%.2f GB использовано\n", usedGB)
	}

	msg += "\n<b>EU сервер:</b> Безлимит\n"

	if user.SubscriptionStatus == database.StatusActive || user.SubscriptionStatus == database.StatusTrial {
		msg += fmt.Sprintf("\n<b>Ссылка подписки:</b>\n<code>%s</code>", subLink)
	}

	return msg
}

// FormatDaysLeft formats days remaining message
func FormatDaysLeft(endAt *time.Time) string {
	if endAt == nil {
		return ""
	}

	days := int(time.Until(*endAt).Hours() / 24)
	if days < 0 {
		return "Подписка истекла"
	}
	if days == 0 {
		return "Истекает сегодня"
	}
	if days == 1 {
		return "Остался 1 день"
	}
	if days < 5 {
		return fmt.Sprintf("Осталось %d дня", days)
	}
	return fmt.Sprintf("Осталось %d дней", days)
}

// FormatPromoResult formats the result of promo code application
func FormatPromoResult(promo *database.PromoCode) string {
	switch promo.Type {
	case database.PromoTypeDiscount:
		return fmt.Sprintf("Скидка %d%% на следующую оплату!", promo.Value)
	case database.PromoTypeFreeDays:
		return fmt.Sprintf("Добавлено %d дней к подписке!", promo.Value)
	case database.PromoTypeExtraTraffic:
		gb := float64(promo.Value) / (1024 * 1024 * 1024)
		return fmt.Sprintf("Добавлено %.0f GB трафика на RU сервер!", gb)
	default:
		return "Промокод применен!"
	}
}

// Admin messages
const (
	MsgAdminWelcome = `<b>Админ-панель</b>

Выберите действие:`

	MsgAdminClientCreated = `<b>Клиент создан!</b>

<b>Email:</b> %s
<b>UUID:</b> <code>%s</code>

<b>Ссылка подписки:</b>
<code>%s</code>`

	MsgAdminClientList = `<b>Список клиентов</b>

Всего: %d

%s`

	MsgAdminEnterClientName = `<b>Создание клиента</b>

Отправьте имя (email) для нового клиента:`

	MsgAdminEnterPromoData = `<b>Создание промокода</b>

Введите данные через пробел:
<code>Code Type Value MaxUses [Days]</code>

Типы: <i>discount, free_days, extra_traffic</i>
Пример: <code>FREE3DAYS free_days 3 100 30</code>`

	MsgAdminEnterPromoCode = `<b>Удаление промокода</b>

Отправьте код для удаления:`

	MsgAdminPromoList = `<b>Управление промокодами</b>

Активные промокоды:
%s`
)
