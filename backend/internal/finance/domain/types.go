// Package domain holds entities, value objects, and domain errors for the
// finance bounded context. It depends only on the stdlib and uuid; nothing
// here imports HTTP, SQL, or any platform/adapter package.
package domain

type EntryType string

const (
	EntryTypeIncome  EntryType = "income"
	EntryTypeExpense EntryType = "expense"
)

func (t EntryType) Valid() bool {
	return t == EntryTypeIncome || t == EntryTypeExpense
}

type CardNetwork string

const (
	CardNetworkVisa       CardNetwork = "visa"
	CardNetworkMastercard CardNetwork = "mastercard"
	CardNetworkElo        CardNetwork = "elo"
	CardNetworkAmex       CardNetwork = "amex"
)

func (n CardNetwork) Valid() bool {
	switch n {
	case CardNetworkVisa, CardNetworkMastercard, CardNetworkElo, CardNetworkAmex:
		return true
	}
	return false
}

type TransactionStatus string

const (
	TransactionStatusPaid      TransactionStatus = "paid"
	TransactionStatusPending   TransactionStatus = "pending"
	TransactionStatusScheduled TransactionStatus = "scheduled"
)

func (s TransactionStatus) Valid() bool {
	switch s {
	case TransactionStatusPaid, TransactionStatusPending, TransactionStatusScheduled:
		return true
	}
	return false
}

type PaymentMethod string

const (
	PaymentMethodDebit    PaymentMethod = "debit"
	PaymentMethodCredit   PaymentMethod = "credit"
	PaymentMethodPix      PaymentMethod = "pix"
	PaymentMethodCash     PaymentMethod = "cash"
	PaymentMethodTransfer PaymentMethod = "transfer"
)

func (m PaymentMethod) Valid() bool {
	switch m {
	case PaymentMethodDebit, PaymentMethodCredit, PaymentMethodPix, PaymentMethodCash, PaymentMethodTransfer:
		return true
	}
	return false
}

type TransactionSource string

const (
	TransactionSourceManual   TransactionSource = "manual"
	TransactionSourceWhatsApp TransactionSource = "whatsapp"
	TransactionSourceImport   TransactionSource = "import"
	TransactionSourceAI       TransactionSource = "ai"
)

func (s TransactionSource) Valid() bool {
	switch s {
	case TransactionSourceManual, TransactionSourceWhatsApp, TransactionSourceImport, TransactionSourceAI:
		return true
	}
	return false
}
