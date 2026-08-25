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

	// SavedMethodID заполняется, только когда касса подтвердила сохранение
	// способа (`payment_method.saved: true`). Пустая строка означает «способ не
	// сохранён», а не «сохранён неизвестно какой».
	SavedMethodID string
	// SavedMethodTitle — что показать пользователю: «•••• 4242» или «СБП».
	SavedMethodTitle string
	// CancellationReason — причина отказа кассы, как её назвал провайдер.
	// Хранится сырой: нужна логам и разбору инцидентов.
	CancellationReason string
	// MethodGone — касса сказала, что способа больше нет. Гасит Способ
	// автосписания, но не согласие пользователя: это разные сущности.
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
	// SavePaymentMethod просит кассу запомнить способ оплаты. Параметр, а не
	// константа: тестовый платёж админа его не ставит, и выключенный рубильник
	// автопродления гасит его целиком.
	SavePaymentMethod bool
}

// ChargeRequest — списание по сохранённому Способу автосписания. Подтверждения
// от пользователя такой платёж не требует и отвечает синхронно.
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

// RecurringProvider умеет списывать по ранее сохранённому способу. Отдельный
// интерфейс, а не метод в Provider: списание по сохранённому способу умеет
// только ЮKassa, и Platega возвращала бы здесь заглушку.
type RecurringProvider interface {
	Provider
	ChargeSavedMethod(ChargeRequest) (*Payment, error)
}
