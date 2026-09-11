//go:build integration

// Integration tests for Memory Policy and for who composed a memory
// (v1.1.0), against a real Postgres.
//
// The harness — newEnv, seed, do, decode — lives in
// chat_integration_test.go. This file adds the two invariants the batch
// exists to establish:
//
//	the policy is configuration of the agent, validated by the backend,
//	and omitting it from a PATCH never resets it
//
//	provenance cannot be manufactured by a request: the public route
//	always writes model_proposed = false, and the only path that writes
//	true is an application entry point with no transport
//
// Nothing here consumes the policy. The operation it governs is the next
// batch's; what is being fixed now is the contract that operation obeys.
package chat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/app"
	"github.com/corsi/backend/internal/platform/postgres"
)

/* ── fixtures ────────────────────────────────────────────────────────── */

type apiMemoryPolicy struct {
	Mode  string `json:"mode"`
	Notes string `json:"notes"`
}

// agentPolicy reads one agent's policy back through the route that returns
// the agent, which is the only read there is: the policy is part of the
// agent and deliberately has no endpoint of its own.
func (e *env) agentPolicy(ws uuid.UUID, agentID string) apiMemoryPolicy {
	e.t.Helper()
	rec := e.do("GET", "/chat/agents/"+agentID, ws, nil)
	wantStatus(e.t, rec, http.StatusOK)
	return decode[struct {
		MemoryPolicy apiMemoryPolicy `json:"memory_policy"`
	}](e.t, rec).MemoryPolicy
}

func (e *env) patchAgent(ws uuid.UUID, agentID string, body map[string]any) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.do("PATCH", "/chat/agents/"+agentID, ws, body)
}

/* ── the policy ──────────────────────────────────────────────────────── */

func TestMemoryPolicyDefaultsToOnRequest(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	// An agent created without mentioning memory at all. The default is a
	// permission, not an activity: it costs nothing until someone asks.
	got := e.agentPolicy(e.wsA, s.agentID)
	if got.Mode != "on_request" {
		t.Fatalf("mode = %q, want on_request", got.Mode)
	}
	if got.Notes != "" {
		t.Fatalf("notes = %q, want empty", got.Notes)
	}
}

func TestMemoryPolicyIsCreatedAsAsked(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	rec := e.do("POST", "/chat/agents", e.wsA, map[string]any{
		"provider_id": s.providerID,
		"name":        "policy at birth",
		"memory_policy": map[string]any{
			"mode":  "off",
			"notes": "  guarde decisões de arquitetura  ",
		},
	})
	wantStatus(t, rec, http.StatusCreated)
	created := decode[struct {
		ID           string          `json:"id"`
		MemoryPolicy apiMemoryPolicy `json:"memory_policy"`
	}](t, rec)

	if created.MemoryPolicy.Mode != "off" {
		t.Fatalf("mode = %q, want off", created.MemoryPolicy.Mode)
	}
	// Trimmed on the way in: guidance made of whitespace is no guidance,
	// and these characters are destined for a prompt.
	if created.MemoryPolicy.Notes != "guarde decisões de arquitetura" {
		t.Fatalf("notes = %q, want them trimmed", created.MemoryPolicy.Notes)
	}
	if got := e.agentPolicy(e.wsA, created.ID); got != created.MemoryPolicy {
		t.Fatalf("read back %+v, want %+v", got, created.MemoryPolicy)
	}
}

func TestMemoryPolicyModesPersist(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	for _, mode := range []string{"off", "on_request"} {
		rec := e.patchAgent(e.wsA, s.agentID, map[string]any{
			"memory_policy": map[string]any{"mode": mode},
		})
		wantStatus(t, rec, http.StatusOK)
		if got := e.agentPolicy(e.wsA, s.agentID); got.Mode != mode {
			t.Fatalf("mode = %q after setting %q", got.Mode, mode)
		}
	}
}

func TestMemoryPolicyRefusesWhatItDoesNotImplement(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	// `automatic` is the mode a future version might add. Accepting it now
	// would store configuration describing behaviour that does not exist,
	// and the agent would claim a capability nothing implements.
	for _, mode := range []string{"automatic", "on-request", "OFF", "", "always"} {
		rec := e.patchAgent(e.wsA, s.agentID, map[string]any{
			"memory_policy": map[string]any{"mode": mode},
		})
		wantErrorCode(t, rec, http.StatusBadRequest, "invalid")
	}
	if got := e.agentPolicy(e.wsA, s.agentID); got.Mode != "on_request" {
		t.Fatalf("a refused write changed the stored mode to %q", got.Mode)
	}
}

