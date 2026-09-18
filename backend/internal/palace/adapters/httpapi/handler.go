// Package httpapi exposes the Palace context over HTTP, read only.
//
// ══════════════════════════════════════════════════════════════════════
//
//	EVERY ROUTE HERE IS A GET. THERE IS NO WRITE SURFACE AND NO PLAN FOR ONE
//
// ══════════════════════════════════════════════════════════════════════
//
// Writing to the Palace is a conversation with an authorized agent, and
// the capabilities are where that is audited, bounded and granted one at a
// time. A second write path would be a second set of rules about what may
// be created, and the first thing it would get wrong is the one thing no
// foreign key can catch, because `palace.relations` has none.
//
// This adapter exists for the operator's OWN projections: screens that
// read their own record. Wire shape matches the rest of the platform:
//
//	error responses: { "error": { "code": "...", "message": "..." } }
//	list reads:      { "items": [...], "total": N, "limit": L, "offset": O }
//
// ── The surface withholds more than the Core does ──────────────────────
// Two rules apply to everything below, and both are stricter than what a
// tool sees:
//
//  1. Highly sensitive content is NEVER returned. There is no opt-in.
//     No query parameter named include_highly_sensitive exists in this
//     package, so a caller cannot ask, a bookmark cannot carry it, and a
//     future handler cannot forget to default it to false.
//
//  2. Visual containment inherits visibility. Something filed in a room
//     this surface withholds is withheld too, whatever it says about
//     itself. See app/surface.go, which states the rule in full.
//
// Both refusals are `404`, identical to the answer a fabricated id gets. A
// `403` would confirm that the row exists, which is the one thing the
// withholding is for.
//
// ── What is deliberately NOT mounted yet, and why that is not a gap ────
// The unscoped artifact listing and the memory listing are absent. Both
// would need the containment rule INSIDE the SQL, because a filter applied
// after the rows come back cannot make `Count` agree with `List`, and a
// total that counts what the list withholds announces exactly what the
// withholding exists to prevent. Rather than ship a listing that is
// correct about levels and wrong about containment, this slice mounts only
// the listings it can answer completely:
//
//	GET /rooms                  complete
//	GET /rooms/{roomId}         complete
//	GET /artifacts?room_id=…    complete, BECAUSE it is room scoped: the
//	                            room is resolved through the surface first,
//	                            so every row returned is inside a room this
//	                            surface already showed
//	GET /artifacts/{artifactId} complete
//	GET /memories/{memoryId}    complete
//
// An unmounted route answers the router's own 404 in the standard
// envelope. That is a better failure than a listing that quietly omits the
// containment rule.
package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/corsi/backend/internal/palace/app"
	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/palace/ports"
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
	r.Get("/overview", h.overview)
	r.Route("/rooms", func(r chi.Router) {
		r.Get("/", h.listRooms)
		r.Get("/{roomId}", h.getRoom)
	})
	r.Route("/artifacts", func(r chi.Router) {
		r.Get("/", h.listArtifacts)
		r.Get("/{artifactId}", h.getArtifact)
		// A sub-resource of the artifact, not a top-level /relations. A
		// generic relation route would hand back raw polymorphic ids with
		// nothing resolved, which is the one shape that cannot be made
		// safe: the caller would have to resolve them, and it would do so
		// under its own idea of what may be shown.
		r.Get("/{artifactId}/neighbors", h.artifactNeighbors)
	})
	r.Route("/memories", func(r chi.Router) {
		r.Get("/", h.listMemories)
		r.Get("/{memoryId}", h.getMemory)
	})
}

/* ── the visibility this surface imposes ─────────────────────────────── */

// surfaceVisibility is the ONLY Visibility value this package constructs.
//
// ══════════════════════════════════════════════════════════════════════
//
//	IT IS A FUNCTION, NOT A PARAMETER, AND THAT IS THE MECHANISM
//
// ══════════════════════════════════════════════════════════════════════
//
// Nothing reads a query parameter to build it and nothing accepts one as
// an argument, so there is no path from a request to a laxer value. The
// two fields are stated literally, together, in one place, where a reader
// can see both at once:
//
//	IncludeHighlySensitive  false, always. The most withheld content does
//	                        not appear on this surface at all.
//	InheritRoomVisibility   true, always. A withheld room withholds what
//	                        is in it.
//
// A future handler that forgot to call this would be MORE permissive than
// intended, not less, which is why there is a test asserting every route
// withholds. Defaults protect a forgetful caller; a test protects a
// forgetful author, and this boundary is worth both.
func surfaceVisibility() ports.Visibility {
	return ports.Visibility{
		IncludeHighlySensitive: false,
		InheritRoomVisibility:  true,
	}
}

