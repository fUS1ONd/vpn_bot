package yookassa

import (
	"bytes"
	"context"
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

// Сетевой сбой на пути к кассе стоит дорого: сорвавшийся запрос — это либо
// пользователь без ссылки на оплату, либо вебхук, который не смог сверить уже
// оплаченный платёж. Поэтому запрос повторяется. Повтор безопасен для обеих
// операций: GET идемпотентен по определению, а POST /v3/payments защищён
// ключом идемпотентности — по нему касса вернёт тот же платёж, а не создаст второй.
const (
	maxAttempts           = 3
	defaultAttemptTimeout = 15 * time.Second
	defaultRetryBackoff   = time.Second
)

var rubleAmount = regexp.MustCompile(`^([0-9]+)\.00$`)

type Client struct {
	shopID, secret, baseURL string
	http                    *http.Client
	// Бюджет одной попытки и пауза перед следующей. Их сумма ограничена сверху
	// не ради красоты: ответ вебхуку пишется с WriteTimeout 65 секунд, и серия
	// повторов обязана уложиться в него, иначе касса получит обрыв вместо ответа.
	attemptTimeout time.Duration
	retryBackoff   time.Duration
}

func NewClient(shopID, secret string) *Client {
	return NewClientWithBaseURL(shopID, secret, defaultBaseURL)
}
func NewClientWithBaseURL(shopID, secret, baseURL string) *Client {
	return &Client{
		shopID:         shopID,
		secret:         secret,
		baseURL:        strings.TrimRight(baseURL, "/"),
		http:           &http.Client{Timeout: 30 * time.Second},
		attemptTimeout: defaultAttemptTimeout,
		retryBackoff:   defaultRetryBackoff,
	}
}

// SetRetryBackoff задаёт паузу между повторами. Нужен тестам: с боевой паузой
// каждый сценарий со сбоем кассы стоил бы секунды ожидания.
func (c *Client) SetRetryBackoff(d time.Duration) { c.retryBackoff = d }

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
	// Параметр ставится только по явной просьбе: у большинства способов касса
	// сохраняет безусловно, не спрашивая человека, — согласие берётся на нашей
	// стороне, до редиректа.
	if req.SavePaymentMethod {
		body["save_payment_method"] = true
	}
	return c.doPayment(http.MethodPost, "/v3/payments", body, key)
}

// ChargeSavedMethod списывает по сохранённому Способу. Ответ синхронный:
// succeeded или canceled приходят прямо здесь, вебхук остаётся страховкой.
// Ключ идемпотентности обязателен — по нему повтор вернёт тот же платёж.
func (c *Client) ChargeSavedMethod(req paymentprovider.ChargeRequest) (*paymentprovider.Payment, error) {
	if req.PaymentMethodID == "" {
		return nil, fmt.Errorf("charge saved method: empty payment method id")
	}
	key := req.IdempotenceKey
	if key == "" {
		var err error
		key, err = NewIdempotenceKey()
		if err != nil {
			return nil, err
		}
	}
	// Без confirmation: подтверждения от пользователя такой платёж не требует.
	body := map[string]any{
		"amount":            map[string]string{"value": fmt.Sprintf("%d.00", req.Amount), "currency": req.Currency},
		"capture":           true,
		"payment_method_id": req.PaymentMethodID,
		"description":       req.Description,
		"metadata":          map[string]string{"local_payment_id": fmt.Sprintf("%d", req.LocalPaymentID)},
	}
	return c.doPayment(http.MethodPost, "/v3/payments", body, key)
}

func (c *Client) GetPayment(id string) (*paymentprovider.Payment, error) {
	return c.doPayment(http.MethodGet, "/v3/payments/"+id, nil, "")
}

