// Package httpapi exposes the Closet context over HTTP.
//
// Wire shape matches the rest of the platform:
//
//	error responses: { "error": { "code": "...", "message": "..." } }
//	list reads:      { "items": [...], "total": N }
//
// ── Why attaching an image is a PUT on the view ────────────────────────
// `PUT /closet/items/{id}/images/{view}` rather than a POST to a
// collection. A piece has AT MOST ONE image per angle, so uploading the
// folded shot twice is a replacement and not a second folded shot — which
// is exactly what PUT means. A POST would imply a growing list and would
// leave the caller to work out which of the two folded images is current.
//
// ── Why the asset route is separate from the item read ─────────────────
// Because the bytes are the one thing a listing must not carry. Forty
// pieces with four views each is a hundred and sixty images; inlined as
// base64 that is a response nobody can render. The item read returns the
// asset's id, dimensions and size, and the browser fetches each one from
// `GET /closet/assets/{id}`, which is also the only place the caching
// headers have to be right.
package httpapi

import (
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/corsi/backend/internal/closet/app"
	"github.com/corsi/backend/internal/closet/domain"
	"github.com/corsi/backend/internal/closet/ports"
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
	r.Get("/catalog", h.catalog)

	r.Route("/items", func(r chi.Router) {
		r.Get("/", h.listItems)
		r.Post("/", h.createItem)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", h.getItem)
			r.Patch("/", h.updateItem)
			// Archive and restore are their own addresses rather than a
			// status field on the PATCH, for the reason the port gives: they
			// are a different decision with different consequences, and a
			// caller fixing a typo in a brand must not be able to retire a
			// garment by echoing back a field it did not mean to send.
			r.Post("/archive", h.archiveItem)
			r.Post("/restore", h.restoreItem)
			r.Put("/images/{view}", h.attachImage)
			r.Delete("/images/{view}", h.removeImage)
		})
	})

	r.Get("/assets/{id}", h.getAsset)

	r.Route("/looks", func(r chi.Router) {
		r.Get("/", h.listLooks)
		r.Post("/", h.createLook)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", h.getLook)
			r.Patch("/", h.updateLook)
			// The composition, written as a whole. See app.SetLookItems for
			// why the client sends pieces and not slots.
			r.Put("/items", h.setLookItems)
			r.Post("/archive", h.archiveLook)
			r.Post("/restore", h.restoreLook)
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

func (h *Handler) pathID(w http.ResponseWriter, r *http.Request, what string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest,
			"invalid", "that is not a valid "+what+" id"))
		return uuid.Nil, false
	}
	return id, true
}

// decode reads a JSON body, reporting a malformed one as a 400.
//
// render.DecodeJSON returns a plain error, and handing that straight to
// apierror.Write renders it as a 500: a client sending broken JSON would be
// told the server broke. This wraps it once, here, so every route in the
// module gets the same honest answer.
func (h *Handler) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := render.DecodeJSON(r, dst); err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid", err.Error()))
		return false
	}
	return true
}

/* ── catalog ─────────────────────────────────────────────────────────── */

type catalogCategory struct {
	Category string   `json:"category"`
	Slot     string   `json:"slot"`
	Views    []string `json:"views"`
	// Composition is the fallback chain the builder walks. Published so the
	// frontend picks the same image the backend would, rather than
	// hard-coding "prefer folded" in a component and drifting the day a
	// category changes its preference.
	Composition []string `json:"composition"`
}

type catalogSlot struct {
	Slot     string `json:"slot"`
	Capacity int    `json:"capacity"`
}

// catalog publishes the vocabulary: the categories, the slots they fill,
// the views each may carry, and the occasions.
//
// It exists so the frontend renders the rail, the capture flow and the
// composition from what the server ENFORCES. A hard-coded list in the
// client is a second source of truth that only disagrees on the day a
// category is added — which, for this module, is the day it is supposed to
// be easy.
//
// Deliberately not workspace-scoped in its CONTENT — the vocabulary is the
// same for everyone — but it stays behind the workspace middleware with the
// rest of the module rather than becoming the one route with different
// rules.
func (h *Handler) catalog(w http.ResponseWriter, _ *http.Request) {
	defs := domain.Categories()
	categories := make([]catalogCategory, 0, len(defs))
	for _, def := range defs {
		categories = append(categories, catalogCategory{
			Category:    def.Category.String(),
			Slot:        def.Slot.String(),
			Views:       viewStrings(def.Views),
			Composition: viewStrings(def.Composition),
		})
	}

	slotDefs := domain.Slots()
	slots := make([]catalogSlot, 0, len(slotDefs))
	for _, def := range slotDefs {
		slots = append(slots, catalogSlot{Slot: def.Slot.String(), Capacity: def.Capacity})
	}

	render.JSON(w, http.StatusOK, map[string]any{
		"categories": categories,
		"slots":      slots,
		"occasions":  domain.OccasionNames(),
		// The ceiling a composition cannot exceed, derived from the slots
		// rather than restated, so a client can refuse locally with the same
		// number the server would.
		"max_look_items": app.MaxLookItems(),
	})
}