// The three filter constructors below are the only way this package
// builds a filter.
//
// ══════════════════════════════════════════════════════════════════════
//
//	A FILTER STARTS SAFE AND IS ONLY EVER NARROWED
//
// ══════════════════════════════════════════════════════════════════════
//
// Both visibility rules are set here, together, from the same
// `surfaceVisibility()`. A handler that built `ports.ArtifactFilter{...}`
// literally would have to remember two fields, and the failure mode of
// remembering one is a listing that withholds the right levels and shows
// the contents of a withheld room. Starting from a constructor makes the
// half-applied state something a reviewer can see is absent, rather than
// something they have to check for at every call site.
//
// Nothing below reads a request. They take no arguments on purpose.

func newRoomFilter() ports.RoomFilter {
	v := surfaceVisibility()
	// A room is not inside anything, so only the level rule applies. That
	// is the same reason RoomForSurface ignores the second field.
	return ports.RoomFilter{
		Sensitive: ports.Sensitive{IncludeHighlySensitive: v.IncludeHighlySensitive},
	}
}

func newArtifactFilter() ports.ArtifactFilter {
	v := surfaceVisibility()
	return ports.ArtifactFilter{
		Sensitive:             ports.Sensitive{IncludeHighlySensitive: v.IncludeHighlySensitive},
		InheritRoomVisibility: v.InheritRoomVisibility,
	}
}

func newMemoryFilter() ports.MemoryFilter {
	v := surfaceVisibility()
	return ports.MemoryFilter{
		Sensitive:             ports.Sensitive{IncludeHighlySensitive: v.IncludeHighlySensitive},
		InheritRoomVisibility: v.InheritRoomVisibility,
	}
}

/* ── request context ─────────────────────────────────────────────────── */

// ws reads the workspace the middleware stamped.
//
// Absent is a 401 rather than a default. The middleware only lets a
// request through without one in dev, where it substitutes the sentinel,
// so arriving here without one is a wiring fault and not a request to
// serve. There is no fallback value that is not somebody's real life.
func (h *Handler) ws(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := workspace.FromContext(r.Context())
	if !ok || id == uuid.Nil {
		apierror.Write(w, h.log, apierror.New(http.StatusUnauthorized,
			"unauthorized", "a workspace is required"))
		return uuid.Nil, false
	}
	return id, true
}

// pathID parses a uuid out of the URL.
//
// A malformed id is a 400 and not a 404, because they are different
// mistakes: the first means the caller sent something that is not an id at
// all, and answering not-found would send it looking for a record instead
// of fixing its request.
func (h *Handler) pathID(w http.ResponseWriter, r *http.Request, param, what string) (uuid.UUID, bool) {
	id, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, param)))
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest,
			"invalid", "that is not a valid "+what+" id"))
		return uuid.Nil, false
	}
	return id, true
}

/* ── query parsing ───────────────────────────────────────────────────── */

// lifecycle reads the status filter.
//
// ══════════════════════════════════════════════════════════════════════
//
//	AN ABSENT status MEANS active ON THIS SURFACE. IT DOES NOT MEAN BOTH
//
// ══════════════════════════════════════════════════════════════════════
//
// The Core's filter uses nil for "either state", and that is right for a
// capability answering "onde foi que eu guardei aquilo", where hiding
// retired rows would make the question unanswerable. A projection of what
// the operator is working on is the opposite case: reusing the zero value
// here would fill a room with things that were explicitly retired, and
// nobody asked to see them.
//
// So the zero value of the Core filter is never passed through. This
// function always returns a non-nil pointer.
func (h *Handler) lifecycle(w http.ResponseWriter, raw string) (*domain.Lifecycle, bool) {
	if strings.TrimSpace(raw) == "" {
		status := domain.LifecycleActive
		return &status, true
	}
	status, err := domain.ParseLifecycle(raw)
	if err != nil {
		h.writeDomainErr(w, err)
		return nil, false
	}
	return &status, true
}