func (c *Client) doPayment(method, path string, body any, idempotenceKey string) (*paymentprovider.Payment, error) {
	var payload []byte
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		payload = data
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			time.Sleep(c.retryBackoff * time.Duration(attempt-1))
		}

		data, statusCode, err := c.sendOnce(method, path, payload, idempotenceKey)
		if err != nil {
			lastErr = err
			continue
		}
		// 5xx — сбой на стороне кассы, повтор осмыслен. Остальные неуспешные
		// коды (401, 400, 404) повтором не лечатся: ответ будет тем же.
		if statusCode >= 500 {
			lastErr = fmt.Errorf("yookassa API error %d: %s", statusCode, string(data))
			continue
		}
		if statusCode < 200 || statusCode >= 300 {
			return nil, fmt.Errorf("yookassa API error %d: %s", statusCode, string(data))
		}
		return parsePayment(data)
	}

	return nil, lastErr
}

// sendOnce выполняет одну попытку запроса и отдаёт тело с кодом ответа.
func (c *Client) sendOnce(method, path string, payload []byte, idempotenceKey string) ([]byte, int, error) {
	ctx := context.Background()
	if c.attemptTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.attemptTimeout)
		defer cancel()
	}

	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}
	req.SetBasicAuth(c.shopID, c.secret)
	req.Header.Set("Content-Type", "application/json")
	if idempotenceKey != "" {
		req.Header.Set("Idempotence-Key", idempotenceKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}
	return data, resp.StatusCode, nil
}

func parsePayment(data []byte) (*paymentprovider.Payment, error) {
	var raw struct {
		ID, Status    string
		Amount        struct{ Value, Currency string }
		PaymentMethod struct {
			Type  string `json:"type"`
			ID    string `json:"id"`
			Saved bool   `json:"saved"`
			Title string `json:"title"`
			Card  struct {
				Last4    string `json:"last4"`
				CardType string `json:"card_type"`
			} `json:"card"`
		} `json:"payment_method"`
		CancellationDetails struct {
			Party  string `json:"party"`
			Reason string `json:"reason"`
		} `json:"cancellation_details"`
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
	p := &paymentprovider.Payment{
		ID:                 raw.ID,
		Status:             status(raw.Status),
		Amount:             amount,
		Currency:           raw.Amount.Currency,
		PaymentMethod:      raw.PaymentMethod.Type,
		ConfirmationURL:    raw.Confirmation.URL,
		ExpiresAt:          raw.ExpiresAt,
		RecipientID:        raw.Recipient.AccountID,
		CancellationReason: raw.CancellationDetails.Reason,
		MethodGone:         methodGone(raw.CancellationDetails.Reason),
	}
	// Способ считается сохранённым только когда касса сказала об этом сама:
	// saved: false и пустой id — это «не сохранён», а не «сохранён неизвестно что».
	if raw.PaymentMethod.Saved && raw.PaymentMethod.ID != "" {
		p.SavedMethodID = raw.PaymentMethod.ID
		p.SavedMethodTitle = methodTitle(raw.PaymentMethod.Type, raw.PaymentMethod.Card.Last4, raw.PaymentMethod.Title)
	}
	return p, nil
}

// methodGoneReasons — отказы, означающие, что списывать больше нечем.
// Список намеренно узкий: обычный отказ карты Способ не гасит (Р1), и ошибочно
// погашенный Способ стоит пользователю повторного включения автопродления.
var methodGoneReasons = map[string]bool{
	// Пользователь отозвал разрешение на автоплатежи.
	"permission_revoked": true,
	// Карта просрочена — этим инструментом уже не списать.
	"card_expired": true,
	// Номер карты недействителен.
	"invalid_card_number": true,
	// Способ оплаты запрещён для этого магазина или платежа.
	"payment_method_restricted": true,
}

func methodGone(reason string) bool { return methodGoneReasons[reason] }

// methodTitle — что показать пользователю вместо технического типа способа.
func methodTitle(methodType, last4, apiTitle string) string {
	switch methodType {
	case "bank_card":
		if last4 != "" {
			return "•••• " + last4
		}
		return "Карта"
	case "sbp":
		return "СБП"
	case "yoo_money":
		return "ЮMoney"
	case "sberbank", "sber_pay":
		return "SberPay"
	case "mir_pay":
		return "Mir Pay"
	case "tinkoff_bank", "t_pay":
		return "T-Pay"
	case "alfabank", "alfa_pay":
		return "Alfa Pay"
	}
	if apiTitle != "" {
		return apiTitle
	}
	return methodType
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
