package bot

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDecideSubscriptionActions(t *testing.T) {
	now := time.Date(2026, time.March, 4, 12, 0, 0, 0, time.UTC)

	t.Run("За 3 дня до истечения", func(t *testing.T) {
		expireAt := time.Date(2026, time.March, 7, 0, 0, 0, 0, time.UTC)
		decision := decideSubscriptionActions(expireAt, now, true, false, false)
		assert.NotEmpty(t, decision.ThreeDaysMessage)
		assert.Empty(t, decision.ExpireTodayMessage)
		assert.False(t, decision.ShouldKick)
	})

	t.Run("Подписка истекла сегодня", func(t *testing.T) {
		expireAt := time.Date(2026, time.March, 4, 0, 0, 0, 0, time.UTC)
		decision := decideSubscriptionActions(expireAt, now, false, true, false)
		assert.Empty(t, decision.ThreeDaysMessage)
		assert.NotEmpty(t, decision.ExpireTodayMessage)
		assert.Contains(t, decision.ExpireTodayMessage, "куратор больше не обслуживает")
		assert.False(t, decision.ShouldKick)
	})

	t.Run("Автокик через 3 дня после истечения", func(t *testing.T) {
		expireAt := time.Date(2026, time.February, 28, 0, 0, 0, 0, time.UTC)
		decision := decideSubscriptionActions(expireAt, now, true, true, true)
		assert.True(t, decision.ShouldKick)
	})

	t.Run("Повторные уведомления не отправляются", func(t *testing.T) {
		expireAt := time.Date(2026, time.March, 7, 0, 0, 0, 0, time.UTC)
		decision := decideSubscriptionActions(expireAt, now, true, true, true)
		assert.Empty(t, decision.ThreeDaysMessage)
		assert.Empty(t, decision.ExpireTodayMessage)
		assert.False(t, decision.ShouldKick)
	})
}
