package httpapi

// The wire types of the Palace read surface.
//
// ══════════════════════════════════════════════════════════════════════
//
//	NOTHING HERE IS A PALACE ENTITY, AND NOTHING EVER MAY BE
//
// ══════════════════════════════════════════════════════════════════════
//
// Every response below is a struct declared in this file and filled field
// by field. No handler hands a Room, an Artifact, a Memory or a Session to
// an encoder, directly or nested inside something else.
//
// ── Why that is a rule and not a preference ────────────────────────────
// Because the entities REDACT THEMSELVES. `domain.Room` implements
// MarshalJSON and returns `{"type":"palace.room","id":…,"redacted":true}`,
// and so do Artifact, ArtifactItem, Memory, Source and Session. See
// domain/redaction.go, which exists so that a careless `log.Info("saved",
// "memory", m)` cannot put somebody's text into stdout.
//
// That net makes the failure SAFE, not invisible: a handler that shortcut
// through the entity would ship a response with no content in it at all,
// and the surface would be broken rather than leaky. The rule here is the
// other half of the same design, and it is the half that keeps the
// surface working: a projection that wants to expose a field has to NAME
// it, and this file is where every name is.
//
// There is a test that fails if any response type gains a field whose
// type comes from the domain package, and another that fails if a
// response body ever contains `"redacted"`. Both are regression detectors
// for exactly this boundary.

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/palace/app"
	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/palace/ports"
)

/* ── shared shaping ──────────────────────────────────────────────────── */

// timeFormat is RFC3339 in UTC, the same instant format the Palace tools
// emit. One format across both surfaces, so a reader that learned it on
// one recognises it on the other.
const timeFormat = "2006-01-02T15:04:05Z"

func stamp(t time.Time) string { return t.UTC().Format(timeFormat) }

func idString(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	s := id.String()
	return &s
}

// excerptRunes bounds the preview a listing carries per row.
//
// 160, which is the same number the tools use, and the duplication is
// deliberate rather than an oversight: the constant belongs to the idea of
// "a row in a listing", and the tools package does not export it. Sharing
// it would mean this adapter importing the tool adapter, which is a
// dependency between two sibling adapters that has no other reason to
// exist. Two adapters agreeing on a number is cheaper than that edge.
const excerptRunes = 160

// excerpt returns the opening of some text, collapsed to one line, and
// whether it had to cut.
//
// An excerpt that does not declare itself is indistinguishable from a very
// short record, so the caller always gets both values.
func excerpt(text string) (string, bool) {
	flat := strings.Join(strings.Fields(text), " ")
	if flat == "" {
		return "", false
	}
	runes := []rune(flat)
	if len(runes) <= excerptRunes {
		return flat, false
	}
	return strings.TrimSpace(string(runes[:excerptRunes])) + "…", true
}

/* ── the listing envelope ────────────────────────────────────────────── */

