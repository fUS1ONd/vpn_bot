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

---

## Итоговая цепочка пайплайна

```
push в main → ci (fmt + vet + build + tests) → build-and-push → deploy
```

---

## Проверка

После мержа в main убедиться что:
- [ ] В GitHub Actions появляется job `Lint and Test` перед сборкой Docker
- [ ] При падении теста/vet/build job `build-and-push` не запускается
- [ ] При успехе всё работает как раньше
