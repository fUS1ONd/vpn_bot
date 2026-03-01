# Прогресс: CI в GitHub Actions

**План:** [docs/plans/2026-03-01-ci-github-actions.md](../plans/2026-03-01-ci-github-actions.md)

**Дата выполнения:** 2026-03-01

**Статус:** ✅ Выполнено

---

## Что было сделано

### Task 1: Добавить job `ci` и зависимость в `build-and-push`

**Файл:** `.github/workflows/deploy.yml`

- ✅ Добавлен job `ci` (Lint and Test) перед `build-and-push`
- ✅ Добавлен шаг проверки форматирования (`gofmt -l .`)
- ✅ Добавлен шаг `go vet ./...`
- ✅ Добавлен шаг сборки бинаря `go build ./cmd/bot/...`
- ✅ Добавлен шаг `go test ./...`
- ✅ Добавлена зависимость `needs: ci` в job `build-and-push`

### Доработка: разделение триггеров по событиям

**Файл:** `.github/workflows/deploy.yml`

- ✅ Триггер `on.push` расширен на все ветки (без фильтра)
- ✅ Добавлен триггер `on.pull_request.branches: [main]`
- ✅ Добавлено условие `if` на `build-and-push`: только при `push` в `main`
- ✅ Добавлено условие `if` на `deploy`: только при `push` в `main`

---

## Итоговая цепочка пайплайна

| Событие | `ci` | `build-and-push` | `deploy` |
|---|---|---|---|
| push в любую ветку | ✅ | ❌ | ❌ |
| PR в main | ✅ | ❌ | ❌ |
| push в main | ✅ | ✅ | ✅ |

---

## Проверка

После мержа в main убедиться что:
- [ ] В GitHub Actions появляется job `Lint and Test` перед сборкой Docker
- [ ] При падении теста/vet/build job `build-and-push` не запускается
- [ ] CI запускается на PR в main
- [ ] CI запускается на пуш в любую ветку
- [ ] `build-and-push` и `deploy` запускаются только при пуше в main
