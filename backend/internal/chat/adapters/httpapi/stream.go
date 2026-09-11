package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/app"
	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/platform/apierror"
)

type sendMessageRequest struct {
	Content string `json:"content"`
	// References is the capability selection the composer attached to this
	// turn. Additive: a client that never sends it gets the behaviour it has
	// always had, and an explicit `[]` means the same as omitting it.
	//
	// See referenceRequest for why the wire shape carries no label.
	References []referenceRequest `json:"references"`
	// ContextReferences are the entities this turn is about. Additive in
	// exactly the same way: a client that never sends it behaves as it
	// always did.
	ContextReferences []contextReferenceRequest `json:"context_references"`
}

// referenceRequest is one attached capability, as the client may state it.
//
// Identity only. The display label of the permanent record is written by
// the server from its own registry — a client that could name a capability
// must not also get to name it whatever it likes in the transcript. The
// decoder rejects unknown fields, so sending a label is a 400 rather than a
// value that is silently thrown away, which is the honest answer to a
// client asserting something the server will not honour.
type referenceRequest struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// contextReferenceRequest is one attached ENTITY, as the client may state
// it.
//
// ── Why it carries no label either, and why that matters more here ─────
// Same rule as referenceRequest, with a sharper edge. A tool label the
// client could author would be a lie in a transcript; an ENTITY label the
// client could author would also be a lie in the MODEL'S CONTEXT — the
// block that tells the agent what it is looking at. So the label is
// resolved server-side from the owning module and whatever the client sent
// is not read. The decoder rejects unknown fields, so sending one is a 400
// rather than a value silently discarded.
type contextReferenceRequest struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// toContextReferences converts the wire shape, carrying identity only.
// Resolution, workspace scoping and the label all happen in the
// application layer — see app.admitContextReferences.
func toContextReferences(in []contextReferenceRequest) []domain.ContextReference {
	if len(in) == 0 {
		return nil
	}
	out := make([]domain.ContextReference, 0, len(in))
	for _, r := range in {
		out = append(out, domain.ContextReference{
			Type: domain.ContextReferenceType(r.Type),
			ID:   r.ID,
		})
	}
	return out
}

// sendMessage streams the model's reply as Server-Sent Events.
//
// The stream is opened lazily — on the first byte the model produces, not
// when the handler starts. That keeps the ordinary failures (unknown
// conversation, unreadable credential, provider refusing the key) as real
// HTTP status codes with the same error body as every other endpoint,
// because none of them can happen after a token has been sent. Once the
// stream is open there is no status code left to give, so failures are
// reported as an `error` frame inside it.
//
// Frames:
//
//	event: reasoning  data: {"text":"…"}
//	event: delta      data: {"text":"…"}
//	event: tool       data: {"call_id":"…","name":"…","status":"running|ok|error",…}
//	event: done       data: {"message":{…}}
//	event: error      data: {"error":{"code":"…","message":"…"}}
//
// `reasoning` frames, when a reasoning model produces them, all arrive
// before the first `delta`.
//
// `tool` frames arrive only on a turn that runs a tool, in pairs: one
// `running` when a call starts, one `ok` or `error` when it ends. A turn
// with no tools emits none, so the four frames that existed before are
// exactly the four such a turn still sends — and a client that has never
// heard of `tool` ignores the event name, which is how SSE is specified to
// behave.
func (h *Handler) sendMessage(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	var req sendMessageRequest
	if !h.decode(w, r, &req) {
		return
	}

	sink := newSSESink(w, r.Context(), h.log)
	// The receipt is read after the turn has been written back, using a
	// context that outlives the request the way the write-back does: a
	// client that hung up still gets its audit row, and the frame is simply
	// not delivered.
	sink.receipts = func(messageID uuid.UUID) (domain.WriteReceipt, error) {
		got, err := h.svc.WriteReceipts(context.WithoutCancel(r.Context()), ws, []uuid.UUID{messageID})
		if err != nil {
			return domain.WriteReceipt{}, err
		}
		return got[messageID], nil
	}
	// The read-side twin, on the same terms.
	sink.readReceipts = func(messageID uuid.UUID) (domain.ReadReceipt, error) {
		got, err := h.svc.ReadReceipts(context.WithoutCancel(r.Context()), ws, id, []uuid.UUID{messageID})
		if err != nil {
			return domain.ReadReceipt{}, err
		}
		return got[messageID], nil
	}

	msg, err := h.svc.SendMessage(r.Context(), app.SendMessageInput{
		WorkspaceID:       ws,
		ConversationID:    id,
		Content:           req.Content,
		References:        toTurnReferences(req.References),
		ContextReferences: toContextReferences(req.ContextReferences),
	}, sink)

	if err != nil {
		if !sink.opened {
			h.writeDomainErr(w, err)
			return
		}
		sink.writeError(err)
		return
	}
	sink.writeDone(msg)
}