func TestMemoryPolicyNotesAreBounded(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	// The ceiling is in runes, not bytes: these characters end up in a
	// prompt, and Portuguese with accents would otherwise be refused
	// hundreds of characters early.
	ok := strings.Repeat("á", 1000)
	rec := e.patchAgent(e.wsA, s.agentID, map[string]any{
		"memory_policy": map[string]any{"mode": "on_request", "notes": ok},
	})
	wantStatus(t, rec, http.StatusOK)

	rec = e.patchAgent(e.wsA, s.agentID, map[string]any{
		"memory_policy": map[string]any{"mode": "on_request", "notes": strings.Repeat("á", 1001)},
	})
	wantErrorCode(t, rec, http.StatusBadRequest, "invalid")

	if got := e.agentPolicy(e.wsA, s.agentID); len([]rune(got.Notes)) != 1000 {
		t.Fatalf("stored notes are %d runes, want the 1000 that were accepted", len([]rune(got.Notes)))
	}
}

// TestMemoryPolicySurvivesAPatchThatDoesNotMentionIt is the rule every
// other field in this route already follows: omission is not a reset.
//
// It matters more here than elsewhere. The settings page saves the form and
// the policy card separately, so every save of the instructions is a PATCH
// that does not mention memory — and if that cleared the policy, a user
// would lose their configuration by editing something unrelated.
func TestMemoryPolicySurvivesAPatchThatDoesNotMentionIt(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	rec := e.patchAgent(e.wsA, s.agentID, map[string]any{
		"memory_policy": map[string]any{"mode": "off", "notes": "não guarde nada de cliente"},
	})
	wantStatus(t, rec, http.StatusOK)

	// A save of the form above the card: name, prompt, knobs. No memory.
	rec = e.patchAgent(e.wsA, s.agentID, map[string]any{
		"name":          "renamed",
		"system_prompt": "você é direto",
		"temperature":   0.2,
	})
	wantStatus(t, rec, http.StatusOK)

	got := e.agentPolicy(e.wsA, s.agentID)
	if got.Mode != "off" || got.Notes != "não guarde nada de cliente" {
		t.Fatalf("an unrelated patch changed the policy to %+v", got)
	}
}

func TestMemoryPolicyIsWorkspaceScoped(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	rec := e.patchAgent(e.wsB, s.agentID, map[string]any{
		"memory_policy": map[string]any{"mode": "off"},
	})
	wantStatus(t, rec, http.StatusNotFound)

	rec = e.do("GET", "/chat/agents/"+s.agentID, e.wsB, nil)
	wantStatus(t, rec, http.StatusNotFound)

	if got := e.agentPolicy(e.wsA, s.agentID); got.Mode != "on_request" {
		t.Fatalf("wsB changed wsA's policy to %q", got.Mode)
	}
}

/* ── who composed the memory ─────────────────────────────────────────── */

func TestManualCaptureIsNeverModelProposed(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	fromPage := e.remember(e.wsA, s.agentID, "prefere respostas diretas")
	fromThread := e.rememberFrom(e.wsA, s.agentID, s.conversationID, "veio da conversa")

	for _, m := range []apiMemory{fromPage, fromThread} {
		if m.ModelProposed {
			t.Fatalf("a memory the user wrote came back as model-proposed: %+v", m)
		}
	}
}

// TestARequestCannotManufactureProvenance is the guarantee the column is
// worth having.
//
// "The model suggested this" is a claim about what happened, and a claim a
// client could set is not evidence of anything. The field exists on no
// request struct in this module, and the decoder refuses unknown fields —
// so a client asking for it is told it does not exist rather than being
// quietly ignored, which is the stronger of the two answers: silence would
// let a caller believe it had worked.
func TestARequestCannotManufactureProvenance(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	rec := e.do("POST", "/chat/agents/"+s.agentID+"/memories", e.wsA, map[string]any{
		"content":                "eu digitei isto",
		"source_conversation_id": s.conversationID,
		"model_proposed":         true,
	})
	wantErrorCode(t, rec, http.StatusBadRequest, "invalid")

	// And through the update route, which cannot rewrite provenance either:
	// editing the words of a memory does not change who first wrote them.
	created := e.remember(e.wsA, s.agentID, "outra")
	rec = e.do("PATCH", "/chat/agents/"+s.agentID+"/memories/"+created.ID, e.wsA, map[string]any{
		"content":        "editada",
		"model_proposed": true,
	})
	wantErrorCode(t, rec, http.StatusBadRequest, "invalid")

	// The refusal was of the field, not of the write: the same content
	// without it is accepted, and lands as the user's own words.
	rec = e.do("POST", "/chat/agents/"+s.agentID+"/memories", e.wsA, map[string]any{
		"content":                "eu digitei isto",
		"source_conversation_id": s.conversationID,
	})
	wantStatus(t, rec, http.StatusCreated)
	if decode[apiMemory](t, rec).ModelProposed {
		t.Fatal("a memory the user wrote came back as model-proposed")
	}
	if got := e.memories(e.wsA, s.agentID); got.Total != 2 {
		t.Fatalf("%d memories exist, want the 2 that were actually accepted", got.Total)
	}
}