func viewStrings(views []domain.ImageView) []string {
	out := make([]string, len(views))
	for i, v := range views {
		out[i] = v.String()
	}
	return out
}

/* ── items ───────────────────────────────────────────────────────────── */

func (h *Handler) listItems(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	filter := ports.ItemFilter{
		Search:          q.Get("search"),
		IncludeArchived: q.Get("include_archived") == "true",
		Limit:           atoi(q.Get("limit")),
		Offset:          atoi(q.Get("offset")),
	}
	if raw := q.Get("category"); raw != "" {
		category, err := domain.ParseCategory(raw)
		if err != nil {
			h.writeDomainErr(w, err)
			return
		}
		filter.Category = &category
	}
	if raw := q.Get("status"); raw != "" {
		status, err := domain.ParseStatus(raw)
		if err != nil {
			h.writeDomainErr(w, err)
			return
		}
		filter.Status = &status
	}
	if raw := q.Get("favorite"); raw != "" {
		favorite := raw == "true"
		filter.Favorite = &favorite
	}

	items, total, err := h.svc.ListItems(r.Context(), ws, filter)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	if items == nil {
		items = []*domain.ClosetItem{}
	}
	render.JSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (h *Handler) getItem(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "piece")
	if !ok {
		return
	}
	item, err := h.svc.GetItem(r.Context(), ws, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, item)
}

type createItemRequest struct {
	Name           string `json:"name"`
	Category       string `json:"category"`
	Subtype        string `json:"subtype"`
	PrimaryColor   string `json:"primary_color"`
	SecondaryColor string `json:"secondary_color"`
	Brand          string `json:"brand"`
	Notes          string `json:"notes"`
	Favorite       bool   `json:"favorite"`
}

func (h *Handler) createItem(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	var req createItemRequest
	if !h.decode(w, r, &req) {
		return
	}
	category, err := domain.ParseCategory(req.Category)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	item, err := h.svc.CreateItem(r.Context(), ws, app.CreateItemInput{
		Name:           req.Name,
		Category:       category,
		Subtype:        req.Subtype,
		PrimaryColor:   req.PrimaryColor,
		SecondaryColor: req.SecondaryColor,
		Brand:          req.Brand,
		Notes:          req.Notes,
		Favorite:       req.Favorite,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusCreated, item)
}

type updateItemRequest struct {
	Name           *string `json:"name"`
	Category       *string `json:"category"`
	Subtype        *string `json:"subtype"`
	PrimaryColor   *string `json:"primary_color"`
	SecondaryColor *string `json:"secondary_color"`
	Brand          *string `json:"brand"`
	Notes          *string `json:"notes"`
	Favorite       *bool   `json:"favorite"`
}

func (h *Handler) updateItem(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "piece")
	if !ok {
		return
	}
	var req updateItemRequest
	if !h.decode(w, r, &req) {
		return
	}

	in := app.UpdateItemInput{
		Name:           req.Name,
		Subtype:        req.Subtype,
		PrimaryColor:   req.PrimaryColor,
		SecondaryColor: req.SecondaryColor,
		Brand:          req.Brand,
		Notes:          req.Notes,
		Favorite:       req.Favorite,
	}
	if req.Category != nil {
		category, err := domain.ParseCategory(*req.Category)
		if err != nil {
			h.writeDomainErr(w, err)
			return
		}
		in.Category = &category
	}

	item, err := h.svc.UpdateItem(r.Context(), ws, id, in)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, item)
}

func (h *Handler) archiveItem(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "piece")
	if !ok {
		return
	}
	result, err := h.svc.ArchiveItem(r.Context(), ws, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	// The count travels with the response so the screen can say what the
	// archive touched. It is information, never a refusal — see
	// app.ArchiveResult.
	render.JSON(w, http.StatusOK, map[string]any{
		"item":           result.Item,
		"looks_affected": result.LooksAffected,
	})
}

