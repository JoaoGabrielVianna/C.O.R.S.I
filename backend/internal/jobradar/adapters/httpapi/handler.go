// Package httpapi exposes the Job Radar context over HTTP.
//
// Wire shape matches the rest of the platform:
//
//	error responses: { "error": { "code": "...", "message": "..." } }
//	list reads:      { "items": [...], "total": N }
//
// ── Why a stage change is its own endpoint ─────────────────────────────
// `POST /opportunities/{id}/move` rather than a PATCH that happens to carry
// a stage field. A move records history and resets the stage clock; a
// generic update does neither. Two operations with different consequences
// should not share one address, or the caller that meant to fix a typo in
// the salary ends up filing a transition.
package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/corsi/backend/internal/jobradar/app"
	"github.com/corsi/backend/internal/jobradar/domain"
	"github.com/corsi/backend/internal/jobradar/ports"
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
	r.Get("/stages", h.listStages)
	r.Get("/companies", h.listCompanies)
	r.Route("/opportunities", func(r chi.Router) {
		r.Get("/", h.listOpportunities)
		r.Post("/", h.createOpportunity)
		// The importer. A separate address from the plain create because it
		// is a one-time migration of records that already have a history,
		// and because it is the only write that accepts timestamps from the
		// caller. See importOpportunities.
		r.Post("/import", h.importOpportunities)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", h.getOpportunity)
			r.Patch("/", h.updateOpportunity)
			r.Delete("/", h.deleteOpportunity)
			r.Post("/move", h.moveOpportunity)
			r.Post("/untrack", h.untrackOpportunity)
		})
	})
}

/* ── request context ─────────────────────────────────────────────────── */

// ws reads the workspace the middleware stamped. Absent is a 401 rather
// than a default: the middleware only lets a request through without one in
// dev, where it substitutes the sentinel, so reaching here without one is a
// wiring fault and not a request to serve.
func (h *Handler) ws(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := workspace.FromContext(r.Context())
	if !ok || id == uuid.Nil {
		apierror.Write(w, h.log, apierror.New(http.StatusUnauthorized,
			"unauthorized", "a workspace is required"))
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) id(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest,
			"invalid", "that is not a valid opportunity id"))
		return uuid.Nil, false
	}
	return id, true
}

/* ── reads ───────────────────────────────────────────────────────────── */

// listStages publishes the stage vocabulary.
//
// It exists so the frontend renders the board from what the server
// enforces. A hard-coded list in the client would be a second source of
// truth that only disagrees on the day a stage is added.
func (h *Handler) listStages(w http.ResponseWriter, _ *http.Request) {
	render.JSON(w, http.StatusOK, map[string]any{"items": domain.StageNames()})
}

