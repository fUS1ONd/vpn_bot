#!/bin/bash
set -euo pipefail

# Детерминированный релиз-скрипт: вычисляет SemVer из conventional commits и
# выкатывает релиз (merge dev -> main + тег vX.Y.Z + push). Вызывается через
# skill /release в две фазы:
#
#   release.sh --dry-run
#       Считает версию, печатает список коммитов и SHA HEAD dev. Ничего не меняет.
#
#   release.sh --version vX.Y.Z --expect-sha <sha>
#       Выполняет релиз. --expect-sha защищает от рассинхрона: если dev уехал
#       после dry-run, скрипт откажется тегать не то состояние.
#
# Модель веток: main двигается ТОЛЬКО этим скриптом, dev — интеграционная.

RELEASE_BRANCH="dev"
MAIN_BRANCH="main"

MODE=""
FORCED_VERSION=""
EXPECT_SHA=""

# --- разбор аргументов ---
while [[ $# -gt 0 ]]; do
	case "$1" in
	--dry-run)
		MODE="dry-run"
		shift
		;;
	--version)
		MODE="release"
		FORCED_VERSION="${2:?--version требует значение vX.Y.Z}"
		shift 2
		;;
	--expect-sha)
		EXPECT_SHA="${2:?--expect-sha требует значение SHA}"
		shift 2
		;;
	*)
		echo "Неизвестный аргумент: $1" >&2
		exit 2
		;;
	esac
done

if [[ -z "$MODE" ]]; then
	echo "Использование: release.sh --dry-run | --version vX.Y.Z [--expect-sha <sha>]" >&2
	exit 2
fi

# --- общие проверки состояния ---

# Грязное рабочее дерево — жёсткий стоп (иначе релиз зацепит мусор).
if [[ -n "$(git status --porcelain)" ]]; then
	echo "ОШИБКА: есть незакоммиченные изменения. Закоммить или спрячь (git stash)." >&2
	exit 1
fi

# Релиз выпускается только из dev.
CURRENT_BRANCH="$(git rev-parse --abbrev-ref HEAD)"
if [[ "$CURRENT_BRANCH" != "$RELEASE_BRANCH" ]]; then
	echo "ОШИБКА: релиз выпускается из ветки '$RELEASE_BRANCH', а сейчас '$CURRENT_BRANCH'." >&2
	echo "Перейди на '$RELEASE_BRANCH' и повтори." >&2
	exit 1
fi

git fetch --quiet --tags origin

# Предупреждение, если локальный dev отстал от origin/dev.
if git rev-parse --verify --quiet "origin/$RELEASE_BRANCH" >/dev/null; then
	BEHIND="$(git rev-list --count "$RELEASE_BRANCH..origin/$RELEASE_BRANCH")"
	if [[ "$BEHIND" -gt 0 ]]; then
		echo "ВНИМАНИЕ: локальный '$RELEASE_BRANCH' отстаёт от origin на $BEHIND коммит(ов)." >&2
		echo "Релиз соберётся из локального состояния. Рекомендуется git pull перед релизом." >&2
	fi
fi

# --- вычисление последнего тега и диапазона коммитов ---

LAST_TAG="$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")"

if git rev-parse --verify --quiet "refs/tags/$LAST_TAG" >/dev/null; then
	RANGE="$LAST_TAG..$RELEASE_BRANCH"
else
	# Тегов ещё нет — берём весь лог ветки.
	RANGE="$RELEASE_BRANCH"
fi

# --- парсинг conventional commits -> тип bump ---

BUMP="patch" # дефолт: любой релиз получает номер
HAS_FEAT=false
HAS_BREAKING=false

# Однострочный список для показа пользователю.
COMMIT_LIST="$(git log --no-merges --format='- %s' "$RANGE" 2>/dev/null || true)"

# Читаем полные сообщения коммитов напрямую из git log через NUL-разделитель.
# ВАЖНО: NUL нельзя гонять через $(...) — bash вырезает null-байты. Поэтому
# git log подаётся в while напрямую process substitution, без промежуточной
# переменной. Каждая запись: <заголовок>\n<тело>, записи разделены NUL.
while IFS= read -r -d '' record; do
	[[ -z "$record" ]] && continue
	subject="${record%%$'\n'*}" # первая строка (заголовок)

	# Breaking: '!' перед двоеточием в заголовке ИЛИ 'BREAKING CHANGE:' в теле.
	if [[ "$subject" =~ ^[a-z]+(\([^\)]*\))?!: ]] || printf '%s' "$record" | grep -q 'BREAKING CHANGE:'; then
		HAS_BREAKING=true
	fi
	# feat: в заголовке.
	if [[ "$subject" =~ ^feat(\([^\)]*\))?!?: ]]; then
		HAS_FEAT=true
	fi
done < <(git log --no-merges --format='%B%x00' "$RANGE" 2>/dev/null || true)

