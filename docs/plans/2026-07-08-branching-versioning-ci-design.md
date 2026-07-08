# CI, ветвление, версионирование и релизы — дизайн

Дата: 2026-07-08
Статус: согласован, готов к реализации

## Цель

Ввести предсказуемую модель ветвления (`main`/`dev`), автоматическое
версионирование по conventional commits, публикацию образов в GHCR с каналами
`latest`/`dev` и формализованный процесс релиза через skill-обёртку.

## Сравнение с текущей системой

| Аспект | Сейчас | Становится |
|---|---|---|
| Ветки | `main` + ~30 разрозненных feature-веток | `main` (релизы) + `dev` (интеграция) |
| Версионирование | нет (`latest` + `<branch>-<sha>`) | SemVer `x.y.z`, авто по conventional commits |
| Каналы образов | один `latest` | `latest` (релиз) + `dev` (разработка) |
| Registry | Docker Hub | только GHCR (`GITHUB_TOKEN`, без внешних секретов) |
| Тег `latest` | двигается на каждый push в main | двигается только при релизе (пуш тега) |
| Релизы | нет | GitHub Release со списком коммитов |
| Деплой | ручной по SSH | без изменений (ручной по SSH) |

## Модель веток

- `dev` — активная разработка. Feature-ветки вливаются гибко: крупное через PR
  (ревью/@claude), мелкие правки — напрямую. Создаётся от текущего `main`.
- `main` — только релизы. Обновляется исключительно через `/release`.

## CI (`.github/workflows/ci.yml`)

Три триггера:

| Триггер | Шаги | Образ |
|---|---|---|
| `pull_request` (dev/main) | fmt + vet + build + test | не пушит |
| `push: branches [dev]` | проверки → build | `ghcr.io/fus1ond/vpn-bot:dev` (перезапись) |
| `push: tags [v*.*.*]` | проверки → build → GitHub Release | `:X.Y.Z`, `:X.Y`, `:X`, `:latest` |

- Registry — только GHCR, auth через встроенный `GITHUB_TOKEN` (`packages: write`).
- PR никогда не собирает и не пушит образ — только проверки.
- Тег `:latest` двигает только тег-релиз; push в `main` сам по себе образ не собирает.
- Changelog в Release — автосписок коммитов (`gh release create --generate-notes`).
- Существующий `claude.yml` (@claude-ревью на PR) сохраняется без изменений.

## Версионирование — conventional commits

Маппинг на bump:

- `feat:` → **minor**
- `fix:` → **patch**
- `feat!:` / `fix!:` / тело с `BREAKING CHANGE:` → **major**
- `chore:` / `docs:` / `refactor:` / `plan:` в одиночку → **patch** (чтобы релиз получил номер)
- Приоритет: breaking → major, иначе feat → minor, иначе patch.

Оговорка для зоны `0.x`: пока версия ниже `1.0.0`, breaking-коммиты двигают
**minor**, а не major (стандартное поведение semantic-release для нестабильных версий).

Стартовая база — `0.0.0` (тегов ещё нет). Первый `/release` даст `0.1.0` при
наличии `feat` или `0.0.1` иначе.

## Release-skill (`/release` → `scripts/release.sh`)

Тонкая skill-обёртка вызывает детерминированный bash-скрипт:

1. Проверка: рабочее дерево чистое, текущая ветка `dev`.
2. Последний тег (`git describe --tags --abbrev=0`, база `0.0.0` если нет) →
   коммиты `<tag>..dev` → парсинг conventional-префиксов → вычисление bump.
3. Показ предлагаемой версии + списка коммитов, ожидание подтверждения `y/n`
   (версию можно переопределить вручную).
4. `git checkout main && git merge --no-ff dev` → push `main`.
5. Постановка и push тега `vX.Y.Z` на merge-коммит → CI подхватывает и собирает.
6. Возврат на `dev`.

Release это механическая операция — детерминизм скрипта важнее гибкости.

## docker-compose.yml

Переключение канала одной строкой:

```yaml
image: ghcr.io/fus1ond/vpn-bot:latest   # прод (дефолт)
# image: ghcr.io/fus1ond/vpn-bot:dev    # тест-канал разработки
```

## Деплой

Без изменений: ручной по SSH (`docker compose pull && docker compose up -d`).
Автодеплой из CI намеренно не добавляем.

## Шаги внедрения

1. Создать ветку `dev` от `main`.
2. Заменить `deploy.yml` на новый `ci.yml` (GHCR, три триггера, версионирование).
3. Обновить `docker-compose.yml` на `ghcr.io/fus1ond/vpn-bot` с комментарием про каналы.
4. Добавить `scripts/release.sh` + skill-обёртку `/release`.
5. Удалить секреты Docker Hub из репозитория (после проверки GHCR).
6. Почистить устаревшие feature-ветки (опционально).
