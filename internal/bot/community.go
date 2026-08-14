package bot

import (
	"fmt"
	"log/slog"
	"time"

	tele "gopkg.in/telebot.v3"
)

// communityDeclineCooldownWindow — интервал между двумя объяснениями отказа
// одному пользователю. Telegram не запрещает подавать заявку заново, и
// настойчивый пользователь способен нажать кнопку десяток раз подряд: заявки
// обрабатываются каждый раз, а личка не должна превращаться в поток одинаковых
// отказов. Кулдаун in-memory, по образцу кулдауна багрепортов: после
// перезапуска бот в худшем случае повторит объяснение один раз.
const communityDeclineCooldownWindow = 5 * time.Minute

// communityMentionCooldown — как часто Платящий видит приписку про Канал.
// Кулдаун общий на все места показа (карточка подписки, успешная оплата,
// «Серверы»): реклама одного и того же не должна умножаться на число экранов.
const communityMentionCooldown = 7 * 24 * time.Hour

// shouldInviteToCommunity сообщает, есть ли смысл звать пользователя в Канал:
// он Платящий и ещё не помечен как участник.
func (b *Bot) shouldInviteToCommunity(telegramID int64) (bool, error) {
	member, err := b.db.IsCommunityMember(telegramID)
	if err != nil {
		return false, err
	}
	if member {
		return false, nil
	}
	return b.isPayingUser(telegramID)
}

// claimCommunityMention возвращает приписку про Канал для ответов бота — или
// пустую строку, если показывать её сейчас не нужно. Имя с claim не случайно:
// функция и решает, и забирает право на показ, записывая его в базу.
//
// Решение и фиксация вместе проверяются тестом без telebot-контекста. Приписка
// не показывается никогда, если фича выключена, пользователь не Платящий
// (дразнить недоступным нельзя — единственное исключение это «Информация») или
// он уже в Канале.
func (b *Bot) claimCommunityMention(telegramID int64) string {
	if !b.config.CommunityEnabled() {
		return ""
	}

	invite, err := b.shouldInviteToCommunity(telegramID)
	if err != nil {
		slog.Error("Failed to check community mention eligibility", "error", err, "telegram_id", telegramID)
		return ""
	}
	if !invite {
		return ""
	}

	// Мьютекс по той же причине, что и в кулдауне отказов: «прочитать кулдаун и
	// занять его» обязано быть одной операцией. Иначе оплата и открытая карточка
	// подписки, совпавшие по времени, обе увидят пустой кулдаун и покажут
	// приписку дважды подряд. Берётся после проверки права: она ходит в панель, и
	// держать на её время общий замок значило бы выстроить всех пользователей в
	// очередь за медленной Remnawave.
	b.communityMentionMu.Lock()
	defer b.communityMentionMu.Unlock()

	sentAt, err := b.db.CommunityMentionSentAt(telegramID)
	if err != nil {
		slog.Error("Failed to read community mention timestamp", "error", err, "telegram_id", telegramID)
		return ""
	}
	now := time.Now().UTC()
	if sentAt != nil && now.Sub(sentAt.UTC()) < communityMentionCooldown {
		return ""
	}

	if err := b.db.MarkCommunityMentionSent(telegramID, now); err != nil {
		// Показ не зафиксировался — приписку не показываем: без записи кулдаун
		// не работает, и пользователь увидит её в каждом следующем ответе.
		slog.Error("Failed to mark community mention", "error", err, "telegram_id", telegramID)
		return ""
	}
	return BuildCommunityMention(b.config)
}

// joinRequestOutcome — решение бота по заявке на вступление в Канал.
//
// Нулевое значение означает «решение не принято»: заявку не трогаем, и она
// остаётся в Telegram висеть. Это третий исход, а не разновидность отказа:
// отклонённая заявка исчезает необратимо, и отказать платящему из-за минутной
// недоступности панели значит выгнать его без повода и без объяснения.
type joinRequestOutcome struct {
	approve bool // одобрить заявку
	decline bool // отклонить заявку
	explain bool // отправить объяснение отказа (false — отказ молча, кулдаун)
}

