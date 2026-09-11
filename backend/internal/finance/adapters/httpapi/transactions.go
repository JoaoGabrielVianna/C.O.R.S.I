package httpapi

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/finance/app"
	"github.com/corsi/backend/internal/finance/domain"
	"github.com/corsi/backend/internal/finance/ports"
	"github.com/corsi/backend/internal/platform/apierror"
	"github.com/corsi/backend/internal/platform/render"
)

type createTxReq struct {
	AccountID     *uuid.UUID                `json:"account_id,omitempty"`
	CategoryID    uuid.UUID                 `json:"category_id"`
	PersonID      *uuid.UUID                `json:"person_id,omitempty"`
	AmountCents   int64                     `json:"amount_cents"`
	Description   string                    `json:"description"`
	OccurredAt    time.Time                 `json:"occurred_at"`
	Status        *domain.TransactionStatus `json:"status,omitempty"`
	PaymentMethod *domain.PaymentMethod     `json:"payment_method,omitempty"`
	Source        *domain.TransactionSource `json:"source,omitempty"`
	Notes         *string                   `json:"notes,omitempty"`
}

func (h *Handler) createTransaction(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	var req createTxReq
	if err := render.DecodeJSON(r, &req); err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "malformed_body", err.Error()))
		return
	}
	t, err := h.svc.CreateTransaction(r.Context(), app.CreateTransactionInput{
		WorkspaceID:   wsID,
		AccountID:     req.AccountID,
		CategoryID:    req.CategoryID,
		PersonID:      req.PersonID,
		AmountCents:   req.AmountCents,
		Description:   req.Description,
		OccurredAt:    req.OccurredAt,
		Status:        req.Status,
		PaymentMethod: req.PaymentMethod,
		Source:        req.Source,
		Notes:         req.Notes,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusCreated, t)
}

// updateTxReq uses pointers so omitted fields stay untouched, and explicit
// *_clear flags so the API can unset nullable foreign keys.
type updateTxReq struct {
	AccountID      *uuid.UUID                `json:"account_id,omitempty"`
	ClearAccountID bool                      `json:"clear_account_id,omitempty"`
	CategoryID     *uuid.UUID                `json:"category_id,omitempty"`
	PersonID       *uuid.UUID                `json:"person_id,omitempty"`
	ClearPersonID  bool                      `json:"clear_person_id,omitempty"`
	AmountCents    *int64                    `json:"amount_cents,omitempty"`
	Description    *string                   `json:"description,omitempty"`
	OccurredAt     *time.Time                `json:"occurred_at,omitempty"`
	Status         *domain.TransactionStatus `json:"status,omitempty"`
	PaymentMethod  *domain.PaymentMethod     `json:"payment_method,omitempty"`
	Source         *domain.TransactionSource `json:"source,omitempty"`
	Notes          *string                   `json:"notes,omitempty"`
}

func (h *Handler) updateTransaction(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid_id", "id must be a uuid"))
		return
	}
	var req updateTxReq
	if err := render.DecodeJSON(r, &req); err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "malformed_body", err.Error()))
		return
	}
	t, err := h.svc.UpdateTransaction(r.Context(), app.UpdateTransactionInput{
		WorkspaceID:    wsID,
		ID:             id,
		AccountID:      req.AccountID,
		ClearAccountID: req.ClearAccountID,
		CategoryID:     req.CategoryID,
		PersonID:       req.PersonID,
		ClearPersonID:  req.ClearPersonID,
		AmountCents:    req.AmountCents,
		Description:    req.Description,
		OccurredAt:     req.OccurredAt,
		Status:         req.Status,
		PaymentMethod:  req.PaymentMethod,
		Source:         req.Source,
		Notes:          req.Notes,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, t)
}

func (h *Handler) deleteTransaction(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid_id", "id must be a uuid"))
		return
	}
	if err := h.svc.DeleteTransaction(r.Context(), wsID, id); err != nil {
		h.writeDomainErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) getTransaction(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid_id", "id must be a uuid"))
		return
	}
	t, err := h.svc.GetTransaction(r.Context(), wsID, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, t)
}

func (h *Handler) listTransactions(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	limit, offset := parseLimitOffset(r)
	q := r.URL.Query()
	in := app.ListTransactionsInput{WorkspaceID: wsID, Limit: limit, Offset: offset}
	if v := q.Get("type"); v != "" {
		et := domain.EntryType(v)
		if !et.Valid() {
			apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid", "type must be income or expense"))
			return
		}
		in.Type = &et
	}
	if v := q.Get("category"); v != "" {
		cid, err := uuid.Parse(v)
		if err != nil {
			apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid", "category must be a uuid"))
			return
		}
		in.CategoryID = &cid
	}
	if v := q.Get("status"); v != "" {
		s := domain.TransactionStatus(v)
		if !s.Valid() {
			apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid", "status must be paid, pending or scheduled"))
			return
		}
		in.Status = &s
	}
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid", "from must be RFC3339"))
			return
		}
		in.From = &t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid", "to must be RFC3339"))
			return
		}
		in.To = &t
	}
	// Cursor pagination is additive. `?cursor=` and `?offset=` are mutually
	// exclusive — sending both is a client bug.
	cursorRaw := q.Get("cursor")
	if cursorRaw != "" {
		if q.Get("offset") != "" {
			apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid", "cursor and offset are mutually exclusive"))
			return
		}
		c, err := app.DecodeCursor(cursorRaw)
		if err != nil {
			apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid_cursor", err.Error()))
			return
		}
		in.CursorOccurredAt = &c.OccurredAt
		in.CursorID = &c.ID
		// When the client sends a cursor, the offset they may have already
		// passed becomes meaningless; ensure the repo branches on cursor.
		in.Offset = 0
	}
	res, err := h.svc.ListTransactions(r.Context(), in)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, cursoredEnvelope{
		Items:      res.Items,
		Limit:      limit,
		Offset:     offset,
		NextCursor: res.NextCursor,
	})
}

