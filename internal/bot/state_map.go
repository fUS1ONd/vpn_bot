package bot

import "sync"

// stateMap — потокобезопасная обёртка над map[int64]string для хранения состояний пользователей
type stateMap struct {
	mu sync.RWMutex
	m  map[int64]string
}

// newStateMap создаёт новый stateMap
func newStateMap() *stateMap {
	return &stateMap{
		m: make(map[int64]string),
	}
}

// Get возвращает состояние пользователя (пустая строка если не найден)
func (sm *stateMap) Get(telegramID int64) string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.m[telegramID]
}

// Set устанавливает состояние пользователя
func (sm *stateMap) Set(telegramID int64, state string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.m[telegramID] = state
}

// Delete удаляет состояние пользователя
func (sm *stateMap) Delete(telegramID int64) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.m, telegramID)
}