# Текущий major из последнего тега (для оговорки 0.x).
CUR_MAJOR="$(printf '%s' "$LAST_TAG" | sed -E 's/^v?([0-9]+)\..*/\1/')"

if [[ "$HAS_BREAKING" == true ]]; then
	if [[ "$CUR_MAJOR" -eq 0 ]]; then
		BUMP="minor" # оговорка 0.x: breaking двигает minor
	else
		BUMP="major"
	fi
elif [[ "$HAS_FEAT" == true ]]; then
	BUMP="minor"
else
	BUMP="patch"
fi

# --- вычисление следующей версии ---

VER_CORE="${LAST_TAG#v}"
MAJOR="$(printf '%s' "$VER_CORE" | cut -d. -f1)"
MINOR="$(printf '%s' "$VER_CORE" | cut -d. -f2)"
PATCH="$(printf '%s' "$VER_CORE" | cut -d. -f3)"

case "$BUMP" in
major)
	MAJOR=$((MAJOR + 1))
	MINOR=0
	PATCH=0
	;;
minor)
	MINOR=$((MINOR + 1))
	PATCH=0
	;;
patch)
	PATCH=$((PATCH + 1))
	;;
esac

COMPUTED_VERSION="v${MAJOR}.${MINOR}.${PATCH}"
HEAD_SHA="$(git rev-parse "$RELEASE_BRANCH")"

# --- фаза dry-run ---
if [[ "$MODE" == "dry-run" ]]; then
	echo "Последний тег:    $LAST_TAG"
	echo "Тип bump:         $BUMP"
	echo "Новая версия:     $COMPUTED_VERSION"
	echo "HEAD $RELEASE_BRANCH:        $HEAD_SHA"
	echo ""
	echo "Коммиты в релизе:"
	if [[ -n "$COMMIT_LIST" ]]; then
		echo "$COMMIT_LIST"
	else
		echo "  (нет новых коммитов с прошлого тега)"
	fi
	exit 0
fi

# --- фаза release ---

TARGET_VERSION="$FORCED_VERSION"
if [[ ! "$TARGET_VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	echo "ОШИБКА: версия '$TARGET_VERSION' не в формате vX.Y.Z." >&2
	exit 1
fi

# Защита от рассинхрона: dev не должен был уехать после dry-run.
if [[ -n "$EXPECT_SHA" && "$EXPECT_SHA" != "$HEAD_SHA" ]]; then
	echo "ОШИБКА: '$RELEASE_BRANCH' сдвинулся с момента dry-run." >&2
	echo "  ожидалось: $EXPECT_SHA" >&2
	echo "  сейчас:    $HEAD_SHA" >&2
	echo "Перезапусти dry-run и подтверди заново." >&2
	exit 1
fi

# Тег не должен уже существовать.
if git rev-parse --verify --quiet "refs/tags/$TARGET_VERSION" >/dev/null; then
	echo "ОШИБКА: тег '$TARGET_VERSION' уже существует." >&2
	exit 1
fi

# trap: при любом сбое во время работы с main вернуться на dev и откатить merge.
cleanup() {
	local ec=$?
	if [[ $ec -ne 0 ]]; then
		git merge --abort 2>/dev/null || true
		git checkout "$RELEASE_BRANCH" 2>/dev/null || true
	fi
}
trap cleanup EXIT

# main должен совпадать с origin/main (иначе push отклонят non-fast-forward).
git checkout "$MAIN_BRANCH"
if git rev-parse --verify --quiet "origin/$MAIN_BRANCH" >/dev/null; then
	git merge --ff-only "origin/$MAIN_BRANCH"
fi

# Merge dev -> main. main — предок dev by design, конфликтов быть не должно.
# --no-ff даёт merge-коммит как якорь для тега.
if ! git merge --no-ff "$RELEASE_BRANCH" -m "chore: релиз $TARGET_VERSION"; then
	git merge --abort 2>/dev/null || true
	echo "ОШИБКА: конфликт при merge '$RELEASE_BRANCH' в '$MAIN_BRANCH'." >&2
	echo "Похоже, '$MAIN_BRANCH' трогали руками. Разберись вручную." >&2
	exit 1
fi

git push origin "$MAIN_BRANCH"

# Тег на merge-коммит. Если push тега упадёт после push main — явная диагностика.
git tag -a "$TARGET_VERSION" -m "$TARGET_VERSION"
if ! git push origin "$TARGET_VERSION"; then
	echo "ОШИБКА: '$MAIN_BRANCH' запушен, но push тега '$TARGET_VERSION' не прошёл." >&2
	echo "Повтори вручную: git push origin $TARGET_VERSION" >&2
	exit 1
fi

# Успех — возвращаемся на dev штатно (trap на success ничего не делает).
git checkout "$RELEASE_BRANCH"
trap - EXIT

echo ""
echo "Релиз $TARGET_VERSION выпущен."
echo "CI подхватит тег и соберёт образ + GitHub Release."
