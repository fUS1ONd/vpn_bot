package remnawave

import (
	"errors"
	"net/http"
	"strconv"
)

// UserRef — ссылка на пользователя панели, независимая от версии API.
// На 2.8.x значим UUID, на 3.x — ID; в переходный период известны оба.
type UserRef struct {
	UUID string
	ID   int64
}

// Ошибки неполной ссылки. Разделены намеренно: текст сразу говорит, какой половины
// связки не хватает, — на 3.x это почти всегда незаполненный remnawave_id в БД.
var (
	// ErrUserRefMissingID — панель 3.x, а числовой id пользователя неизвестен.
	ErrUserRefMissingID = errors.New("user ref has no numeric id")
	// ErrUserRefMissingUUID — панель 2.8.x, а UUID пользователя неизвестен.
	ErrUserRefMissingUUID = errors.New("user ref has no uuid")
)

// IsZero сообщает, что ссылка пуста в обеих половинах.
func (r UserRef) IsZero() bool {
	return r.UUID == "" && r.ID == 0
}

// Ref отдаёт ссылку на пользователя по ответу панели. На 3.x поля uuid в ответе нет
// вовсе, поэтому брать UUID напрямую из User нельзя — только через Ref.
func (u *User) Ref() UserRef {
	return UserRef{UUID: u.UUID, ID: u.ID}
}

// userPathSegment возвращает идентификатор пользователя для пути в том виде,
// в каком его ждёт панель заданной версии.
//
// ID == 0 как признак «не задан» безопасен: в 3.2.3 параметр пути объявлен
// number с exclusiveMinimum 0, то есть валидный id всегда ≥ 1.
func userPathSegment(version APIVersion, ref UserRef) (string, error) {
	switch version {
	case APIVersionV3:
		if ref.ID == 0 {
			return "", ErrUserRefMissingID
		}
		return strconv.FormatInt(ref.ID, 10), nil
	case APIVersionV2:
		if ref.UUID == "" {
			return "", ErrUserRefMissingUUID
		}
		return ref.UUID, nil
	default:
		return "", ErrPanelVersionUnknown
	}
}

// userRequest — описание запроса к маршруту пользователя, собранное под конкретную
// версию контракта.
type userRequest struct {
	method string
	path   string
	body   []byte
}

// doUserRequest выполняет запрос по маршруту пользователя и, если панель ответила
// 400, один раз перечитывает версию и повторяет запрос с новым контрактом.
//
// Владелец обновляет панель, не перезапуская бота, поэтому смену контракта надо
// заметить на живом трафике. Триггером взят именно 400 («идентификатор не того
// типа»): 404 в него намеренно не входит — это штатный ответ «пользователь удалён
// из панели», который scheduler получает на автокиках регулярно, и повторять на нём
// неидемпотентные PATCH/DELETE нельзя.
func (c *Client) doUserRequest(ref UserRef, build func(APIVersion, UserRef) (userRequest, error)) ([]byte, error) {
	version, err := c.DetectAPIVersion()
	if err != nil {
		return nil, err
	}

	req, err := build(version, ref)
	if err != nil {
		return nil, err
	}

	resp, err := c.doRequest(req.method, req.path, req.body)
	if err == nil {
		return resp, nil
	}

	status, isAPIErr := APIStatusCode(err)
	if !isAPIErr || status != http.StatusBadRequest {
		return nil, err
	}

	newVersion, refreshErr := c.RefreshAPIVersion()
	if refreshErr != nil || newVersion == version {
		return nil, err
	}

	retryReq, buildErr := build(newVersion, ref)
	if buildErr != nil {
		// Ссылка не полна под новый контракт — честнее вернуть исходную ошибку
		// панели вместе с причиной, чем притворяться, что повтор был возможен.
		return nil, errors.Join(err, buildErr)
	}

	return c.doRequest(retryReq.method, retryReq.path, retryReq.body)
}

// userPathRequest собирает билдер запроса к пути вида /api/users/{id}<suffix>.
func userPathRequest(method, suffix string, body []byte) func(APIVersion, UserRef) (userRequest, error) {
	return func(version APIVersion, ref UserRef) (userRequest, error) {
		segment, err := userPathSegment(version, ref)
		if err != nil {
			return userRequest{}, err
		}
		return userRequest{method: method, path: "/api/users/" + segment + suffix, body: body}, nil
	}
}
