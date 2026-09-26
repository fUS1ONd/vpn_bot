package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
)

// Снимок пользователей заглушки на диске. Без него база бота в data-test
// переживала stand-down, а пользователи панели — нет, и «👤 Моя подписка»
// после каждого перезапуска стенда упиралась в 404. Файл лежит там же, в
// data-test: stand-down сохраняет обе стороны, stand-reset сносит обе.
//
// Рычаги отказов и задержек в снимок не входят намеренно: они разовые, и
// забытый включённым отказ не должен переживать перезапуск.

type snapshot struct {
	NextID int64           `json:"nextId"`
	Users  []persistedUser `json:"users"`
}

// persistedUser добавляет к пользователю устройства: в JSON пользователя их
// нет (у панели они за отдельным HWID-маршрутом), а терять их при перезапуске
// незачем.
type persistedUser struct {
	user
	Devices []device `json:"devices"`
}

// load читает снимок. Отсутствие файла — обычный первый запуск. Битый файл —
// ошибка: молча начать с пустой панели значило бы вернуть ту самую асимметрию
// с 404, только без объяснения.
func (s *store) load(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("разбор %s: %w", path, err)
	}
	for _, pu := range snap.Users {
		u := pu.user
		u.devices = pu.Devices
		s.users[u.UUID] = &u
	}
	if snap.NextID > s.nextID {
		s.nextID = snap.NextID
	}
	return nil
}

// save пишет снимок атомарно: через временный файл и rename, чтобы оборванная
// запись не оставила битый файл. Вызывать под s.mu.
func (s *store) save(path string) error {
	snap := snapshot{NextID: s.nextID, Users: make([]persistedUser, 0, len(s.users))}
	for _, u := range s.users {
		snap.Users = append(snap.Users, persistedUser{user: *u, Devices: u.devices})
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".mockpanel-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// withPersistence сохраняет снимок после каждого запроса, который мог изменить
// пользователей. Проще и надёжнее, чем помнить о сохранении в каждом маршруте:
// новый маршрут не сможет его забыть.
func withPersistence(s *store, path string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		if r.Method == http.MethodGet {
			return
		}

		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.save(path); err != nil {
			slog.Error("Не удалось сохранить состояние заглушки", "error", err, "path", path)
		}
	})
}
