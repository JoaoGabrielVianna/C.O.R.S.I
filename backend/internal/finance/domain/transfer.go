package domain

// TransferPair is the value object the application returns after creating
// a transfer. It is not a table — the persistence shape is two linked
// finance.transactions rows sharing the same TransferPairID. The pair
// invariant (always exactly two rows, one expense + one income with
// equal amount_cents and equal occurred_at) is enforced by the
// application service; the schema only ensures the link itself.
type TransferPair struct {
	From Transaction `json:"from"`
	To   Transaction `json:"to"`
}
