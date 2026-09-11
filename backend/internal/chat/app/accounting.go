package app

import (
	"context"
	"time"
	"unicode/utf8"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

// Turn accounting: what one turn consumed, at what price, for what cost.
//
// This file is the single answer to "what did this turn cost?", and the
// answer is computed once — when the turn ends — and then frozen. Nothing
// downstream recomputes it. That is the whole point:
//
//	current model pricing   is a live fact about the gateway
//	historical turn cost    is a fact about something that already happened
//
// Conflating the two is what made a statement change retroactively whenever
// a model's price changed, and it is what this file exists to prevent.
//
// ── What it is responsible for ─────────────────────────────────────────
// Deciding, from what the provider actually said, how much of a turn is
// known and how much is guessed, and pricing the part that is known.
//
// ── What it is deliberately NOT responsible for ────────────────────────
// Calling the provider, reading the rate card, persisting, aggregating, or
// enforcing anything. It is a pure function over values, which is why the
// invariants below are testable without a database or a gateway.
//
// ── Unknown is not zero ────────────────────────────────────────────────
// Every field that can be unknown is a pointer, and every count is
// qualified by a UsageSource. A turn we know nothing about reports nothing,
// rather than reporting a confident zero that a later budget would read as
// "no spend here".

// turnAccounting is the accounting half of a persisted assistant turn.
type turnAccounting struct {
	Source           domain.UsageSource
	PromptTokens     int
	CompletionTokens int
	// The rate card applied, and the resulting cost. Nil together: there is
	// no cost without a price, and no price is invented to produce one.
	InputCostPerToken  *float64
	OutputCostPerToken *float64
	Cost               *float64
	// EstimatedPromptTokens is the Context Builder's pre-call prediction,
	// recorded whether or not the provider later contradicted it. Comparing
	// the two is how the characters/4 heuristic gets calibrated instead of
	// trusted forever.
	EstimatedPromptTokens *int
	// What the input was made of, when the provider said so. Nil is ABSENT.
	//
	// They are summed across the rounds that reported them, and a turn where
	// no round did keeps them nil rather than reporting a confident zero —
	// the same rule the counts above already follow through UsageSource.
	CacheReadTokens     *int
	CacheCreationTokens *int
	ReasoningTokens     *int
}

// accountingInput is everything the decision depends on.
type accountingInput struct {
	// StreamOpened says whether the provider accepted the request and began
	// a stream. It is the line between "we know tokens were consumed but
	// not how many" and "we do not know that anything was consumed at all".
	StreamOpened bool
	// Usage is what the gateway reported, or nil if it reported nothing.
	Usage *ports.Usage
	// Price is the rate card for this model at the time of the turn, or nil
	// if it could not be read.
	Price *ports.Price
	// EstimatedPromptTokens comes from ContextReport.TotalEstimatedTokens:
	// what the builder predicted before the call went out.
	EstimatedPromptTokens int
	// Content and Reasoning are what actually came back. Both count toward
	// output: reasoning tokens are billed as output tokens, and a turn that
	// thought for a page and answered in a line did not produce a line.
	Content   string
	Reasoning string
	// Estimator converts characters to tokens at the calibrated density of
	// the model this call went to. The zero value is the pessimistic
	// model-agnostic default, which is the right answer for a caller that
	// does not know the model.
	Estimator TokenEstimator
}

// newTurnAccounting decides what a finished turn is allowed to claim.
//
// Three outcomes, in the order they are ruled out:
//
//  1. The gateway reported usage. Those numbers are the truth; nothing here
//     second-guesses them, not even when they look wrong.
//  2. It did not, but the request reached the model. Input tokens were
//     charged whatever happened next — a mid-stream failure and a reader
//     who hung up both leave a real bill behind — so the turn carries an
//     estimate that is *labelled* an estimate. Silent zero would be worse
//     than declared approximation.
//  3. The stream never opened. The request may never have reached the
//     model, and no measurement exists to make. Unknown, and it stays
//     unknown: an estimate here would be a number we invented about a call
//     that may not have happened.
func newTurnAccounting(in accountingInput) turnAccounting {
	acct := turnAccounting{Source: domain.UsageUnknown}

	// The prediction is recorded on every turn the builder ran for, even
	// the ones that failed. It describes what we were about to send, which
	// is knowable regardless of how the turn ended.
	if in.EstimatedPromptTokens > 0 {
		estimate := in.EstimatedPromptTokens
		acct.EstimatedPromptTokens = &estimate
	}

	switch {
	case in.Usage != nil:
		acct.Source = domain.UsageProvider
		acct.PromptTokens = nonNegative(in.Usage.PromptTokens)
		acct.CompletionTokens = nonNegative(in.Usage.CompletionTokens)
		// Carried only on the provider branch, and that is the point. There
		// is no local way to estimate how much of a prompt was served from
		// cache, so an estimated turn reports nothing rather than reporting
		// zero — which a cache hit rate would otherwise average in as a miss.
		acct.CacheReadTokens = clampOptional(in.Usage.CacheReadTokens)
		acct.CacheCreationTokens = clampOptional(in.Usage.CacheCreationTokens)
		acct.ReasoningTokens = clampOptional(in.Usage.ReasoningTokens)
	case in.StreamOpened:
		acct.Source = domain.UsageEstimated
		acct.PromptTokens = in.EstimatedPromptTokens
		acct.CompletionTokens = in.Estimator.Tokens(
			utf8.RuneCountInString(in.Content) + utf8.RuneCountInString(in.Reasoning))
	default:
		return acct // unknown: no counts, no price, no cost
	}

	if in.Price == nil {
		// Price unknown at the time of the turn. The tokens are still
		// recorded; the cost stays nil, forever, rather than being filled
		// in later from a rate card that did not exist for this turn.
		return acct
	}
	input, output := in.Price.InputCostPerToken, in.Price.OutputCostPerToken
	acct.InputCostPerToken = &input
	acct.OutputCostPerToken = &output
	cost := inputCost(acct, in.Price) + float64(acct.CompletionTokens)*output
	acct.Cost = &cost
	return acct
}

// inputCost prices the prompt side of one call, splitting it by how each
// token was actually billed.
//
// ── The measured fact this rests on ────────────────────────────────────
// `prompt_tokens` INCLUDES the cache read and cache creation counts. That is
// not an assumption about the protocol: X3 sent the same request twice, once
// with a breakpoint and once without, and both reported 13.982 prompt
// tokens — the cached one attributing 13.965 of them to a read. If the
// counts were additive, the cached call would have reported 17.
//
// So the three classes PARTITION the prompt:
//
//	full price = prompt_tokens − cache_read − cache_creation
//	cache read      billed at ~0,10x input
//	cache creation  billed at ~1,25x input
//
// ── Why an absent rate falls back to the input rate ────────────────────
// It cannot happen on a turn that reports cache tokens from a gateway that
// prices them, and if it ever does, the fallback OVERSTATES the cost — a
// cache read charged at the full input rate. Overstating is the only
// direction a cost figure may be wrong in when the alternative is a saving
// this system cannot substantiate. The counts stay on the row either way,
// so the mistake is visible rather than baked in.
func inputCost(acct turnAccounting, price *ports.Price) float64 {
	input := price.InputCostPerToken
	read, creation := 0, 0
	if acct.CacheReadTokens != nil {
		read = *acct.CacheReadTokens
	}
	if acct.CacheCreationTokens != nil {
		creation = *acct.CacheCreationTokens
	}

	// A gateway reporting more cached tokens than prompt tokens would break
	// the partition. Clamp rather than produce a negative charge: the rest
	// of the row is still a real record.
	full := acct.PromptTokens - read - creation
	if full < 0 {
		full = 0
	}

	total := float64(full) * input
	total += float64(read) * rateOr(price.CacheReadCostPerToken, input)
	total += float64(creation) * rateOr(price.CacheCreationCostPerToken, input)
	return total
}

func rateOr(rate *float64, fallback float64) float64 {
	if rate == nil {
		return fallback
	}
	return *rate
}

/* ── a turn that called the provider more than once ──────────────────── */

// aggregateTurn folds every provider call of one turn into the figures the
// message stores, and into the per-call breakdown the Inspector shows.
//
// ── Why the totals are sums and not the last call ──────────────────────
// Because every call was billed. A turn that asked for a tool, ran it and
// answered paid for two prompts and two completions, and recording only the
// second would understate it by exactly the part tools added — which is the
// part somebody enabling tools most needs to see.
//
// ── Why the breakdown is kept as well ──────────────────────────────────
// A sum answers "what did this turn cost" and destroys "why". The rounds
// are what let the Inspector say the second call carried four thousand more
// prompt tokens than the first because a tool returned a document.
//
// ── The source of a multi-call turn is its weakest link ────────────────
// If one call was measured by the gateway and another had to be estimated,
// the total is an estimate. Reporting `provider` because most of it was
// exact would be claiming a precision the number does not have.
//
// A call that never opened contributes nothing: no tokens, no cost, no
// downgrade. It is still listed in the breakdown, marked unknown, because
// a round that failed is a fact about the turn.
func aggregateTurn(rounds []providerRound, price *ports.Price, est TokenEstimator) (turnAccounting, []domain.ContextRound) {
	reports := make([]domain.ContextRound, 0, len(rounds))
	total := turnAccounting{Source: domain.UsageUnknown}
	estimated := 0
	opened := 0

	for i, r := range rounds {
		acct := newTurnAccounting(accountingInput{
			StreamOpened:          r.Opened,
			Usage:                 r.Usage,
			Price:                 price,
			EstimatedPromptTokens: r.EstimatedPromptTokens,
			Content:               r.Content,
			Reasoning:             r.Reasoning,
			Estimator:             est,
		})

		reports = append(reports, domain.ContextRound{
			Round:                i + 1,
			AddedCharacters:      r.AddedCharacters,
			AddedEstimatedTokens: est.Tokens(r.AddedCharacters),
			PromptTokens:         acct.PromptTokens,
			CompletionTokens:     acct.CompletionTokens,
			UsageSource:          acct.Source,
			Cost:                 acct.Cost,
			FinishReason:         string(r.FinishReason),
			Tools:                r.Tools,
			Usage:                roundUsageOf(r.Usage),
		})

		// The prediction is recorded for every round the builder ran for,
		// including one whose stream never opened. It describes what we were
		// about to send, which is knowable regardless of how the call ended —
		// the same rule newTurnAccounting has always applied, extended
		// to a turn that makes more than one call.
		estimated += r.EstimatedPromptTokens

		if !r.Opened {
			continue
		}
		opened++
		total.PromptTokens += acct.PromptTokens
		total.CompletionTokens += acct.CompletionTokens
		// Summed over the rounds that reported them. See addOptional for why
		// a round the gateway did not measure contributes nothing instead of
		// erasing the turn's figures.
		total.CacheReadTokens = addOptional(total.CacheReadTokens, acct.CacheReadTokens)
		total.CacheCreationTokens = addOptional(total.CacheCreationTokens, acct.CacheCreationTokens)
		total.ReasoningTokens = addOptional(total.ReasoningTokens, acct.ReasoningTokens)
		if acct.Source == domain.UsageEstimated {
			total.Source = domain.UsageEstimated
		} else if total.Source != domain.UsageEstimated {
			total.Source = domain.UsageProvider
		}
		if acct.Cost != nil {
			sum := acct.Cost
			if total.Cost != nil {
				v := *total.Cost + *sum
				sum = &v
			}
			total.Cost = sum
		}
	}

	if estimated > 0 {
		total.EstimatedPromptTokens = &estimated
	}
	if opened == 0 {
		// Nothing reached the model. Unknown, and it stays unknown — the
		// same answer this turn would have given before tools existed. The
		// prediction above is still recorded: it is a statement about what
		// was going to be sent, not about what was consumed.
		return total, reports
	}
	if price != nil && total.Cost != nil {
		input, output := price.InputCostPerToken, price.OutputCostPerToken
		total.InputCostPerToken = &input
		total.OutputCostPerToken = &output
	}
	return total, reports
}

// roundUsageOf carries the provider's per-call detail into the report.
//
// It returns nil when the provider reported nothing beyond the two totals,
// so a gateway that does not speak about caching leaves the report byte
// identical to what it was before this field existed. That is what keeps
// every stored report — and every golden test over one — unchanged by the
// arrival of this field.
func roundUsageOf(u *ports.Usage) *domain.RoundUsage {
	if u == nil {
		return nil
	}
	ru := domain.RoundUsage{
		TotalTokens:         u.TotalTokens,
		CacheReadTokens:     clampOptional(u.CacheReadTokens),
		CacheCreationTokens: clampOptional(u.CacheCreationTokens),
		CachedTokens:        clampOptional(u.CachedTokens),
		ReasoningTokens:     clampOptional(u.ReasoningTokens),
	}
	if ru == (domain.RoundUsage{}) {
		return nil
	}
	return &ru
}

// priceLookupTimeout bounds the rate-card read that stamps a turn. It runs
// inside the write-back, which has persistTimeout in total: a gateway that
// is slow to answer /model/info should cost the turn its price, never its
// record.
const priceLookupTimeout = 5 * time.Second

// priceAtTurn reads the rate card in force for this turn.
//
// Read here, when the turn ends, rather than cached or read up front. Two
// reasons, both about the critical path: the answer has already streamed by
// the time this runs, so a slow rate card delays only the final frame and
// never the first token; and keeping it uncached means the stamped price is
// the one the gateway was actually quoting for this call, with no TTL
// window in which a turn is priced from a rate card that has since changed.
//
// It is best-effort by design. A gateway that cannot be asked leaves the
// turn unpriced — tokens recorded, cost nil — which is a worse report and a
// correct one. Failing the turn over a price would be trading the answer
// for the bookkeeping.
func (s *Service) priceAtTurn(ctx context.Context, creds ports.Credentials, model string, streamOpened bool) *ports.Price {
	if !streamOpened {
		// Nothing to price: the accounting is unknown either way, and this
		// spares a gateway round trip on the failure path.
		return nil
	}
	return s.priceFor(ctx, creds, model)
}

// priceFor reads the rate card and finds one model on it.
//
// Split out of priceAtTurn so the budget preflight can ask the same
// question before the turn instead of after it — and hand the answer back,
// so a budgeted agent makes one rate-card call per turn rather than two.
func (s *Service) priceFor(ctx context.Context, creds ports.Credentials, model string) *ports.Price {
	ctx, cancel := context.WithTimeout(ctx, priceLookupTimeout)
	defer cancel()

	prices, err := s.llm.ModelPrices(ctx, creds)
	if err != nil {
		s.log.Warn("turn pricing: rate card unavailable", "model", model, "err", err)
		return nil
	}
	// A model absent from the card is not free — the gateway drops models
	// it reports at zero cost precisely so "free" and "unknown" stay
	// distinguishable (see Client.ModelPrices). Unknown it is.
	p, ok := prices[model]
	if !ok {
		s.log.Warn("turn pricing: model is not on the rate card", "model", model)
		return nil
	}
	return &p
}

// nonNegative guards against a gateway reporting a negative count. Clamping
// is right here rather than rejecting: the rest of the turn is still a real
// record, and a negative token count would fail the column constraint and
// cost us the whole row.
func nonNegative(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// clampOptional is nonNegative for a count that may be absent.
//
// It preserves the distinction the whole optional-usage design rests on:
// nil in, nil out. A nil is "the provider did not say", and turning it into
// a zero here would undo at the last step what the wire types, the port and
// the column all went to trouble to keep apart.
func clampOptional(n *int) *int {
	if n == nil {
		return nil
	}
	v := nonNegative(*n)
	return &v
}

// addOptional sums two counts that may each be absent.
//
// ── Why absent + present is present ────────────────────────────────────
// A turn's total is the sum over the rounds that reported one. A round the
// gateway did not measure contributes nothing and must not veto the rounds
// it did measure — otherwise one silent round would erase the whole turn's
// cache figures. The complementary rule is that absent + absent stays
// absent, so a turn nobody measured still reports nothing.
//
// It understates when some rounds are unmeasured, and that is the correct
// direction for a saving: a cache figure that is too small is a claim we can
// defend, one that is too large is not.
func addOptional(a, b *int) *int {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	sum := *a + *b
	return &sum
}
