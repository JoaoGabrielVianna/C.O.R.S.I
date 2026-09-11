package tools

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
	chatports "github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/finance/app"
	"github.com/corsi/backend/internal/finance/domain"
)

// The Finance context-reference resolver.
//
// ── What a reference is for here ───────────────────────────────────────
// Opening a conversation about one transaction, and then talking about it
// the way a person does: "essa compra", "corrige essa", "por que isso caiu
// nessa categoria". Without a reference the user has to describe the row
// well enough for the model to find it again, which for money means
// describing an amount and a date out loud every time.
//
// ── What it does NOT do, and this is the point ─────────────────────────
// It does not return the transaction. It returns the words needed to
// recognise one:
//
//	resolver  →  "mercado"                       (which record this is)
//	tool      →  the amount, the date, the rest  (what it says)
//
// The distinction is what keeps a reference from becoming a capability. An
// agent holding a reference and no grant can see the row's DESCRIPTION —
// which the user just told it by attaching it — and cannot learn the
// amount. Everything beyond the name arrives through
// finance.transaction.get, which is refused without authorization.
//
// ── Hydration: the same service, a different question ──────────────────
//
//	Resolve   →  identity + STABLE recognition text.  Persisted. Ungated.
//	Hydrate   →  MUTABLE state, read fresh every turn. Never persisted.
//	             Gated on the agent's grant for TransactionGetTool.
//
// Nothing here decides whether the agent may read state. Agents checks the
// grant before Hydrate is reached, which is why this file may call the same
// application service the tool calls without that being a second, quieter
// door into the same data.

type referenceResolver struct {
	svc *app.Service
	loc *time.Location
}

// NewReferenceResolver builds the resolver, ready for the reference
// registry in cmd/corsi.
func NewReferenceResolver(svc *app.Service, loc *time.Location) chatports.ContextReferenceResolver {
	return referenceResolver{svc: svc, loc: loc}
}

// TransactionReferenceType is the one type this module owns today.
//
// One and not four. A category, a card or a person is a dimension a
// question is sliced by, not a subject a conversation is about — "corrige
// essa" is said of a transaction and of nothing else in this domain. A
// reference type that no sentence would ever attach is a surface with no
// caller.
const TransactionReferenceType chatdomain.ContextReferenceType = "finance.transaction"

func (referenceResolver) Types() []chatdomain.ContextReferenceType {
	return []chatdomain.ContextReferenceType{TransactionReferenceType}
}

// Resolve looks one transaction up for one workspace.
//
// ── Why a bad id is `found=false` and not an error ─────────────────────
// The three ways an id can fail to name something this workspace owns —
// malformed, never existed, someone else's — are one answer on purpose.
// Reporting them apart would let a fabricated id probe for the existence of
// rows in other workspaces, which is exactly the attack the single answer
// closes. An `error` is kept for a genuine failure to ask, such as the
// database being unreachable, because that is not a fact about the id.
func (r referenceResolver) Resolve(
	ctx context.Context,
	workspaceID uuid.UUID,
	ref chatdomain.ContextReference,
) (chatports.ResolvedReference, bool, error) {
	if ref.Type != TransactionReferenceType {
		return chatports.ResolvedReference{}, false, nil
	}
	id, err := uuid.Parse(strings.TrimSpace(ref.ID))
	if err != nil {
		return chatports.ResolvedReference{}, false, nil
	}

	tx, err := r.svc.GetTransaction(ctx, workspaceID, id)
	if err != nil {
		var de *domain.Error
		if errors.As(err, &de) && de.Kind == domain.KindNotFound {
			return chatports.ResolvedReference{}, false, nil
		}
		return chatports.ResolvedReference{}, false, err
	}

	// ── Why there is no subtitle, and why the AMOUNT is not in the label ──
	// A label and a subtitle are written ONCE, at attach time, into a
	// column that is never rewritten. So they may carry only facts that do
	// not move. Job Radar learned this expensively: it put the stage in the
	// subtitle, and a conversation opened at `applied` kept telling the
	// model `applied` long after the opportunity reached `interview`.
	//
	// A transaction's amount is the single most likely thing to move in
	// this very conversation — "na verdade eram 79,90" is the reason the
	// reference is attached at all. Writing R$ 89,90 into a permanent label
	// would put the OLD figure in front of the model on every future turn,
	// beside the corrected one from hydration, and the two would disagree
	// with nothing saying which is current. The date and the status have the
	// same problem for the same reason.
	//
	// What is left is the description, which is what a person calls the row.
	// It can be corrected too, but it is the only field whose job is
	// recognition, and an empty label would make the attachment unreadable.
	// So: description as the label, nothing as the subtitle, and every
	// mutable fact — the amount above all — reaches the model through
	// hydration, freshly, on the turn it matters.
	label := strings.TrimSpace(tx.Description)
	if label == "" {
		// A row with no description is still attachable. Naming it by its
		// category beats an empty chip the user cannot identify, and the
		// category of a row that has no description is as stable as
		// anything here gets.
		if cat, err := r.svc.GetCategory(ctx, workspaceID, tx.CategoryID); err == nil {
			label = cat.Name
		} else {
			label = "transação"
		}
	}
	return chatports.ResolvedReference{Label: label}, true, nil
}

