package bot

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
		assert.Contains(t, alerts[0].Text, fmt.Sprintf("Платёж #%d ", id))
		assert.Contains(t, alerts[0].Text, paymentprovider.YooKassa)
		assert.Contains(t, alerts[0].Text, "succeeded")
	}
	assert.Equal(t, "pending", env.status(t, id), "сверка продолжается: закрыть платёж значило бы потерять деньги")
}

// Локально закрытый платёж сверяется только сутки от создания, и дольше суток
// «ждать» ему некуда: на последнем проходе окна он выпадает из сверки навсегда.
// Если касса говорит «оплачено», а записать не выходит, владелец должен узнать
// об этом на последнем проходе, а не никогда.
func TestСверка_СбойЗаписиЗакрытогоПлатежаВКонцеОкна_ОдноСообщениеВладельцу(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = yooSays("succeeded", "2026-09-27T10:00:00Z")
	id := env.pending(t, paymentprovider.YooKassa, 20*time.Minute)
	require.NoError(t, env.db.ExpirePendingPayment(id))
	failProviderPaidAtWrites(t, env.db)
	lastPass := env.createdAt(t, id).Add(pendingMaxAge - schedulerInterval/2)

	env.bot.reconcilePendingPayment(id, env.createdAt(t, id).Add(time.Hour), "test")
	assert.Empty(t, env.tg.matching(localVerificationStuckMarker), "до конца окна — только лог")

	env.bot.reconcilePendingPayment(id, lastPass, "test")
	assert.Len(t, env.tg.matching(localVerificationStuckMarker), 1)
	assert.Equal(t, "expired", env.status(t, id))
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
	require.Empty(t, delivered.matching(localVerificationStuckMarker), "первая отправка отвергнута Telegram")

	env.bot.reconcilePendingPayment(id, afterDeadline.Add(30*time.Minute), "test")
	assert.Len(t, delivered.matching(localVerificationStuckMarker), 1, "следующий проход повторяет недоставленное")

	env.bot.reconcilePendingPayment(id, afterDeadline.Add(time.Hour), "test")
	assert.Len(t, delivered.matching(localVerificationStuckMarker), 1, "доставленное не повторяется")
	assert.Equal(t, "pending", env.status(t, id))
}
