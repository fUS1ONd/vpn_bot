package platega

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultBaseURL = "https://app.platega.io"

// Способы оплаты (Platega paymentMethod int)
const (
	PaymentMethodSBP    = 2
	PaymentMethodCard   = 11
	PaymentMethodCrypto = 13
)

// Статусы платежа
const (
	StatusPending         = "PENDING"
	StatusConfirmed       = "CONFIRMED"
	StatusManualConfirmed = "MANUAL_CONFIRMED"
	StatusCanceled        = "CANCELED"
	StatusChargebacked    = "CHARGEBACKED"
)

// Client — HTTP-клиент Platega API
type Client struct {
	merchantID string
	secret     string
	baseURL    string
	http       *http.Client
}

// NewClient создаёт клиент Platega с production URL
func NewClient(merchantID, secret string) *Client {
	return NewClientWithBaseURL(merchantID, secret, defaultBaseURL)
}

// NewClientWithBaseURL создаёт клиент Platega с заданным базовым URL (для тестов)
func NewClientWithBaseURL(merchantID, secret, baseURL string) *Client {
	return &Client{
		merchantID: merchantID,
		secret:     secret,
		baseURL:    baseURL,
		http:       &http.Client{Timeout: 30 * time.Second},
	}
}

// SetHTTPClient переопределяет HTTP-клиент (используется в тестах).
func (c *Client) SetHTTPClient(httpClient *http.Client) {
	if httpClient != nil {
		c.http = httpClient
	}
}

// MerchantID возвращает merchant_id (для верификации callback)
func (c *Client) MerchantID() string {
	return c.merchantID
}

// Secret возвращает secret (для верификации callback)
func (c *Client) Secret() string {
	return c.secret
}

// CreateTransactionRequest — запрос на создание платежа
type CreateTransactionRequest struct {
	PaymentMethod int    `json:"paymentMethod"`
	Amount        int    `json:"amount"`   // В рублях (целое число)
	Currency      string `json:"currency"` // "RUB"
	Description   string `json:"description"`
	ReturnURL     string `json:"return"`      // URL возврата после оплаты (бот Telegram)
	FailedURL     string `json:"failedUrl"`   // URL при ошибке
	CallbackURL   string `json:"callbackUrl"` // URL для callback
	Payload       string `json:"payload"`     // Произвольные данные (telegram_id)
}

// CreateTransactionResponse — ответ на создание платежа
type CreateTransactionResponse struct {
	TransactionID string        `json:"transactionId"`
	Redirect      string        `json:"redirect"` // Ссылка для перенаправления пользователя
	Status        string        `json:"status"`
	ExpiresIn     time.Duration `json:"-"`
}

// PaymentDetails — денежные реквизиты платежа.
type PaymentDetails struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// TransactionStatus — полный статус транзакции.
type TransactionStatus struct {
	ID             string         `json:"id"`
	PaymentDetails PaymentDetails `json:"paymentDetails"`
	Status         string         `json:"status"`
	PaymentMethod  string         `json:"paymentMethod"`
	Payload        string         `json:"payload"`
	ExpiresIn      time.Duration  `json:"-"`
}

// CallbackPayload — тело callback-запроса от Platega.
// Используется и в platega-клиенте, и в callback-сервере (импортируется оттуда).
type CallbackPayload struct {
	ID            string  `json:"id"`
	Amount        float64 `json:"amount"`
	Currency      string  `json:"currency"`
	Status        string  `json:"status"`
	PaymentMethod int     `json:"paymentMethod"`
	Payload       string  `json:"payload"`
}