// toTurnReferences lifts the wire shape into the domain's, unchanged and
// unvalidated.
//
// Deliberately dumb: whether a kind exists, whether the tool is real and
// whether this agent may use it are all questions for the application
// layer, which is the only place that can see the registry and the grants.
// A transport that answered any of them would be a second authorization
// point, free to disagree with the first.
//
// An empty list becomes a nil slice, because omitted and empty are the same
// input — see app.SendMessageInput.References.
func toTurnReferences(in []referenceRequest) []domain.TurnReference {
	if len(in) == 0 {
		return nil
	}
	out := make([]domain.TurnReference, 0, len(in))
	for _, r := range in {
		out = append(out, domain.TurnReference{
			Kind: domain.ReferenceKind(r.Kind),
			ID:   r.ID,
		})
	}
	return out
}

// --- SSE sink -------------------------------------------------------------

type sseSink struct {
	w      http.ResponseWriter
	rc     *http.ResponseController
	ctx    context.Context
	log    *slog.Logger
	opened bool
	// receipts resolves the finished turn's write receipt. A function
	// rather than a service handle so the sink keeps knowing nothing about
	// the application layer, which is the arrangement every other frame
	// already has.
	receipts     func(messageID uuid.UUID) (domain.WriteReceipt, error)
	readReceipts func(messageID uuid.UUID) (domain.ReadReceipt, error)
}

func newSSESink(w http.ResponseWriter, ctx context.Context, log *slog.Logger) *sseSink {
	// ResponseController rather than a direct http.Flusher assertion: the
	// router wraps the writer twice (request logging, then metrics), and
	// the controller unwraps through both to reach the real one.
	return &sseSink{w: w, rc: http.NewResponseController(w), ctx: ctx, log: log}
}

func (s *sseSink) open() {
	if s.opened {
		return
	}
	h := s.w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	// Without this, an nginx in front of the API buffers the whole response
	// and the reply lands in one lump at the end — the stream still works,
	// but nothing about it looks streamed.
	h.Set("X-Accel-Buffering", "no")
	s.w.WriteHeader(http.StatusOK)
	s.opened = true
	_ = s.rc.Flush()
}

// event writes one SSE frame. Payloads are JSON-encoded, which also solves
// the framing problem: SSE terminates a frame on a blank line, and JSON
// escaping guarantees the encoded data contains no raw newline.
func (s *sseSink) event(name string, payload any) error {
	if err := s.ctx.Err(); err != nil {
		return err // client already gone; don't bother writing
	}
	s.open()

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal %s frame: %w", name, err)
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, data); err != nil {
		return err
	}
	// Flush is where a dead connection actually surfaces — Write alone
	// buffers and can succeed long after the reader is gone.
	return s.rc.Flush()
}

// Reasoning, Delta and Tool implement app.TurnSink.
func (s *sseSink) Reasoning(text string) error {
	return s.event("reasoning", map[string]string{"text": text})
}

func (s *sseSink) Delta(text string) error {
	return s.event("delta", map[string]string{"text": text})
}

// Tool forwards a tool call's progress. The payload is the app-layer event
// verbatim: it already carries only the names and outcomes a transcript
// shows, and no arguments or results.
func (s *sseSink) Tool(ev app.ToolEvent) error {
	return s.event("tool", ev)
}

func (s *sseSink) writeDone(msg *domain.Message) {
	// msg is nil only when the post-turn write-back failed; the reply was
	// still delivered, so the stream closes normally and the client keeps
	// what it rendered.
	payload := map[string]any{"message": msg}
	// ── The turn's own receipt, in the frame that ends it ───────────
	//
	// So a live client never has to infer from the prose it just rendered
	// whether anything changed. A turn with no write executed carries
	// `write_receipt.executed == 0`, which is the statement that would have
	// contradicted "8 transações importadas" when a model said it after
	// calling nothing.
	//
	// Built from the same audit rows the transcript reads, so the live view
	// and a reload cannot disagree.
	if msg != nil && s.receipts != nil {
		if r, err := s.receipts(msg.ID); err == nil {
			payload["write_receipt"] = r
		} else {
			s.log.Warn("write receipt for done frame", "message_id", msg.ID, "err", err)
		}
	}
	// ── The read-side twin, and the reason it is not optional ──────
	//
	// A turn that read nothing external carries
	// `read_receipt.status == "NO_EXTERNAL_READ"`, which is the statement
	// that would have contradicted "1.535 seguidores, 40.055 views" when a
	// model produced those figures after calling nothing.
	//
	// Emitted for every turn, including the ones that read nothing:
	// absence has to be a value, or a surface renders nothing at all for
	// exactly the turn that invented a number.
	if msg != nil && s.readReceipts != nil {
		if r, err := s.readReceipts(msg.ID); err == nil {
			payload["read_receipt"] = r
		} else {
			s.log.Warn("read receipt for done frame", "message_id", msg.ID, "err", err)
		}
	}
	if err := s.event("done", payload); err != nil {
		s.log.Debug("write done frame", "err", err)
	}
}

func (s *sseSink) writeError(err error) {
	apiErr := domainAPIError(err)
	if apiErr == nil {
		s.log.Error("unhandled error mid-stream", "err", err)
		apiErr = apierror.ErrInternal
	}
	if werr := s.event("error", map[string]any{"error": apiErr}); werr != nil {
		s.log.Debug("write error frame", "err", werr)
	}
}
