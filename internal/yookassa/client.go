package yookassa

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
)

const defaultBaseURL = "https://api.yookassa.ru"

var rubleAmount = regexp.MustCompile(`^([0-9]+)\.00$`)

type Client struct {
	shopID, secret, baseURL string
	http                    *http.Client
}

func NewClient(shopID, secret string) *Client {
	return NewClientWithBaseURL(shopID, secret, defaultBaseURL)
}
func NewClientWithBaseURL(shopID, secret, baseURL string) *Client {
	return &Client{shopID: shopID, secret: secret, baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: 30 * time.Second}}
}
func (c *Client) SetHTTPClient(client *http.Client) {
	if client != nil {
		c.http = client
	}
}
func (c *Client) Name() string   { return paymentprovider.YooKassa }
func (c *Client) ShopID() string { return c.shopID }

func (c *Client) CreatePayment(req paymentprovider.CreateRequest) (*paymentprovider.Payment, error) {
	key := req.IdempotenceKey
	if key == "" {
		var err error
		key, err = NewIdempotenceKey()
		if err != nil {
			return nil, err
		}
	}
	body := map[string]any{
		"amount":       map[string]string{"value": fmt.Sprintf("%d.00", req.Amount), "currency": req.Currency},
		"capture":      true,
		"confirmation": map[string]string{"type": "redirect", "return_url": req.ReturnURL},
		"description":  req.Description,
		"metadata":     map[string]string{"local_payment_id": fmt.Sprintf("%d", req.LocalPaymentID)},
	}
	return c.doPayment(http.MethodPost, "/v3/payments", body, key)
}

func (c *Client) GetPayment(id string) (*paymentprovider.Payment, error) {
	return c.doPayment(http.MethodGet, "/v3/payments/"+id, nil, "")
}

func (c *Client) doPayment(method, path string, body any, idempotenceKey string) (*paymentprovider.Payment, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.SetBasicAuth(c.shopID, c.secret)
	req.Header.Set("Content-Type", "application/json")
	if idempotenceKey != "" {
		req.Header.Set("Idempotence-Key", idempotenceKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("yookassa API error %d: %s", resp.StatusCode, string(data))
	}
	var raw struct {
		ID, Status    string
		Amount        struct{ Value, Currency string }
		PaymentMethod struct {
			Type string `json:"type"`
		} `json:"payment_method"`
		Confirmation struct {
			URL string `json:"confirmation_url"`
		} `json:"confirmation"`
		Recipient struct {
			AccountID string `json:"account_id"`
		} `json:"recipient"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	matches := rubleAmount.FindStringSubmatch(raw.Amount.Value)
	if matches == nil {
		return nil, fmt.Errorf("invalid ruble payment amount %q", raw.Amount.Value)
	}
	amount, err := strconv.Atoi(matches[1])
	if err != nil {
		return nil, fmt.Errorf("parse payment amount: %w", err)
	}
	return &paymentprovider.Payment{ID: raw.ID, Status: status(raw.Status), Amount: amount, Currency: raw.Amount.Currency, PaymentMethod: raw.PaymentMethod.Type, ConfirmationURL: raw.Confirmation.URL, ExpiresAt: raw.ExpiresAt, RecipientID: raw.Recipient.AccountID}, nil
}

func status(s string) string {
	switch s {
	case "succeeded":
		return paymentprovider.StatusSucceeded
	case "canceled":
		return paymentprovider.StatusCanceled
	case "pending", "waiting_for_capture":
		return paymentprovider.StatusPending
	default:
		return s
	}
}
func NewIdempotenceKey() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate idempotence key: %w", err)
	}
	return hex.EncodeToString(b), nil
}