func (r *CreateTransactionResponse) UnmarshalJSON(data []byte) error {
	var raw struct {
		TransactionID string `json:"transactionId"`
		Redirect      string `json:"redirect"`
		Status        string `json:"status"`
		ExpiresIn     string `json:"expiresIn"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	expiresIn, err := parseHHMMSSDuration(raw.ExpiresIn)
	if err != nil {
		return fmt.Errorf("parse expiresIn: %w", err)
	}

	r.TransactionID = raw.TransactionID
	r.Redirect = raw.Redirect
	r.Status = raw.Status
	r.ExpiresIn = expiresIn

	return nil
}

func (r *TransactionStatus) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID             string         `json:"id"`
		PaymentDetails PaymentDetails `json:"paymentDetails"`
		Status         string         `json:"status"`
		PaymentMethod  string         `json:"paymentMethod"`
		Payload        string         `json:"payload"`
		ExpiresIn      string         `json:"expiresIn"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	expiresIn, err := parseHHMMSSDuration(raw.ExpiresIn)
	if err != nil {
		return fmt.Errorf("parse expiresIn: %w", err)
	}

	r.ID = raw.ID
	r.PaymentDetails = raw.PaymentDetails
	r.Status = raw.Status
	r.PaymentMethod = raw.PaymentMethod
	r.Payload = raw.Payload
	r.ExpiresIn = expiresIn

	return nil
}

// CreatePayment создаёт платёж в Platega
func (c *Client) CreatePayment(req CreateTransactionRequest) (*CreateTransactionResponse, error) {
	// Формируем тело запроса согласно API
	body := map[string]interface{}{
		"paymentMethod": req.PaymentMethod,
		"paymentDetails": map[string]interface{}{
			"amount":   req.Amount,
			"currency": req.Currency,
		},
		"description": req.Description,
	}
	if req.ReturnURL != "" {
		body["return"] = req.ReturnURL
	}
	if req.FailedURL != "" {
		body["failedUrl"] = req.FailedURL
	}
	if req.CallbackURL != "" {
		body["callbackUrl"] = req.CallbackURL
	}
	if req.Payload != "" {
		body["payload"] = req.Payload
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", c.baseURL+"/transaction/process", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	c.setHeaders(httpReq)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("platega API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result CreateTransactionResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &result, nil
}

// GetTransactionStatus проверяет статус транзакции
func (c *Client) GetTransactionStatus(transactionID string) (*TransactionStatus, error) {
	httpReq, err := http.NewRequest("GET", c.baseURL+"/transaction/"+transactionID, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	c.setHeaders(httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("platega API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result TransactionStatus
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &result, nil
}

// setHeaders устанавливает авторизационные заголовки
func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("X-MerchantId", c.merchantID)
	req.Header.Set("X-Secret", c.secret)
}

func parseHHMMSSDuration(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}

	parsed, err := time.Parse("15:04:05", raw)
	if err != nil {
		return 0, fmt.Errorf("invalid HH:MM:SS value %q: %w", raw, err)
	}

	return time.Duration(parsed.Hour())*time.Hour +
		time.Duration(parsed.Minute())*time.Minute +
		time.Duration(parsed.Second())*time.Second, nil
}

// PaymentMethodName возвращает человекочитаемое название способа оплаты
func PaymentMethodName(method int) string {
	switch method {
	case PaymentMethodSBP:
		return "СБП"
	case PaymentMethodCard:
		return "Карта"
	case PaymentMethodCrypto:
		return "Крипта"
	default:
		return "Неизвестно"
	}
}

// PaymentMethodString возвращает строковый идентификатор для БД
func PaymentMethodString(method int) string {
	switch method {
	case PaymentMethodSBP:
		return "sbp"
	case PaymentMethodCard:
		return "card"
	case PaymentMethodCrypto:
		return "crypto"
	default:
		return "unknown"
	}
}

// PaymentMethodFromString возвращает int из строкового идентификатора
func PaymentMethodFromString(s string) int {
	switch s {
	case "sbp":
		return PaymentMethodSBP
	case "card":
		return PaymentMethodCard
	case "crypto":
		return PaymentMethodCrypto
	default:
		return 0
	}
}
