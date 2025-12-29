package threexui

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMockClient_AddClientWithSettings(t *testing.T) {
	mock := NewMockClient()

	settings := ClientSettings{
		UUID:       "test-uuid",
		Email:      "test@example.com",
		LimitIP:    2,
		TotalGB:    30 * 1024 * 1024 * 1024,
		ExpiryTime: 1735689600000,
		Enable:     true,
	}

	err := mock.AddClientWithSettings(1, settings)
	require.NoError(t, err)

	// Verify client was added
	client := mock.GetMockClient("test@example.com")
	require.NotNil(t, client)
	assert.Equal(t, "test-uuid", client.ID)
	assert.Equal(t, 2, client.LimitIP)
	assert.True(t, client.Enable)
}

func TestMockClient_AddClientDuplicate(t *testing.T) {
	mock := NewMockClient()

	settings := ClientSettings{
		UUID:  "test-uuid",
		Email: "test@example.com",
	}

	err := mock.AddClientWithSettings(1, settings)
	require.NoError(t, err)

	// Try to add duplicate
	err = mock.AddClientWithSettings(1, settings)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestMockClient_GetClientStatus(t *testing.T) {
	mock := NewMockClient()

	settings := ClientSettings{
		UUID:       "test-uuid",
		Email:      "test@example.com",
		LimitIP:    2,
		TotalGB:    30 * 1024 * 1024 * 1024,
		ExpiryTime: 1735689600000,
		Enable:     true,
	}

	mock.AddClientWithSettings(1, settings)
	mock.AddMockTraffic("test@example.com", 5*1024*1024*1024) // 5GB

	status, err := mock.GetClientStatus(1, "test@example.com")
	require.NoError(t, err)
	require.NotNil(t, status)

	assert.Equal(t, "test@example.com", status.Email)
	assert.Equal(t, "test-uuid", status.UUID)
	assert.True(t, status.Enable)
	assert.Equal(t, int64(5*1024*1024*1024), status.UsedTraffic)
}

func TestMockClient_GetClientStatusNotFound(t *testing.T) {
	mock := NewMockClient()

	_, err := mock.GetClientStatus(1, "nonexistent@example.com")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestMockClient_UpdateClient(t *testing.T) {
	mock := NewMockClient()

	settings := ClientSettings{
		UUID:    "test-uuid",
		Email:   "test@example.com",
		LimitIP: 2,
		Enable:  true,
	}

	mock.AddClientWithSettings(1, settings)

	// Update client
	newSettings := ClientSettings{
		UUID:       "test-uuid",
		Email:      "test@example.com",
		LimitIP:    3,
		TotalGB:    50 * 1024 * 1024 * 1024,
		ExpiryTime: 1735689600000,
		Enable:     false,
	}

	err := mock.UpdateClient(1, "test-uuid", newSettings)
	require.NoError(t, err)

	status, _ := mock.GetClientStatus(1, "test@example.com")
	assert.Equal(t, 3, status.LimitIP)
	assert.Equal(t, int64(50*1024*1024*1024), status.TotalGB)
	assert.False(t, status.Enable)
}

func TestMockClient_ResetClientTraffic(t *testing.T) {
	mock := NewMockClient()

	settings := ClientSettings{
		UUID:  "test-uuid",
		Email: "test@example.com",
	}

	mock.AddClientWithSettings(1, settings)
	mock.AddMockTraffic("test@example.com", 10*1024*1024*1024)

	// Verify traffic was added
	traffic, _ := mock.GetClientTraffic(1, "test@example.com")
	assert.Equal(t, int64(10*1024*1024*1024), traffic)

	// Reset traffic
	err := mock.ResetClientTraffic(1, "test@example.com")
	require.NoError(t, err)

	traffic, _ = mock.GetClientTraffic(1, "test@example.com")
	assert.Equal(t, int64(0), traffic)
}

func TestMockClient_DeleteClient(t *testing.T) {
	mock := NewMockClient()

	settings := ClientSettings{
		UUID:  "test-uuid",
		Email: "test@example.com",
	}

	mock.AddClientWithSettings(1, settings)

	// Delete client
	err := mock.DeleteClient(1, "test-uuid")
	require.NoError(t, err)

	// Verify client was deleted
	client := mock.GetMockClient("test@example.com")
	assert.Nil(t, client)
}

func TestMockClient_GetAllClients(t *testing.T) {
	mock := NewMockClient()

	mock.AddClientWithSettings(1, ClientSettings{UUID: "uuid-1", Email: "user1@test.com"})
	mock.AddClientWithSettings(1, ClientSettings{UUID: "uuid-2", Email: "user2@test.com"})
	mock.AddClientWithSettings(1, ClientSettings{UUID: "uuid-3", Email: "user3@test.com"})

	clients, err := mock.GetAllClients(1)
	require.NoError(t, err)
	assert.Len(t, clients, 3)
}

func TestMockClient_WithError(t *testing.T) {
	mock := NewMockClient()
	testErr := errors.New("test error")
	mock.SetError(testErr)

	err := mock.Login()
	assert.Equal(t, testErr, err)

	err = mock.AddClientWithSettings(1, ClientSettings{})
	assert.Equal(t, testErr, err)

	_, err = mock.GetClientStatus(1, "test@example.com")
	assert.Equal(t, testErr, err)
}

func TestMockClient_LegacyAddClient(t *testing.T) {
	mock := NewMockClient()

	err := mock.AddClient(1, "test@example.com", "test-uuid", 30*1024*1024*1024)
	require.NoError(t, err)

	client := mock.GetMockClient("test@example.com")
	require.NotNil(t, client)
	assert.Equal(t, "test-uuid", client.ID)
	assert.Equal(t, int64(30*1024*1024*1024), client.TotalGB)
}
