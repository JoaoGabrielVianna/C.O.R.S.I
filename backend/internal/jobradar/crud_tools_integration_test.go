//go:build integration

// The create and delete capabilities.
//
//	Run with: TEST_POSTGRES_DSN=... go test -tags=integration ./internal/jobradar/...
//
// Both go through the SAME application layer the web form goes through.
// What these tests hold is that they did not acquire a second set of rules
// on the way: same validation, same default stage, same soft delete, same
// workspace scoping, same error vocabulary.
//
// The sharpest one is at the bottom: removal and rejection are different
// events, and a model that conflates them destroys the record of an
// application that really happened.
package jobradar

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
	jrtools "github.com/corsi/backend/internal/jobradar/tools"
)

/* ── registration ────────────────────────────────────────────────────── */

// Both are writes, and both say so. The effect is the field an operator
// reads before granting, so a write that declared itself read would be the
// module lying in the one place it must not.
func TestTheNewToolsAreRegisteredAsWrites(t *testing.T) {
	e := newEnv(t)
	for _, name := range []chatdomain.ToolName{
		jrtools.OpportunityCreateTool, jrtools.OpportunityDeleteTool,
	} {
		tool, ok := e.registry.Lookup(name)
		if !ok {
			t.Fatalf("%s is not in the registry", name)
		}
		if got := tool.Definition().Effect; got != chatdomain.EffectWrite {
			t.Errorf("%s declares effect %q, want %q", name, got, chatdomain.EffectWrite)
		}
	}
}

// A new capability must not appear on an existing agent by surprise. Deny
// by default is the rule, and "the tool exists" has never been permission.
func TestTheNewToolsStartUnauthorized(t *testing.T) {
	e := newEnv(t)
	agentID, _ := e.newAgent(e.wsA, "existing")
	e.authorize(e.wsA, agentID, "job_radar.opportunity.list")

	report, err := e.chatSvc.AgentTools(ctxFor(e.wsA), e.wsA, agentID)
	if err != nil {
		t.Fatalf("agent tools: %v", err)
	}
	for _, item := range report.Items {
		switch item.Name {
		case jrtools.OpportunityCreateTool, jrtools.OpportunityDeleteTool:
			if item.Authorized {
				t.Errorf("%s was authorized without anyone granting it", item.Name)
			}
		}
	}
}

/* ── create ──────────────────────────────────────────────────────────── */

// The minimum a person actually says. Everything else stays empty rather
// than being filled with something plausible.
func TestCreateNeedsOnlyACompanyAndARole(t *testing.T) {
	e := newEnv(t)

	out := e.execute(t, e.wsA, "job_radar.opportunity.create", map[string]any{
		"company": "Safra", "role": "Backend Engineer",
	})
	id, _ := out["opportunity_id"].(string)
	if id == "" {
		t.Fatalf("no id came back: %+v", out)
	}
	if out["created"] != true {
		t.Errorf("created = %v", out["created"])
	}

	// The id is immediately usable, which is the point of returning it: the
	// next thing the model does is get, move or delete on what it just made.
	got := e.execute(t, e.wsA, "job_radar.opportunity.get", map[string]any{"opportunity_id": id})
	o, _ := got["opportunity"].(map[string]any)
	if o["company"] != "Safra" || o["role"] != "Backend Engineer" {
		t.Fatalf("stored record = %+v", o)
	}
	// Nothing was invented into the fields the user never mentioned.
	for _, blank := range []string{"salary", "location"} {
		if v, _ := o[blank].(string); v != "" {
			t.Errorf("%s = %q; the tool filled in a field the user never gave", blank, v)
		}
	}
	if _, present := o["description"]; present {
		t.Errorf("a description was invented: %v", o["description"])
	}
}

// Absent stage means Discover, which is the canonical default the web form
// already applies. The tool does not get its own opinion about where a new
// record starts.
func TestCreateWithoutAStageLandsInDiscover(t *testing.T) {
	e := newEnv(t)
	out := e.execute(t, e.wsA, "job_radar.opportunity.create", map[string]any{
		"company": "Safra", "role": "Backend Engineer",
	})
	if out["stage"] != "discover" {
		t.Fatalf("stage = %v, want discover", out["stage"])
	}
}