func (h *Handler) restoreItem(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "piece")
	if !ok {
		return
	}
	item, err := h.svc.RestoreItem(r.Context(), ws, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, item)
}

/* ── images ──────────────────────────────────────────────────────────── */

// uploadField is the multipart field the image arrives in. One name, and
// nothing else in the part is read — see attachImage.
const uploadField = "file"

// maxUploadBytes bounds the whole request body.
//
// The image limit plus a kilobyte of multipart framing. It is enforced with
// MaxBytesReader, which means an oversized upload stops being READ at the
// limit rather than being buffered in full and rejected afterwards — the
// difference between refusing a 2 GB file and allocating it first.
const maxUploadBytes = domain.MaxImageBytes + 1024

// attachImage stores an uploaded PNG under one view of one piece.
//
// ══════════════════════════════════════════════════════════════════════
//
//	NOTHING THE CLIENT SAYS ABOUT THE FILE IS USED. THE FILENAME IS
//	NEVER READ
//
// ══════════════════════════════════════════════════════════════════════
//
// Not to build a path, not to derive an extension, not to name the stored
// object, and not in a log line. There is no path traversal defence in this
// module because there is no path: an asset's identity is a uuid the
// database issues and a digest the server computes, and a filename of
// `../../../etc/passwd` is discarded with the same indifference as
// `shirt.png`.
//
// The declared content-type is discarded for the same reason. What the
// bytes ARE is decided by decoding them — see domain.DecodeImage.
func (h *Handler) attachImage(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "piece")
	if !ok {
		return
	}
	view := chi.URLParam(r, "view")

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	// The multipart parser buffers up to this much in memory and spills the
	// rest to a temp file. Set to the whole allowance so a legitimate upload
	// never touches disk, while MaxBytesReader above is what actually caps
	// the request.
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		// Too big and malformed are DIFFERENT answers, and collapsing them
		// was a real defect: a client sending a broken multipart body was
		// told its file was over 8 MB, which sends the operator off to
		// shrink an image that was never the problem. MaxBytesError is the
		// only one of the two the reader can identify, so it is the one
		// that gets the specific status.
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			apierror.Write(w, h.log, apierror.New(http.StatusRequestEntityTooLarge, "invalid",
				"the image is larger than "+strconv.Itoa(domain.MaxImageBytes>>20)+" MB"))
			return
		}
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid",
			"the upload is not a readable multipart request"))
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	file, _, err := r.FormFile(uploadField)
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid",
			"the upload must carry the image in a multipart field named "+uploadField))
		return
	}
	defer func() { _ = file.Close() }()

	raw, err := io.ReadAll(io.LimitReader(file, domain.MaxImageBytes+1))
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid",
			"the upload could not be read"))
		return
	}

	img, err := h.svc.AttachImage(r.Context(), ws, id, view, raw)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, img)
}

func (h *Handler) removeImage(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "piece")
	if !ok {
		return
	}
	if err := h.svc.RemoveImage(r.Context(), ws, id, chi.URLParam(r, "view")); err != nil {
		h.writeDomainErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getAsset serves the bytes of one image.
//
// ── Caching, and why it is safe to be this aggressive ──────────────────
// An asset is immutable: its id addresses bytes whose digest is part of its
// identity, and nothing in this module ever rewrites one. Replacing a
// piece's photograph creates a NEW asset and repoints the image row, so a
// cached response can never be stale — there is no edit that would
// invalidate it. `immutable` says exactly that, and `private` keeps it out
// of any shared cache, because these are one operator's photographs.
//
// ── Why the ETag is the digest ─────────────────────────────────────────
// Because it already exists and it is exactly what an ETag means. A
// conditional request costs one small query and no bytes on the wire, which
// is what makes a wardrobe of two hundred images cheap to re-open.
func (h *Handler) getAsset(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "image")
	if !ok {
		return
	}
	asset, err := h.svc.GetAsset(r.Context(), ws, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}

	etag := `"` + hex.EncodeToString(asset.SHA256) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	// The response body is a file the operator uploaded. nosniff stops a
	// browser from second-guessing the content-type the server determined,
	// which is the whole point of having determined it.
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if match := r.Header.Get("If-None-Match"); match != "" && etagMatches(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", asset.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(asset.Bytes)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write(asset.Bytes); err != nil {
		// The response is already committed, so there is nothing to tell the
		// client. A dropped image request is ordinary — a scrolled-away
		// thumbnail, a closed tab — and logging it at error would make the
		// normal case look like a fault.
		h.log.Debug("closet: asset write interrupted", "asset_id", id, "err", err)
	}
}

