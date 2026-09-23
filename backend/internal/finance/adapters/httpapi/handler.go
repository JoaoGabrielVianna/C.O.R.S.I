// Package httpapi is the driving adapter that exposes the finance bounded
// context over HTTP. Handlers parse DTOs, call the application service, and
// translate domain errors into HTTP error responses.
//
// Wire shape:
//
//	error responses: { "error": { "code": "...", "message": "..." } }
//	success reads:   the domain entity as JSON
//	list reads:      { "items": [...], "limit": N, "offset": N }
package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/corsi/backend/internal/finance/app"
	"github.com/corsi/backend/internal/finance/domain"
	"github.com/corsi/backend/internal/platform/apierror"
	"github.com/corsi/backend/internal/platform/render"
	"github.com/corsi/backend/internal/platform/workspace"
)

type Handler struct {
	svc *app.Service
	log *slog.Logger
}

func NewHandler(svc *app.Service, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

func (h *Handler) Mount(r chi.Router) {
	r.Get("/ping", h.ping)

	r.Route("/categories", func(r chi.Router) {
		r.Post("/", h.createCategory)
		r.Get("/", h.listCategories)
		r.Get("/{id}", h.getCategory)
		r.Patch("/{id}", h.updateCategory)
		r.Delete("/{id}", h.deleteCategory)
	})

	r.Route("/transactions", func(r chi.Router) {
		r.Post("/", h.createTransaction)
		r.Get("/", h.listTransactions)
		// /totals must be registered before /{id} so chi doesn't try to
		// parse "totals" as a UUID id and 404 the route.
		r.Get("/totals", h.getTransactionTotals)
		r.Get("/{id}", h.getTransaction)
		r.Patch("/{id}", h.updateTransaction)
		r.Delete("/{id}", h.deleteTransaction)
	})

	r.Route("/transfers", func(r chi.Router) {
		r.Post("/", h.createTransfer)
		r.Delete("/{pair_id}", h.deleteTransfer)
	})

	r.Route("/cards", func(r chi.Router) {
		r.Post("/", h.createCard)
		r.Get("/", h.listCards)
		r.Get("/{id}", h.getCard)
		r.Patch("/{id}", h.updateCard)
		r.Delete("/{id}", h.deleteCard) // archive (soft delete)
	})

	r.Route("/persons", func(r chi.Router) {
		r.Post("/", h.createPerson)
		r.Get("/", h.listPersons)
		r.Get("/{id}", h.getPerson)
		r.Patch("/{id}", h.updatePerson)
		r.Delete("/{id}", h.deletePerson)
	})

	r.Route("/recurring-entries", func(r chi.Router) {
		r.Post("/", h.createRecurringEntry)
		r.Get("/", h.listRecurringEntries)
		// /summary and /commitment before /{id} so chi does not try to
		// parse either word as a uuid — the same ordering
		// /transactions/totals needs.
		//
		// The two are DIFFERENT readings and are never summed: /summary
		// answers "quanto sai por mês" and divides an annual entry by
		// twelve; /commitment answers "o que tenho a pagar neste mês" and
		// puts an annual entry's whole amount in its due month.
		r.Get("/summary", h.getRecurringSummary)
		r.Get("/commitment", h.getMonthlyCommitment)
		r.Get("/{id}", h.getRecurringEntry)
		r.Patch("/{id}", h.updateRecurringEntry)
		r.Delete("/{id}", h.deleteRecurringEntry)

		// One month of one obligation. No create, no delete, and no route
		// that takes an occurrence's own id: materialisation belongs to
		// the application service, and a month that happened does not stop
		// having happened. See adapters/httpapi/recurringoccurrences.go.
		//
		// POST pays and DELETE unpays, rather than a PUT taking a boolean:
		// a toggle's meaning depends on a state the caller cannot see, so
		// a retried request would undo the first one.
		r.Route("/{entryID}/occurrences/{period}", func(r chi.Router) {
			r.Post("/pay", h.payOccurrence)
			r.Delete("/pay", h.unpayOccurrence)
			r.Patch("/", h.patchOccurrence)
		})
	})

	r.Route("/purchase-plans", func(r chi.Router) {
		r.Post("/", h.createPurchasePlan)
		r.Get("/", h.listPurchasePlans)
		r.Get("/{id}", h.getPurchasePlan)
		// Cancel future (still-scheduled) installments. Paid installments
		// stay as history — see Service.CancelPurchasePlan.
		r.Post("/{id}/cancel", h.cancelPurchasePlan)
	})
}

func (h *Handler) ping(w http.ResponseWriter, _ *http.Request) {
	render.JSON(w, http.StatusOK, map[string]string{"module": "finance", "status": "ok"})
}

// --- helpers --------------------------------------------------------------

func (h *Handler) workspaceID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := workspace.FromContext(r.Context())
	if !ok {
		apierror.Write(w, h.log, apierror.New(http.StatusForbidden, "workspace_missing", "no workspace in context"))
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) writeDomainErr(w http.ResponseWriter, err error) {
	var de *domain.Error
	if !errors.As(err, &de) {
		apierror.Write(w, h.log, err)
		return
	}
	switch de.Kind {
	case domain.KindInvalid:
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid", de.Message))
	case domain.KindNotFound:
		apierror.Write(w, h.log, apierror.New(http.StatusNotFound, "not_found", de.Message))
	case domain.KindConflict:
		apierror.Write(w, h.log, apierror.New(http.StatusConflict, "conflict", de.Message))
	default:
		apierror.Write(w, h.log, err)
	}
}

func parseUUIDParam(r *http.Request, name string) (uuid.UUID, error) {
	return uuid.Parse(chi.URLParam(r, name))
}

func parseLimitOffset(r *http.Request) (limit, offset int) {
	limit = 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	return
}

type listEnvelope struct {
	Items  any `json:"items"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}
