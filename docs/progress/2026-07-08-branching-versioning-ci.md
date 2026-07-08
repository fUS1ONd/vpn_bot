# Прогресс: CI, ветвление, версионирование и релизы

Дата: 2026-07-08
Ветка: `chore/dev-guidelines-adoption`
План: [2026-07-08-branching-versioning-ci-design.md](../plans/2026-07-08-branching-versioning-ci-design.md)

## Статус: реализовано (в рамках текущей ветки)

Дизайн проработан по skill `grill-me` (18 узлов дерева решений), затем реализован.
`make fmt` и `make tests` зелёные. Логика `release.sh` проверена на синтетических
git-репозиториях (парсинг conventional commits, все bump-кейсы, фаза release с
merge/tag/push, guard-проверки).

## Что сделано

| # | Изменение | Файл | Статус |
|---|-----------|------|--------|
| 1 | Новый CI: 3 триггера (PR / push dev / теги), GHCR, версионирование, Release, concurrency, кеш `type=gha` | `.github/workflows/ci.yml` | ✅ |
| 2 | Удалён старый workflow Docker Hub | `.github/workflows/deploy.yml` | ✅ (удалён) |
| 3 | Образ переключён на GHCR с комментарием про каналы `latest`/`dev` | `docker-compose.yml` | ✅ |
| 4 | Детерминированный релиз-скрипт (двухфазный: `--dry-run` / `--version --expect-sha`) | `scripts/release.sh` | ✅ |
| 5 | Skill-обёртка `/release` (тонкий оркестратор двух фаз) | `.claude/skills/release/SKILL.md` | ✅ |
| 6 | Раздел коммитов расширен маппингом type→bump + breaking; правило синхронизации с AGENTS.md | `CLAUDE.md` | ✅ |
| 7 | `AGENTS.md` синхронизирован 1-в-1 с `CLAUDE.md` (был устаревшим дублем) | `AGENTS.md` | ✅ |

## Ключевые решения (отличия/уточнения к плану)

- **Путь образа** выводится из `github.repository` через `metadata-action`
  (авто-лоуэркейз owner), а не хардкодится. Имя пакета — `vpn_bot` (под имя репо,
  не `vpn-bot` из черновика плана). В `docker-compose.yml` — `ghcr.io/fus1ond/vpn_bot`.
- **Триггеры ровно три:** голые feature-ветки без PR не проверяются (осознанная
  экономия, дисциплина держится правилом «крупное — через PR»).
- **concurrency** отменяет устаревшие прогоны, но НИКОГДА не отменяет релизный
  билд по тегу (`cancel-in-progress: !startsWith(ref,'refs/tags/')`).
- **Кеш** `type=gha, mode=max` (не registry-buildcache — не мусорит пакет).
- **Release-подтверждение** — репликой в чат, не интерактивным `read`: skill
  зовёт `--dry-run`, показывает версию, ждёт «да», зовёт `--version --expect-sha`.
- **`--expect-sha`** защищает от рассинхрона: если `dev` уехал после dry-run,
  фаза release отказывается тегать не то состояние.
- **0.x оговорка**: breaking-коммиты двигают minor, пока версия ниже `1.0.0`.
- **Найден и исправлен баг** парсинга: bash вырезает NUL-байты в `$(...)`,
  из-за чего разделитель коммитов терялся и bump всегда был patch. Исправлено —
  `git log` подаётся в `while` напрямую через process substitution.

## Отложено (не в этой задаче, по согласованию)

- **Удаление секретов `DOCKERHUB_USERNAME`/`DOCKERHUB_TOKEN`** из настроек репо —
  гейт: только после того, как первый GHCR-образ соберётся и прод поднимется с
  него. Код на Docker Hub уже не ссылается; секреты просто станут неиспользуемыми.
- **Создание ветки `dev`** от `origin/main` + влить эту работу + push (первый
  `:dev`-образ) — выполняется после мержа этой ветки.
- **Чистка ~30 старых feature-веток** (шаг 6 плана, опционально) — отдельная сессия.

## Проверки

- `make fmt` — чисто (go vet + gofmt).
- `make tests` — все пакеты `ok`.
- `ci.yml` — валиден (yaml.safe_load).
- `scripts/release.sh` — `bash -n` чисто; логика прогнана на синтетических репо:
  `init+fix→v0.0.1`, `+feat→v0.1.0`, `breaking(0.x)→minor`, `после тега chore→patch`,
  `breaking(1.x)→v2.0.0`; фаза release создаёт merge-коммит с 2 родителями, тег на
  нём, возврат на `dev`; guard'ы (дубль-тег, не-`dev`, неверный `--expect-sha`,
  грязное дерево) срабатывают.
