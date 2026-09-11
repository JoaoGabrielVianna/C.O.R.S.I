package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

/*
The receipt that would have contradicted an invented follower count.

Every case here is written from the failure: a turn produced
"1.535 seguidores, 40.055 views" having called nothing, and no layer could
say so. What is asserted is that the receipt derives from EXECUTION, never
from the answer, and that absence is a value rather than a gap.
*/

func rec(name string, status ToolCallStatus, external bool) ToolCallRecord {
	return ToolCallRecord{
		ID:         uuid.New(),
		ToolName:   ToolName(name),
		Status:     status,
		External:   external,
		CreatedAt:  time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC),
		DurationMS: 12,
	}
}

// THE case. A turn with no external call is NO_EXTERNAL_READ, and that is
// a statement — not a missing field a surface can skip rendering.
func TestATurnThatReadNothingExternalSaysSo(t *testing.T) {
	r := NewReadReceipt(uuid.New(), true, nil)

	if r.Status != NoExternalRead {
		t.Fatalf("status = %q, want %q", r.Status, NoExternalRead)
	}
	if r.Verifiable() {
		t.Error("a turn that read nothing reported itself verifiable")
	}
	if r.Reads == nil {
		t.Error("Reads is nil; it must serialize as an empty list, not null")
	}
	if r.Verified != 0 || r.Failed != 0 {
		t.Errorf("counts = %d/%d", r.Verified, r.Failed)
	}
}

// Reading our OWN state is not evidence about somebody else's. A turn full
// of internal calls is still NO_EXTERNAL_READ.
func TestInternalReadsAreNotExternalEvidence(t *testing.T) {
	r := NewReadReceipt(uuid.New(), true, []ToolCallRecord{
		rec("threads.thread.list", ToolCallOK, false),
		rec("threads.thread.get", ToolCallOK, false),
		rec("finance.summary.get", ToolCallOK, false),
	})

	if r.Status != NoExternalRead {
		t.Fatalf("status = %q; internal reads were counted as external evidence", r.Status)
	}
	if len(r.Reads) != 0 {
		t.Errorf("%d reads recorded, want 0", len(r.Reads))
	}
}

// A successful external read is what makes a turn verifiable.
func TestASuccessfulExternalReadMakesTheTurnVerifiable(t *testing.T) {
	r := NewReadReceipt(uuid.New(), true, []ToolCallRecord{
		rec("meta_threads.profile.insights", ToolCallOK, true),
	})

	if r.Status != VerifiedExternalRead || !r.Verifiable() {
		t.Fatalf("status = %q", r.Status)
	}
	if r.Verified != 1 {
		t.Errorf("verified = %d", r.Verified)
	}
	if got := r.Reads[0].Source; got != "meta_threads" {
		t.Errorf("source = %q; it must come from the capability name", got)
	}
	if r.Reads[0].Capability != "meta_threads.profile.insights" {
		t.Errorf("capability = %q", r.Reads[0].Capability)
	}
}

// A failed read is its own state. Reported as FAILED, never as verified and
// never as "no posts" — the distinction the whole integration is built on.
func TestAFailedExternalReadIsNotVerifiedAndNotSilent(t *testing.T) {
	r := NewReadReceipt(uuid.New(), true, []ToolCallRecord{
		rec("meta_threads.post.list", ToolCallError, true),
	})

	if r.Status != FailedExternalRead {
		t.Fatalf("status = %q, want %q", r.Status, FailedExternalRead)
	}
	if r.Verifiable() {
		t.Error("a failed read reported itself verifiable")
	}
	if r.Failed != 1 || r.Verified != 0 {
		t.Errorf("counts = %d verified / %d failed", r.Verified, r.Failed)
	}
	// And it is DISTINGUISHABLE from having read nothing, because the two
	// call for different words and different next actions.
	if r.Status == NoExternalRead {
		t.Error("a failure collapsed into no-read")
	}
}

// A refusal — unauthorized, bad arguments — never ran, so it produced no
// evidence. Grouped with failure because they do not differ in what the
// turn may claim, only in whose fault it was, which the code carries.
func TestARefusedExternalCallProducesNoEvidence(t *testing.T) {
	refused := rec("meta_threads.post.list", ToolCallNotExecuted, true)
	refused.ErrorCode = ToolErrNotAuthorized
	r := NewReadReceipt(uuid.New(), true, []ToolCallRecord{refused})

	if r.Verifiable() {
		t.Fatal("a refused call made the turn verifiable")
	}
	if r.Status != FailedExternalRead {
		t.Errorf("status = %q", r.Status)
	}
	if r.Reads[0].ErrorCode != ToolErrNotAuthorized {
		t.Errorf("the error code was lost: %q", r.Reads[0].ErrorCode)
	}
}

// One success alongside a failure still means evidence was obtained. The
// failure is reported in the counts rather than by downgrading the turn.
func TestOneSuccessAmongFailuresStillCountsAsEvidence(t *testing.T) {
	r := NewReadReceipt(uuid.New(), true, []ToolCallRecord{
		rec("meta_threads.post.search", ToolCallError, true),
		rec("meta_threads.post.insights", ToolCallOK, true),
		rec("meta_threads.post.search", ToolCallError, true),
	})

	if r.Status != VerifiedExternalRead {
		t.Fatalf("status = %q", r.Status)
	}
	if r.Verified != 1 || r.Failed != 2 {
		t.Errorf("counts = %d/%d, want 1 verified and 2 failed", r.Verified, r.Failed)
	}
	// The counts are what let a surface be specific instead of binary.
	if len(r.Reads) != 3 {
		t.Errorf("%d reads, want all three listed", len(r.Reads))
	}
}

/* ── what the receipt must never carry ───────────────────────────────── */

// No payload. The receipt says a read HAPPENED, never what it returned —
// copying metrics in here would persist somebody's data a second time,
// outside the rule that governs the first copy.
func TestTheReceiptCarriesNoPayload(t *testing.T) {
	args := `{"post_id":"p_005"}`
	result := `{"metrics":{"followers_count":163,"views":224}}`
	full := rec("meta_threads.profile.insights", ToolCallOK, true)
	full.Arguments = &args
	full.Result = &result

	r := NewReadReceipt(uuid.New(), true, []ToolCallRecord{full})

	// The struct has no field for either, so this is a compile-time
	// guarantee restated as a runtime one for the day somebody adds a map.
	read := r.Reads[0]
	if read.Capability == "" || read.ToolCallID == uuid.Nil {
		t.Fatal("the receipt lost the metadata it is supposed to carry")
	}
	// Serialised, nothing of the payload may appear.
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	encoded := string(raw)
	for _, secret := range []string{"163", "224", "followers_count", "post_id", "p_005"} {
		if strings.Contains(encoded, secret) {
			t.Errorf("the receipt serialisation carries payload: %q in %s", secret, encoded)
		}
	}
}

// Availability is a presentation gate and carries no claim. It must not
// change what the turn is allowed to assert.
func TestAvailabilityDoesNotChangeTheClaim(t *testing.T) {
	records := []ToolCallRecord{rec("meta_threads.profile.get", ToolCallOK, true)}

	on := NewReadReceipt(uuid.New(), true, records)
	off := NewReadReceipt(uuid.New(), false, records)

	if on.Status != off.Status || on.Verifiable() != off.Verifiable() {
		t.Fatal("the presentation gate changed the evidence state")
	}
	if !on.Available || off.Available {
		t.Error("Available did not travel")
	}
}
