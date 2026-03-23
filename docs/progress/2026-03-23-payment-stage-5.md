# Этап 5: Event-driven scheduler

**Дата:** 2026-03-23
**План:** [2026-03-22-payment-implementation-plan.md](../plans/2026-03-22-payment-implementation-plan.md), строки 1501–1631
**Коммит:** `feat: этап 5 — event-driven scheduler`

## Что сделано

### scheduler.go — полная переработка
- **Новые константы уведомлений:** `trial_expire_1d`, `trial_expired`, `expire_3d`, `expire_1d`, `expired`, `grace_kick`
- **StartScheduler:** ticker 30 минут + первый проход при старте (вместо 24ч в 12:00 МСК)
- **runSubscriptionSchedulerPass:**
  - `ExpireOldPendingPayments()` — протухание PENDING платежей
  - `retryConfirmedNotActivated()` — retry активации после сбоя
  - Для каждого пользователя: бесконечные → пропуск, триал → кик при точном `expireAt`, оплаченная → disable + grace 72ч
- **processTrialUser:** уведомление менее чем за 24 часа, кик при точном `expireAt` (без grace period)
- **processPaidUser:** уведомления в окнах 3д/1д, disable при точном `expireAt`, кик через 72ч grace
- **isTrialUser:** invite.ExpireDays != NULL + нет confirmed платежей
- **Защита от ложного кика:** `HasConfirmedPaymentSince(expireAt)` + свежий статус через API перед grace kick
- **maintenanceMode:** блокирует кик и disable
- **sendNotification:** вспомогательная функция с идемпотентной отправкой
- **sendSchedulerMessage:** добавлен ParseMode HTML для поддержки форматирования

### payments.go
- **HasConfirmedPaymentSince:** проверка подтверждённого платежа после указанной даты

### scheduler_test.go — новые тесты
- `TestIsTrialUser` — определение типа подписки (триал/оплаченная/админская)
- `TestSchedulerTrialKick` — кик триального после expireAt
- `TestSchedulerTrialWaitsForExactExpireAt` — триал не кикается раньше точного времени
- `TestSchedulerTrialNotKickedIfPaid` — защита от кика оплатившего
- `TestSchedulerPaidDisableAndGraceKick` — disable + кик через 72ч
- `TestSchedulerPaidWaitsForExactExpireAt` — disable не происходит раньше времени
- `TestSchedulerPaidDisableIgnoresPaymentsBeforeExpireAt` — старые оплаты не блокируют expire-обработку
- `TestSchedulerMaintenanceMode` — maintenance блокирует кики
- `TestSchedulerRetryConfirmedNotActivated` — retry активации

## Удалено
- `decideSubscriptionActions` — заменена на `processTrialUser`/`processPaidUser`
- `subscriptionDecision` struct — больше не нужна
- `notificationExpireToday` — заменена на раздельные типы