func (h *Handler) artifactKind(w http.ResponseWriter, raw string) (*domain.ArtifactKind, bool) {
	if strings.TrimSpace(raw) == "" {
		return nil, true
	}
	kind, err := domain.ParseArtifactKind(raw)
	if err != nil {
		h.writeDomainErr(w, err)
		return nil, false
	}
	return &kind, true
}

func (h *Handler) memoryKind(w http.ResponseWriter, raw string) (*domain.MemoryKind, bool) {
	if strings.TrimSpace(raw) == "" {
		return nil, true
	}
	kind, err := domain.ParseMemoryKind(raw)
	if err != nil {
		h.writeDomainErr(w, err)
		return nil, false
	}
	return &kind, true
}

// window reads limit and offset.
//
// The limit is left to the repository, which lowers anything above its
// ceiling rather than refusing: a caller asking for too much wants as much
// as it can have. The offset is clamped here instead, because a negative
// one reaches Postgres as `OFFSET -1` and fails the whole statement, which
// would surface as a 500 for what is a typo in a query string.
func window(q url.Values) (limit, offset int) {
	return atoi(q.Get("limit")), max0(atoi(q.Get("offset")))
}

func atoi(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	return n
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// unfiledSentinel is what a caller sends to ask for the rows filed
// nowhere.
//
// ── Why a sentinel and not a separate parameter ────────────────────────
// Because `room_id` already answers "which room", and "no room" is an
// answer to that question rather than a different question. Two
// parameters would admit the contradictory state where both are set, and
// somebody would have to decide which wins. The same shape Job Radar uses
// for `stage=discover`.
//
// It cannot collide with a real value: `none` is not a uuid.
const unfiledSentinel = "none"

// applyRoomScope reads `room_id` into whichever filter fields the caller
// passes, and resolves a named room through the surface before any content
// is read.
//
// ── Why the room is resolved at all, when the predicate would cover it ─
// Because the two answers differ and both are right. Filtering by a
// withheld room's id would return an empty page, which says "that room has
// nothing in it" about a room the caller is not allowed to know exists.
// Resolving first turns it into the same not-found a fabricated id gets,
// which says nothing at all.
//
// Returns false when it has already written a response.
func (h *Handler) applyRoomScope(
	w http.ResponseWriter, r *http.Request, ws uuid.UUID,
	raw string, roomID **uuid.UUID, unfiled *bool,
) bool {
	raw = strings.TrimSpace(raw)
	switch {
	case raw == "":
		return true
	case raw == unfiledSentinel:
		*unfiled = true
		return true
	}

	parsed, err := uuid.Parse(raw)
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid",
			"room_id must be a room id or \""+unfiledSentinel+"\""))
		return false
	}
	room, err := h.svc.RoomForSurface(r.Context(), ws, parsed, surfaceVisibility())
	if err != nil {
		h.writeDomainErr(w, err)
		return false
	}
	id := room.ID
	*roomID = &id
	return true
}

/* ── the overview ────────────────────────────────────────────────────── */

// overview is the map: every eligible active room, with what it holds.
//
// Five statements regardless of how many rooms exist. Every number in the
// response is computed under the same rules as the listing that would
// return the rows, which is what makes a room card a claim the surface can
// actually honour when somebody opens it.
func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	out, err := h.svc.Overview(r.Context(), ws, surfaceVisibility())
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, overviewOf(out))
}

/* ── rooms ───────────────────────────────────────────────────────────── */

func (h *Handler) listRooms(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()

	status, ok := h.lifecycle(w, q.Get("status"))
	if !ok {
		return
	}
	limit, offset := window(q)

	filter := newRoomFilter()
	filter.Status = status
	filter.Search = q.Get("search")
	filter.Page = ports.Page{Limit: limit, Offset: offset}

	rooms, total, err := h.svc.ListRooms(r.Context(), ws, filter)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}

	rows := make([]roomRow, 0, len(rooms))
	for _, room := range rooms {
		rows = append(rows, roomRowOf(room))
	}
	render.JSON(w, http.StatusOK, newPage(rows, total, limit, offset))
}

