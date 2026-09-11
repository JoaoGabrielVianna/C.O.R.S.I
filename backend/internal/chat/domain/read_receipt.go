package domain

import (
	"time"

	"github.com/google/uuid"
)

// Read receipts: one turn's answer to "was anything actually read from
// outside this product?"
//
// ══════════════════════════════════════════════════════════════════════
//  The failure this exists to close
// ══════════════════════════════════════════════════════════════════════
//
// A live content agent answered:
//
//	followers = 1535 · views = 40055 · likes = 1375
//
// in a turn that made ZERO tool calls. Read through the capability
// moments later, the true figures were 163 and 224. Every layer behaved:
// the gate allowed the call that was never made, the tool worked, the
// audit recorded the truth — that nothing ran. The person was told
// invented numbers because the product renders prose, and the absence of a
// read is silent.
//
// This is the read-side twin of the defect WriteReceipt answers, and the
// answer has the same shape: make the absence explicit, for every turn,
// whether or not anything ran. `NO_EXTERNAL_READ` is a statement, not a
// missing field.
//
// ── What this is NOT ───────────────────────────────────────────────────
// It is not a claim detector. Nothing here reads the model's sentence,
// looks for numbers, or decides whether a paragraph "sounds like a
// metric" — that would be a heuristic pretending to be a guarantee, and
// the first false negative would be indistinguishable from safety.
//
// The guarantee is precise and worth stating exactly: this does not stop
// a model writing a false sentence. It stops the PRODUCT presenting that
// sentence as a verified reading of an external system. What the reader
// sees beside the answer is derived from execution records, not from the
// answer.

/* ── the vocabulary ──────────────────────────────────────────────────── */

// ExternalReadStatus is a turn's overall evidence state.
type ExternalReadStatus string

const (
	// VerifiedExternalRead: at least one external capability ran and
	// succeeded in this turn. Whatever the answer says about that system,
	// something was actually read.
	VerifiedExternalRead ExternalReadStatus = "VERIFIED_EXTERNAL_READ"
	// FailedExternalRead: external reads were attempted and none succeeded.
	//
	// Its own state rather than folded into NO_EXTERNAL_READ, because the
	// two call for different words on screen and different next actions: a
	// failure is "we tried and the source did not answer", which the reader
	// can retry, and which must never be shown as an empty result.
	FailedExternalRead ExternalReadStatus = "FAILED_EXTERNAL_READ"
	// NoExternalRead: nothing external ran. THE important one — this is the
	// state the invented follower count was produced in.
	NoExternalRead ExternalReadStatus = "NO_EXTERNAL_READ"
)

// ExternalRead is one call to a system this product does not own.
//
// ── Why it carries no payload, and must not ────────────────────────────
// The receipt answers "was this read", not "what did it say". The answer
// itself is in the transcript and the payload — when the capability
// permits keeping one at all — is in the audit trail, under whatever
// redaction that capability declared. Copying a follower count in here to
// make a receipt would persist somebody's metrics a second time, in a
// record built for provenance, outside the rule that governs the first
// copy. See ToolDefinition.Confidential.
type ExternalRead struct {
	ToolCallID uuid.UUID `json:"tool_call_id"`
	Capability ToolName  `json:"capability"`
	// Source is the capability's owner — the first segment of its name.
	// Derived, never stored: it is the same fact the name already carries,
	// and a second copy would be free to disagree. It is what lets a
	// surface say "read from meta_threads" without Agents holding a list of
	// vendors.
	Source     string             `json:"source"`
	Status     ExternalReadStatus `json:"status"`
	OccurredAt time.Time          `json:"occurred_at"`
	DurationMS int                `json:"duration_ms"`
	// ErrorCode is present on a failure. A code, not a message: a message
	// can carry the very data redaction removed.
	ErrorCode ToolErrorCode `json:"error_code,omitempty"`
}

// ReadReceipt is one assistant turn's external-evidence record.
type ReadReceipt struct {
	MessageID uuid.UUID      `json:"message_id"`
	Reads     []ExternalRead `json:"reads"`
	// Status is the turn's overall state, precomputed so no surface has to
	// re-derive the precedence rule and get it subtly different.
	Status ExternalReadStatus `json:"status"`
	// Verified and Failed are the counts behind Status, so a surface can be
	// specific ("2 of 3 reads failed") without walking the list.
	Verified int `json:"verified"`
	Failed   int `json:"failed"`
	// Available says whether this turn could have read externally at all —
	// whether any external capability was on the table.
	//
	// ── Why this is here, and what it is not ──────────────────────────
	// It is a PRESENTATION GATE and carries no claim about the past. An
	// agent with no external capability produces NO_EXTERNAL_READ on every
	// turn, correctly and uselessly; showing that everywhere would train
	// the reader to ignore the one place it matters. So a surface renders
	// the absence only where the absence is meaningful.
	//
	// It is deliberately NOT part of the truth the receipt asserts. The
	// three statuses come from execution records; this comes from
	// configuration, and configuration is a fact about now.
	Available bool `json:"available"`
}

// Verifiable reports whether this turn's answer may be presented as
// resting on a current external reading.
//
// The one question a surface should ask. It is deliberately narrow: a
// single successful external read makes the turn verifiABLE, not correct.
// Nothing in this package can tell whether the sentence the model wrote
// matches what the capability returned.
func (r ReadReceipt) Verifiable() bool { return r.Status == VerifiedExternalRead }

// NewReadReceipt derives a turn's receipt from its audit records.
//
// ── The precedence, and why ────────────────────────────────────────────
// Any success makes the turn VERIFIED, even alongside a failure: the model
// did obtain current evidence, and the failed sibling is reported in the
// counts rather than by downgrading the whole turn. A turn where reads
// were attempted and all failed is FAILED. A turn with no external call at
// all is NO_EXTERNAL_READ — including a turn that made plenty of internal
// ones, because reading our own database says nothing about theirs.
func NewReadReceipt(messageID uuid.UUID, available bool, records []ToolCallRecord) ReadReceipt {
	r := ReadReceipt{
		MessageID: messageID,
		Reads:     []ExternalRead{},
		Status:    NoExternalRead,
		Available: available,
	}
	for _, rec := range records {
		if !rec.External {
			continue
		}
		read := ExternalRead{
			ToolCallID: rec.ID,
			Capability: rec.ToolName,
			Source:     rec.ToolName.Namespace(),
			OccurredAt: rec.CreatedAt,
			DurationMS: rec.DurationMS,
			ErrorCode:  rec.ErrorCode,
		}
		if rec.Status == ToolCallOK {
			read.Status = VerifiedExternalRead
			r.Verified++
		} else {
			// Everything that is not a success is a failure to obtain
			// evidence, and they are one state on purpose. A refusal and an
			// upstream error differ in whose fault it was, which the error
			// code carries; they do not differ in what the turn may claim.
			read.Status = FailedExternalRead
			r.Failed++
		}
		r.Reads = append(r.Reads, read)
	}
	switch {
	case r.Verified > 0:
		r.Status = VerifiedExternalRead
	case r.Failed > 0:
		r.Status = FailedExternalRead
	}
	return r
}
