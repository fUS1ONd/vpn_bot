package bot

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stringConsts возвращает строковые константы файла пакета с заданным
// префиксом имени: имя → значение. Каталог Действий сверяется с исходником,
// а не с копией списка в тесте — иначе новая кнопка прошла бы мимо обоих.
func stringConsts(t *testing.T, file, prefix string) map[string]string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	require.NoError(t, err)

	consts := make(map[string]string)
	for _, decl := range parsed.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, prefix) || i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				value, err := strconv.Unquote(lit.Value)
				require.NoError(t, err)
				consts[name.Name] = value
			}
		}
	}
	require.NotEmpty(t, consts, "в %s нет констант %s*", file, prefix)
	return consts
}

// inlineOnlyCaptions — Btn*-константы, которые служат только подписями
// inline-кнопок: их нажатие приходит callback-ом или inline-запросом, а не
// текстом, и в карте reply-кнопок им не место.
var inlineOnlyCaptions = map[string]struct{}{
	"BtnPaidCheck":   {}, // «Я оплатил» на экране оплаты — cbPayCheck
	"BtnInviteShare": {}, // «Поделиться» — switch_inline_query
}

// У каждой reply-кнопки есть id Действия: и у подписей, которые убираются из
// чата, и у любой Btn*-константы, кроме подписей inline-кнопок.
func TestReplyButtonActions_CoverEveryButton(t *testing.T) {
	for caption := range replyButtonCaptions {
		_, ok := replyButtonActions[caption]
		assert.True(t, ok, "у reply-кнопки %q нет id в replyButtonActions", caption)
	}
	for name, caption := range stringConsts(t, "keyboards.go", "Btn") {
		if _, inlineOnly := inlineOnlyCaptions[name]; inlineOnly {
			continue
		}
		_, ok := replyButtonActions[caption]
		assert.True(t, ok, "у reply-кнопки %s (%q) нет id в replyButtonActions", name, caption)
	}
}

// Id Действий не повторяются: две кнопки под одним id слились бы в журнале
// в одну, и Шаг воронки считал бы чужие нажатия.
func TestActionIDs_Unique(t *testing.T) {
	owners := make(map[string]string)
	claim := func(id, owner string) {
		t.Helper()
		assert.NotEmpty(t, id, "пустой id Действия у %s", owner)
		if prev, taken := owners[id]; taken {
			t.Errorf("id Действия %q повторяется: %s и %s", id, prev, owner)
		}
		owners[id] = owner
	}

	for caption, id := range replyButtonActions {
		claim(id, "reply-кнопка "+strconv.Quote(caption))
	}
	for unique := range inlineButtonActions {
		claim(unique, "inline-кнопка "+unique)
	}
	for _, id := range []string{
		actionStart, actionStartInvite, actionStartShare,
		actionText, actionVoice, actionVideoNote, actionMedia,
	} {
		claim(id, "Действие-факт "+id)
	}
}

// Каждая inline-кнопка (константа cb* с её Unique) есть в каталоге Действий:
// новая кнопка, забытая в каталоге, роняет тест, а не пишется в журнал как
// «неизвестная».
func TestInlineButtonActions_CoverEveryButton(t *testing.T) {
	for name, unique := range stringConsts(t, "keyboards.go", "cb") {
		_, ok := inlineButtonActions[unique]
		assert.True(t, ok, "inline-кнопки %s (%q) нет в inlineButtonActions", name, unique)
		assert.LessOrEqual(t, len(unique), maxUnknownUniqueLen,
			"Unique %s длиннее предела, которым режутся кнопки вне каталога", name)
	}
}
