package app

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

type CreateConversationInput struct {
	WorkspaceID uuid.UUID
	AgentID     uuid.UUID
	// Title is optional. Left blank, it is filled in from the first user
	// message — see SendMessage.
	Title string
	// ContextReferences seeds what the whole thread is about, for a
	// conversation opened FROM an entity rather than from the composer.
	//
	// It is set once, here, and not editable afterwards in this build:
	// "what this thread is about" is a fact about why it was started, and
	// an editable version would need to answer what happens to the turns
	// that were already answered under the old subject. Attaching a
	// different subject to a later turn is the supported way to change
	// what is being discussed.
	ContextReferences []domain.ContextReference
}

func (s *Service) CreateConversation(ctx context.Context, in CreateConversationInput) (*domain.Conversation, error) {
	if _, err := s.repos.Agents.FindByID(ctx, in.WorkspaceID, in.AgentID); err != nil {
		return nil, err
	}
	// Resolved against this workspace before the row exists, by the same
	// door every other attachment goes through. A fabricated id fails here,
	// so no conversation is created holding a subject it may not see.
	attached, err := s.admitContextReferences(ctx, in.WorkspaceID, in.ContextReferences)
	if err != nil {
		return nil, err
	}

	c := &domain.Conversation{
		ID:                uuid.New(),
		WorkspaceID:       in.WorkspaceID,
		AgentID:           in.AgentID,
		Title:             strings.TrimSpace(in.Title),
		ContextReferences: attached,
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if err := s.repos.Conversations.Create(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Service) RenameConversation(ctx context.Context, workspaceID, id uuid.UUID, title string) (*domain.Conversation, error) {
	t := strings.TrimSpace(title)
	if t == "" {
		return nil, domain.Invalid("title required")
	}
	if len(t) > 200 {
		return nil, domain.Invalid("title must be <= 200 chars")
	}
	return s.repos.Conversations.Rename(ctx, workspaceID, id, t)
}

// DeleteConversation soft-deletes the thread. Its messages stay in place:
// they are FK'd ON DELETE CASCADE, which only fires on a hard delete, and
// keeping them means a restore is one UPDATE away.
func (s *Service) DeleteConversation(ctx context.Context, workspaceID, id uuid.UUID) error {
	return s.repos.Conversations.SoftDelete(ctx, workspaceID, id)
}

func (s *Service) GetConversation(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Conversation, error) {
	conv, err := s.repos.Conversations.FindByID(ctx, workspaceID, id)
	if err != nil {
		return nil, err
	}
	// Availability is recomputed rather than stored, so an entity that was
	// deleted reads as unavailable and one that came back reads as present,
	// without anything having to write to the conversation.
	s.refreshForDisplay(ctx, workspaceID, conv.ContextReferences)
	return conv, nil
}

// refreshForDisplay flags subjects whose entity can no longer be resolved,
// and re-asks the provider for how each one reads today.
//
// ── Why this never fails the read ──────────────────────────────────────
// A conversation is months of someone's thinking, and a resolver that is
// down — or an opportunity that was deleted — must not make it unopenable.
// So every outcome here is a flag on a chip, never an error: a failure to
// ask is treated exactly like a failure to find, because from the reader's
// side both mean "this cannot be shown right now" and neither is worth
// losing the thread over.
//
// It also cannot widen anything. It calls the same workspace-scoped
// resolver admission used, so a reference that somehow named another
// workspace's row would come back not-found here too — and the only
// consequence would be a chip that says unavailable.
// ── Why the label and subtitle are refreshed and not just read back ────
// The resolver is already being called, and what it returns is how the
// provider names the entity TODAY. Handing back the words stored months ago
// when a truer copy is in hand would show a chip for "Acme · Backend
// Engineer" after the role was renamed, or — the case that motivated this —
// a subtitle still carrying a stage the entity has left, written before
// subtitles were required to be stable.
//
// It stays in memory. Nothing here writes to the conversation: the stored
// reference is the record of what was attached, this is one read's view of
// it, and a display path that quietly rewrote history would be a far worse
// trade than a chip that is regenerated on every open.
//
// A provider that answers nothing leaves the stored words in place. Blanking
// a label because a resolver had a bad minute would turn a recognisable chip
// into an empty one for no gain.
func (s *Service) refreshForDisplay(ctx context.Context, workspaceID uuid.UUID, refs []domain.ContextReference) {
	if len(refs) == 0 || s.references == nil {
		return
	}
	for i := range refs {
		resolved, found, err := s.references.Resolve(ctx, workspaceID, refs[i])
		if err != nil {
			s.log.Warn("context reference could not be resolved for display",
				"type", refs[i].Type, "workspace_id", workspaceID, "err", err)
		}
		refs[i].Unavailable = !found
		if !found {
			continue
		}
		if resolved.Label != "" {
			refs[i].Label = resolved.Label
		}
		refs[i].Subtitle = resolved.Subtitle
	}
}

// ListConversationsInput is one page of the workspace's threads, optionally
// narrowed to a single agent.
type ListConversationsInput struct {
	WorkspaceID uuid.UUID
	// AgentID nil is every conversation in the workspace — the behaviour
	// every caller had before this filter existed.
	AgentID uuid.NullUUID
	Limit   int
	Offset  int
}

// ConversationPage carries the page and how large the whole collection is.
//
// Total is not decoration. Without it a list that stops at fifty looks
// identical to a list that ends at fifty, and the reader has no way to tell
// which one they are looking at.
type ConversationPage struct {
	Items  []domain.Conversation
	Total  int64
	Limit  int
	Offset int
}

// ListConversations reads one page and its total.
//
// An agent id that names nothing in this workspace yields an empty page,
// not a 404: this is a filter over a collection, and "no conversations
// match" is a correct answer to it. Whether that id exists elsewhere is
// not a question this route answers, which is also why it cannot leak it.
func (s *Service) ListConversations(ctx context.Context, in ListConversationsInput) (*ConversationPage, error) {
	bounds := clampList(in.Limit, in.Offset)
	f := ports.ConversationFilter{ListFilter: bounds}
	if in.AgentID.Valid {
		id := in.AgentID.UUID
		f.AgentID = &id
	}

	items, err := s.repos.Conversations.List(ctx, in.WorkspaceID, f)
	if err != nil {
		return nil, err
	}
	total, err := s.repos.Conversations.Count(ctx, in.WorkspaceID, f)
	if err != nil {
		return nil, err
	}
	return &ConversationPage{Items: items, Total: total, Limit: bounds.Limit, Offset: bounds.Offset}, nil
}

// TruncateFrom deletes a message and everything after it in the thread,
// returning how many rows went.
//
// This is the half of regenerate and edit that the UI cannot do on its own:
// both mean "replace this turn", and appending the question a second time
// would leave the thread reading as if it had been asked twice — and would
// feed the model that duplicate on every later turn.
//
// Only a user turn may be truncated from. Cutting from an assistant turn
// would leave the question hanging with no reply and no way to ask again
// without duplicating it.
func (s *Service) TruncateFrom(ctx context.Context, workspaceID, conversationID uuid.UUID, seq int64) (int64, error) {
	if _, err := s.repos.Conversations.FindByID(ctx, workspaceID, conversationID); err != nil {
		return 0, err
	}
	target, err := s.repos.Messages.FindBySeq(ctx, workspaceID, conversationID, seq)
	if err != nil {
		return 0, err
	}
	if target.Role != domain.RoleUser {
		return 0, domain.Invalid("can only truncate from a user message")
	}
	return s.repos.Messages.DeleteFromSeq(ctx, workspaceID, conversationID, seq)
}

// MaxTranscriptMessages bounds a transcript read. Threads longer than this
// are readable in full only by raising the limit; the UI does not need it.
//
// Exported so the transport can tell the client which ceiling it actually
// applied. A cut nobody is told about is the same to the reader as a thread
// that simply began there.
const MaxTranscriptMessages = 500

// TranscriptLimit resolves the ceiling a requested limit lands on.
func TranscriptLimit(limit int) int {
	if limit <= 0 || limit > MaxTranscriptMessages {
		return MaxTranscriptMessages
	}
	return limit
}

// ListMessages returns the trailing `limit` messages of a conversation, in
// reading order.
func (s *Service) ListMessages(ctx context.Context, workspaceID, conversationID uuid.UUID, limit int) ([]domain.Message, error) {
	if _, err := s.repos.Conversations.FindByID(ctx, workspaceID, conversationID); err != nil {
		return nil, err
	}
	return s.repos.Messages.ListRecent(ctx, workspaceID, conversationID, TranscriptLimit(limit))
}
