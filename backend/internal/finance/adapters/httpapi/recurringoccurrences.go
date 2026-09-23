package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/corsi/backend/internal/finance/app"
	"github.com/corsi/backend/internal/finance/domain"
	"github.com/corsi/backend/internal/platform/apierror"
	"github.com/corsi/backend/internal/platform/render"
)

// Monthly commitment over HTTP.
//
// ── Why there is no occurrence CRUD here ───────────────────────────────
// No POST that creates one, no DELETE that removes one, and no route that
// takes an occurrence's own id. An occurrence is MATERIALISED by reading
// the month it belongs to, and a month that happened does not stop having
// happened. Offering a create would be offering a second way to put a
// figure in a month, free to disagree with the rules the service applies;
// offering a delete would be offering a way to make last March's total
// quietly different.
//
// So the mutable surface is three verbs on a row that already exists:
// settle it, unsettle it, and say what it actually cost.
//
// ── The route shape, and the one thing it must not be ──────────────────
// `/{entryID}/occurrences/{period}/pay` addresses a month of an obligation
// by the two things a caller can know. There is no `PUT .../paid` taking a
// boolean and no `/toggle`: a toggle's meaning depends on a state the
// caller cannot see, so a retried request undoes the first one. POST pays
// and DELETE unpays, both idempotent.
//
// ── Workspace ──────────────────────────────────────────────────────────
// Never in a body, never in a query argument. It comes from the middleware,
// like every other Finance route, so there is no field through which a
// request could propose somebody else's money.

/* ── wire shapes ─────────────────────────────────────────────────────── */

type occurrenceResp struct {
	RecurringEntryID string `json:"recurring_entry_id"`
	// OccurrenceID is absent on a projected month: those rows do not
	// exist, and handing back an id that resolves to nothing would invite
	// a caller to address it.
	OccurrenceID    string  `json:"occurrence_id,omitempty"`
	Period          string  `json:"period"`
	Description     string  `json:"description"`
	CategoryID      string  `json:"category_id,omitempty"`
	Category        string  `json:"category,omitempty"`
	Direction       string  `json:"direction,omitempty"`
	DueOn           string  `json:"due_on"`
	AmountCents     int64   `json:"amount_cents"`
	AmountEstimated bool    `json:"amount_estimated"`
	Status          string  `json:"status"`
	PaidOn          *string `json:"paid_on,omitempty"`
	Overdue         bool    `json:"overdue"`
}

type unplaceableResp struct {
	RecurringEntryID string `json:"recurring_entry_id"`
	Description      string `json:"description"`
	Reason           string `json:"reason"`
}

// commitmentResp is the whole month.
//
// Totals are SERVER-COMPUTED and the client must not re-derive them: an
// annual premium counted at face value in the wrong month, or a partial
// page summed as if complete, are exactly the mistakes this module has
// already had once in the browser.
type commitmentResp struct {
	Period       string `json:"period"`
	IsProjection bool   `json:"is_projection"`
	Today        string `json:"today"`
	TimeZone     string `json:"time_zone"`

	CommittedCents int64 `json:"committed_cents"`
	PaidCents      int64 `json:"paid_cents"`
	RemainingCents int64 `json:"remaining_cents"`
	EstimatedCents int64 `json:"estimated_cents"`

	OccurrenceCount int `json:"occurrence_count"`
	PaidCount       int `json:"paid_count"`
	PendingCount    int `json:"pending_count"`
	EstimatedCount  int `json:"estimated_count"`
	OverdueCount    int `json:"overdue_count"`

	Items []occurrenceResp `json:"items"`
	// Unplaceable is non-empty when a definition could not be put in a
	// month. The totals are then INCOMPLETE and a screen has to say so.
	Unplaceable []unplaceableResp `json:"unplaceable,omitempty"`
}

const dayLayout = "2006-01-02"

func occurrenceFromLine(l app.MonthlyCommitmentLine) occurrenceResp {
	o := l.Occurrence
	out := occurrenceResp{
		RecurringEntryID: l.RecurringEntryID.String(),
		Period:           o.Period.String(),
		Description:      l.Description,
		Category:         l.CategoryName,
		Direction:        string(l.Direction),
		DueOn:            o.DueOn.Format(dayLayout),
		AmountCents:      o.AmountCents,
		AmountEstimated:  o.AmountEstimated,
		Status:           string(o.Status),
		Overdue:          l.Overdue,
	}
	if o.ID != uuid.Nil {
		out.OccurrenceID = o.ID.String()
	}
	if l.CategoryID != uuid.Nil {
		out.CategoryID = l.CategoryID.String()
	}
	if o.PaidAt != nil {
		d := o.PaidAt.In(time.UTC).Format(dayLayout)
		out.PaidOn = &d
	}
	return out
}

