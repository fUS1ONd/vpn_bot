// Package paymentprovider contains the provider-neutral payment contract used by the bot.
package paymentprovider

import "time"

const (
	Platega  = "platega"
	YooKassa = "yookassa"

	StatusPending      = "pending"
	StatusSucceeded    = "succeeded"
	StatusCanceled     = "canceled"
	StatusChargebacked = "chargebacked"
)

// Payment is the verified state returned by a payment provider.
type Payment struct {
	ID              string
	Status          string
	Amount          int
	Currency        string
	PaymentMethod   string
	ConfirmationURL string
	ExpiresAt       *time.Time
	RecipientID     string
}

// CreateRequest carries only server-controlled data to a provider.
type CreateRequest struct {
	Amount         int
	Currency       string
	Description    string
	ReturnURL      string
	CallbackURL    string
	LocalPaymentID int64
	IdempotenceKey string
}

// Provider creates a redirect payment and retrieves its authoritative state.
type Provider interface {
	Name() string
	CreatePayment(CreateRequest) (*Payment, error)
	GetPayment(string) (*Payment, error)
}