func (h *Handler) getRoom(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "roomId", "room")
	if !ok {
		return
	}

	room, err := h.svc.RoomForSurface(r.Context(), ws, id, surfaceVisibility())
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	// Counted only after the room passed. Tallying first would be work
	// done on behalf of a caller who is about to get a 404, and the counts
	// themselves are facts about a room this surface has now agreed to
	// describe.
	tallies, err := h.svc.TalliesForRoom(r.Context(), ws, room.ID, surfaceVisibility())
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, roomDetailOf(room, tallies))
}

/* ── artifacts ───────────────────────────────────────────────────────── */

// listArtifacts returns the workspace's artifacts, optionally scoped.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THE CONTAINMENT RULE IS IN THE PREDICATE, NOT IN THE SHAPE OF THE URL
//
// ══════════════════════════════════════════════════════════════════════
//
// An earlier version of this handler required `room_id`, because a
// room-scoped listing is safe by construction: resolve the room through
// the surface, and everything inside it is something the surface already
// agreed to show. That was a correct listing of a narrower question, and
// it is no longer needed. The filter now carries the rule into SQL, so the
// unscoped listing is safe for the same reason the scoped one was, and it
// is safe in `count(*)` too.
//
// Three shapes, all honest:
//
//	room_id=<uuid>  that room, resolved through the surface first so a
//	                withheld room answers not-found before anything inside
//	                it is read
//	room_id=none    filed nowhere. A real state, not an error state
//	absent          everywhere, with the containment predicate doing the
//	                work the URL used to do
func (h *Handler) listArtifacts(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()

	filter := newArtifactFilter()
	if !h.applyRoomScope(w, r, ws, q.Get("room_id"), &filter.RoomID, &filter.Unfiled) {
		return
	}

	status, ok := h.lifecycle(w, q.Get("status"))
	if !ok {
		return
	}
	kind, ok := h.artifactKind(w, q.Get("kind"))
	if !ok {
		return
	}
	limit, offset := window(q)

	filter.Kind = kind
	filter.Status = status
	filter.Search = q.Get("search")
	filter.Page = ports.Page{Limit: limit, Offset: offset}

	artifacts, total, err := h.svc.ListArtifacts(r.Context(), ws, filter)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}

	// One grouped read for the whole page, not one per row. The ids come
	// from what was actually listed, so the tallies can only ever describe
	// the page: they cannot widen it.
	ids := make([]uuid.UUID, 0, len(artifacts))
	for _, a := range artifacts {
		ids = append(ids, a.ID)
	}
	tallies, err := h.svc.TalliesForArtifacts(r.Context(), ws, ids)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}

	rows := make([]artifactRow, 0, len(artifacts))
	for _, a := range artifacts {
		rows = append(rows, artifactRowOf(a, tallies[a.ID]))
	}
	render.JSON(w, http.StatusOK, newPage(rows, total, limit, offset))
}

func (h *Handler) getArtifact(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "artifactId", "artifact")
	if !ok {
		return
	}

	artifact, err := h.svc.ArtifactForSurface(r.Context(), ws, id, surfaceVisibility())
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}

	q := r.URL.Query()
	itemLimit, itemOffset := max0(atoi(q.Get("item_limit"))), max0(atoi(q.Get("item_offset")))

	// The entries are read only after the artifact passed the surface. An
	// entry carries no sensitivity of its own: it inherits everything from
	// the artifact that holds it, which is why checking the artifact once
	// is enough and why reading the entries first would be a leak.
	items, itemTotal, err := h.svc.ListItems(r.Context(), ws, artifact.ID,
		ports.Page{Limit: itemLimit, Offset: itemOffset})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}

	rows := make([]artifactItem, 0, len(items))
	for _, i := range items {
		rows = append(rows, artifactItemOf(i))
	}

	// The room is named only when there is one. Under D1 an eligible
	// artifact cannot be inside an ineligible room, so this read cannot
	// refuse: if it somehow did, the artifact should not have passed, and
	// failing loudly is better than rendering a card with a hole in it.
	var room *domain.Room
	if artifact.RoomID != nil {
		room, err = h.svc.RoomForSurface(r.Context(), ws, *artifact.RoomID, surfaceVisibility())
		if err != nil {
			h.writeDomainErr(w, err)
			return
		}
	}

	render.JSON(w, http.StatusOK,
		artifactDetailOf(artifact, room, rows, itemTotal, itemLimit, itemOffset))
}