func commitmentFromView(v app.MonthlyCommitmentView) commitmentResp {
	t := v.Totals
	out := commitmentResp{
		Period:          t.Period.String(),
		IsProjection:    t.Projection,
		Today:           v.Today.Format(dayLayout),
		TimeZone:        v.TimeZone,
		CommittedCents:  t.CommittedCents,
		PaidCents:       t.PaidCents,
		RemainingCents:  t.RemainingCents,
		EstimatedCents:  t.EstimatedCents,
		OccurrenceCount: t.Count,
		PaidCount:       t.PaidCount,
		PendingCount:    t.PendingCount,
		EstimatedCount:  t.EstimatedCount,
		OverdueCount:    t.OverdueCount,
		Items:           make([]occurrenceResp, 0, len(v.Lines)),
	}
	for _, l := range v.Lines {
		out.Items = append(out.Items, occurrenceFromLine(l))
	}
	for _, u := range v.Unplaceable {
		out.Unplaceable = append(out.Unplaceable, unplaceableResp{
			RecurringEntryID: u.RecurringEntryID.String(),
			Description:      u.Description,
			Reason:           string(u.Reason),
		})
	}
	return out
}

/* ── GET /finance/recurring-entries/commitment ───────────────────────── */

// getMonthlyCommitment answers "how is this month going".
//
// `period` is optional. Omitted, the CURRENT month is resolved from the
// database clock in the reporting zone — never from the caller's, because
// a browser open on a laptop set to another country would otherwise cut a
// different month than the one the server would settle a payment into.
func (h *Handler) getMonthlyCommitment(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	var period domain.Period
	if raw := strings.TrimSpace(r.URL.Query().Get("period")); raw != "" {
		p, err := domain.ParsePeriod(raw)
		if err != nil {
			h.writeDomainErr(w, err)
			return
		}
		period = p
	}
	view, err := h.svc.GetMonthlyCommitment(r.Context(), app.GetMonthlyCommitmentInput{
		WorkspaceID: wsID, Period: period,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, commitmentFromView(view))
}

/* ── the three writes ────────────────────────────────────────────────── */

// occurrenceRef reads the (entry, period) pair out of the path.
//
// Both halves are path parameters rather than a body, because they identify
// WHICH row the verb applies to. A body carrying an identity is a body that
// can disagree with the URL it was posted to.
func (h *Handler) occurrenceRef(w http.ResponseWriter, r *http.Request) (app.OccurrenceRef, bool) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return app.OccurrenceRef{}, false
	}
	entryID, err := parseUUIDParam(r, "entryID")
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid_id", "entryID must be a uuid"))
		return app.OccurrenceRef{}, false
	}
	period, err := domain.ParsePeriod(chi.URLParam(r, "period"))
	if err != nil {
		h.writeDomainErr(w, err)
		return app.OccurrenceRef{}, false
	}
	return app.OccurrenceRef{WorkspaceID: wsID, RecurringEntryID: entryID, Period: period}, true
}

// payOccurrence settles one month. Idempotent: paying an already-paid month
// succeeds and changes nothing, including its recorded payment date.
func (h *Handler) payOccurrence(w http.ResponseWriter, r *http.Request) {
	ref, ok := h.occurrenceRef(w, r)
	if !ok {
		return
	}
	o, err := h.svc.MarkOccurrencePaid(r.Context(), ref)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, o)
}

// unpayOccurrence undoes a mistaken tick. Idempotent in the same way, and
// it never touches the ledger or the amount.
func (h *Handler) unpayOccurrence(w http.ResponseWriter, r *http.Request) {
	ref, ok := h.occurrenceRef(w, r)
	if !ok {
		return
	}
	o, err := h.svc.UnmarkOccurrencePaid(r.Context(), ref)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, o)
}

// patchOccurrenceReq is the occurrence-specific mutable surface, and it is
// deliberately one field.
//
// Not a general update: period and due_on are identity and frozen history,
// and status has its own two verbs because a boolean here would be a toggle
// wearing a different hat.
type patchOccurrenceReq struct {
	AmountCents *int64 `json:"amount_cents,omitempty"`
}

func (h *Handler) patchOccurrence(w http.ResponseWriter, r *http.Request) {
	ref, ok := h.occurrenceRef(w, r)
	if !ok {
		return
	}
	var req patchOccurrenceReq
	if err := render.DecodeJSON(r, &req); err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "malformed_body", err.Error()))
		return
	}
	if req.AmountCents == nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid",
			"amount_cents is required"))
		return
	}
	o, err := h.svc.SetOccurrenceAmount(r.Context(), app.SetOccurrenceAmountInput{
		OccurrenceRef: ref, AmountCents: *req.AmountCents,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, o)
}
