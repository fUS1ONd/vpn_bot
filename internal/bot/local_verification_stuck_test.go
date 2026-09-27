package bot

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
)

// Метка сообщения владельцу о платеже, застрявшем на сбое записи в нашу базу.
const localVerificationStuckMarker = "записать в базу не удаётся"

// Касса ответила «оплачено», ответ сошёлся, но записать его не выходит больше
// суток: платёж не закрывается (за ним деньги), а владелец узнаёт об этом —
// ровно одним сообщением, сколько бы проходов сверки ни было.
func TestСверка_СбойЗаписиПослеСрока_ОдноСообщениеВладельцуИПлатёжЖдёт(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = yooSays("succeeded", "2026-09-27T10:00:00Z")
	id := env.pending(t, paymentprovider.YooKassa, 20*time.Minute)
	failProviderPaidAtWrites(t, env.db)
	afterDeadline := env.createdAt(t, id).Add(pendingMaxAge)

	for pass := 0; pass < 3; pass++ {
		env.bot.reconcilePendingPayment(id, afterDeadline.Add(time.Duration(pass)*30*time.Minute), "test")
	}

	alerts := env.tg.matching(localVerificationStuckMarker)
	if assert.Len(t, alerts, 1) {
		assert.Contains(t, alerts[0].Text, "#")
		assert.Contains(t, alerts[0].Text, paymentprovider.YooKassa)
		assert.Contains(t, alerts[0].Text, "succeeded")
	}
	assert.Equal(t, "pending", env.status(t, id), "сверка продолжается: закрыть платёж значило бы потерять деньги")
}

// До срока сбой записи — только лог: сверка ещё успеет записать ответ, и
// тревожить владельца каждой заминкой базы незачем.
func TestСверка_СбойЗаписиДоСрока_БезСообщенияВладельцу(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = yooSays("succeeded", "2026-09-27T10:00:00Z")
	id := env.pending(t, paymentprovider.YooKassa, 20*time.Minute)
	failProviderPaidAtWrites(t, env.db)
	beforeDeadline := env.createdAt(t, id).Add(pendingMaxAge - time.Minute)

	env.bot.reconcilePendingPayment(id, beforeDeadline, "test")

	assert.Empty(t, env.tg.matching(localVerificationStuckMarker))
	assert.Equal(t, "pending", env.status(t, id))
}

// Telegram не принял сообщение — пометки нет, и следующий проход повторяет его,
// а не ждёт перезапуска бота.
func TestСверка_СбойЗаписиПослеСрока_НедоставленноеСообщениеПовторяется(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = yooSays("succeeded", "2026-09-27T10:00:00Z")
	delivered := failFirstTelegramSend(t, env.bot)
	id := env.pending(t, paymentprovider.YooKassa, 20*time.Minute)
	failProviderPaidAtWrites(t, env.db)
	afterDeadline := env.createdAt(t, id).Add(pendingMaxAge)

	env.bot.reconcilePendingPayment(id, afterDeadline, "test")
	env.bot.reconcilePendingPayment(id, afterDeadline.Add(30*time.Minute), "test")
	env.bot.reconcilePendingPayment(id, afterDeadline.Add(time.Hour), "test")

	assert.Len(t, delivered.matching(localVerificationStuckMarker), 1)
	assert.Equal(t, "pending", env.status(t, id))
}
