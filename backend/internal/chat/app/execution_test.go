package app

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/domain"
)

/* ── fixtures ────────────────────────────────────────────────────────── */

func writeRecord(msg uuid.UUID, name string, status domain.ToolCallStatus, code domain.ToolErrorCode) domain.ToolCallRecord {
	return domain.ToolCallRecord{
		ID:        uuid.New(),
		MessageID: msg,
		ToolName:  domain.ToolName(name),
		Effect:    domain.EffectWrite,
		Status:    status,
		ErrorCode: code,
		CreatedAt: time.Now(),
	}
}

func readRecord(msg uuid.UUID, name string) domain.ToolCallRecord {
	r := writeRecord(msg, name, domain.ToolCallOK, "")
	r.Effect = domain.EffectRead
	return r
}

/* ── what the block is made of ───────────────────────────────────────── */

func TestExecutionEvidenceReportsWhatRanAndHowItEnded(t *testing.T) {
	m := uuid.New()
	got := SelectExecutionEvidence([]uuid.UUID{m}, []domain.ToolCallRecord{
		writeRecord(m, "palace.artifact.create", domain.ToolCallOK, ""),
		writeRecord(m, "palace.memory.create", domain.ToolCallError, domain.ToolErrExecutionFailed),
	}, ExecutionBudgetChars)

	if len(got.Turns) != 1 || len(got.Turns[0].Entries) != 2 {
		t.Fatalf("selection = %+v", got)
	}
	block := RenderExecutionBlock(got.Turns)
	for _, want := range []string{
		"EXECUTED palace.artifact.create x1",
		"FAILED palace.memory.create x1 tool_execution_failed",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("block does not contain %q:\n%s", want, block)
		}
	}
}

// A redacted row is what the whole slice exists for. Evidence continuity
// treats it as carrying nothing — correctly, it has no payload — and this
// block must treat it as carrying everything it needs.
func TestARedactedRecordStillReportsItsExecution(t *testing.T) {
	m := uuid.New()
	rec := writeRecord(m, "finance.transaction.create", domain.ToolCallOK, "")
	rec.Redacted = true
	rec.Arguments, rec.Result = nil, nil

	got := SelectExecutionEvidence([]uuid.UUID{m}, []domain.ToolCallRecord{rec}, ExecutionBudgetChars)
	if len(got.Turns) != 1 {
		t.Fatal("a Confidential write vanished from the execution record; " +
			"this is the S7 false negative, reintroduced")
	}
	if s := RenderExecutionBlock(got.Turns); !strings.Contains(s, "EXECUTED") {
		t.Fatalf("block = %q", s)
	}
}

// A read is not a change. The rule is inherited from NewWriteReceipt rather
// than re-implemented, and this pins the consequence.
func TestReadsNeverEnterTheExecutionRecord(t *testing.T) {
	m := uuid.New()
	got := SelectExecutionEvidence([]uuid.UUID{m}, []domain.ToolCallRecord{
		readRecord(m, "palace.artifact.list"),
		readRecord(m, "palace.memory.get"),
	}, ExecutionBudgetChars)

	if !got.Empty() {
		t.Fatalf("reads were reported as executions: %+v", got)
	}
}

// Identical outcomes fold; different ones do not. Five calls to one
// capability are one line saying five, because the model gains nothing from
// the repetition and the budget pays for all of it.
func TestIdenticalOutcomesFoldAndDifferentOnesDoNot(t *testing.T) {
	m := uuid.New()
	recs := []domain.ToolCallRecord{
		writeRecord(m, "palace.artifact.item.add", domain.ToolCallOK, ""),
		writeRecord(m, "palace.artifact.item.add", domain.ToolCallOK, ""),
		writeRecord(m, "palace.artifact.item.add", domain.ToolCallNotExecuted, domain.ToolErrRoundLimit),
	}
	got := SelectExecutionEvidence([]uuid.UUID{m}, recs, ExecutionBudgetChars)

	if len(got.Turns[0].Entries) != 2 {
		t.Fatalf("entries = %+v, want executed and refused kept apart", got.Turns[0].Entries)
	}
	block := RenderExecutionBlock(got.Turns)
	if !strings.Contains(block, "EXECUTED palace.artifact.item.add x2") {
		t.Errorf("the two executions did not fold into one line:\n%s", block)
	}
	if !strings.Contains(block, "NOT_EXECUTED palace.artifact.item.add x1 tool_round_limit") {
		t.Errorf("the refusal was folded into the successes:\n%s", block)
	}
}

/* ── scope and order ─────────────────────────────────────────────────── */