// resolveJoinRequest принимает решение по заявке. Походов в Telegram Bot API
// здесь нет — их делает тонкий обработчик, поэтому решение целиком проверяется
// тестами. Пометку «в Канале» ставит обработчик, и только после успешного
// одобрения: см. markCommunityJoined.
func (b *Bot) resolveJoinRequest(telegramID int64) joinRequestOutcome {
	paying, err := b.isPayingUser(telegramID)
	// 404 панели — не сбой, а ответ по существу: пользователя в Remnawave нет,
	// значит доступа у него нет и Платящим он не является. Держать такую заявку
	// висящей нельзя: рассинхрон базы и панели сам не рассосётся, а человек
	// остался бы без решения и без объяснения навсегда.
	if err != nil && !isRemnawaveNotFound(err) {
		// Проверить право не удалось — значит решения нет. Ни одобрять, ни
		// отклонять: заявка подождёт, пока панель или база вернутся.
		slog.Error("Failed to check paying status for join request", "error", err, "telegram_id", telegramID)
		return joinRequestOutcome{}
	}

	if !paying {
		// Забаненному объяснение не шлём: «оплатите и подайте заявку снова» —
		// обещание пути внутрь тому, для кого вход закрыт навсегда.
		banned, err := b.db.IsBanned(telegramID)
		if err != nil {
			slog.Error("Failed to check ban status for join request", "error", err, "telegram_id", telegramID)
			return joinRequestOutcome{}
		}
		if banned {
			return joinRequestOutcome{decline: true}
		}
		return joinRequestOutcome{decline: true, explain: b.claimDeclineExplanation(telegramID)}
	}

	return joinRequestOutcome{approve: true}
}

// markCommunityJoined ставит пометку «в Канале» после успешного одобрения.
//
// Порядок важен: пометка гасит упоминания Канала навсегда, поэтому поставить её
// до одобрения значит при упавшем вызове Telegram оставить человека вне Канала и
// без единого напоминания о нём — расхождение, которое уже ничем не лечится.
func (b *Bot) markCommunityJoined(telegramID int64) {
	if err := b.db.MarkCommunityJoined(telegramID, time.Now().UTC()); err != nil {
		slog.Error("Failed to mark community membership", "error", err, "telegram_id", telegramID)
	}
}

// claimDeclineExplanation сообщает, пора ли объяснять отказ, и сразу забирает
// право на объяснение: два параллельных обработчика заявок не должны отправить
// два одинаковых сообщения.
func (b *Bot) claimDeclineExplanation(telegramID int64) bool {
	// Мьютекс, а не голая sync.Map: «проверить и занять» обязано быть одной
	// операцией, иначе две заявки, пришедшие одновременно, обе увидят пустой
	// кулдаун и пошлют по сообщению — ровно то, от чего кулдаун и спасает.
	b.communityDeclineMu.Lock()
	defer b.communityDeclineMu.Unlock()

	now := time.Now()
	if last, ok := b.communityDeclineCooldown.Load(telegramID); ok {
		if lastTime, ok := last.(time.Time); ok && now.Sub(lastTime) < communityDeclineCooldownWindow {
			return false
		}
	}
	b.communityDeclineCooldown.Store(telegramID, now)
	return true
}