// cursoredEnvelope is the transactions list response envelope.
// `next_cursor` is omitted when nil, so non-cursor (offset-mode) clients
// don't see a foreign field in their response.
type cursoredEnvelope struct {
	Items      any     `json:"items"`
	Limit      int     `json:"limit"`
	Offset     int     `json:"offset"`
	NextCursor *string `json:"next_cursor,omitempty"`
}

// getTransactionTotals serves the read-side aggregations the dashboards
// need without iterating every transaction in the page-cap.
func (h *Handler) getTransactionTotals(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	fromS, toS := q.Get("from"), q.Get("to")
	if fromS == "" || toS == "" {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid", "from and to are required RFC3339 timestamps"))
		return
	}
	from, err := time.Parse(time.RFC3339, fromS)
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid", "from must be RFC3339"))
		return
	}
	to, err := time.Parse(time.RFC3339, toS)
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid", "to must be RFC3339"))
		return
	}
	totals, err := h.svc.GetTransactionTotals(r.Context(), app.GetTransactionTotalsInput{
		WorkspaceID: wsID, From: from, To: to,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, totalsResponse{
		Window: totalsWindow{From: totals.From, To: totals.To},
		ByStatus: map[string]totalsBucket{
			"paid":      bucketDTO(totals.ByStatus[domain.TransactionStatusPaid]),
			"pending":   bucketDTO(totals.ByStatus[domain.TransactionStatusPending]),
			"scheduled": bucketDTO(totals.ByStatus[domain.TransactionStatusScheduled]),
		},
		ByRealization: map[string]totalsBucket{
			"realized":  bucketDTO(totals.ByRealization[ports.RealizationRealized]),
			"projected": bucketDTO(totals.ByRealization[ports.RealizationProjected]),
			"excluded":  bucketDTO(totals.ByRealization[ports.RealizationExcluded]),
		},
		ByCategory: categoryDTOs(totals.ByCategory),
		ByMethod:   methodDTOs(totals.ByMethod),
	})
}

// totalsResponse mirrors the OpenAPI TransactionTotals schema. `by_status`
// stays for filter-driven callers; `by_realization` is the canonical
// dashboard grouping defined in docs/totals-contract.md — every new
// consumer must read realized/projected/excluded instead of paid/pending.
type totalsResponse struct {
	Window        totalsWindow            `json:"window"`
	ByStatus      map[string]totalsBucket `json:"by_status"`
	ByRealization map[string]totalsBucket `json:"by_realization"`
	ByCategory    []categoryTotalDTO      `json:"by_category"`
	ByMethod      []methodTotalDTO        `json:"by_method"`
}

type totalsWindow struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

type totalsBucket struct {
	IncomeCents  int64 `json:"income_cents"`
	ExpenseCents int64 `json:"expense_cents"`
	Count        int64 `json:"count"`
}

type categoryTotalDTO struct {
	CategoryID string `json:"category_id"`
	Type       string `json:"type"`
	TotalCents int64  `json:"total_cents"`
	Count      int64  `json:"count"`
}

type methodTotalDTO struct {
	PaymentMethod string `json:"payment_method"`
	TotalCents    int64  `json:"total_cents"`
	Count         int64  `json:"count"`
}

func bucketDTO(b ports.TotalsBucket) totalsBucket {
	return totalsBucket{
		IncomeCents:  b.IncomeCents,
		ExpenseCents: b.ExpenseCents,
		Count:        b.Count,
	}
}

func categoryDTOs(in []ports.CategoryTotal) []categoryTotalDTO {
	out := make([]categoryTotalDTO, len(in))
	for i, c := range in {
		out[i] = categoryTotalDTO{
			CategoryID: c.CategoryID.String(),
			Type:       string(c.Type),
			TotalCents: c.TotalCents,
			Count:      c.Count,
		}
	}
	return out
}

func methodDTOs(in []ports.MethodTotal) []methodTotalDTO {
	out := make([]methodTotalDTO, len(in))
	for i, m := range in {
		out[i] = methodTotalDTO{
			PaymentMethod: string(m.PaymentMethod),
			TotalCents:    m.TotalCents,
			Count:         m.Count,
		}
	}
	return out
}