// TestTheProposedPathMarksProvenanceAndHasNoRoute exercises the seam Batch
// 2 will call, and asserts the other half of the same fact: it is reachable
// from the application and from nowhere else.
func TestTheProposedPathMarksProvenanceAndHasNoRoute(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	seq := int64(2)
	m, err := e.svc.CreateProposedMemory(context.Background(), app.CreateProposedMemoryInput{
		WorkspaceID:          e.wsA,
		AgentID:              uuid.MustParse(s.agentID),
		Content:              "o modelo sugeriu isto e o usuário confirmou",
		SourceConversationID: uuid.MustParse(s.conversationID),
		SourceMessageSeq:     &seq,
	})
	if err != nil {
		t.Fatalf("create proposed memory: %v", err)
	}
	if !m.ModelProposed {
		t.Fatal("the proposed path did not mark provenance")
	}
	// A proposal is always captured in the thread it was read from, so the
	// origin follows from the act rather than being chosen separately.
	if m.Origin != "conversation" || m.SourceConversationID == nil {
		t.Fatalf("provenance is incomplete: %+v", m)
	}

	// It reads back the same way through the ordinary list, so the Memory
	// page and the Brain see one collection, not two.
	page := e.memories(e.wsA, s.agentID)
	found := false
	for _, item := range page.Items {
		if item.ID == m.ID.String() {
			found = true
			if !item.ModelProposed {
				t.Fatal("the list dropped the provenance flag")
			}
		}
	}
	if !found {
		t.Fatal("a proposed memory is missing from the agent's list")
	}
}

func TestTheProposedPathIsStillWorkspaceAndAgentScoped(t *testing.T) {
	e := newEnv(t)
	a := e.seed(e.wsA)
	b := e.seed(e.wsB)

	// The agent belongs to wsA; asking as wsB resolves nothing. The
	// internal entry point gets no shortcut past the same lookup the route
	// would have made.
	if _, err := e.svc.CreateProposedMemory(context.Background(), app.CreateProposedMemoryInput{
		WorkspaceID:          e.wsB,
		AgentID:              uuid.MustParse(a.agentID),
		Content:              "cross workspace",
		SourceConversationID: uuid.MustParse(a.conversationID),
	}); err == nil {
		t.Fatal("a proposed memory crossed workspaces")
	}

	// A conversation of another agent is refused for the same reason the
	// public path refuses it: provenance that points somewhere else is a
	// lie about where the memory came from.
	if _, err := e.svc.CreateProposedMemory(context.Background(), app.CreateProposedMemoryInput{
		WorkspaceID:          e.wsB,
		AgentID:              uuid.MustParse(b.agentID),
		Content:              "cross agent",
		SourceConversationID: uuid.MustParse(a.conversationID),
	}); err == nil {
		t.Fatal("a proposed memory named another agent's conversation")
	}
}

// TestMemoryBehaviourIsUnchangedByProvenance guards the rest of the
// capability against this batch: the flag is additive, and everything the
// v1.0.0 Memory did it still does.
func TestMemoryBehaviourIsUnchangedByProvenance(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	m := e.rememberFrom(e.wsA, s.agentID, s.conversationID, "fato antigo")
	if m.Origin != "conversation" || !m.Enabled || m.Pinned {
		t.Fatalf("a memory captured the old way changed shape: %+v", m)
	}

	// `in_context` is computed by the list read, over the whole set and the
	// budget — never by the write, which has no set to weigh one against.
	page := e.memories(e.wsA, s.agentID)
	if page.Total != 1 || page.BudgetCharacters != app.MemoryBudgetChars {
		t.Fatalf("the page changed: %+v", page)
	}
	if !page.Items[0].InContext {
		t.Fatalf("a lone short memory stopped reaching the model: %+v", page.Items[0])
	}
}

