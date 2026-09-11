package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// ImportSource is where a statement came from, as a row this database
// issues rather than a string a caller composes.
//
// ── The defect it closes ───────────────────────────────────────────────
// external_source is half of the identity duplicate detection matches on.
// It used to be free text supplied by the model, and a live run produced
// two spellings of the same card in consecutive turns: "nubank-4242" and
// "nubank-cartao-4242". The same statement, carrying the same bank
// identifiers, imported twice.
//
// Idempotence cannot rest on a language model reproducing a string
// byte-for-byte across turns. It demonstrably does not.
//
// ── Why not finance.Card ───────────────────────────────────────────────
// Because a statement is not always a card. A bank account has no last4 in
// the card sense, no limit and no closing day, and pushing one into Card
// would make Card mean "any account" — a quiet remodelling that leaves a
// table lying about itself. A source MAY point at a card; it may not BE one.
type ImportSourceKind string

const (
	ImportSourceCard        ImportSourceKind = "card"
	ImportSourceBankAccount ImportSourceKind = "bank_account"
	ImportSourceOther       ImportSourceKind = "other"
)

func (k ImportSourceKind) Valid() bool {
	switch k {
	case ImportSourceCard, ImportSourceBankAccount, ImportSourceOther:
		return true
	}
	return false
}

type ImportSource struct {
	ID          uuid.UUID        `json:"id"`
	WorkspaceID uuid.UUID        `json:"workspace_id"`
	Kind        ImportSourceKind `json:"kind"`
	Institution string           `json:"institution"`
	Label       string           `json:"label"`
	Last4       *string          `json:"last4,omitempty"`
	CardID      *uuid.UUID       `json:"card_id,omitempty"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
	DeletedAt   *time.Time       `json:"deleted_at,omitempty"`
}

// ExternalSourceNamespace is the string that lands in
// transactions.external_source for everything imported through this source.
//
// ── Why it is derived and not stored ───────────────────────────────────
// Because then there is nothing to disagree with. It is a pure function of
// the row's id, so no field a person can edit — the label, the institution,
// a last4 filled in later — can change what an already-imported transaction
// is matched against. Renaming a source must never make its history look
// new, and this is what makes that structural instead of a rule someone has
// to remember.
//
// No caller supplies it and no schema property accepts it.
func (s ImportSource) ExternalSourceNamespace() string {
	return ImportNamespaceFor(s.ID)
}

// ImportNamespaceFor is the same derivation, by id.
//
// 14 characters plus a 36-character uuid is 50, inside the 60 the
// transactions.external_source CHECK allows.
func ImportNamespaceFor(id uuid.UUID) string {
	return "import-source:" + id.String()
}

func (s *ImportSource) Validate() error {
	if s.WorkspaceID == uuid.Nil {
		return Invalid("workspace_id required")
	}
	if !s.Kind.Valid() {
		return Invalid("kind must be card, bank_account or other")
	}
	s.Institution = strings.TrimSpace(s.Institution)
	s.Label = strings.TrimSpace(s.Label)
	if n := len(s.Institution); n < 1 || n > 80 {
		return Invalid("institution must be 1..80 characters")
	}
	if n := len(s.Label); n < 1 || n > 80 {
		return Invalid("label must be 1..80 characters")
	}
	if s.Last4 != nil {
		v := strings.TrimSpace(*s.Last4)
		if v == "" {
			s.Last4 = nil
		} else {
			if len(v) != 4 {
				return Invalid("last4 must be exactly four digits")
			}
			for _, r := range v {
				if r < '0' || r > '9' {
					return Invalid("last4 must be exactly four digits")
				}
			}
			s.Last4 = &v
		}
	}
	return nil
}