func (h *Handler) listCompanies(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	items, err := h.svc.ListCompanies(r.Context(), ws)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	if items == nil {
		items = []*domain.Company{}
	}
	render.JSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) listOpportunities(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	filter := ports.OpportunityFilter{
		Company: q.Get("company"),
		Search:  q.Get("search"),
		Limit:   atoi(q.Get("limit")),
		Offset:  atoi(q.Get("offset")),
	}
	if raw := q.Get("stage"); raw != "" {
		if raw == "discover" {
			filter.Untracked = true
		} else {
			stage, err := domain.ParseStage(raw)
			if err != nil {
				h.writeDomainErr(w, err)
				return
			}
			filter.Stage = &stage
		}
	}

	items, total, err := h.svc.ListOpportunities(r.Context(), ws, filter)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	if items == nil {
		items = []*domain.Opportunity{}
	}
	render.JSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (h *Handler) getOpportunity(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	id, ok := h.id(w, r)
	if !ok {
		return
	}
	o, events, err := h.svc.GetOpportunityHistory(r.Context(), ws, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, map[string]any{"opportunity": o, "history": events})
}

/* ── writes ──────────────────────────────────────────────────────────── */

type createRequest struct {
	Company      string        `json:"company"`
	Role         string        `json:"role"`
	Salary       string        `json:"salary"`
	Location     string        `json:"location"`
	Stack        []string      `json:"stack"`
	Description  string        `json:"description"`
	Source       string        `json:"source"`
	SourceURL    string        `json:"source_url"`
	MatchPercent *int          `json:"match_percent"`
	Stage        string        `json:"stage"`
	NextAction   string        `json:"next_action"`
	Notes        *domain.Notes `json:"notes"`
}

func (c createRequest) toInput() (app.CreateInput, error) {
	in := app.CreateInput{
		CompanyName:  c.Company,
		Role:         c.Role,
		Salary:       c.Salary,
		Location:     c.Location,
		Stack:        c.Stack,
		Description:  c.Description,
		Source:       c.Source,
		SourceURL:    c.SourceURL,
		MatchPercent: c.MatchPercent,
		NextAction:   c.NextAction,
	}
	if c.Notes != nil {
		in.Notes = *c.Notes
	}
	if c.Stage != "" && c.Stage != "discover" {
		stage, err := domain.ParseStage(c.Stage)
		if err != nil {
			return app.CreateInput{}, err
		}
		in.Stage = &stage
	}
	return in, nil
}

func (h *Handler) createOpportunity(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	var req createRequest
	if err := render.DecodeJSON(r, &req); err != nil {
		apierror.Write(w, h.log, err)
		return
	}
	in, err := req.toInput()
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	o, err := h.svc.CreateOpportunity(r.Context(), ws, in)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusCreated, o)
}

type updateRequest struct {
	Company      *string       `json:"company"`
	Role         *string       `json:"role"`
	Salary       *string       `json:"salary"`
	Location     *string       `json:"location"`
	Stack        *[]string     `json:"stack"`
	Description  *string       `json:"description"`
	SourceURL    *string       `json:"source_url"`
	MatchPercent *int          `json:"match_percent"`
	NextAction   *string       `json:"next_action"`
	Notes        *domain.Notes `json:"notes"`
}

func (h *Handler) updateOpportunity(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	id, ok := h.id(w, r)
	if !ok {
		return
	}
	var req updateRequest
	if err := render.DecodeJSON(r, &req); err != nil {
		apierror.Write(w, h.log, err)
		return
	}
	o, err := h.svc.UpdateOpportunity(r.Context(), ws, id, app.UpdateInput{
		CompanyName: req.Company, Role: req.Role, Salary: req.Salary,
		Location: req.Location, Stack: req.Stack, Description: req.Description,
		SourceURL: req.SourceURL, MatchPercent: req.MatchPercent,
		NextAction: req.NextAction, Notes: req.Notes,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, o)
}

type moveRequest struct {
	Stage string `json:"stage"`
}

func (h *Handler) moveOpportunity(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	id, ok := h.id(w, r)
	if !ok {
		return
	}
	var req moveRequest
	if err := render.DecodeJSON(r, &req); err != nil {
		apierror.Write(w, h.log, err)
		return
	}
	stage, err := domain.ParseStage(req.Stage)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	result, err := h.svc.MoveOpportunity(r.Context(), ws, id, stage)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}

	body := map[string]any{"opportunity": result.Opportunity, "unchanged": result.Unchanged}
	if result.PreviousStage != nil {
		body["previous_stage"] = string(*result.PreviousStage)
	}
	render.JSON(w, http.StatusOK, body)
}

func (h *Handler) untrackOpportunity(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	id, ok := h.id(w, r)
	if !ok {
		return
	}
	o, err := h.svc.UntrackOpportunity(r.Context(), ws, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, o)
}