/* ── hydration ───────────────────────────────────────────────────────── */

// HydrationPolicy declares how this module's entities stay fresh.
//
// ── Why per_turn for a transaction ─────────────────────────────────────
// The two properties the design asks for, and this entity has both in the
// strongest form in the system. It MOVES: the whole point of talking about
// a transaction is usually to correct it, and the correction lands mid
// conversation. And it is TINY: an amount, a date, three words and three
// enum values — under two hundred characters, so a turn's cost cannot run
// away no matter how long the conversation gets.
//
// The failure this closes is the expensive one in this module. The model
// reads R$ 89,90, says so, the user corrects it to R$ 79,90, and three
// turns later the model's own earlier sentence — carrying 89,90 — is the
// strongest evidence in its context. Without a fresh reading it answers
// from that, and the user is told a number that is not in their database.
//
// ── Why the capability is the get tool ─────────────────────────────────
// Because hydration reads exactly what finance.transaction.get reads, from
// the same service, and nothing an agent could not have read by calling it.
// Naming any other capability would be claiming the read is a different
// kind of access than it is; naming none would be asking to skip the check.
func (referenceResolver) HydrationPolicy(t chatdomain.ContextReferenceType) (chatports.ReferenceHydrationPolicy, bool) {
	if t != TransactionReferenceType {
		return chatports.ReferenceHydrationPolicy{}, false
	}
	return chatports.ReferenceHydrationPolicy{
		Freshness:  chatports.FreshnessPerTurn,
		Capability: TransactionGetTool,
	}, true
}

// Hydrate reads one transaction's present state.
//
// The same rule Resolve follows for a bad id: malformed, never existed and
// another workspace's are one answer, so a fabricated id cannot be used to
// probe for rows this workspace may not see. Note that the id reaching here
// was admitted for this workspace at attach time — this is the second check
// on the same door, because a workspace's access can be true when a
// reference is stored and false later.
func (r referenceResolver) Hydrate(
	ctx context.Context,
	workspaceID uuid.UUID,
	ref chatdomain.ContextReference,
) (chatports.ReferenceState, bool, error) {
	if ref.Type != TransactionReferenceType {
		return chatports.ReferenceState{}, false, nil
	}
	id, err := uuid.Parse(strings.TrimSpace(ref.ID))
	if err != nil {
		return chatports.ReferenceState{}, false, nil
	}

	tx, err := r.svc.GetTransaction(ctx, workspaceID, id)
	if err != nil {
		var de *domain.Error
		if errors.As(err, &de) && de.Kind == domain.KindNotFound {
			// A transaction the conversation is about can be REMOVED during
			// that conversation — by this very agent, when the user asks. The
			// reference then stops hydrating, which is the honest state: the
			// alternative is a turn asserting the present values of a record
			// that no longer exists.
			return chatports.ReferenceState{}, false, nil
		}
		return chatports.ReferenceState{}, false, err
	}

	fields := []chatports.ReferenceStateField{
		// Restated beside the state so the model never has to correlate two
		// lists by label.
		{Name: "description", Value: tx.Description},
		// The amount both ways, for the reason money.go gives: the integer
		// is what every capability takes, the rendering is what makes a
		// wrong reading visible in the sentence that reports it.
		{Name: "amount", Value: formatCents(tx.AmountCents)},
		{Name: "amount_cents", Value: itoa64(tx.AmountCents)},
		{Name: "type", Value: string(tx.Type)},
		{Name: "status", Value: string(tx.Status)},
		{Name: "occurred_on", Value: tx.OccurredAt.In(r.loc).Format(dateLayout)},
		{Name: "payment_method", Value: string(tx.PaymentMethod)},
	}
	if cat, err := r.svc.GetCategory(ctx, workspaceID, tx.CategoryID); err == nil {
		fields = append(fields, chatports.ReferenceStateField{Name: "category", Value: cat.Name})
	}
	// Structural role, stated only when it is true. A model asked to correct
	// an amount on a transfer leg will be refused by the tool; being told
	// beforehand is the difference between explaining the situation and
	// reporting a failure.
	if tx.TransferPairID != nil {
		fields = append(fields, chatports.ReferenceStateField{
			Name:  "structure",
			Value: "one leg of a transfer between the user's own accounts",
		})
	}
	if tx.PlanID != nil && tx.InstallmentNumber != nil {
		fields = append(fields, chatports.ReferenceStateField{
			Name:  "structure",
			Value: "installment " + itoa(*tx.InstallmentNumber) + " of a purchase plan",
		})
	}
	return chatports.ReferenceState{Fields: fields}, true, nil
}

func itoa64(n int64) string { return itoa(int(n)) }
