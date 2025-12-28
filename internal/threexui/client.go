package threexui

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"

	"github.com/fus1ond/vpn_bot/internal/config"
)

// Client represents a 3X-UI API client
type Client struct {
	config     config.ServerConfig
	httpClient *http.Client
	baseURL    string
	webPath    string
}

// New creates a new 3X-UI client
func New(cfg config.ServerConfig) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create cookie jar: %w", err)
	}

	return &Client{
		config: cfg,
		httpClient: &http.Client{
			Jar: jar,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
		},
		baseURL: cfg.BaseURL,
		webPath: strings.TrimSuffix(cfg.WebPath, "/"),
	}, nil
}

// Login authenticates with the 3X-UI panel
func (c *Client) Login() error {
	loginURL := fmt.Sprintf("%s%s/login", c.baseURL, c.webPath)

	data := url.Values{}
	data.Set("username", c.config.Username)
	data.Set("password", c.config.Password)

	req, err := http.NewRequest("POST", loginURL, strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create login request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("login failed with status %d: %s", resp.StatusCode, string(body))
	}

	slog.Info("Successfully logged in to 3X-UI", "server", c.baseURL)
	return nil
}

// AddClient adds a new client to the specified inbound
func (c *Client) AddClient(inboundID int, email, uuid string, limitBytes int64) error {
	apiURL := fmt.Sprintf("%s%s/panel/api/inbounds/addClient", c.baseURL, c.webPath)

	clientSettings := map[string]interface{}{
		"clients": []map[string]interface{}{
			{
				"id":         uuid,
				"email":      email,
				"flow":       "xtls-rprx-vision",
				"totalGB":    limitBytes,
				"expiryTime": 0,
				"enable":     true,
				"tgId":       "",
				"subId":      "",
			},
		},
	}

	settingsJSON, err := json.Marshal(clientSettings)
	if err != nil {
		return fmt.Errorf("failed to marshal client settings: %w", err)
	}

	payload := map[string]interface{}{
		"id":       inboundID,
		"settings": string(settingsJSON),
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(payloadJSON))
	if err != nil {
		return fmt.Errorf("failed to create add client request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("add client request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("add client failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if success, ok := result["success"].(bool); !ok || !success {
		msg := result["msg"]
		return fmt.Errorf("API returned failure: %v", msg)
	}

	return nil
}

// GetClientTraffic retrieves traffic usage for a specific client
func (c *Client) GetClientTraffic(inboundID int, email string) (int64, error) {
	apiURL := fmt.Sprintf("%s%s/panel/api/inbounds/list", c.baseURL, c.webPath)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create list request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("list request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("list failed with status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response: %w", err)
	}

	var result struct {
		Success bool `json:"success"`
		Obj     []struct {
			ID          int `json:"id"`
			ClientStats []struct {
				Email string `json:"email"`
				Up    int64  `json:"up"`
				Down  int64  `json:"down"`
			} `json:"clientStats"`
		} `json:"obj"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("failed to parse response: %w", err)
	}

	if !result.Success {
		return 0, fmt.Errorf("API returned failure")
	}

	// Find the target inbound
	for _, inbound := range result.Obj {
		if inbound.ID == inboundID {
			// Find client stats
			for _, client := range inbound.ClientStats {
				if client.Email == email {
					total := client.Up + client.Down
					slog.Info("Found traffic for client", "email", email, "bytes", total)
					return total, nil
				}
			}
			slog.Info("Client exists but no traffic yet", "email", email)
			return 0, nil
		}
	}

	slog.Warn("Inbound not found", "id", inboundID)
	return 0, nil
}

// ClientInfo represents client information from the panel
type ClientInfo struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// GetAllClients retrieves all clients from a specific inbound
func (c *Client) GetAllClients(inboundID int) ([]ClientInfo, error) {
	apiURL := fmt.Sprintf("%s%s/panel/api/inbounds/list", c.baseURL, c.webPath)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create list request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list failed with status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var result struct {
		Success bool `json:"success"`
		Obj     []struct {
			ID       int    `json:"id"`
			Settings string `json:"settings"`
		} `json:"obj"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !result.Success {
		return nil, fmt.Errorf("API returned failure")
	}

	// Find the target inbound and parse settings
	for _, inbound := range result.Obj {
		if inbound.ID == inboundID {
			var settings struct {
				Clients []ClientInfo `json:"clients"`
			}
			if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
				return nil, fmt.Errorf("failed to parse settings: %w", err)
			}
			return settings.Clients, nil
		}
	}

	return nil, fmt.Errorf("inbound not found")
}
