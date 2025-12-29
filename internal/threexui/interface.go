package threexui

// ThreeXUIClient defines the interface for 3X-UI panel operations
type ThreeXUIClient interface {
	// Login authenticates with the 3X-UI panel
	Login() error

	// AddClient adds a new client to the specified inbound (legacy)
	AddClient(inboundID int, email, uuid string, limitBytes int64) error

	// AddClientWithSettings adds a new client with full settings
	AddClientWithSettings(inboundID int, settings ClientSettings) error

	// GetClientTraffic retrieves traffic usage for a specific client
	GetClientTraffic(inboundID int, email string) (int64, error)

	// GetAllClients retrieves all clients from a specific inbound
	GetAllClients(inboundID int) ([]ClientInfo, error)

	// GetClientStatus retrieves the full status of a client
	GetClientStatus(inboundID int, email string) (*ClientStatus, error)

	// UpdateClient updates client settings in the panel
	UpdateClient(inboundID int, clientUUID string, settings ClientSettings) error

	// ResetClientTraffic resets traffic counters for a client
	ResetClientTraffic(inboundID int, email string) error

	// DeleteClient deletes a client from the panel
	DeleteClient(inboundID int, clientUUID string) error
}

// Ensure Client implements ThreeXUIClient
var _ ThreeXUIClient = (*Client)(nil)
