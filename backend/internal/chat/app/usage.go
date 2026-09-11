package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/ports"
)

// Usage reporting.
//
// A report here is a *statement*: it says what already happened. Every
// number in it was frozen by the turn that produced it (see accounting.go),
// and nothing in this file consults the gateway's current rate card. That
// is the difference between "what did this cost?" and "what would this cost
// if I ran it again today?" — the second is a legitimate question and it is
// not the one a statement answers.
//
// Estimated cost and billed spend stay separate, as they always have:
//
//	EstimatedCost   ours, from tokens x the rate stamped on each turn
//	ProviderSpend   the gateway's, read from the key it bills
//
// Neither falls back to the other. Knowing they disagree is useful; a
// silent substitution would destroy exactly that signal.

// UsageLine is one model's contribution to a report.
type UsageLine struct {
	Model            string `json:"model"`
	Messages         int64  `json:"messages"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	// The unit rates these turns were priced at, when the whole group agrees
	// on them. Null when the group spans a price change, or when nothing in
	// it carries a price. Null is not zero.
	InputCostPerToken  *float64 `json:"input_cost_per_token"`
	OutputCostPerToken *float64 `json:"output_cost_per_token"`
	// Cost sums only the turns that recorded one. UnpricedMessages says how
	// many did not, and therefore how much of this figure is missing.
	Cost             float64 `json:"cost"`
	UnpricedMessages int64   `json:"unpriced_messages"`
	// How the token counts above were arrived at. The three sum to
	// Messages. Unknown turns contributed zero tokens, because nothing is
	// known about them — not because they consumed nothing.
	ProviderMessages     int64 `json:"provider_messages"`
	EstimatedMessages    int64 `json:"estimated_messages"`
	UnknownUsageMessages int64 `json:"unknown_usage_messages"`
	// What the input was made of, summed over the turns the provider
	// measured. Read CacheMeasuredMessages BEFORE the three sums: it is how
	// many turns they cover, and zero means nothing here was observed —
	// which is not the same as nothing being cached.
	CacheReadTokens       int64 `json:"cache_read_tokens"`
	CacheCreationTokens   int64 `json:"cache_creation_tokens"`
	ReasoningTokens       int64 `json:"reasoning_tokens"`
	CacheMeasuredMessages int64 `json:"cache_measured_messages"`
}

// UsageReport aggregates a conversation, an agent, or a workspace, over an
// optional window.
//
// Priced answers one question and it is not "was the rate card reachable?".
// It is: does every turn in this report carry a cost? False means the total
// understates, and by how much is UnpricedMessages. A reader that hides the
// money when Priced is false is behaving correctly.
type UsageReport struct {
	Currency string `json:"currency"`
	// The window this report covers, echoed back. Null bounds mean open:
	// two nulls is the lifetime total.
	From                  *time.Time  `json:"from"`
	To                    *time.Time  `json:"to"`
	Priced                bool        `json:"priced"`
	Lines                 []UsageLine `json:"lines"`
	Messages              int64       `json:"messages"`
	TotalPromptTokens     int64       `json:"total_prompt_tokens"`
	TotalCompletionTokens int64       `json:"total_completion_tokens"`
	TotalTokens           int64       `json:"total_tokens"`
	// EstimatedCost is ours: the sum of what each turn recorded costing at
	// the time it ran. It is named an estimate because it is priced from a
	// rate card rather than from an invoice, not because it is recomputed.
	EstimatedCost        float64 `json:"estimated_cost"`
	ProviderMessages     int64   `json:"provider_messages"`
	EstimatedMessages    int64   `json:"estimated_messages"`
	UnknownUsageMessages int64   `json:"unknown_usage_messages"`
	UnpricedMessages     int64   `json:"unpriced_messages"`
	// Cache and reasoning totals, and the count of turns they were measured
	// over.
	//
	// ── Why the count is not optional decoration ───────────────────────
	// CacheMeasuredMessages is the denominator. A report showing
	// "cache_read_tokens: 0" over 200 turns none of which were measured is
	// indistinguishable, without it, from a report over 200 turns that all
	// missed the cache — and those two say opposite things about whether
	// caching works. The same argument UnpricedMessages already makes about
	// money.
	TotalCacheReadTokens     int64 `json:"total_cache_read_tokens"`
	TotalCacheCreationTokens int64 `json:"total_cache_creation_tokens"`
	TotalReasoningTokens     int64 `json:"total_reasoning_tokens"`
	CacheMeasuredMessages    int64 `json:"cache_measured_messages"`
}

// ConversationUsage tallies one conversation.
func (s *Service) ConversationUsage(ctx context.Context, workspaceID, conversationID uuid.UUID, f ports.UsageFilter) (*UsageReport, error) {
	// Resolved first so a conversation in another workspace is a 404 rather
	// than an empty report, which would leak whether the id exists.
	if _, err := s.repos.Conversations.FindByID(ctx, workspaceID, conversationID); err != nil {
		return nil, err
	}
	rows, err := s.repos.Messages.UsageByConversation(ctx, workspaceID, conversationID, f)
	if err != nil {
		return nil, err
	}
	return buildUsageReport(rows, f), nil
}

// AgentUsage tallies every conversation an agent owns.
func (s *Service) AgentUsage(ctx context.Context, workspaceID, agentID uuid.UUID, f ports.UsageFilter) (*UsageReport, error) {
	if _, err := s.repos.Agents.FindByID(ctx, workspaceID, agentID); err != nil {
		return nil, err
	}
	rows, err := s.repos.Messages.UsageByAgent(ctx, workspaceID, agentID, f)
	if err != nil {
		return nil, err
	}
	return buildUsageReport(rows, f), nil
}

// WorkspaceUsage tallies everything in the workspace. This is the level a
// daily total is asked at, and it needs no agent or conversation to exist.
func (s *Service) WorkspaceUsage(ctx context.Context, workspaceID uuid.UUID, f ports.UsageFilter) (*UsageReport, error) {
	rows, err := s.repos.Messages.UsageByWorkspace(ctx, workspaceID, f)
	if err != nil {
		return nil, err
	}
	return buildUsageReport(rows, f), nil
}

// ProviderSpend returns the real billed spend of a provider's key, straight
// from the gateway. Deliberately not merged into UsageReport: this number
// is the gateway's and covers every call made with the key, including ones
// this system never made.
func (s *Service) ProviderSpend(ctx context.Context, workspaceID, providerID uuid.UUID) (*ports.KeySpend, error) {
	p, err := s.repos.Providers.FindByID(ctx, workspaceID, providerID)
	if err != nil {
		return nil, err
	}
	creds, err := s.credentialsFor(p)
	if err != nil {
		return nil, err
	}
	spend, err := s.llm.KeyInfo(ctx, creds)
	if err != nil {
		return nil, err
	}
	return &spend, nil
}

// buildUsageReport folds the per-model rows into a report. Pure: it takes
// what the database already tallied and adds nothing to it. There is no
// network call on this path and there must not be one — a report that
// reaches for the current rate card is a report that changes the past.
func buildUsageReport(rows []ports.ModelUsage, f ports.UsageFilter) *UsageReport {
	rep := &UsageReport{
		Currency: "USD",
		From:     f.From,
		To:       f.To,
		Lines:    make([]UsageLine, 0, len(rows)),
	}

	for _, r := range rows {
		line := UsageLine{
			Model:                 r.Model,
			Messages:              r.Messages,
			PromptTokens:          r.PromptTokens,
			CompletionTokens:      r.CompletionTokens,
			TotalTokens:           r.PromptTokens + r.CompletionTokens,
			InputCostPerToken:     r.InputCostPerToken,
			OutputCostPerToken:    r.OutputCostPerToken,
			Cost:                  r.Cost,
			UnpricedMessages:      r.UnpricedMessages,
			ProviderMessages:      r.ProviderMessages,
			EstimatedMessages:     r.EstimatedMessages,
			UnknownUsageMessages:  r.UnknownMessages,
			CacheReadTokens:       r.CacheReadTokens,
			CacheCreationTokens:   r.CacheCreationTokens,
			ReasoningTokens:       r.ReasoningTokens,
			CacheMeasuredMessages: r.CacheMeasuredMessages,
		}
		rep.Lines = append(rep.Lines, line)

		rep.Messages += line.Messages
		rep.TotalPromptTokens += line.PromptTokens
		rep.TotalCompletionTokens += line.CompletionTokens
		rep.TotalTokens += line.TotalTokens
		rep.EstimatedCost += line.Cost
		rep.ProviderMessages += line.ProviderMessages
		rep.EstimatedMessages += line.EstimatedMessages
		rep.UnknownUsageMessages += line.UnknownUsageMessages
		rep.UnpricedMessages += line.UnpricedMessages
		rep.TotalCacheReadTokens += line.CacheReadTokens
		rep.TotalCacheCreationTokens += line.CacheCreationTokens
		rep.TotalReasoningTokens += line.ReasoningTokens
		rep.CacheMeasuredMessages += line.CacheMeasuredMessages
	}

	// Every turn accounted for, or none to account for. An empty report is
	// priced: there is nothing in it whose cost is being hidden.
	rep.Priced = rep.UnpricedMessages == 0
	return rep
}
