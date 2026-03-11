# Прогресс: регистронезависимый парсинг тега bandwidth

**План:** [docs/plans/2026-03-11-fix-bw-tag-case-insensitive.md](../plans/2026-03-11-fix-bw-tag-case-insensitive.md)

**Дата:** 2026-03-11

**Статус:** ✅ Выполнено

---

## Выполнено

### Фикс парсера

- ✅ `ParseBandwidthTag` в `internal/monitoring/sync.go` — тег приводится к uppercase через `strings.ToUpper()` перед `HasPrefix` и `Sscanf`
- ✅ Обновлён комментарий функции

### Тесты

- ✅ Создан `internal/monitoring/sync_test.go` с 11 тест-кейсами:
  - Все варианты регистра (`BW`, `bw`, `Bw`, `bW`)
  - Тег среди других тегов
  - Пустой / nil список
  - Невалидные значения (текст, ноль, отрицательное)
- ✅ `make tests` — все тесты проходят
- ✅ `make fmt` — форматирование ок

## Название коммита

`fix: исправил парсинг BW и добавил прогресс файл`
