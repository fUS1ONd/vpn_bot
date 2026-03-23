# Защита confirmed_not_activated от scheduler

**Дата:** 2026-03-23
**План:** [2026-03-23-confirmed-not-activated-scheduler-guard-plan.md](../plans/2026-03-23-confirmed-not-activated-scheduler-guard-plan.md)
**Коммит:** `fix: защитить confirmed_not_activated от scheduler`

## Что сделано

### `internal/database/payments.go`
- `HasConfirmedPayment` теперь считает защитной оплатой оба статуса:
  - `confirmed`
  - `confirmed_not_activated`
- `HasConfirmedPaymentSince` расширен аналогично
- Добавлены поясняющие комментарии, почему это касается только защитных проверок scheduler, а не финансовой статистики

### `internal/database/payments_test.go`
- Добавлен регрессионный тест `TestHasConfirmedPaymentTreatsConfirmedNotActivatedAsPaid`
- Добавлен регрессионный тест `TestHasConfirmedPaymentSinceTreatsConfirmedNotActivatedAsPaid`
- RED зафиксирован: до правки оба теста падали, потому что helper-методы искали только `status='confirmed'`

### `internal/bot/scheduler_test.go`
- Добавлен регрессионный тест `TestSchedulerTrialNotKickedIfPaymentConfirmedNotActivated`
- Добавлен регрессионный тест `TestSchedulerPaidDisableSkippedIfPaymentConfirmedNotActivated`
- Добавлен регрессионный тест `TestSchedulerGraceKickSkippedIfPaymentConfirmedNotActivated`
- Добавлен интеграционный тест `TestSchedulerPassDoesNotPunishConfirmedNotActivatedWhenRetryStillFails`

### `internal/bot/scheduler.go`
- Уточнены комментарии в `processTrialUser`, `processPaidUser` и `isTrialUser`
- В коде явно зафиксировано, что `confirmed_not_activated` не означает успешную активацию в панели, но уже запрещает считать пользователя неоплатившим

## Что не менялось

- Финансовые агрегаты в `payments.go`:
  - `CountConfirmedPaymentsByMonth`
  - `SumConfirmedPaymentsByMonth`
  - `CountFirstPaymentsByMonth`
  - `CountPayingSubscribersByModerator`
- Механика retry в `payment.go`
- `README.md` не обновлялся: изменение внутреннее, без новой пользовательской или операционной настройки

## Проверка

### TDD / таргетные проверки
- `GOCACHE=/tmp/go-build go test ./internal/database/ -run 'TestHasConfirmedPaymentTreatsConfirmedNotActivatedAsPaid|TestHasConfirmedPaymentSinceTreatsConfirmedNotActivatedAsPaid' -v`
  - сначала `FAIL`
  - после правки `PASS`
- `GOCACHE=/tmp/go-build go test ./internal/bot/ -run 'TestSchedulerTrialNotKickedIfPaymentConfirmedNotActivated|TestSchedulerPaidDisableSkippedIfPaymentConfirmedNotActivated|TestSchedulerGraceKickSkippedIfPaymentConfirmedNotActivated|TestSchedulerPassDoesNotPunishConfirmedNotActivatedWhenRetryStillFails' -v`
  - `PASS`

### Обязательная полная проверка
- `make fmt`
  - `PASS`
- `make tests`
  - `PASS`
