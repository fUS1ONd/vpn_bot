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

	// SavedMethodID непуст, только когда касса подтвердила сохранение способа.
	SavedMethodID    string
	SavedMethodTitle string // «•••• 4242» или «СБП»
	// CancellationReason — сырая причина отказа: нужна логам и разбору инцидентов.
	CancellationReason string
	// MethodGone — способа у кассы больше нет. Гасит Способ, но не согласие.
	MethodGone bool
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
	// SavePaymentMethod просит кассу запомнить способ. Параметр, а не константа:
	// тестовый платёж его не ставит, выключенный рубильник гасит целиком.
	SavePaymentMethod bool
}

// ChargeRequest — списание по сохранённому Способу: подтверждения от
// пользователя не требует, отвечает синхронно.
type ChargeRequest struct {
	Amount          int
	Currency        string
	Description     string
	LocalPaymentID  int64
	IdempotenceKey  string
	PaymentMethodID string
}

// Provider creates a redirect payment and retrieves its authoritative state.
type Provider interface {
	Name() string
	CreatePayment(CreateRequest) (*Payment, error)
	GetPayment(string) (*Payment, error)
}

// RecurringProvider умеет списывать по сохранённому способу. Отдельный
// интерфейс, а не метод в Provider: Platega возвращала бы здесь заглушку.
type RecurringProvider interface {
	Provider
	ChargeSavedMethod(ChargeRequest) (*Payment, error)
}
