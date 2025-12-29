package threexui

import (
	"fmt"
	"sync"
)

// MockClient is a mock implementation of ThreeXUIClient for testing
type MockClient struct {
	mu      sync.RWMutex
	clients map[string]*mockClientData // key: email
	err     error                      // error to return on all operations
}

type mockClientData struct {
	info    ClientInfo
	traffic int64
}

// NewMockClient creates a new mock 3X-UI client
func NewMockClient() *MockClient {
	return &MockClient{
		clients: make(map[string]*mockClientData),
	}
}

// SetError sets an error to be returned by all operations
func (m *MockClient) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

// Login simulates authentication
func (m *MockClient) Login() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.err
}

// AddClient adds a new client (legacy method)
func (m *MockClient) AddClient(inboundID int, email, uuid string, limitBytes int64) error {
	return m.AddClientWithSettings(inboundID, ClientSettings{
		UUID:    uuid,
		Email:   email,
		TotalGB: limitBytes,
		Enable:  true,
	})
}

// AddClientWithSettings adds a new client with full settings
func (m *MockClient) AddClientWithSettings(inboundID int, settings ClientSettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return m.err
	}

	if _, exists := m.clients[settings.Email]; exists {
		return fmt.Errorf("client already exists: %s", settings.Email)
	}

	m.clients[settings.Email] = &mockClientData{
		info: ClientInfo{
			ID:         settings.UUID,
			Email:      settings.Email,
			LimitIP:    settings.LimitIP,
			TotalGB:    settings.TotalGB,
			ExpiryTime: settings.ExpiryTime,
			Enable:     settings.Enable,
		},
		traffic: 0,
	}

	return nil
}

// GetClientTraffic retrieves traffic usage
func (m *MockClient) GetClientTraffic(inboundID int, email string) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.err != nil {
		return 0, m.err
	}

	client, exists := m.clients[email]
	if !exists {
		return 0, nil
	}

	return client.traffic, nil
}

// GetAllClients retrieves all clients
func (m *MockClient) GetAllClients(inboundID int) ([]ClientInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.err != nil {
		return nil, m.err
	}

	var clients []ClientInfo
	for _, data := range m.clients {
		clients = append(clients, data.info)
	}

	return clients, nil
}

// GetClientStatus retrieves the full status of a client
func (m *MockClient) GetClientStatus(inboundID int, email string) (*ClientStatus, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.err != nil {
		return nil, m.err
	}

	client, exists := m.clients[email]
	if !exists {
		return nil, fmt.Errorf("client not found: %s", email)
	}

	return &ClientStatus{
		Email:       client.info.Email,
		UUID:        client.info.ID,
		Enable:      client.info.Enable,
		LimitIP:     client.info.LimitIP,
		TotalGB:     client.info.TotalGB,
		ExpiryTime:  client.info.ExpiryTime,
		UsedTraffic: client.traffic,
	}, nil
}

// UpdateClient updates client settings
func (m *MockClient) UpdateClient(inboundID int, clientUUID string, settings ClientSettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return m.err
	}

	// Find client by UUID
	for email, data := range m.clients {
		if data.info.ID == clientUUID {
			m.clients[email] = &mockClientData{
				info: ClientInfo{
					ID:         settings.UUID,
					Email:      settings.Email,
					LimitIP:    settings.LimitIP,
					TotalGB:    settings.TotalGB,
					ExpiryTime: settings.ExpiryTime,
					Enable:     settings.Enable,
				},
				traffic: data.traffic,
			}
			return nil
		}
	}

	return fmt.Errorf("client not found: %s", clientUUID)
}

// ResetClientTraffic resets traffic counters
func (m *MockClient) ResetClientTraffic(inboundID int, email string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return m.err
	}

	client, exists := m.clients[email]
	if !exists {
		return fmt.Errorf("client not found: %s", email)
	}

	client.traffic = 0
	return nil
}

// DeleteClient deletes a client
func (m *MockClient) DeleteClient(inboundID int, clientUUID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return m.err
	}

	// Find client by UUID
	for email, data := range m.clients {
		if data.info.ID == clientUUID {
			delete(m.clients, email)
			return nil
		}
	}

	return fmt.Errorf("client not found: %s", clientUUID)
}

// === Test helpers ===

// AddMockTraffic adds traffic to a client (for testing)
func (m *MockClient) AddMockTraffic(email string, bytes int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if client, exists := m.clients[email]; exists {
		client.traffic += bytes
	}
}

// GetMockClient returns a client by email (for testing)
func (m *MockClient) GetMockClient(email string) *ClientInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if data, exists := m.clients[email]; exists {
		return &data.info
	}
	return nil
}

// Ensure MockClient implements ThreeXUIClient
var _ ThreeXUIClient = (*MockClient)(nil)
