package platega

import (
	"fmt"
	"strconv"
	"time"

	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
)

// Provider adapts the legacy Platega API to the neutral payment contract.
type Provider struct{ client *Client }

func NewProvider(client *Client) *Provider { return &Provider{client: client} }
func (p *Provider) Name() string           { return paymentprovider.Platega }
func (p *Provider) CreatePayment(req paymentprovider.CreateRequest) (*paymentprovider.Payment, error) {
	resp, err := p.client.CreatePayment(CreateTransactionRequest{
		PaymentMethod: PaymentMethodCrypto, Amount: req.Amount, Currency: req.Currency,
		Description: req.Description, ReturnURL: req.ReturnURL, FailedURL: req.ReturnURL,
		CallbackURL: req.CallbackURL, Payload: strconv.FormatInt(req.LocalPaymentID, 10),
	})
	if err != nil {
		return nil, err
	}
	var expires *time.Time
	if resp.ExpiresIn > 0 {
		t := time.Now().UTC().Add(resp.ExpiresIn)
		expires = &t
	}
	return &paymentprovider.Payment{ID: resp.TransactionID, Status: normalizeStatus(resp.Status), Amount: req.Amount, Currency: req.Currency, PaymentMethod: "crypto", ConfirmationURL: resp.Redirect, ExpiresAt: expires}, nil
}
func (p *Provider) GetPayment(id string) (*paymentprovider.Payment, error) {
	status, err := p.client.GetTransactionStatus(id)
	if err != nil {
		return nil, err
	}
	return &paymentprovider.Payment{ID: status.ID, Status: normalizeStatus(status.Status), Amount: int(status.PaymentDetails.Amount), Currency: status.PaymentDetails.Currency, PaymentMethod: status.PaymentMethod}, nil
}
func normalizeStatus(s string) string {
	switch s {
	case StatusConfirmed, StatusManualConfirmed:
		return paymentprovider.StatusSucceeded
	case StatusCanceled:
		return paymentprovider.StatusCanceled
	case StatusChargebacked:
		return paymentprovider.StatusChargebacked
	case StatusPending:
		return paymentprovider.StatusPending
	default:
		return fmt.Sprintf("%s", s)
	}
}