// The window decides the scope, not the audit trail. A record whose turn is
// not in `order` describes an exchange the model cannot see.
func TestOnlyTurnsInTheWindowAreDescribed(t *testing.T) {
	inWindow, outOfWindow := uuid.New(), uuid.New()
	got := SelectExecutionEvidence([]uuid.UUID{inWindow}, []domain.ToolCallRecord{
		writeRecord(inWindow, "a.b.c", domain.ToolCallOK, ""),
		writeRecord(outOfWindow, "x.y.z", domain.ToolCallOK, ""),
	}, ExecutionBudgetChars)

	if len(got.Turns) != 1 || got.Turns[0].MessageID != inWindow {
		t.Fatalf("turns = %+v", got.Turns)
	}
	if strings.Contains(RenderExecutionBlock(got.Turns), "x.y.z") {
		t.Fatal("a turn outside the window reached the block")
	}
}

func TestTheRecordIsChronological(t *testing.T) {
	first, second := uuid.New(), uuid.New()
	got := SelectExecutionEvidence([]uuid.UUID{first, second}, []domain.ToolCallRecord{
		writeRecord(first, "first.tool.run", domain.ToolCallOK, ""),
		writeRecord(second, "second.tool.run", domain.ToolCallOK, ""),
	}, ExecutionBudgetChars)

	block := RenderExecutionBlock(got.Turns)
	if strings.Index(block, "first.tool.run") > strings.Index(block, "second.tool.run") {
		t.Fatalf("the record is not in the order the conversation happened:\n%s", block)
	}
}

/* ── the budget ──────────────────────────────────────────────────────── */

// The rendered block never exceeds what the selection charged for it. The
// same identity memory, sources and evidence are each held to.
func TestExecutionBudgetMatchesTheRenderedBlock(t *testing.T) {
	var order []uuid.UUID
	var recs []domain.ToolCallRecord
	for i := 0; i < 60; i++ {
		m := uuid.New()
		order = append(order, m)
		recs = append(recs,
			writeRecord(m, "palace.artifact.create", domain.ToolCallOK, ""),
			writeRecord(m, "palace.memory.create", domain.ToolCallNotExecuted, domain.ToolErrRoundLimit))
	}

	got := SelectExecutionEvidence(order, recs, ExecutionBudgetChars)
	if got.DroppedTurns == 0 {
		t.Fatal("sixty write-heavy turns did not reach the ceiling; the " +
			"fixture no longer exercises the bound")
	}
	if n := utf8.RuneCountInString(RenderExecutionBlock(got.Turns)); n > ExecutionBudgetChars {
		t.Fatalf("the block is %d characters, above the %d budgeted", n, ExecutionBudgetChars)
	}
}

// Newest first while the budget lasts. The false negative is about the turn
// that just happened, so that is the entry that must survive a squeeze.
func TestTheBudgetKeepsTheMostRecentTurns(t *testing.T) {
	var order []uuid.UUID
	var recs []domain.ToolCallRecord
	for i := 0; i < 60; i++ {
		m := uuid.New()
		order = append(order, m)
		recs = append(recs, writeRecord(m, "palace.artifact.create", domain.ToolCallOK, ""))
	}
	newest := order[len(order)-1]
	oldest := order[0]

	// Room for the header and roughly three turns, so the squeeze is real
	// and the surviving set is small enough to assert on. Derived rather
	// than typed: a literal would silently become "nothing fits" the next
	// time the header gains a clause.
	budget := utf8.RuneCountInString(executionHeader) +
		3*utf8.RuneCountInString(renderExecutionTurn(ExecutionTurn{
			Entries: []ExecutionEntry{{
				Capability: "palace.artifact.create",
				Status:     domain.WriteExecuted,
				Count:      1,
			}},
		}))

	got := SelectExecutionEvidence(order, recs, budget)
	if got.DroppedTurns == 0 {
		t.Fatal("the fixture did not reach the ceiling")
	}
	var sawNewest, sawOldest bool
	for _, turn := range got.Turns {
		sawNewest = sawNewest || turn.MessageID == newest
		sawOldest = sawOldest || turn.MessageID == oldest
	}
	if !sawNewest {
		t.Error("the most recent turn was dropped; it is the one that matters")
	}
	if sawOldest {
		t.Error("the oldest turn survived a squeeze the newest should have won")
	}
}

/* ── the block in a turn ─────────────────────────────────────────────── */