// When the user says where they already are, the record is born there.
func TestCreateCanStartAtAStage(t *testing.T) {
	e := newEnv(t)
	out := e.execute(t, e.wsA, "job_radar.opportunity.create", map[string]any{
		"company": "Safra", "role": "Backend Engineer", "stage": "interview",
	})
	if out["stage"] != "interview" {
		t.Fatalf("stage = %v", out["stage"])
	}
	// And it enters the pipeline with the event that says so, exactly as a
	// record created through the form at a stage does. Read over HTTP,
	// because that is the shape both the board and the modal consume.
	id, _ := out["opportunity_id"].(string)
	rec := e.do(http.MethodGet, "/job-radar/opportunities/"+id, e.wsA, nil)
	var body struct {
		History []struct {
			FromStage *string `json:"from_stage"`
			ToStage   string  `json:"to_stage"`
		} `json:"history"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.History) != 1 || body.History[0].ToStage != "interview" {
		t.Fatalf("history = %+v, want one entry event into interview", body.History)
	}
	// Entering the pipeline comes from Discover; naming a predecessor would
	// invent a visit that never happened.
	if body.History[0].FromStage != nil {
		t.Error("the entry event was given a predecessor it never had")
	}
}

// The two required fields are required, and the refusal is the domain's own
// sentence rather than a schema code the model cannot act on.
func TestCreateRefusesAnEmptyCompanyOrRole(t *testing.T) {
	e := newEnv(t)

	if _, err := e.executeErr(e.wsA, "job_radar.opportunity.create",
		map[string]any{"company": "Safra"}); err == nil {
		t.Error("a create with no role was accepted")
	}
	if _, err := e.executeErr(e.wsA, "job_radar.opportunity.create",
		map[string]any{"role": "Backend Engineer"}); err == nil {
		t.Error("a create with no company was accepted")
	}
	_, err := e.executeErr(e.wsA, "job_radar.opportunity.create",
		map[string]any{"company": "   ", "role": "Backend Engineer"})
	if err == nil {
		t.Fatal("a create with a blank company was accepted")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "company") {
		t.Errorf("the refusal does not name the missing field: %v", err)
	}
}

// An unknown stage fails rather than being mapped to something plausible,
// the same rule the import boundary keeps.
func TestCreateRefusesAnUnknownStage(t *testing.T) {
	e := newEnv(t)
	_, err := e.executeErr(e.wsA, "job_radar.opportunity.create", map[string]any{
		"company": "Safra", "role": "Backend Engineer", "stage": "recruiter",
	})
	if err == nil {
		t.Fatal("an unknown stage was accepted")
	}
}

// Two legitimate postings for one role at one employer must both exist. The
// tool adds no uniqueness the form does not have.
func TestCreateDoesNotDeduplicateOnCompanyAndRole(t *testing.T) {
	e := newEnv(t)
	first := e.execute(t, e.wsA, "job_radar.opportunity.create",
		map[string]any{"company": "Safra", "role": "Backend Engineer"})
	second := e.execute(t, e.wsA, "job_radar.opportunity.create",
		map[string]any{"company": "Safra", "role": "Backend Engineer"})

	if first["opportunity_id"] == second["opportunity_id"] {
		t.Fatal("the second create returned the first record")
	}
	list := e.execute(t, e.wsA, "job_radar.opportunity.list", map[string]any{})
	if n, _ := list["matching_total"].(float64); int(n) != 2 {
		t.Fatalf("matching_total = %v, want 2", list["matching_total"])
	}
}

// Create is always in the workspace the call arrived in. The model has no
// way to name another one, and that is structural: the argument does not
// exist in the schema.
func TestCreateLandsInTheCallersWorkspace(t *testing.T) {
	e := newEnv(t)
	tool, _ := e.registry.Lookup(jrtools.OpportunityCreateTool)
	for name := range tool.Definition().Schema.Properties {
		if strings.Contains(strings.ToLower(name), "workspace") {
			t.Fatalf("the schema exposes %q; a model must not be able to name a workspace", name)
		}
	}

	e.execute(t, e.wsA, "job_radar.opportunity.create",
		map[string]any{"company": "Safra", "role": "Backend Engineer"})

	other := e.execute(t, e.wsB, "job_radar.opportunity.list", map[string]any{})
	if n, _ := other["matching_total"].(float64); int(n) != 0 {
		t.Fatalf("workspace B sees %v records", other["matching_total"])
	}
}

/* ── delete ──────────────────────────────────────────────────────────── */

// Delete is the SAME soft delete the web UI performs: gone from the board,
// still on disk with its history.
func TestDeleteIsTheSameSoftDeleteTheUIPerforms(t *testing.T) {
	e := newEnv(t)
	created := e.execute(t, e.wsA, "job_radar.opportunity.create",
		map[string]any{"company": "Safra", "role": "Backend Engineer", "stage": "applied"})
	id, _ := created["opportunity_id"].(string)

	out := e.execute(t, e.wsA, "job_radar.opportunity.delete",
		map[string]any{"opportunity_id": id})
	if out["removed"] != true {
		t.Fatalf("removed = %v", out["removed"])
	}
	// The result names what went, so the model can acknowledge it in words
	// rather than echoing a uuid.
	if out["company"] != "Safra" || out["role"] != "Backend Engineer" {
		t.Errorf("the result does not name the record: %+v", out)
	}

	// Gone from every active read.
	list := e.execute(t, e.wsA, "job_radar.opportunity.list", map[string]any{})
	if n, _ := list["matching_total"].(float64); int(n) != 0 {
		t.Fatalf("a removed record still lists: %v", list["matching_total"])
	}
	if _, err := e.executeErr(e.wsA, "job_radar.opportunity.get",
		map[string]any{"opportunity_id": id}); err == nil {
		t.Error("a removed record is still readable through get")
	}

	// And still on disk, soft-deleted, with its history intact — which is
	// what makes this reversible by a person and is the semantics the
	// product already chose.
	var deletedAt *string
	var events int
	if err := e.pool.QueryRow(ctxFor(e.wsA), `
		SELECT to_char(deleted_at, 'YYYY-MM-DD'),
		       (SELECT count(*) FROM jobradar.stage_events WHERE opportunity_id = $1)
		FROM jobradar.opportunities WHERE id = $1`, id).Scan(&deletedAt, &events); err != nil {
		t.Fatalf("the row was hard-deleted: %v", err)
	}
	if deletedAt == nil {
		t.Error("deleted_at was not stamped")
	}
	if events == 0 {
		t.Error("the stage history was destroyed by a soft delete")
	}
}

// Another workspace's id is not deletable and teaches nothing: the same
// not-found a fabricated id gets.
func TestDeleteCannotReachAnotherWorkspace(t *testing.T) {
	e := newEnv(t)
	created := e.execute(t, e.wsA, "job_radar.opportunity.create",
		map[string]any{"company": "Safra", "role": "Backend Engineer"})
	id, _ := created["opportunity_id"].(string)

	foreign := e.mustFail(t, e.wsB, "job_radar.opportunity.delete",
		map[string]any{"opportunity_id": id})
	fabricated := e.mustFail(t, e.wsB, "job_radar.opportunity.delete",
		map[string]any{"opportunity_id": uuid.New().String()})

	// The two answers must be the same shape, or the difference becomes a
	// probe for rows in other workspaces.
	if !strings.Contains(foreign, "not found") || !strings.Contains(fabricated, "not found") {
		t.Fatalf("foreign=%q fabricated=%q, want both to read as not found", foreign, fabricated)
	}
	if strings.Contains(strings.ToLower(foreign), "safra") {
		t.Errorf("the refusal leaked the record: %q", foreign)
	}

	// And nothing happened to it.
	list := e.execute(t, e.wsA, "job_radar.opportunity.list", map[string]any{})
	if n, _ := list["matching_total"].(float64); int(n) != 1 {
		t.Fatalf("workspace A now has %v records", list["matching_total"])
	}
}

// mustFail runs a tool expecting a refusal and returns its message.
func (e *env) mustFail(t *testing.T, ws uuid.UUID, name string, args map[string]any) string {
	t.Helper()
	_, err := e.executeErr(ws, name, args)
	if err == nil {
		t.Fatalf("%s unexpectedly succeeded", name)
	}
	return err.Error()
}

/* ── removal is not rejection ────────────────────────────────────────── */

// The invariant with the sharpest edge, stated as a property of the data
// rather than of the model: a rejection MOVES, and the record survives.
//
// Getting this wrong is asymmetric. A rejection recorded as a deletion
// destroys the evidence that an application happened, and the pipeline
// stops being able to answer "how many did I apply to". The opposite
// mistake is merely untidy.
func TestARejectionMovesAndDoesNotRemove(t *testing.T) {
	e := newEnv(t)
	created := e.execute(t, e.wsA, "job_radar.opportunity.create",
		map[string]any{"company": "Safra", "role": "Backend Engineer", "stage": "interview"})
	id, _ := created["opportunity_id"].(string)

	e.execute(t, e.wsA, "job_radar.opportunity.move",
		map[string]any{"opportunity_id": id, "stage": "rejected"})

	got := e.execute(t, e.wsA, "job_radar.opportunity.get", map[string]any{"opportunity_id": id})
	o, _ := got["opportunity"].(map[string]any)
	if o["stage"] != "rejected" {
		t.Fatalf("stage = %v", o["stage"])
	}
	// Still on the board. That is the whole distinction.
	list := e.execute(t, e.wsA, "job_radar.opportunity.list", map[string]any{})
	if n, _ := list["matching_total"].(float64); int(n) != 1 {
		t.Fatalf("a rejected opportunity left the board: %v", list["matching_total"])
	}
}

// The description is what teaches the model the distinction, so the words
// that carry it are held here. A future edit that drops them would pass
// every other test in this file.
func TestTheDeleteDescriptionRefusesTheRejectionReading(t *testing.T) {
	e := newEnv(t)
	tool, _ := e.registry.Lookup(jrtools.OpportunityDeleteTool)
	d := strings.ToLower(tool.Definition().Description)

	for _, phrase := range []string{"rejection", "skip", "move"} {
		if !strings.Contains(d, phrase) {
			t.Errorf("the delete description no longer mentions %q; the model has "+
				"nothing telling it that a rejection is a move", phrase)
		}
	}
	create, _ := e.registry.Lookup(jrtools.OpportunityCreateTool)
	c := strings.ToLower(create.Definition().Description)
	if !strings.Contains(c, "never invent") && !strings.Contains(c, "do not invent") {
		t.Error("the create description no longer forbids inventing optional fields")
	}
}

/* ── audit ───────────────────────────────────────────────────────────── */

// Both appear in the existing audit trail, exactly as move does. No second
// log: the same table, read the same way.
func TestCreateAndDeleteAreAudited(t *testing.T) {
	e := newEnv(t)
	agentID, conv := e.newAgent(e.wsA, "operator")
	e.authorize(e.wsA, agentID,
		jrtools.OpportunityCreateTool.String(), jrtools.OpportunityDeleteTool.String())

	e.llm.scriptToolCall(jrtools.OpportunityCreateTool.String(),
		`{"company":"Safra","role":"Backend Engineer"}`)
	sink := e.turn(e.wsA, conv, "coloca essa vaga no radar")
	if ev, ok := sink.finished(jrtools.OpportunityCreateTool.String()); !ok || ev.Status != "ok" {
		t.Fatalf("create did not run: %+v", ev)
	}

	records, err := e.chatSvc.ConversationToolCalls(ctxFor(e.wsA), e.wsA, conv)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("audit has %d rows, want 1", len(records))
	}
	rec := records[0]
	if rec.ToolName != jrtools.OpportunityCreateTool {
		t.Errorf("tool_name = %q", rec.ToolName)
	}
	if rec.Arguments == nil || !strings.Contains(*rec.Arguments, "Safra") {
		t.Error("the arguments were not recorded")
	}
	if rec.Result == nil || !strings.Contains(*rec.Result, "opportunity_id") {
		t.Error("the result was not recorded")
	}
	if string(rec.Status) != "ok" {
		t.Errorf("status = %q", rec.Status)
	}
	if rec.Round != 1 {
		t.Errorf("round = %d, want 1", rec.Round)
	}
}

// An agent without the grant cannot create, and the refusal is told apart
// from "no such tool" so the model explains rather than guessing spellings.
func TestCreateIsRefusedWithoutTheGrant(t *testing.T) {
	e := newEnv(t)
	agentID, conv := e.newAgent(e.wsA, "ungranted")
	_ = agentID

	e.llm.scriptToolCall(jrtools.OpportunityCreateTool.String(),
		`{"company":"Safra","role":"Backend Engineer"}`)
	sink := e.turn(e.wsA, conv, "coloca essa vaga no radar")

	ev, ok := sink.finished(jrtools.OpportunityCreateTool.String())
	if !ok {
		t.Fatal("no terminal event")
	}
	if ev.ErrorCode != string(chatdomain.ToolErrNotAuthorized) {
		t.Fatalf("code = %q, want %s", ev.ErrorCode, chatdomain.ToolErrNotAuthorized)
	}
	list := e.execute(t, e.wsA, "job_radar.opportunity.list", map[string]any{})
	if n, _ := list["matching_total"].(float64); int(n) != 0 {
		t.Fatalf("an unauthorized create wrote %v records", list["matching_total"])
	}
}