// handleChatJoinRequest — обработчик заявки на вступление. Регистрируется
// только при включённой фиче, поэтому здесь остаётся лишь отсечь чужие чаты
// (бота могли добавить и в другую группу) и выполнить решение.
func (b *Bot) handleChatJoinRequest(c tele.Context) error {
	request := c.ChatJoinRequest()
	if request == nil || request.Chat == nil || request.Sender == nil {
		return nil
	}
	if request.Chat.ID != b.config.CommunityChatID {
		return nil
	}

	telegramID := request.Sender.ID
	outcome := b.resolveJoinRequest(telegramID)
	if outcome.approve || outcome.decline {
		// Решение принято — следующая зависшая заявка этого пользователя снова
		// достойна сообщения владельцу.
		b.communityPendingAlerted.Delete(telegramID)
	}

	if outcome.approve {
		if err := c.Bot().ApproveJoinRequest(request.Chat, request.Sender); err != nil {
			slog.Error("Failed to approve community join request", "error", err, "telegram_id", telegramID)
			return nil
		}
		slog.Info("Community join request approved", "telegram_id", telegramID)
		b.markCommunityJoined(telegramID)
		return nil
	}

	if !outcome.decline {
		// Решения нет — заявка остаётся висеть в Telegram и переживёт сбой.
		slog.Warn("Community join request left pending", "telegram_id", telegramID)
		b.alertPendingJoinRequest(telegramID)
		return nil
	}

	if err := c.Bot().DeclineJoinRequest(request.Chat, request.Sender); err != nil {
		slog.Error("Failed to decline community join request", "error", err, "telegram_id", telegramID)
	}
	if outcome.explain {
		if _, err := c.Bot().Send(request.Sender, MsgCommunityDeclined, &tele.SendOptions{ParseMode: tele.ModeHTML}); err != nil {
			// Личка закрыта — обычное дело для пользователя, не начинавшего диалог.
			slog.Info("Failed to explain community decline", "error", err, "telegram_id", telegramID)
		}
	}
	return nil
}

// alertPendingJoinRequest сообщает владельцу о заявке, оставшейся без решения.
//
// Заявка висит в Telegram, пока её не разберут: подать её заново пользователь не
// может (Telegram держит одну заявку на чат), а бот к ней больше не вернётся —
// апдейт приходит один раз. Без сообщения владельцу человек застревает молча.
// Алерт шлётся один раз на пользователя: при недоступной панели заявок может
// прийти много, и поток одинаковых сообщений владельцу не поможет.
func (b *Bot) alertPendingJoinRequest(telegramID int64) {
	if _, alreadyAlerted := b.communityPendingAlerted.LoadOrStore(telegramID, struct{}{}); alreadyAlerted {
		return
	}

	b.sendAdminAlert(fmt.Sprintf(
		"⚠️ Заявка на вступление в сообщество от <code>%d</code> осталась без решения: проверить право не удалось (панель или база недоступны).\n\nЗаявка висит в Telegram — одобрите или отклоните её вручную.",
		telegramID,
	))
}

// kickFromCommunity выгоняет забаненного пользователя из Канала.
//
// Ban+Unban, а не просто Ban: чёрный список чата не нужен — вход и так закрыт
// гейтом, а «разбана» в системе не существует, и оставленная запись в чёрном
// списке только мешала бы владельцу решать нестандартные случаи руками.
// Недоступность Telegram API кик не отменяет и бан не срывает: сам бан уже
// зафиксирован в базе, VPN-доступ отобран.
func (b *Bot) kickFromCommunity(telegramID int64) {
	if !b.config.CommunityEnabled() || b.bot == nil {
		return
	}

	chat := &tele.Chat{ID: b.config.CommunityChatID}
	user := &tele.User{ID: telegramID}
	member := &tele.ChatMember{User: user}

	if err := b.bot.Ban(chat, member); err != nil {
		slog.Error("Failed to kick banned user from community", "error", err, "telegram_id", telegramID)
		// Бан «сработал», а человек продолжает сидеть в сообществе — узнать об
		// этом случайно нельзя, поэтому говорим владельцу прямо.
		b.sendAdminAlert(fmt.Sprintf(
			"⚠️ Пользователь <code>%d</code> забанен, но выгнать его из сообщества не удалось: %v\n\nВыгоните его вручную.",
			telegramID, err,
		))
		return
	}
	if err := b.bot.Unban(chat, user); err != nil {
		// Кик прошёл, снять чёрный список не вышло. Человек уже не в Канале,
		// поэтому это мелочь: запись в чёрном списке мешает только ручному
		// возврату, которого при перманентном бане не бывает.
		slog.Error("Failed to lift community ban after kick", "error", err, "telegram_id", telegramID)
		return
	}
	slog.Info("Banned user kicked from community", "telegram_id", telegramID)
}