// An empty record is never rendered, because it would be a statement. A
// failed read is never rendered either, for the same reason and more so.
func TestNoBlockWithoutWritesAndNoBlockWhenTheAuditFailed(t *testing.T) {
	for _, tc := range []struct {
		name        string
		evidence    ExecutionEvidence
		unavailable bool
		wantReason  domain.ExclusionReason
	}{
		{name: "nothing was written"},
		{name: "the audit read failed", unavailable: true, wantReason: domain.ReasonUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			built := BuildContext(ContextInput{
				Agent:               &domain.Agent{SystemPrompt: "p", Model: "claude-opus-4-7"},
				Execution:           tc.evidence,
				EvidenceUnavailable: tc.unavailable,
			})
			for _, m := range built.Messages {
				if strings.Contains(m.Content, executionHeader) {
					t.Fatal("the header reached the model with nothing under it")
				}
			}
			block, ok := built.Report.Block(domain.BlockExecutionEvidence)
			if tc.wantReason == "" {
				if ok && block.Characters > 0 {
					t.Fatalf("block = %+v, want nothing", block)
				}
				return
			}
			if !ok {
				t.Fatal("a failed audit read was not recorded as a degradation")
			}
			if len(block.Exclusions) != 1 || block.Exclusions[0].Reason != tc.wantReason {
				t.Fatalf("exclusions = %+v, want %q", block.Exclusions, tc.wantReason)
			}
		})
	}
}

// Every character that reaches the model appears in the account. The
// discipline the whole report exists for.
func TestTheExecutionBlockIsCharged(t *testing.T) {
	m := uuid.New()
	ev := SelectExecutionEvidence([]uuid.UUID{m},
		[]domain.ToolCallRecord{writeRecord(m, "palace.artifact.create", domain.ToolCallOK, "")},
		ExecutionBudgetChars)

	built := BuildContext(ContextInput{
		Agent:     &domain.Agent{SystemPrompt: "p", Model: "claude-opus-4-7"},
		Execution: ev,
	})

	block, ok := built.Report.Block(domain.BlockExecutionEvidence)
	if !ok {
		t.Fatal("the block was sent and not reported")
	}
	var sent string
	for _, msg := range built.Messages {
		if strings.Contains(msg.Content, executionHeader) {
			sent = msg.Content
		}
	}
	if got := utf8.RuneCountInString(sent); got != block.Characters {
		t.Fatalf("reported %d characters, sent %d", block.Characters, got)
	}
	if block.Items != 1 {
		t.Fatalf("items = %d, want the number of turns described", block.Items)
	}
}

/* ── the boundary, as a property of the text ─────────────────────────── */

// RECEIPT PROVES EXECUTION; READ PROVES STATE.
//
// The half that is easy to lose in a rewrite is the second one. A block that
// told the model a write happened without telling it that this says nothing
// about the current state would invite exactly the reconstruction redaction
// exists to prevent.
func TestTheHeaderRefusesToBeReadAsState(t *testing.T) {
	lower := strings.ToLower(executionHeader + " " + groundingPolicy)
	for _, must := range []string{
		// it is the log of what ran
		"execution",
		// absence is not proof
		"not evidence that it did not happen",
		// and it does not describe the present
		"holds now",
		"read it with a capability",
	} {
		if !strings.Contains(lower, strings.ToLower(must)) {
			t.Errorf("the contract no longer says %q", must)
		}
	}
}

// The rule that closes the observed defect, pinned so a rewrite cannot drop
// it while keeping the paragraph.
func TestThePolicyForbidsDenyingAnExecutedWrite(t *testing.T) {
	lower := strings.ToLower(groundingPolicy)
	for _, must := range []string{
		"never deny doing what it records as executed",
		"withheld payload",
	} {
		if !strings.Contains(lower, must) {
			t.Errorf("the policy no longer says %q", must)
		}
	}
}

// The block carries capability names, statuses, codes and counts. It must
// never learn to carry anything a capability returned.
func TestTheRenderedBlockHasNoPlaceToPutAPayload(t *testing.T) {
	m := uuid.New()
	rec := writeRecord(m, "finance.transaction.create", domain.ToolCallOK, "")
	args := `{"amount_cents":899000}`
	result := `{"id":"SEGREDO"}`
	rec.Arguments, rec.Result = &args, &result
	rec.ErrorMessage = "categoria SEGREDO-MENSAGEM não existe"

	got := SelectExecutionEvidence([]uuid.UUID{m}, []domain.ToolCallRecord{rec}, ExecutionBudgetChars)
	block := RenderExecutionBlock(got.Turns)

	for _, forbidden := range []string{"899000", "SEGREDO", "SEGREDO-MENSAGEM", "amount_cents"} {
		if strings.Contains(block, forbidden) {
			t.Fatalf("the block rendered %q; it may carry no payload and no "+
				"error MESSAGE, only codes", forbidden)
		}
	}
}
