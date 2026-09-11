package tools

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
	chatports "github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/threads/app"
	"github.com/corsi/backend/internal/threads/domain"
)

// The Threads context-reference resolver.
//
// ── Why it lives beside the tools and not in a package of its own ──────
// Because it is the same relationship to the same collaborator: this file
// and tools.go both satisfy an interface the Agents module declares, both
// call the Threads application service, and both are handed over by
// module.go to the composition root. Splitting them would create a second
// package whose entire content is "the other way Agents reaches Threads".
//
// ── What it does NOT do, and this is the point ─────────────────────────
// It does not return the thread. It returns the words needed to recognise
// one:
//
//	resolver  →  "Microservices cedo demais"        (which piece this is)
//	tool      →  the text, the status, the history  (what it says)
//
// The distinction is what keeps a reference from becoming a capability. An
// agent with a reference and without a grant can see the subject's TITLE —
// which the user just told it by attaching it — and cannot read a word of
// the draft. Everything it learns beyond the name comes through
// threads.thread.get, which is refused without authorization.
//
// ── Hydration: the same service, a different question ──────────────────
// This file also answers "what does it say NOW", through the hydrator half
// of the port:
//
//	Resolve   →  identity + STABLE recognition text.  Persisted. Ungated:
//	             the user attached it, so they may see what it is called.
//	Hydrate   →  MUTABLE state, read fresh every turn. Never persisted.
//	             Gated on the agent's grant for ThreadGetTool.
//
// Nothing here decides whether the agent may read state. Agents checks the
// grant before this file's Hydrate is reached — see
// Service.hydrationAuthorized — which is why Hydrate may call the same
// application service the tool calls without that being a second, quieter
// door into the same data. It reads on behalf of an agent that was
// authorized to read exactly this, or it is not called.

// referenceResolver answers for Threads' entity types.
type referenceResolver struct{ svc *app.Service }

// NewReferenceResolver builds the resolver, ready for the reference
// registry in cmd/corsi.
func NewReferenceResolver(svc *app.Service) chatports.ContextReferenceResolver {
	return referenceResolver{svc: svc}
}

// ThreadReferenceType is the one type this module owns today.
//
// Exported because the composition root does not name it but the tests do,
// and a string literal repeated across files is a rename waiting to go
// half-done.
const ThreadReferenceType chatdomain.ContextReferenceType = "threads.thread"

func (referenceResolver) Types() []chatdomain.ContextReferenceType {
	return []chatdomain.ContextReferenceType{ThreadReferenceType}
}

// Resolve looks one thread up for one workspace.
//
// ── Why a bad id is `found=false` and not an error ─────────────────────
// Because the caller is holding a string that came from a client, and the
// three ways it can fail to name something this workspace owns —
// malformed, never existed, someone else's — are one answer on purpose.
// Reporting them apart would let a fabricated id be used to probe for the
// existence of rows in other workspaces, which is exactly the attack the
// single answer closes. An `error` is kept for a genuine failure to ask,
// such as the database being unreachable, because that is not a fact about
// the id.
func (r referenceResolver) Resolve(
	ctx context.Context,
	workspaceID uuid.UUID,
	ref chatdomain.ContextReference,
) (chatports.ResolvedReference, bool, error) {
	if ref.Type != ThreadReferenceType {
		return chatports.ResolvedReference{}, false, nil
	}

	id, err := uuid.Parse(strings.TrimSpace(ref.ID))
	if err != nil {
		return chatports.ResolvedReference{}, false, nil
	}

	// The same application service the tools call. There is no second read
	// path to Threads, and no repository reachable from here.
	//
	// The workspace comes from the caller and is applied inside the service,
	// which filters in SQL — so an id belonging to another workspace reads
	// exactly like one that never existed, without this function having to
	// remember to check anything.
	thread, err := r.svc.GetThread(ctx, workspaceID, id)
	if err != nil {
		var de *domain.Error
		if errors.As(err, &de) && de.Kind == domain.KindNotFound {
			return chatports.ResolvedReference{}, false, nil
		}
		return chatports.ResolvedReference{}, false, err
	}

	// ── Why there is no subtitle ───────────────────────────────────────
	// Because a subtitle is written ONCE, at attach time, into a JSONB
	// column that is never rewritten — so it may only carry facts that do
	// not move. Job Radar learned this the expensive way: it put the stage
	// in the subtitle, and a conversation opened at `applied` kept saying
	// `applied` in the model's context after the opportunity reached
	// `interview`.
	//
	// A thread has NOTHING stable below its title. The status moves, the
	// text moves, the timestamp moves — that is what a content thread is.
	// So the honest subtitle is the empty one, and every mutable fact
	// reaches the model through hydration, freshly, on the turn it matters.
	// An empty line is better than a wrong one.
	return chatports.ResolvedReference{Label: thread.Title}, true, nil
}

