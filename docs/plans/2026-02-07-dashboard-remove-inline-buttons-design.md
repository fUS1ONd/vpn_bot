# Дашборд: убрать inline-кнопки

**Дата:** 2026-02-07

## Контекст

Inline-кнопки дашборда (Обновить, Стоп, Запустить) работают нестабильно. Решение — убрать их полностью, оставив простой live-дашборд с автоматическим TTL.

## Поведение

1. Пользователь нажимает "Серверы" в reply-меню
2. Запускается live-дашборд: обновление каждые 5 секунд, TTL 60 секунд
3. По истечении TTL обновления прекращаются, последний снапшот остаётся с пометкой времени
4. Для повторного запуска — снова "Серверы" из меню

## Что убираем

- Глобальные переменные `btnDashRefresh`, `btnDashStop`, `btnDashStart`
- Функцию `registerDashboardHandlers()` и её вызов в `handlers.go`
- Callback-обработчики: `handleDashCallbackRefresh`, `handleDashCallbackStop`, `handleDashCallbackStart`
- Функцию `sendDashboardStopped`
- Inline markup из `updateDashboardMessage`

## Что меняем

- `runDashboardLoop`: при истечении TTL — последнее обновление с пометкой времени вместо вызова `sendDashboardStopped`
- `updateDashboardMessage`: убираем создание inline markup, Edit только с текстом
- Рендеринг: при TTL=0 добавляем строку "Обновлено в HH:MM" вместо таймера live-сессии

## Затрагиваемые файлы

- `internal/bot/dashboard.go`
- `internal/bot/dashboard_render.go`
- `internal/bot/handlers.go`