func (h *Handler) deleteOpportunity(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	id, ok := h.id(w, r)
	if !ok {
		return
	}
	if err := h.svc.DeleteOpportunity(r.Context(), ws, id); err != nil {
		h.writeDomainErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

/* ── import ──────────────────────────────────────────────────────────── */

// importItem is one record as the browser stored it.
//
// Timestamps arrive as epoch milliseconds because that is what the
// localStorage document holds. They are accepted ONLY here: every other
// write stamps its own time, and a general API that let a caller choose
// `created_at` would be one a bug could use to backdate history.
type importItem struct {
	// LegacyID is the id the record already had in the browser document,
	// e.g. "op_mpbh75to_fl6bu". It is what makes a repeated import
	// recognisable, and it is required: see the refusal in
	// app.ImportOpportunity.
	LegacyID       string        `json:"legacy_id"`
	Company        string        `json:"company"`
	CompanyDomain  string        `json:"company_domain"`
	Role           string        `json:"role"`
	Salary         string        `json:"salary"`
	Location       string        `json:"location"`
	Stack          []string      `json:"stack"`
	Description    string        `json:"description"`
	Source         string        `json:"source"`
	SourceURL      string        `json:"source_url"`
	MatchPercent   *int          `json:"match_percent"`
	PostedAt       *int64        `json:"posted_at"`
	Stage          string        `json:"stage"`
	TrackedAt      *int64        `json:"tracked_at"`
	StageEnteredAt *int64        `json:"stage_entered_at"`
	NextAction     string        `json:"next_action"`
	Notes          *domain.Notes `json:"notes"`
	// CreatedAt and UpdatedAt are the record's own clock, in epoch
	// milliseconds. Accepted ONLY here: every other write stamps its own
	// time, and a general API that let a caller choose `created_at` would be
	// one a bug could use to backdate history.
	CreatedAt *int64 `json:"created_at"`
	UpdatedAt *int64 `json:"updated_at"`
	// History is the legacy `tracking.history`, oldest first.
	History []importVisit `json:"history"`
}

// importVisit mirrors the browser's `{stage, at}` entries.
type importVisit struct {
	Stage string `json:"stage"`
	At    int64  `json:"at"`
}

type importRequest struct {
	Items []importItem `json:"items"`
}

// maxImportItems bounds one import call. The pipeline being migrated is a
// personal job search, not a dataset; a request above this is a mistake,
// and refusing it is cheaper than discovering it half-written.
const maxImportItems = 500

// importOpportunities migrates the browser's localStorage document.
//
// ── Why it IS idempotent now, and how ──────────────────────────────────
// It used to create unconditionally: running it twice created twice, and
// the only protection was a `localStorage` flag — per ORIGIN, so the same
// document offered at :5173, :5174 and :5175 could be imported three times
// over. A flag in the browser cannot be the integrity boundary for rows on
// the server.
//
// Identity now comes from the legacy record itself: the id it already
// carried, qualified by the document format it came from. A repeat is
// RECOGNISED and reported as `already_imported`, not created again.
//
// What is deliberately NOT used for that: company, role, URL, or any other
// visible field. Two genuinely different postings for the same role at the
// same employer exist, and any heuristic over what a posting SAYS would
// collapse them into one.
//
// ── Why partial success survives ───────────────────────────────────────
// An item that fails validation does not roll back the ones already
// written, and that is now safe rather than merely pragmatic: retrying the
// same payload re-imports nothing, so a document with one bad record can be
// fixed and re-sent without the thirty-nine good ones duplicating. All or
// nothing would mean one malformed record from a year-old browser document
// blocking the entire migration with nothing to show for it.
func (h *Handler) importOpportunities(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	var req importRequest
	if err := render.DecodeJSON(r, &req); err != nil {
		apierror.Write(w, h.log, err)
		return
	}
	if len(req.Items) == 0 {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest,
			"invalid", "there is nothing to import"))
		return
	}
	if len(req.Items) > maxImportItems {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid",
			"an import may carry at most "+strconv.Itoa(maxImportItems)+" opportunities"))
		return
	}

	created := make([]*domain.Opportunity, 0, len(req.Items))
	alreadyImported := []map[string]any{}
	failures := []map[string]any{}

	for i, item := range req.Items {
		in := app.ImportInput{
			CreateInput: app.CreateInput{
				CompanyName:  item.Company,
				Role:         item.Role,
				Salary:       item.Salary,
				Location:     item.Location,
				Stack:        item.Stack,
				Description:  item.Description,
				Source:       item.Source,
				SourceURL:    item.SourceURL,
				MatchPercent: item.MatchPercent,
				NextAction:   item.NextAction,
				PostedAt:     millisToTime(item.PostedAt),
			},
			// The source is attached only when the document supplied an id.
			// Claiming a source for an item with no id would produce "half
			// an identity", and the caller would read a message about a
			// field they never sent instead of about the one they omitted.
			Identity:      legacyIdentity(item.LegacyID),
			CompanyDomain: item.CompanyDomain,
			CreatedAt:     millisToTime(item.CreatedAt),
			UpdatedAt:     millisToTime(item.UpdatedAt),
			History:       importVisits(item.History),
		}
		if item.Notes != nil {
			in.Notes = *item.Notes
		}
		if item.Stage != "" && item.Stage != "discover" {
			stage, err := domain.ParseStage(item.Stage)
			if err != nil {
				failures = append(failures, importFailure(i, item, err))
				continue
			}
			in.Stage = &stage
			in.TrackedAt = millisToTime(item.TrackedAt)
			in.StageEnteredAt = millisToTime(item.StageEnteredAt)
		}

		res, err := h.svc.ImportOpportunity(r.Context(), ws, in)
		if err != nil {
			failures = append(failures, importFailure(i, item, err))
			continue
		}
		if res.AlreadyImported {
			// Reported with the id it already has, so a caller can point at
			// the row rather than only learn that nothing happened.
			alreadyImported = append(alreadyImported, map[string]any{
				"index":     i,
				"legacy_id": item.LegacyID,
				"id":        res.Opportunity.ID,
			})
			continue
		}
		created = append(created, res.Opportunity)
	}

	h.log.Info("jobradar: import finished",
		"workspace_id", ws, "created", len(created),
		"already_imported", len(alreadyImported), "failed", len(failures))

	render.JSON(w, http.StatusOK, map[string]any{
		"created":       created,
		"created_count": len(created),
		// The count that makes a retry legible. Without it a second run
		// would report "0 created" and look like a failure rather than like
		// the no-op it is.
		"already_imported":       alreadyImported,
		"already_imported_count": len(alreadyImported),
		"failed":                 failures,
		"failed_count":           len(failures),
	})
}