// artifactNeighbors returns what is connected to one artifact.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THE HANDLER DOES NOT DECIDE VISIBILITY. IT PASSES IT DOWN
//
// ══════════════════════════════════════════════════════════════════════
//
// There is no check in this function, and there must not be one. The
// anchor is resolved through the surface, the endpoints are resolved under
// the same rules in SQL, and the withheld ones are never loaded. A filter
// written here would be a second policy, applied after the content had
// already arrived, which is the arrangement this whole slice avoids.
//
// The response carries no total and no count, so a caller learns about the
// neighbours it may see and nothing whatsoever about the rest.
func (h *Handler) artifactNeighbors(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "artifactId", "artifact")
	if !ok {
		return
	}

	neighbors, err := h.svc.ArtifactNeighbors(r.Context(), ws, id, surfaceVisibility())
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, neighborsOf(neighbors))
}

/* ── memories ────────────────────────────────────────────────────────── */

// listMemories returns the workspace's memories.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THE RULE HERE IS TRANSITIVE, AND THAT IS WHY THIS COULD NOT SHIP EARLY
//
// ══════════════════════════════════════════════════════════════════════
//
// Scoping by room would not have been enough, unlike for artifacts. A
// memory filed in a visible room can be ABOUT an artifact that lives in a
// withheld one, because `room_id` and `artifact_id` are independent
// references. Its summary then describes the withheld thing. So this
// listing waited for the predicate that follows both hops, and it applies
// it in SQL, inside `count(*)`, before `LIMIT`.
func (h *Handler) listMemories(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()

	filter := newMemoryFilter()
	if !h.applyRoomScope(w, r, ws, q.Get("room_id"), &filter.RoomID, &filter.Unfiled) {
		return
	}

	// The artifact is resolved through the surface for the reason a room
	// is: filtering by a withheld artifact's id would answer "that artifact
	// has no memories" about something the caller may not know exists.
	if raw := strings.TrimSpace(q.Get("artifact_id")); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid",
				"artifact_id is not a valid artifact id"))
			return
		}
		artifact, err := h.svc.ArtifactForSurface(r.Context(), ws, parsed, surfaceVisibility())
		if err != nil {
			h.writeDomainErr(w, err)
			return
		}
		id := artifact.ID
		filter.ArtifactID = &id
	}

	status, ok := h.lifecycle(w, q.Get("status"))
	if !ok {
		return
	}
	kind, ok := h.memoryKind(w, q.Get("kind"))
	if !ok {
		return
	}
	limit, offset := window(q)

	filter.Kind = kind
	filter.Status = status
	filter.MinImportance = max0(atoi(q.Get("min_importance")))
	filter.Search = q.Get("search")
	filter.Page = ports.Page{Limit: limit, Offset: offset}

	memories, total, err := h.svc.ListMemories(r.Context(), ws, filter)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}

	rows := make([]memoryRow, 0, len(memories))
	for _, m := range memories {
		rows = append(rows, memoryRowOf(m))
	}
	render.JSON(w, http.StatusOK, newPage(rows, total, limit, offset))
}

func (h *Handler) getMemory(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.ws(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "memoryId", "memory")
	if !ok {
		return
	}

	memory, err := h.svc.MemoryForSurface(r.Context(), ws, id, surfaceVisibility())
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, memoryDetailOf(memory))
}

/* ── errors ──────────────────────────────────────────────────────────── */

// writeDomainErr maps this context's failure vocabulary onto HTTP.
//
// The domain's own message is carried through, and that is safe by
// construction rather than by luck: every message this context produces
// names a field, a limit, a closed vocabulary or an id, and never quotes
// stored content. See domain/errors.go, which states the rule, and
// domain/privacy_test.go, which enforces it.
//
// Anything that is not a domain error becomes a generic 500 through
// apierror, which logs it and returns nothing about it. A repository
// failure must not describe the row it failed on.
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
	default:
		apierror.Write(w, h.log, err)
	}
}