/* ── hydration ───────────────────────────────────────────────────────── */

// hydratedContentRunes bounds the text hydration carries per turn.
//
// ── Why content is hydrated at all ─────────────────────────────────────
// Because it is the fact this mechanism exists for. A conversation about a
// thread rewrites it repeatedly, and the model's own message from three
// turns ago — carrying draft A in full — is the strongest evidence in its
// context. Without a fresher reading it edits A, and everything since is
// silently reverted. That is the exact failure the hydration design
// describes, and content is where it happens hardest.
//
// ── Why it is bounded, and why the bound is honest ─────────────────────
// Every character is a prompt token on EVERY turn the subject is attached.
// 4000 runes is several times any social post and comfortably holds a short
// essay, so the real case hydrates in full. Above it, the text is NOT
// truncated into the context: a truncated draft is worse than no draft,
// because the model would rewrite from it and hand back a shortened piece
// believing it was complete. Instead the field says how large it is and
// where to read it, which turns a silent corruption into one extra tool
// call.
const hydratedContentRunes = 4000

// HydrationPolicy declares how this module's entities stay fresh.
//
// ── Why per_turn for a thread ──────────────────────────────────────────
// The two properties the design asks for, and it has both. It MOVES —
// harder than anything else in this system; a single conversation can
// rewrite the same thread four times. And it is bounded: the status is one
// word and the text has a ceiling above which it is described rather than
// carried, so a turn's cost cannot run away.
//
// ── Why the capability is the get tool ─────────────────────────────────
// Because hydration reads exactly what `threads.thread.get` reads, from the
// same service, and nothing an agent could not have read by calling it.
// Naming any other capability would be claiming the read is a different
// kind of access than it is; naming none would be asking to skip the check.
func (referenceResolver) HydrationPolicy(t chatdomain.ContextReferenceType) (chatports.ReferenceHydrationPolicy, bool) {
	if t != ThreadReferenceType {
		return chatports.ReferenceHydrationPolicy{}, false
	}
	return chatports.ReferenceHydrationPolicy{
		Freshness:  chatports.FreshnessPerTurn,
		Capability: ThreadGetTool,
	}, true
}

// Hydrate reads one thread's present state.
//
// ── Why a bad id is found=false and not an error ───────────────────────
// The same rule Resolve follows: malformed, never existed, and another
// workspace's are one answer, so a fabricated id cannot be used to probe
// for rows this workspace may not see. Note that the id reaching here was
// admitted for this workspace at attach time — this is the second check on
// the same door, because a workspace's access can be true when a reference
// is stored and false later.
func (r referenceResolver) Hydrate(
	ctx context.Context,
	workspaceID uuid.UUID,
	ref chatdomain.ContextReference,
) (chatports.ReferenceState, bool, error) {
	if ref.Type != ThreadReferenceType {
		return chatports.ReferenceState{}, false, nil
	}

	id, err := uuid.Parse(strings.TrimSpace(ref.ID))
	if err != nil {
		return chatports.ReferenceState{}, false, nil
	}

	// The same application service the tool calls, scoped by the same
	// workspace, filtered in the same SQL. There is no second read path into
	// Threads and no repository reachable from here.
	thread, err := r.svc.GetThread(ctx, workspaceID, id)
	if err != nil {
		var de *domain.Error
		if errors.As(err, &de) && de.Kind == domain.KindNotFound {
			return chatports.ReferenceState{}, false, nil
		}
		return chatports.ReferenceState{}, false, err
	}

	fields := []chatports.ReferenceStateField{
		// The title is restated beside the state so the model never has to
		// correlate two lists by label.
		{Name: "title", Value: thread.Title},
		{Name: "status", Value: string(thread.Status)},
	}
	fields = append(fields, contentField(thread.Content))
	return chatports.ReferenceState{Fields: fields}, true, nil
}

// contentField states the current text, or states why it is not here.
//
// The three cases are told apart deliberately. Empty content says so rather
// than being omitted: a model reading an absent field has to guess whether
// the thread is blank or whether hydration failed, and "this thread has no
// content yet" is the difference between writing the first draft and
// rewriting one it cannot see.
func contentField(content string) chatports.ReferenceStateField {
	if strings.TrimSpace(content) == "" {
		return chatports.ReferenceStateField{
			Name:  "content",
			Value: "(empty — this thread is an idea with no text written yet)",
		}
	}
	if runes := []rune(content); len(runes) > hydratedContentRunes {
		return chatports.ReferenceStateField{
			Name: "content",
			Value: "(too long to carry every turn: " + itoa(len(runes)) +
				" characters. Read it with threads.thread.get before editing it.)",
		}
	}
	return chatports.ReferenceStateField{Name: "content", Value: content}
}

// itoa avoids pulling strconv in for one call site in a file that is
// otherwise free of formatting.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
