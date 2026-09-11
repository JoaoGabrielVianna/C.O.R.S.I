package app

import (
	"testing"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

// The rate card used throughout: a dollar per million in, three per million
// out, which is the shape LiteLLM reports (dollars per single token).
var testPrice = &ports.Price{InputCostPerToken: 1e-6, OutputCostPerToken: 3e-6}

func costOf(t *testing.T, acct turnAccounting) float64 {
	t.Helper()
	if acct.Cost == nil {
		t.Fatalf("cost is unknown, want a number")
	}
	return *acct.Cost
}

// nearly compares money arithmetic, where the two sides are the same
// products summed in a different order and binary floats do not oblige.
func nearly(got, want float64) bool {
	d := got - want
	if d < 0 {
		d = -d
	}
	return d < 1e-15
}

// TestTurnAccountingSources pins the three outcomes and, more importantly,
// which inputs select them.
func TestTurnAccountingSources(t *testing.T) {
	t.Run("provider usage is taken as given", func(t *testing.T) {
		acct := newTurnAccounting(accountingInput{
			StreamOpened:          true,
			Usage:                 &ports.Usage{PromptTokens: 41, CompletionTokens: 17},
			Price:                 testPrice,
			EstimatedPromptTokens: 999, // must not win over the real number
			Content:               "olá, João",
		})
		if acct.Source != domain.UsageProvider {
			t.Fatalf("source = %q, want provider", acct.Source)
		}
		if acct.PromptTokens != 41 || acct.CompletionTokens != 17 {
			t.Fatalf("tokens = %d/%d, want the provider's 41/17",
				acct.PromptTokens, acct.CompletionTokens)
		}
		if got, want := costOf(t, acct), 41*1e-6+17*3e-6; !nearly(got, want) {
			t.Fatalf("cost = %v, want %v", got, want)
		}
	})

	t.Run("no usage frame estimates, and says so", func(t *testing.T) {
		// 12 runes of answer plus 8 of reasoning = 20 characters. At the
		// calibrated default density of 0,50 tokens per character that is
		// 10 tokens — twice what the retired chars/4 heuristic claimed, and
		// the correction is the whole point of the recalibration.
		acct := newTurnAccounting(accountingInput{
			StreamOpened:          true,
			Usage:                 nil,
			Price:                 testPrice,
			EstimatedPromptTokens: 300,
			Content:               "doze runes!!",
			Reasoning:             "oito run",
		})
		if acct.Source != domain.UsageEstimated {
			t.Fatalf("source = %q, want estimated", acct.Source)
		}
		if acct.PromptTokens != 300 {
			t.Fatalf("prompt tokens = %d, want the context estimate 300", acct.PromptTokens)
		}
		if acct.CompletionTokens != 10 {
			t.Fatalf("completion tokens = %d, want 10 (20 characters at the calibrated "+
				"default density)", acct.CompletionTokens)
		}
		// An estimated turn is still a turn that cost money. Refusing to
		// price it would be the same silent zero from the other direction.
		if got, want := costOf(t, acct), 300*1e-6+10*3e-6; !nearly(got, want) {
			t.Fatalf("cost = %v, want %v", got, want)
		}
	})

	t.Run("a stream that never opened stays unknown", func(t *testing.T) {
		acct := newTurnAccounting(accountingInput{
			StreamOpened:          false,
			Price:                 testPrice,
			EstimatedPromptTokens: 300,
		})
		if acct.Source != domain.UsageUnknown {
			t.Fatalf("source = %q, want unknown", acct.Source)
		}
		if acct.PromptTokens != 0 || acct.CompletionTokens != 0 {
			t.Fatalf("tokens = %d/%d, want nothing invented about a call that may not have happened",
				acct.PromptTokens, acct.CompletionTokens)
		}
		// The decisive assertion: no cost, not a zero cost.
		if acct.Cost != nil {
			t.Fatalf("cost = %v for an unknown turn, want nil", *acct.Cost)
		}
		if acct.InputCostPerToken != nil || acct.OutputCostPerToken != nil {
			t.Fatal("an unknown turn was stamped with a rate card it never used")
		}
	})
}

// TestUnknownPricingIsNotZeroCost is the invariant the whole batch exists
// for on the pricing axis: an unreadable rate card must leave the cost
// absent, never zero. A budget reading zero would conclude nothing was
// spent and let everything through.
func TestUnknownPricingIsNotZeroCost(t *testing.T) {
	acct := newTurnAccounting(accountingInput{
		StreamOpened: true,
		Usage:        &ports.Usage{PromptTokens: 1000, CompletionTokens: 500},
		Price:        nil,
	})
	if acct.Source != domain.UsageProvider {
		t.Fatalf("source = %q; a missing price must not downgrade real usage", acct.Source)
	}
	if acct.PromptTokens != 1000 || acct.CompletionTokens != 500 {
		t.Fatalf("tokens = %d/%d, want them recorded even unpriced",
			acct.PromptTokens, acct.CompletionTokens)
	}
	if acct.Cost != nil {
		t.Fatalf("cost = %v with no rate card, want nil (unknown, not free)", *acct.Cost)
	}
	if acct.InputCostPerToken != nil || acct.OutputCostPerToken != nil {
		t.Fatal("rates were stamped from a rate card that could not be read")
	}
}

// TestPricingSnapshotIsFrozenPerTurn: two turns, same tokens, different
// rate cards. Each keeps its own. This is what makes yesterday's cost
// unreachable by today's price change.
func TestPricingSnapshotIsFrozenPerTurn(t *testing.T) {
	usage := &ports.Usage{PromptTokens: 100, CompletionTokens: 100}

	yesterday := newTurnAccounting(accountingInput{
		StreamOpened: true, Usage: usage,
		Price: &ports.Price{InputCostPerToken: 1e-6, OutputCostPerToken: 2e-6},
	})
	today := newTurnAccounting(accountingInput{
		StreamOpened: true, Usage: usage,
		Price: &ports.Price{InputCostPerToken: 10e-6, OutputCostPerToken: 20e-6},
	})

	if got, want := costOf(t, yesterday), 100*1e-6+100*2e-6; !nearly(got, want) {
		t.Fatalf("yesterday = %v, want %v", got, want)
	}
	if got, want := costOf(t, today), 100*10e-6+100*20e-6; !nearly(got, want) {
		t.Fatalf("today = %v, want %v", got, want)
	}
	if *yesterday.InputCostPerToken == *today.InputCostPerToken {
		t.Fatal("both turns carry the same input rate; the snapshot is not per-turn")
	}
}

// TestContextEstimateIsRecordedForEveryOutcome. The comparison between what
// the builder predicted and what the provider charged is only possible if
// the prediction survives, including on turns where the provider then
// contradicted it.
func TestContextEstimateIsRecordedForEveryOutcome(t *testing.T) {
	cases := map[string]accountingInput{
		"provider": {StreamOpened: true, EstimatedPromptTokens: 250,
			Usage: &ports.Usage{PromptTokens: 300, CompletionTokens: 10}},
		"estimated": {StreamOpened: true, EstimatedPromptTokens: 250, Content: "resposta"},
		"unknown":   {StreamOpened: false, EstimatedPromptTokens: 250},
	}
	for name, in := range cases {
		acct := newTurnAccounting(in)
		if acct.EstimatedPromptTokens == nil {
			t.Fatalf("%s: the context estimate was dropped", name)
		}
		if *acct.EstimatedPromptTokens != 250 {
			t.Fatalf("%s: estimate = %d, want 250", name, *acct.EstimatedPromptTokens)
		}
	}

	// On a provider turn the estimate must not overwrite the real number:
	// the two are kept side by side precisely so they can disagree.
	acct := newTurnAccounting(cases["provider"])
	if acct.PromptTokens != 300 {
		t.Fatalf("prompt tokens = %d, want the provider's 300", acct.PromptTokens)
	}
	if *acct.EstimatedPromptTokens == acct.PromptTokens {
		t.Fatal("estimate and measurement collapsed into one number")
	}

	// Nothing to predict means no prediction, rather than a stored zero.
	if none := newTurnAccounting(accountingInput{StreamOpened: true}); none.EstimatedPromptTokens != nil {
		t.Fatalf("estimate = %d with nothing estimated, want nil", *none.EstimatedPromptTokens)
	}
}

// TestNegativeProviderCountsAreClamped: a nonsensical count from the
// gateway must not cost us the whole row to a column constraint.
func TestNegativeProviderCountsAreClamped(t *testing.T) {
	acct := newTurnAccounting(accountingInput{
		StreamOpened: true,
		Usage:        &ports.Usage{PromptTokens: -5, CompletionTokens: 7},
	})
	if acct.PromptTokens != 0 || acct.CompletionTokens != 7 {
		t.Fatalf("tokens = %d/%d, want 0/7", acct.PromptTokens, acct.CompletionTokens)
	}
}

// TestUsageSourceOrUnknown covers the normalisation that keeps the column
// free of a fourth, undocumented state.
func TestUsageSourceOrUnknown(t *testing.T) {
	if got := domain.UsageSource("").OrUnknown(); got != domain.UsageUnknown {
		t.Fatalf("zero value normalised to %q, want unknown", got)
	}
	if got := domain.UsageProvider.OrUnknown(); got != domain.UsageProvider {
		t.Fatalf("a set value was rewritten to %q", got)
	}
	for _, s := range []domain.UsageSource{domain.UsageUnknown, domain.UsageProvider, domain.UsageEstimated} {
		if !s.Valid() {
			t.Fatalf("%q is not accepted by Valid", s)
		}
	}
	if domain.UsageSource("free").Valid() {
		t.Fatal("an unknown source name passed Valid")
	}
}

/* ── report assembly ─────────────────────────────────────────────────── */

// TestBuildUsageReportTotals: the report adds up the per-model rows without
// adding anything of its own, and the honesty counters survive the fold.
func TestBuildUsageReportTotals(t *testing.T) {
	in, out := 1e-6, 3e-6
	rows := []ports.ModelUsage{
		{
			Model: "modelo-a", Messages: 3, PromptTokens: 100, CompletionTokens: 40,
			ProviderMessages: 3, Cost: 0.00022,
			InputCostPerToken: &in, OutputCostPerToken: &out,
		},
		{
			Model: "modelo-b", Messages: 2, PromptTokens: 10, CompletionTokens: 5,
			EstimatedMessages: 1, UnknownMessages: 1, UnpricedMessages: 2, Cost: 0,
		},
	}
	rep := buildUsageReport(rows, ports.UsageFilter{})

	if rep.Messages != 5 || rep.TotalPromptTokens != 110 || rep.TotalCompletionTokens != 45 {
		t.Fatalf("totals = %d msgs, %d in, %d out", rep.Messages, rep.TotalPromptTokens, rep.TotalCompletionTokens)
	}
	if rep.TotalTokens != 155 {
		t.Fatalf("total tokens = %d, want 155", rep.TotalTokens)
	}
	if !nearly(rep.EstimatedCost, 0.00022) {
		t.Fatalf("estimated cost = %v, want only the priced turns", rep.EstimatedCost)
	}
	if rep.UnpricedMessages != 2 {
		t.Fatalf("unpriced = %d, want 2", rep.UnpricedMessages)
	}
	// The counter is what stops the total above from reading as complete.
	if rep.Priced {
		t.Fatal("priced = true while two turns carry no cost")
	}
	if rep.ProviderMessages+rep.EstimatedMessages+rep.UnknownUsageMessages != rep.Messages {
		t.Fatalf("the source counts (%d/%d/%d) do not account for %d messages",
			rep.ProviderMessages, rep.EstimatedMessages, rep.UnknownUsageMessages, rep.Messages)
	}
	if rep.Lines[1].InputCostPerToken != nil {
		t.Fatal("an unpriced line reported a unit rate")
	}
}

// TestBuildUsageReportEmpty: nothing to report is not the same as something
// being hidden.
func TestBuildUsageReportEmpty(t *testing.T) {
	rep := buildUsageReport(nil, ports.UsageFilter{})
	if !rep.Priced {
		t.Fatal("an empty report claims to be hiding a cost")
	}
	if rep.Lines == nil {
		t.Fatal("lines is null; the wire contract requires an array")
	}
	if rep.Currency != "USD" {
		t.Fatalf("currency = %q", rep.Currency)
	}
}

/* ── optional usage: absent is not zero ──────────────────────────────── */

func intp(n int) *int { return &n }

// TestOptionalUsageKeepsAbsentApartFromZero is the accounting half of the
// contract the adapter tests pin on the wire.
//
// A turn the gateway measured as reading zero cached tokens and a turn the
// gateway never measured must not produce the same row. The first is
// evidence that caching did nothing; the second is evidence of nothing at
// all, and a cache hit rate that averaged them together would report a
// working cache as broken, or a broken one as working, depending only on
// which turns happened to be measured.
func TestOptionalUsageKeepsAbsentApartFromZero(t *testing.T) {
	t.Run("a measured zero is present", func(t *testing.T) {
		acct := newTurnAccounting(accountingInput{
			StreamOpened: true,
			Usage: &ports.Usage{
				PromptTokens: 100, CompletionTokens: 10,
				CacheReadTokens: intp(0), CacheCreationTokens: intp(0),
			},
		})
		if acct.CacheReadTokens == nil || *acct.CacheReadTokens != 0 {
			t.Fatalf("CacheReadTokens = %v, want a present 0", acct.CacheReadTokens)
		}
		if acct.CacheCreationTokens == nil {
			t.Fatalf("CacheCreationTokens went ABSENT after being measured as 0")
		}
	})

	t.Run("an unreported field stays absent", func(t *testing.T) {
		acct := newTurnAccounting(accountingInput{
			StreamOpened: true,
			Usage:        &ports.Usage{PromptTokens: 100, CompletionTokens: 10},
		})
		if acct.CacheReadTokens != nil {
			t.Fatalf("CacheReadTokens = %d on a turn the gateway said nothing about",
				*acct.CacheReadTokens)
		}
		if acct.ReasoningTokens != nil {
			t.Fatalf("ReasoningTokens = %d, invented", *acct.ReasoningTokens)
		}
	})

	// An estimated turn is the sharpest case. We can estimate prompt tokens
	// from characters; there is no local way to estimate how much of a
	// prompt a remote cache served, so the honest answer is silence.
	t.Run("an estimated turn reports no cache figures", func(t *testing.T) {
		acct := newTurnAccounting(accountingInput{
			StreamOpened: true, EstimatedPromptTokens: 400, Content: "hello",
		})
		if acct.Source != domain.UsageEstimated {
			t.Fatalf("source = %q", acct.Source)
		}
		if acct.CacheReadTokens != nil || acct.CacheCreationTokens != nil || acct.ReasoningTokens != nil {
			t.Fatalf("an estimated turn invented cache figures: %v/%v/%v",
				acct.CacheReadTokens, acct.CacheCreationTokens, acct.ReasoningTokens)
		}
	})

	t.Run("negative counts are clamped without becoming absent", func(t *testing.T) {
		acct := newTurnAccounting(accountingInput{
			StreamOpened: true,
			Usage: &ports.Usage{
				PromptTokens: 10, CompletionTokens: 1, CacheReadTokens: intp(-5),
			},
		})
		if acct.CacheReadTokens == nil || *acct.CacheReadTokens != 0 {
			t.Fatalf("CacheReadTokens = %v, want a clamped, still-present 0", acct.CacheReadTokens)
		}
	})
}

// TestAggregateTurnSumsOptionalUsageOverRounds pins the rule that keeps one
// silent round from erasing a turn's cache figures.
//
// The realistic shape of a cached tool turn is exactly this: round 1 writes
// the entry, round 2 reads it. If the sum required every round to report,
// a gateway that skipped one frame would report the whole turn as
// unmeasured — and the saving would vanish from the books while still
// appearing on the invoice.
func TestAggregateTurnSumsOptionalUsageOverRounds(t *testing.T) {
	rounds := []providerRound{
		{Opened: true, Usage: &ports.Usage{
			PromptTokens: 12000, CompletionTokens: 100,
			CacheCreationTokens: intp(11000), CacheReadTokens: intp(0),
		}},
		{Opened: true, Usage: &ports.Usage{
			PromptTokens: 12300, CompletionTokens: 200,
			CacheCreationTokens: intp(0), CacheReadTokens: intp(11000),
		}},
	}

	total, reports := aggregateTurn(rounds, testPrice, EstimatorFor(""))

	if total.CacheCreationTokens == nil || *total.CacheCreationTokens != 11000 {
		t.Fatalf("CacheCreationTokens = %v, want 11000", total.CacheCreationTokens)
	}
	if total.CacheReadTokens == nil || *total.CacheReadTokens != 11000 {
		t.Fatalf("CacheReadTokens = %v, want 11000", total.CacheReadTokens)
	}
	// The per-round detail is what explains the total; a sum alone cannot
	// say which call wrote and which read.
	if len(reports) != 2 {
		t.Fatalf("%d round reports", len(reports))
	}
	if reports[0].Usage == nil || reports[0].Usage.CacheCreationTokens == nil ||
		*reports[0].Usage.CacheCreationTokens != 11000 {
		t.Fatalf("round 1 usage = %+v, want the write recorded", reports[0].Usage)
	}
	if reports[1].Usage == nil || reports[1].Usage.CacheReadTokens == nil ||
		*reports[1].Usage.CacheReadTokens != 11000 {
		t.Fatalf("round 2 usage = %+v, want the read recorded", reports[1].Usage)
	}
}

// TestAggregateTurnTolerAtesAnUnmeasuredRound is the same rule from the
// other side: a round with no figures contributes nothing and does not veto
// the round that did report. The result understates, which is the only
// direction a cost saving may err in.
func TestAggregateTurnToleratesAnUnmeasuredRound(t *testing.T) {
	rounds := []providerRound{
		{Opened: true, Usage: &ports.Usage{PromptTokens: 100, CompletionTokens: 5}},
		{Opened: true, Usage: &ports.Usage{
			PromptTokens: 120, CompletionTokens: 6, CacheReadTokens: intp(90),
		}},
	}
	total, _ := aggregateTurn(rounds, testPrice, EstimatorFor(""))
	if total.CacheReadTokens == nil || *total.CacheReadTokens != 90 {
		t.Fatalf("CacheReadTokens = %v, want 90 from the one round that reported",
			total.CacheReadTokens)
	}
	if total.CacheCreationTokens != nil {
		t.Fatalf("CacheCreationTokens = %d; no round reported one", *total.CacheCreationTokens)
	}
}

// TestRoundReportStaysUnchangedWithoutOptionalUsage protects every stored
// report and every golden test over one. A gateway that says nothing beyond
// the two totals must leave ContextRound.Usage nil, so the serialized report
// is byte-identical to what it was before the field existed.
func TestRoundReportStaysUnchangedWithoutOptionalUsage(t *testing.T) {
	_, reports := aggregateTurn([]providerRound{
		{Opened: true, Usage: &ports.Usage{PromptTokens: 100, CompletionTokens: 5}},
		{Opened: true, Usage: &ports.Usage{PromptTokens: 120, CompletionTokens: 6}},
	}, testPrice, EstimatorFor(""))
	for _, r := range reports {
		if r.Usage != nil {
			t.Fatalf("round %d carries a usage block the provider never sent: %+v", r.Round, r.Usage)
		}
	}
}

/* ── pricing a cached turn ───────────────────────────────────────────── */

func f64p(v float64) *float64 { return &v }

// cachedPrice is the rate card this deployment's gateway actually publishes
// for claude-opus-4-7, scaled to the test's usual per-million shape:
// creation at 1,25x input, read at 0,10x.
var cachedPrice = &ports.Price{
	InputCostPerToken:         1e-6,
	OutputCostPerToken:        3e-6,
	CacheCreationCostPerToken: f64p(1.25e-6),
	CacheReadCostPerToken:     f64p(0.1e-6),
}

// TestCachedTurnIsPricedByBillingClass is the correctness half of the
// caching change, and it is the one that decides whether the books tell the
// truth about it.
//
// `prompt_tokens` INCLUDES the cached tokens — measured against the real
// gateway, where the same request with and without a breakpoint both
// reported 13.982 prompt tokens. Pricing all of them at the input rate would
// charge a cache read ten times over and report a saving of exactly zero on
// a turn that saved 90% of its input.
func TestCachedTurnIsPricedByBillingClass(t *testing.T) {
	// 10.000 prompt tokens: 8.000 read from cache, 1.000 written to it,
	// 1.000 at full price.
	acct := newTurnAccounting(accountingInput{
		StreamOpened: true,
		Price:        cachedPrice,
		Usage: &ports.Usage{
			PromptTokens: 10000, CompletionTokens: 100,
			CacheReadTokens: intp(8000), CacheCreationTokens: intp(1000),
		},
	})

	want := 1000*1e-6 + // full price
		8000*0.1e-6 + // cache read
		1000*1.25e-6 + // cache creation
		100*3e-6 // output
	if got := costOf(t, acct); !nearly(got, want) {
		t.Fatalf("cost = %v, want %v", got, want)
	}

	// The same turn priced the old way — every prompt token at the input
	// rate — would cost far more. The gap is the saving, and a formula that
	// did not split the classes would hide all of it.
	naive := 10000*1e-6 + 100*3e-6
	if costOf(t, acct) >= naive {
		t.Fatalf("the cached turn costs %v, no less than the %v it would have cost "+
			"with every prompt token at the input rate; the saving is invisible in "+
			"the books", costOf(t, acct), naive)
	}
}

// TestUncachedTurnPricingIsUnchanged is the regression that matters most to
// every turn already in the database: with no cache counts, the formula must
// produce exactly what it produced before the split existed.
func TestUncachedTurnPricingIsUnchanged(t *testing.T) {
	acct := newTurnAccounting(accountingInput{
		StreamOpened: true, Price: cachedPrice,
		Usage: &ports.Usage{PromptTokens: 10000, CompletionTokens: 100},
	})
	want := 10000*1e-6 + 100*3e-6
	if got := costOf(t, acct); !nearly(got, want) {
		t.Fatalf("cost = %v, want the unchanged %v", got, want)
	}
}

// TestMissingCacheRateOverstatesRatherThanUnderstates pins the direction of
// the fallback.
//
// A gateway that reports cache tokens without publishing cache rates is not
// a case that occurs on this deployment. If it ever does, the turn must be
// priced at the INPUT rate — which is too much — rather than at zero, which
// would invent a saving the system cannot substantiate.
func TestMissingCacheRateOverstatesRatherThanUnderstates(t *testing.T) {
	noCacheRates := &ports.Price{InputCostPerToken: 1e-6, OutputCostPerToken: 3e-6}
	acct := newTurnAccounting(accountingInput{
		StreamOpened: true, Price: noCacheRates,
		Usage: &ports.Usage{
			PromptTokens: 10000, CompletionTokens: 100,
			CacheReadTokens: intp(8000),
		},
	})
	want := 10000*1e-6 + 100*3e-6 // everything at the input rate
	if got := costOf(t, acct); !nearly(got, want) {
		t.Fatalf("cost = %v, want the overstating %v", got, want)
	}
}

// TestCacheCountsAboveThePromptAreClamped: a gateway reporting more cached
// tokens than prompt tokens breaks the partition. The row is still a real
// record and must not carry a negative charge.
func TestCacheCountsAboveThePromptAreClamped(t *testing.T) {
	acct := newTurnAccounting(accountingInput{
		StreamOpened: true, Price: cachedPrice,
		Usage: &ports.Usage{
			PromptTokens: 100, CompletionTokens: 10,
			CacheReadTokens: intp(500),
		},
	})
	if got := costOf(t, acct); got < 0 {
		t.Fatalf("cost = %v; a nonsensical count produced a negative charge", got)
	}
}

// TestThePromptIsPartitionedNotDoubleCounted is the explicit no-double-count
// assertion, and it is the one that makes the cost formula falsifiable.
//
// ── The fact it rests on ───────────────────────────────────────────────
// `prompt_tokens` INCLUDES the cached share. Measured against the real
// gateway, not assumed: the same prompt reported 13.982 tokens on a cache
// write, on a cache read, and with no caching at all. Had the counts been
// additive the cached call would have reported 17.
//
// ── What could go wrong in each direction ──────────────────────────────
// Treat them as additive and every cached token is billed twice — once at
// the input rate inside prompt_tokens and once at the cache rate beside it.
// Ignore the classes and a cache read is billed at ten times what it cost,
// which is what the formula did before this release and is why the first
// controlled A/B reported identical costs for both arms.
//
// So the three classes must PARTITION the prompt: they sum to it exactly,
// and no token appears in two of them.
func TestThePromptIsPartitionedNotDoubleCounted(t *testing.T) {
	const (
		prompt   = 10000
		read     = 8000
		creation = 1000
		output   = 100
	)
	acct := newTurnAccounting(accountingInput{
		StreamOpened: true,
		Price:        cachedPrice,
		Usage: &ports.Usage{
			PromptTokens: prompt, CompletionTokens: output,
			CacheReadTokens: intp(read), CacheCreationTokens: intp(creation),
		},
	})

	// 1. The partition closes. Full price is what is left, never the whole.
	full := prompt - read - creation
	if full != 1000 {
		t.Fatalf("fixture arithmetic: full = %d", full)
	}

	// 2. The cost is exactly the four classes, each counted once.
	want := float64(full)*cachedPrice.InputCostPerToken +
		float64(read)**cachedPrice.CacheReadCostPerToken +
		float64(creation)**cachedPrice.CacheCreationCostPerToken +
		float64(output)*cachedPrice.OutputCostPerToken
	if got := costOf(t, acct); !nearly(got, want) {
		t.Fatalf("cost = %v, want %v", got, want)
	}

	// 3. And it is strictly between the two ways of getting it wrong.
	//
	// additive: every cached token billed inside prompt_tokens AND again
	//           beside it — the double count
	// naive:    every prompt token at the input rate — the pre-release
	//           formula, which hides the entire saving
	additive := float64(prompt)*cachedPrice.InputCostPerToken +
		float64(read)**cachedPrice.CacheReadCostPerToken +
		float64(creation)**cachedPrice.CacheCreationCostPerToken +
		float64(output)*cachedPrice.OutputCostPerToken
	naive := float64(prompt)*cachedPrice.InputCostPerToken +
		float64(output)*cachedPrice.OutputCostPerToken

	got := costOf(t, acct)
	if got >= additive {
		t.Fatalf("cost %v is at or above the double-counted figure %v; cached tokens "+
			"are being billed twice", got, additive)
	}
	if got >= naive {
		t.Fatalf("cost %v is at or above the pre-release figure %v; the saving is "+
			"invisible in the books", got, naive)
	}

	// 4. The turn stores the counts that make the arithmetic checkable
	//    afterwards. A cost nobody can re-derive is a number, not a record.
	if acct.CacheReadTokens == nil || *acct.CacheReadTokens != read {
		t.Fatalf("cache read not recorded: %v", acct.CacheReadTokens)
	}
	if acct.CacheCreationTokens == nil || *acct.CacheCreationTokens != creation {
		t.Fatalf("cache creation not recorded: %v", acct.CacheCreationTokens)
	}
	if acct.PromptTokens != prompt {
		t.Fatalf("prompt tokens = %d; the provider's own total must be stored whole, "+
			"not reduced to the full-price share", acct.PromptTokens)
	}
}