// legacyIdentity builds the identity, or the zero value when the record
// carried no id of its own.
func legacyIdentity(legacyID string) domain.ImportIdentity {
	id := strings.TrimSpace(legacyID)
	if id == "" {
		return domain.ImportIdentity{}
	}
	return domain.ImportIdentity{
		Source:     domain.ImportSourceLocalStorageV1,
		ExternalID: id,
	}
}

// importVisits converts the browser's epoch milliseconds into the domain's
// visit shape. A zero or negative timestamp is passed through as the zero
// time, which BuildStageTimeline refuses by name rather than silently
// filing the transition under 1970.
func importVisits(in []importVisit) []domain.StageVisit {
	if len(in) == 0 {
		return nil
	}
	out := make([]domain.StageVisit, 0, len(in))
	for _, v := range in {
		visit := domain.StageVisit{Stage: v.Stage}
		if v.At > 0 {
			visit.At = time.UnixMilli(v.At).UTC()
		}
		out = append(out, visit)
	}
	return out
}

func importFailure(index int, item importItem, err error) map[string]any {
	return map[string]any{
		"index":   index,
		"company": item.Company,
		"role":    item.Role,
		"error":   err.Error(),
	}
}

// millisToTime converts the browser's epoch milliseconds. Nil and zero both
// mean "not set" — a zero epoch would otherwise import as 1970, which
// sorts a record to the bottom of every list forever.
func millisToTime(ms *int64) *time.Time {
	if ms == nil || *ms <= 0 {
		return nil
	}
	t := time.UnixMilli(*ms).UTC()
	return &t
}

/* ── errors ──────────────────────────────────────────────────────────── */

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
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