// page is the shape every listing on this surface returns.
//
// ── Why total is carried, always ───────────────────────────────────────
// Because a caller has to know whether it is looking at everything, and
// inferring completeness from the length of a page is how a screen ends up
// saying "4 rooms" over a truncated list. The total obeys the SAME
// predicate as the items, which the repository guarantees by sharing one
// where-clause between List and Count: a total that counted withheld rows
// would announce the existence of the row being withheld.
type page[T any] struct {
	Items  []T   `json:"items"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

// newPage guarantees `items` is an array and never `null`, so a client
// can iterate without a nil check.
func newPage[T any](items []T, total int64, limit, offset int) page[T] {
	if items == nil {
		items = []T{}
	}
	return page[T]{Items: items, Total: total, Limit: limit, Offset: offset}
}

/* ── rooms ───────────────────────────────────────────────────────────── */

// roomRow is one room in a listing.
//
// `description` is carried whole rather than as an excerpt, and that is a
// difference from the tool surface on purpose: a tool result is spent as
// prompt tokens on the next provider call, and a screen is not. The bound
// on the column is 2000 characters, which is a paragraph.
type roomRow struct {
	RoomID      string `json:"room_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Sensitivity string `json:"sensitivity"`
	// CreatedAt is the stable key. A projection that wants a deterministic
	// arrangement orders on this and never on UpdatedAt, which moves every
	// time somebody edits a word.
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func roomRowOf(r *domain.Room) roomRow {
	return roomRow{
		RoomID:      r.ID.String(),
		Name:        r.Name,
		Description: r.Description,
		Status:      string(r.Status),
		Sensitivity: string(r.Sensitivity),
		CreatedAt:   stamp(r.CreatedAt),
		UpdatedAt:   stamp(r.UpdatedAt),
	}
}

// roomDetail is one room read by id, with what it contains.
//
// ── Why the counts are written out instead of embedding roomRow ────────
// Because this package's rule is that a field reaching the wire has to be
// NAMED here. An embedded struct inlines whatever it happens to carry, so
// a field added to the listing row would silently appear in the detail
// too. Nine lines of repetition is the price of the surface staying
// explicit, and it is the same price the tool adapter pays.
type roomDetail struct {
	RoomID      string `json:"room_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Sensitivity string `json:"sensitivity"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`

	// The three counts are of ELIGIBLE rows only, computed under the same
	// predicate as the listings that would return them. A number here that
	// counted a withheld row would publish its existence without returning
	// a character of it.
	ArtifactCount int64 `json:"artifact_count"`
	MemoryCount   int64 `json:"memory_count"`
	ArchivedCount int64 `json:"archived_count"`
}

func roomDetailOf(r *domain.Room, t *app.RoomTallies) roomDetail {
	return roomDetail{
		RoomID:        r.ID.String(),
		Name:          r.Name,
		Description:   r.Description,
		Status:        string(r.Status),
		Sensitivity:   string(r.Sensitivity),
		CreatedAt:     stamp(r.CreatedAt),
		UpdatedAt:     stamp(r.UpdatedAt),
		ArtifactCount: t.ArtifactCount,
		MemoryCount:   t.MemoryCount,
		ArchivedCount: t.ArchivedCount,
	}
}

/* ── the overview ────────────────────────────────────────────────────── */

// overviewRoom is one room on the map.
//
// No `status`: the map is the active space by definition, so a field that
// always reads `active` would be a field a client could start branching
// on. Archived rooms are reached through the listing's explicit filter.
type overviewRoom struct {
	RoomID      string `json:"room_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Sensitivity string `json:"sensitivity"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`

	ArtifactCount int64 `json:"artifact_count"`
	MemoryCount   int64 `json:"memory_count"`
	// ArchivedCount is what the room has retired, and it is NOT a withheld
	// count: archiving is the operator's own act and the rows stay
	// reachable through an explicit status filter. The affordance that
	// opens an archive needs to know whether there is anything behind it,
	// and an affordance that opens onto nothing is the one thing this
	// surface must not draw.
	ArchivedCount int64 `json:"archived_count"`
}

// overviewUnfiled is what belongs to no room.
//
// Present always, including when both counts are zero. A key that
// disappeared when empty would make "nothing is unfiled" and "this server
// does not report unfiled" the same response.
type overviewUnfiled struct {
	ArtifactCount int64 `json:"artifact_count"`
	ArchivedCount int64 `json:"archived_count"`
}

type overviewResponse struct {
	Rooms []overviewRoom `json:"rooms"`
	// RoomTotal is how many eligible active rooms exist, ignoring the
	// ceiling `rooms` was cut to.
	//
	// ── Why a map carries a total ──────────────────────────────────────
	// Because the alternative is a map that is quietly short. Every
	// listing in this surface reports its unbounded total for the same
	// reason, and a summary is where a missing row is hardest to notice:
	// there is no page two to be absent from. It counts only what the
	// caller could reach, exactly like every other number here.
	RoomTotal int64           `json:"room_total"`
	Unfiled   overviewUnfiled `json:"unfiled"`
}

func overviewOf(o *app.Overview) overviewResponse {
	rooms := make([]overviewRoom, 0, len(o.Rooms))
	for _, r := range o.Rooms {
		rooms = append(rooms, overviewRoom{
			RoomID:        r.Room.ID.String(),
			Name:          r.Room.Name,
			Description:   r.Room.Description,
			Sensitivity:   string(r.Room.Sensitivity),
			CreatedAt:     stamp(r.Room.CreatedAt),
			UpdatedAt:     stamp(r.Room.UpdatedAt),
			ArtifactCount: r.ArtifactCount,
			MemoryCount:   r.MemoryCount,
			ArchivedCount: r.ArchivedCount,
		})
	}
	return overviewResponse{
		Rooms:     rooms,
		RoomTotal: o.RoomTotal,
		Unfiled: overviewUnfiled{
			ArtifactCount: o.Unfiled.ArtifactCount,
			ArchivedCount: o.Unfiled.ArchivedCount,
		},
	}
}

/* ── artifacts ───────────────────────────────────────────────────────── */

// artifactRow is one artifact in a listing.
//
// `item_count` and `item_done_count` come from one grouped read over the
// whole page, not one read per row. An artifact with no entries reports
// two zeroes, which is the true answer rather than a missing key: unlike
// a count that was never computed, this one was.
type artifactRow struct {
	ArtifactID  string  `json:"artifact_id"`
	Kind        string  `json:"kind"`
	Title       string  `json:"title"`
	Status      string  `json:"status"`
	Sensitivity string  `json:"sensitivity"`
	RoomID      *string `json:"room_id"`

	ItemCount     int64 `json:"item_count"`
	ItemDoneCount int64 `json:"item_done_count"`

	BodyExcerpt   string `json:"body_excerpt"`
	BodyTruncated bool   `json:"body_truncated"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

func artifactRowOf(a *domain.Artifact, tally ports.ItemTally) artifactRow {
	body, truncated := excerpt(a.Body)
	return artifactRow{
		ArtifactID:    a.ID.String(),
		Kind:          string(a.Kind),
		Title:         a.Title,
		Status:        string(a.Status),
		Sensitivity:   string(a.Sensitivity),
		RoomID:        idString(a.RoomID),
		ItemCount:     tally.Total,
		ItemDoneCount: tally.Done,
		BodyExcerpt:   body,
		BodyTruncated: truncated,
		CreatedAt:     stamp(a.CreatedAt),
		UpdatedAt:     stamp(a.UpdatedAt),
	}
}

// artifactRoomRef names the room an artifact is filed in.
//
// Only ever built for a room this surface already resolved. Under D1 an
// eligible artifact cannot be inside an ineligible room, so this is null
// exactly when the artifact is unfiled, and never as a way of saying "its
// room exists but you may not see it": that state cannot reach here,
// because the artifact would not have.
type artifactRoomRef struct {
	RoomID string `json:"room_id"`
	Name   string `json:"name"`
}

// artifactItem is one entry of a list, step of a plan or task of a
// project.
type artifactItem struct {
	ItemID   string `json:"item_id"`
	Position int    `json:"position"`
	Text     string `json:"text"`
	Done     bool   `json:"done"`
}

func artifactItemOf(i *domain.ArtifactItem) artifactItem {
	return artifactItem{
		ItemID:   i.ID.String(),
		Position: i.Position,
		Text:     i.Text,
		Done:     i.Done,
	}
}

// artifactDetail is the whole record, which is the right answer for a read
// by id: the caller has decided WHICH artifact it is looking at, and the
// next thing it does is display the text. An abbreviated body here would
// show a truncation as though it were the work.
//
// ── Why the entries paginate instead of truncating ─────────────────────
// A checklist longer than the ceiling is the operator's work, and silently
// dropping its tail would make the surface lie about how much there is.
// The entries carry their own window and their own total, so a caller can
// ask for the rest.
type artifactDetail struct {
	ArtifactID  string           `json:"artifact_id"`
	Kind        string           `json:"kind"`
	Title       string           `json:"title"`
	Body        string           `json:"body"`
	Status      string           `json:"status"`
	Sensitivity string           `json:"sensitivity"`
	RoomID      *string          `json:"room_id"`
	Room        *artifactRoomRef `json:"room"`
	CreatedAt   string           `json:"created_at"`
	UpdatedAt   string           `json:"updated_at"`

	Items      []artifactItem `json:"items"`
	ItemTotal  int64          `json:"item_total"`
	ItemLimit  int            `json:"item_limit"`
	ItemOffset int            `json:"item_offset"`
}

func artifactDetailOf(a *domain.Artifact, room *domain.Room, items []artifactItem, total int64, limit, offset int) artifactDetail {
	if items == nil {
		items = []artifactItem{}
	}
	var ref *artifactRoomRef
	if room != nil {
		ref = &artifactRoomRef{RoomID: room.ID.String(), Name: room.Name}
	}
	return artifactDetail{
		ArtifactID:  a.ID.String(),
		Kind:        string(a.Kind),
		Title:       a.Title,
		Body:        a.Body,
		Status:      string(a.Status),
		Sensitivity: string(a.Sensitivity),
		RoomID:      idString(a.RoomID),
		Room:        ref,
		CreatedAt:   stamp(a.CreatedAt),
		UpdatedAt:   stamp(a.UpdatedAt),
		Items:       items,
		ItemTotal:   total,
		ItemLimit:   limit,
		ItemOffset:  offset,
	}
}

/* ── neighbours ──────────────────────────────────────────────────────── */

// neighborArtifact is one artifact on the other end of a relation.
//
// Four fields and no excerpt: an inspector's side panel names what is
// connected, and the body is one click away through the artifact's own
// read. `status` is carried so an archived neighbour can be SHOWN AS
// archived rather than dropped: a supersedes chain with its retired links
// removed is a history with holes in it.
type neighborArtifact struct {
	ArtifactID string `json:"artifact_id"`
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	Status     string `json:"status"`
}

// neighborMemory is one memory on the other end of a relation.
type neighborMemory struct {
	MemoryID string `json:"memory_id"`
	Label    string `json:"label"`
	Status   string `json:"status"`
}

// neighborEntity is the mixed shape, for the relation kinds whose other
// end may be either type.
//
// `type` is carried because the caller needs to know which read to follow,
// and `label` rather than `title` because the two entities name themselves
// differently: an artifact has a title, a memory has a summary or its
// first sentence.
type neighborEntity struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"`
}

// neighborsResponse is what is connected to one artifact.
//
// ══════════════════════════════════════════════════════════════════════
//
//	NO TOTAL, NO COUNT, NO PLACEHOLDER, IN ANY FIELD
//
// ══════════════════════════════════════════════════════════════════════
//
// A withheld neighbour leaves no trace here. Not a count of what was
// dropped, not an id, not an entry with a blank label. If ten relations
// exist and three neighbours are eligible, this carries three, and there
// is nothing in it from which the other seven can be inferred. A raw
// relation count would be the entire leak in one integer.
//
// Every slice is an array, never null, including the empty ones: a key
// that disappeared would make "nothing supersedes this" and "this server
// does not report supersedes" the same response.
type neighborsResponse struct {
	// Supersedes: what this artifact REPLACES. SupersededBy: what replaces
	// it. Two keys and not one signed field, because the direction is the
	// thing most easily got backwards and a reader that inverts it renders
	// a confident, coherent, wrong history.
	Supersedes   []neighborArtifact `json:"supersedes"`
	SupersededBy []neighborArtifact `json:"superseded_by"`

	Mentions    []neighborArtifact `json:"mentions"`
	MentionedBy []neighborEntity   `json:"mentioned_by"`
	Related     []neighborEntity   `json:"related"`
	Decisions   []neighborMemory   `json:"decisions"`
}

// memoryLabel is the one line a memory shows when it is not the subject.
//
// The summary when the operator wrote one, because they wrote it to be
// exactly this; an excerpt of the content otherwise, declaring nothing
// about its truncation, because a neighbour label is a handle rather than
// a reading of the memory.
func memoryLabel(m *domain.Memory) string {
	if s := strings.TrimSpace(m.Summary); s != "" {
		return s
	}
	label, _ := excerpt(m.Content)
	return label
}

func neighborArtifactOf(a *domain.Artifact) neighborArtifact {
	return neighborArtifact{
		ArtifactID: a.ID.String(),
		Kind:       string(a.Kind),
		Title:      a.Title,
		Status:     string(a.Status),
	}
}

func neighborsOf(n *app.ArtifactNeighbors) neighborsResponse {
	out := neighborsResponse{
		Supersedes:   artifactList(n.Supersedes),
		SupersededBy: artifactList(n.SupersededBy),
		Mentions:     artifactList(n.Mentions),
		MentionedBy:  entityList(n.MentionedBy),
		Related:      entityList(n.Related),
		Decisions:    make([]neighborMemory, 0, len(n.Decisions)),
	}
	for _, m := range n.Decisions {
		out.Decisions = append(out.Decisions, neighborMemory{
			MemoryID: m.ID.String(),
			Label:    memoryLabel(m),
			Status:   string(m.Status),
		})
	}
	return out
}

func artifactList(in []*domain.Artifact) []neighborArtifact {
	out := make([]neighborArtifact, 0, len(in))
	for _, a := range in {
		out = append(out, neighborArtifactOf(a))
	}
	return out
}

func entityList(set app.NeighborSet) []neighborEntity {
	out := make([]neighborEntity, 0, len(set.Artifacts)+len(set.Memories))
	for _, a := range set.Artifacts {
		out = append(out, neighborEntity{
			Type:   string(domain.EntityArtifact),
			ID:     a.ID.String(),
			Label:  a.Title,
			Status: string(a.Status),
		})
	}
	for _, m := range set.Memories {
		out = append(out, neighborEntity{
			Type:   string(domain.EntityMemory),
			ID:     m.ID.String(),
			Label:  memoryLabel(m),
			Status: string(m.Status),
		})
	}
	return out
}

/* ── memories ────────────────────────────────────────────────────────── */

// memoryRow is one memory in a listing.
//
// ── Why the summary wins over an excerpt when it exists ────────────────
// Because somebody wrote it on purpose to be the one line a listing shows,
// and an excerpt of the content would be the first sentence rather than
// the point. When there is no summary, the excerpt is the honest fallback
// and it declares its own truncation.
type memoryRow struct {
	MemoryID    string  `json:"memory_id"`
	Kind        string  `json:"kind"`
	Importance  int     `json:"importance"`
	Confidence  string  `json:"confidence"`
	Status      string  `json:"status"`
	Sensitivity string  `json:"sensitivity"`
	OccurredAt  *string `json:"occurred_at"`
	RoomID      *string `json:"room_id"`
	ArtifactID  *string `json:"artifact_id"`

	Summary          string `json:"summary"`
	ContentExcerpt   string `json:"content_excerpt"`
	ContentTruncated bool   `json:"content_truncated"`

	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func memoryRowOf(m *domain.Memory) memoryRow {
	row := memoryRow{
		MemoryID:    m.ID.String(),
		Kind:        string(m.Kind),
		Importance:  m.Importance,
		Confidence:  string(m.Confidence),
		Status:      string(m.Status),
		Sensitivity: string(m.Sensitivity),
		OccurredAt:  stampPtr(m.OccurredAt),
		RoomID:      idString(m.RoomID),
		ArtifactID:  idString(m.ArtifactID),
		Summary:     strings.TrimSpace(m.Summary),
		CreatedAt:   stamp(m.CreatedAt),
		UpdatedAt:   stamp(m.UpdatedAt),
	}
	if row.Summary == "" {
		row.ContentExcerpt, row.ContentTruncated = excerpt(m.Content)
	}
	return row
}

func stampPtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := stamp(*t)
	return &s
}

// memoryDetail is one memory read by id, with its content whole.
//
// `occurred_at` is when the thing the memory is ABOUT happened, and it is
// distinct from `created_at`, which is when it was written down. The two
// are routinely far apart, and a surface that showed only one of them
// would date somebody's knowledge by the day they happened to record it.
type memoryDetail struct {
	MemoryID    string  `json:"memory_id"`
	Kind        string  `json:"kind"`
	Content     string  `json:"content"`
	Summary     string  `json:"summary"`
	Importance  int     `json:"importance"`
	Confidence  string  `json:"confidence"`
	Status      string  `json:"status"`
	Sensitivity string  `json:"sensitivity"`
	OccurredAt  *string `json:"occurred_at"`
	RoomID      *string `json:"room_id"`
	ArtifactID  *string `json:"artifact_id"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

func memoryDetailOf(m *domain.Memory) memoryDetail {
	occurred := stampPtr(m.OccurredAt)
	return memoryDetail{
		MemoryID:    m.ID.String(),
		Kind:        string(m.Kind),
		Content:     m.Content,
		Summary:     m.Summary,
		Importance:  m.Importance,
		Confidence:  string(m.Confidence),
		Status:      string(m.Status),
		Sensitivity: string(m.Sensitivity),
		OccurredAt:  occurred,
		RoomID:      idString(m.RoomID),
		ArtifactID:  idString(m.ArtifactID),
		CreatedAt:   stamp(m.CreatedAt),
		UpdatedAt:   stamp(m.UpdatedAt),
	}
}