// etagMatches handles the `*` wildcard and a comma-separated list, which is
// what a browser actually sends on a revalidation.
func etagMatches(header, etag string) bool {
	if strings.TrimSpace(header) == "*" {
		return true
	}
	for _, candidate := range strings.Split(header, ",") {
		if strings.TrimSpace(candidate) == etag {
			return true
		}
	}
	return false
}

/* ── looks ───────────────────────────────────────────────────────────── */

func (h *Handler) listLooks(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	filter := ports.LookFilter{
		Search:          q.Get("search"),
		IncludeArchived: q.Get("include_archived") == "true",
		Limit:           atoi(q.Get("limit")),
		Offset:          atoi(q.Get("offset")),
	}
	if raw := q.Get("occasion"); raw != "" {
		occasion, err := domain.ParseOccasion(raw)
		if err != nil {
			h.writeDomainErr(w, err)
			return
		}
		filter.Occasion = &occasion
	}
	if raw := q.Get("favorite"); raw != "" {
		favorite := raw == "true"
		filter.Favorite = &favorite
	}

	looks, total, err := h.svc.ListLooks(r.Context(), ws, filter)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	if looks == nil {
		looks = []*domain.Look{}
	}
	render.JSON(w, http.StatusOK, map[string]any{"items": looks, "total": total})
}

func (h *Handler) getLook(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "look")
	if !ok {
		return
	}
	look, err := h.svc.GetLook(r.Context(), ws, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, look)
}

type createLookRequest struct {
	Name     string   `json:"name"`
	Occasion string   `json:"occasion"`
	Favorite bool     `json:"favorite"`
	Notes    string   `json:"notes"`
	ItemIDs  []string `json:"item_ids"`
}

func (h *Handler) createLook(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	var req createLookRequest
	if !h.decode(w, r, &req) {
		return
	}
	occasion, err := domain.ParseOccasion(req.Occasion)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	itemIDs, err := parseIDs(req.ItemIDs)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}

	look, err := h.svc.CreateLook(r.Context(), ws, app.CreateLookInput{
		Name:     req.Name,
		Occasion: occasion,
		Favorite: req.Favorite,
		Notes:    req.Notes,
		ItemIDs:  itemIDs,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusCreated, look)
}

type updateLookRequest struct {
	Name     *string `json:"name"`
	Occasion *string `json:"occasion"`
	Favorite *bool   `json:"favorite"`
	Notes    *string `json:"notes"`
}

func (h *Handler) updateLook(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "look")
	if !ok {
		return
	}
	var req updateLookRequest
	if !h.decode(w, r, &req) {
		return
	}

	in := app.UpdateLookInput{
		Name:     req.Name,
		Favorite: req.Favorite,
		Notes:    req.Notes,
	}
	if req.Occasion != nil {
		occasion, err := domain.ParseOccasion(*req.Occasion)
		if err != nil {
			h.writeDomainErr(w, err)
			return
		}
		in.Occasion = &occasion
	}

	look, err := h.svc.UpdateLook(r.Context(), ws, id, in)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, look)
}

type setLookItemsRequest struct {
	// ItemIDs in the order they were chosen. No slot is sent: the slot is
	// derived from each piece's category, so a client cannot file a watch
	// under `bottom` by naming it. See app.SetLookItems.
	ItemIDs []string `json:"item_ids"`
}

func (h *Handler) setLookItems(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "look")
	if !ok {
		return
	}
	var req setLookItemsRequest
	if !h.decode(w, r, &req) {
		return
	}
	itemIDs, err := parseIDs(req.ItemIDs)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	look, err := h.svc.SetLookItems(r.Context(), ws, id, itemIDs)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, look)
}

func (h *Handler) archiveLook(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "look")
	if !ok {
		return
	}
	look, err := h.svc.ArchiveLook(r.Context(), ws, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, look)
}

func (h *Handler) restoreLook(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "look")
	if !ok {
		return
	}
	look, err := h.svc.RestoreLook(r.Context(), ws, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, look)
}

/* ── helpers ─────────────────────────────────────────────────────────── */

// parseIDs converts the wire's strings into uuids, refusing the whole list
// on the first bad one.
//
// All-or-nothing because a composition is one decision: silently dropping
// an unparseable id would save a look missing a piece the operator
// believes they chose.
func parseIDs(raw []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		id, err := uuid.Parse(strings.TrimSpace(s))
		if err != nil {
			return nil, domain.Invalid("%q is not a valid piece id", s)
		}
		out = append(out, id)
	}
	return out, nil
}

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
