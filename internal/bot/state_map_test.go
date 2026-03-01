package bot

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStateMap_Basic(t *testing.T) {
	sm := newStateMap()

	t.Run("Get несуществующего ключа", func(t *testing.T) {
		val := sm.Get(1)
		assert.Equal(t, "", val)
	})

	t.Run("Set и Get", func(t *testing.T) {
		sm.Set(1, "wait_invite")
		val := sm.Get(1)
		assert.Equal(t, "wait_invite", val)
	})

	t.Run("Delete", func(t *testing.T) {
		sm.Set(2, "wait_ban")
		sm.Delete(2)
		val := sm.Get(2)
		assert.Equal(t, "", val)
	})

	t.Run("Delete несуществующего ключа — без паники", func(t *testing.T) {
		assert.NotPanics(t, func() {
			sm.Delete(999)
		})
	})
}

func TestStateMap_Concurrent(t *testing.T) {
	sm := newStateMap()
	var wg sync.WaitGroup

	// 100 горутин пишут и читают одновременно
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			sm.Set(id, "state")
			_ = sm.Get(id)
			sm.Delete(id)
		}(int64(i))
	}

	wg.Wait()
}