/* ── rows written before these migrations ────────────────────────────── */

// TestRowsPredatingTheV11Migrations is the only way to exercise what 0013,
// 0014 and 0015 claim about a populated database.
//
// Column defaults do not prove it. Every writer in this module sets the new
// columns explicitly, so a new row would look right even if the migration
// were wrong — the rows that can tell the two apart are the ones that
// already existed. So the timeline is rolled back to 0012, rows are written
// exactly as the module wrote them then, and the schema is migrated
// forward.
//
// What must be true afterwards, and it is one sentence: nothing that was
// already there changed meaning.
func TestRowsPredatingTheV11Migrations(t *testing.T) {
	d := dsn(t)
	freshDB(t, d)
	ctx := context.Background()

	m, err := migrate.New("file://../../migrations/chat", chatMigrateDSN(t, d))
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	defer func() { _, _ = m.Close() }()
	// 0012 is the last version before memory policy, receipts and
	// provenance existed.
	if err := m.Migrate(12); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate to 12: %v", err)
	}

	pool, err := postgres.Open(ctx, postgres.Config{
		DSN: d, MaxConns: 2, MinConns: 1,
		MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}

	// The chain by hand: the module's own writers do not exist at this
	// schema version.
	ws := uuid.New()
	var providerID, agentID, conversationID, memoryID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO chat.providers (workspace_id, name, base_url, api_key_cipher, default_model)
		 VALUES ($1, 'legado', 'https://gateway.invalid/v1', '\x00'::bytea, 'test-model') RETURNING id`,
		ws).Scan(&providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO chat.agents (workspace_id, provider_id, name, model)
		 VALUES ($1, $2, 'agente legado', 'test-model') RETURNING id`,
		ws, providerID).Scan(&agentID); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO chat.conversations (workspace_id, agent_id) VALUES ($1, $2) RETURNING id`,
		ws, agentID).Scan(&conversationID); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO chat.agent_memories (workspace_id, agent_id, content, origin)
		 VALUES ($1, $2, 'memória escrita por uma pessoa', 'manual') RETURNING id`,
		ws, agentID).Scan(&memoryID); err != nil {
		t.Fatalf("seed memory: %v", err)
	}
	for _, r := range []struct{ role, content string }{
		{"user", "pergunta antiga"},
		{"assistant", "resposta antiga"},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO chat.messages
			 (workspace_id, conversation_id, role, content, model, prompt_tokens, completion_tokens)
			 VALUES ($1, $2, $3, $4, 'test-model', 120, 40)`,
			ws, conversationID, r.role, r.content); err != nil {
			t.Fatalf("seed %s row: %v", r.role, err)
		}
	}
	pool.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate forward: %v", err)
	}

	e := newEnvOn(t, d)

	// An agent that predates the policy is governed by the documented
	// default, not by an empty string nothing validates.
	if got := e.agentPolicy(ws, agentID.String()); got.Mode != "on_request" || got.Notes != "" {
		t.Fatalf("a pre-existing agent came out of the migration with %+v", got)
	}

	// A memory that predates provenance was written by a person. That is
	// not a guess: nothing could have proposed it, because nothing that
	// proposes existed.
	page := e.memories(ws, agentID.String())
	if len(page.Items) != 1 {
		t.Fatalf("the migration lost a memory: %+v", page)
	}
	if page.Items[0].ModelProposed {
		t.Fatal("the migration decided an old memory had been proposed by a model")
	}
	if page.Items[0].Content != "memória escrita por uma pessoa" || page.Items[0].Origin != "manual" {
		t.Fatalf("an old memory changed: %+v", page.Items[0])
	}

	// Messages that predate the split are turns: they are still in the
	// transcript, and they still count as spend.
	msgs := e.messages(ws, conversationID.String())
	if len(msgs) != 2 {
		t.Fatalf("the transcript lost pre-existing turns: %d remain", len(msgs))
	}
	if usage := e.usage(ws, "/chat/agents/"+agentID.String()+"/usage"); usage.TotalPromptTokens != 120 {
		t.Fatalf("pre-existing spend changed: %d prompt tokens", usage.TotalPromptTokens)
	}
}
