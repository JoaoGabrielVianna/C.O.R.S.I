// Package tools exposes Job Radar as capabilities an agent can be
// authorized to use.
//
// ── Why this package imports the Agents module's ports ─────────────────
// It implements `chat/ports.Tool`, an interface Agents DECLARES and this
// package SATISFIES. That is dependency inversion, and it is the exact
// arrangement the Agents module documents for a module-backed capability:
//
//	ports.Tool — "A tool backed by GitHub is implemented in Integrations; a
//	              tool backed by Finance is implemented by Finance exposing
//	              a capability. […] An interface owned by Agents and
//	              satisfied elsewhere, wired at the composition root, is
//	              exactly how a module receives a capability without
//	              learning who provides it."
//
// The only chat symbols reachable from here are the vocabulary of the port
// itself — a name, a schema, an output map, an error code. Nothing here can
// observe which agent is calling, which conversation it belongs to, or
// whether the caller is an agent at all; and nothing in Agents can name Job
// Radar. What would be a violation is this package calling an Agents
// service or reading a chat table. It does neither.
//
// ── Why it calls the application layer and not the repository ──────────
// Because the rules a write must pass are in `app`, not in SQL: the company
// is resolved there, the entity is validated there, the transition is
// performed by the domain there, and the stage event is written in the same
// transaction as the move. A tool that held a repository would be a second
// writer with its own idea of what a move is — which is the failure this
// sprint exists to avoid. There is no SQL in this file, and no way to
// reach any from it.
//
// ── The stage vocabulary is not repeated here ──────────────────────────
// `move` parses its argument with domain.ParseStage and describes itself
// with domain.StageNames(). Writing the six names into a schema string
// would be a seventh copy that a future stage would not update.
package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
	chatports "github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/jobradar/app"
	"github.com/corsi/backend/internal/jobradar/domain"
	"github.com/corsi/backend/internal/jobradar/ports"
	"github.com/corsi/backend/internal/platform/workspace"
)

// New builds every Job Radar tool, ready for chat.Deps.Tools.
//
// Returned as the port type rather than as concrete values: the
// composition root's only job is to hand them over, and a caller that could
// reach a concrete method could reach past the contract.
func New(svc *app.Service) []chatports.Tool {
	b := base{svc: svc}
	return []chatports.Tool{
		opportunityGet{b},
		opportunityList{b},
		opportunityCreate{b},
		opportunityMove{b},
		opportunityDelete{b},
	}
}

type base struct{ svc *app.Service }

/* ── the workspace ───────────────────────────────────────────────────── */

// workspaceOf reads the workspace this call belongs to.
//
// Same mechanism, and the same reasoning, as the GitHub tools: the port's
// Execute takes a context and a validated argument map and nothing else,
// and the platform already puts the workspace on the context of every
// request. The chain is unbroken from the middleware through the SSE
// handler and the turn loop into the executor.
//
// A missing workspace is a refusal, never a default. Guessing would mean
// reading — or moving — another workspace's pipeline, and there is no
// fallback value that is not somebody's real data.
func workspaceOf(ctx context.Context) (uuid.UUID, error) {
	ws, ok := workspace.FromContext(ctx)
	if !ok || ws == uuid.Nil {
		return uuid.Nil, chatdomain.ToolError(chatdomain.ToolErrExecutionFailed,
			"this call arrived without a workspace, so no Job Radar data could be resolved")
	}
	return ws, nil
}

/* ── error translation ───────────────────────────────────────────────── */

// toolError turns a domain error into one the model can act on.
//
// The mapping is the whole reason the domain has a Kind: a missing
// opportunity must reach the model as something it can respond to by
// listing and retrying, not as `internal_error`, which teaches it only that
// the tool is broken.
func toolError(err error) error {
	var de *domain.Error
	if !errAs(err, &de) {
		return chatdomain.ToolError(chatdomain.ToolErrExecutionFailed, err.Error())
	}
	switch de.Kind {
	case domain.KindInvalid, domain.KindNotFound, domain.KindConflict:
		// All three are facts about the request that the model can fix by
		// itself: correct the stage, look the id up again, or tell the user
		// what is contradictory. They go back as recoverable tool failures.
		//
		// KindNotFound is reported with the domain's message rather than a
		// bare code so the model reads "opportunity <id> was not found" and
		// calls list instead of inventing another id.
		return chatdomain.ToolError(chatdomain.ToolErrInvalidArguments, de.Message)
	default:
		return chatdomain.ToolError(chatdomain.ToolErrExecutionFailed, de.Message)
	}
}

func errAs(err error, target **domain.Error) bool {
	for err != nil {
		if de, ok := err.(*domain.Error); ok {
			*target = de
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

/* ── shared shaping ──────────────────────────────────────────────────── */

// stageDescription names the valid stages from the domain enum, so the
// model is told the same list the parser enforces.
func stageDescription(prefix string) string {
	return prefix + " One of: " + strings.Join(domain.StageNames(), ", ") + "."
}

// summary is one opportunity as a listing entry.
//
// ── Why these fields and not the record ────────────────────────────────
// This is what a model needs to RESOLVE a reference: enough to tell two
// Stripe roles apart and to carry the id into the next call. The
// description, the notes and the full stack are not here — they are the
// bulk of a record, they are useless for disambiguation, and thirty of them
// would spend a turn's context on text the model was not asked about.
// `get` returns those, once the model knows which record it wants.
func summary(o *domain.Opportunity) map[string]any {
	m := map[string]any{
		"id":       o.ID.String(),
		"company":  o.CompanyName,
		"role":     o.Role,
		"stage":    stageLabel(o),
		"location": o.Location,
	}
	if o.Tracking != nil {
		m["stage_entered_at"] = o.Tracking.StageEnteredAt.UTC().Format("2006-01-02")
		if o.Tracking.NextAction != "" {
			m["next_action"] = o.Tracking.NextAction
		}
	}
	return m
}

// stageLabel spells the Discover state as a word rather than as an absent
// field. A model reading `stage: null` has to guess what null means here;
// "discover" is the name the product uses on screen for exactly this state.
func stageLabel(o *domain.Opportunity) string {
	if s, tracked := o.Stage(); tracked {
		return string(s)
	}
	return "discover"
}

// detail is one opportunity in full, for `get`.
func detail(o *domain.Opportunity) map[string]any {
	m := map[string]any{
		"id":         o.ID.String(),
		"company":    o.CompanyName,
		"role":       o.Role,
		"stage":      stageLabel(o),
		"location":   o.Location,
		"salary":     o.Salary,
		"stack":      o.Stack,
		"source":     o.Source,
		"posted_at":  o.PostedAt.UTC().Format("2006-01-02"),
		"updated_at": o.UpdatedAt.UTC().Format(timeFormat),
	}
	if o.SourceURL != "" {
		m["source_url"] = o.SourceURL
	}
	if o.Description != "" {
		m["description"] = o.Description
	}
	if o.MatchPercent != nil {
		m["match_percent"] = *o.MatchPercent
	}
	if o.Tracking != nil {
		m["tracked_at"] = o.Tracking.TrackedAt.UTC().Format(timeFormat)
		m["stage_entered_at"] = o.Tracking.StageEnteredAt.UTC().Format(timeFormat)
		if o.Tracking.NextAction != "" {
			m["next_action"] = o.Tracking.NextAction
		}
	}
	// Notes are included only when they carry something. Four empty strings
	// on every read would be four fields of noise in the model's context.
	notes := map[string]any{}
	for k, v := range map[string]string{
		"general": o.Notes.General, "interview": o.Notes.Interview,
		"technical": o.Notes.Technical, "personal": o.Notes.Personal,
	} {
		if strings.TrimSpace(v) != "" {
			notes[k] = v
		}
	}
	if len(notes) > 0 {
		m["notes"] = notes
	}
	return m
}

const timeFormat = "2006-01-02T15:04:05Z"

// maxToolResultBytes bounds what these tools hand back.
//
// Below the Agents ceiling on purpose, and for the reason the GitHub tools
// give: Agents refuses an oversized result as an EXECUTION FAILURE, which
// the model reads as "the tool is broken" and answers by apologising. A
// tool that bounds itself returns a smaller honest answer instead.
const maxToolResultBytes = 24 << 10

// fit shrinks a listing until it fits, and says so when it had to.
//
// A truncated list that does not declare itself is indistinguishable from a
// complete one, and the model would answer "you have 12 opportunities" when
// there are eighty.
func fit(out map[string]any, listKey string) (chatdomain.ToolOutput, error) {
	if size(out) <= maxToolResultBytes {
		return out, nil
	}
	items, _ := out[listKey].([]map[string]any)
	if listKey == "" || items == nil {
		return nil, chatdomain.ToolError(chatdomain.ToolErrExecutionFailed,
			"the result was larger than one tool call may carry; ask for something narrower")
	}
	kept := len(items)
	for kept > 0 {
		kept /= 2
		out[listKey] = items[:kept]
		out["truncated"] = true
		out["truncated_note"] = "the full result did not fit in one tool call; only the " +
			"first entries are shown. Narrow the request with company, stage or search."
		if size(out) <= maxToolResultBytes {
			return out, nil
		}
	}
	return nil, chatdomain.ToolError(chatdomain.ToolErrExecutionFailed,
		"even a single result was larger than one tool call may carry")
}

func size(v any) int {
	raw, err := json.Marshal(v)
	if err != nil {
		return maxToolResultBytes + 1
	}
	return len(raw)
}

/* ── job_radar.opportunity.list ──────────────────────────────────────── */

type opportunityList struct{ base }

func (opportunityList) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   "job_radar.opportunity.list",
		Title:  "Job Radar · Listar oportunidades",
		Effect: chatdomain.EffectRead,
		Description: "Lists the job opportunities tracked in Job Radar, newest first, " +
			"with each one's company, role, stage and location. Call it first " +
			"when the user refers to an opportunity by name rather than by id " +
			"— it is how you find the id every other Job Radar tool needs. " +
			"Filter by company, stage or free text instead of listing " +
			"everything. If more than one result plausibly matches what the " +
			"user meant, ask them which one; do not guess.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"company": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Only opportunities whose company name contains this text.",
					MaxLength:   200,
				},
				"stage": {
					Type: chatdomain.TypeString,
					Description: stageDescription("Optional. Only opportunities at this pipeline stage.") +
						" Use 'discover' for opportunities that are not in the pipeline yet.",
					MaxLength: 20,
				},
				"search": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Matches the role, the company or the location.",
					MaxLength:   200,
				},
				"limit": {
					Type:        chatdomain.TypeInteger,
					Description: "How many to return, 1 to 50. Defaults to 25.",
				},
			},
		},
	}
}

// discoverArgument is the word a caller uses to ask for the untracked
// records. It is not a PipelineStage and must never become one — see the
// note on ports.OpportunityFilter.Untracked.
const discoverArgument = "discover"

func (t opportunityList) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}

	filter := ports.OpportunityFilter{
		Company: argString(args, "company"),
		Search:  argString(args, "search"),
		Limit:   argInt(args, "limit", 25),
	}
	if raw := strings.TrimSpace(strings.ToLower(argString(args, "stage"))); raw != "" {
		if raw == discoverArgument {
			filter.Untracked = true
		} else {
			stage, err := domain.ParseStage(raw)
			if err != nil {
				return nil, toolError(err)
			}
			filter.Stage = &stage
		}
	}

	items, total, err := t.svc.ListOpportunities(ctx, ws, filter)
	if err != nil {
		return nil, toolError(err)
	}

	rows := make([]map[string]any, 0, len(items))
	for _, o := range items {
		rows = append(rows, summary(o))
	}

	out := map[string]any{
		"opportunities": rows,
		// The unbounded count of what the same filter matches. Returned
		// even when it equals the number of rows, because a model that has
		// to infer completeness from a length will eventually infer wrong.
		"matching_total": total,
	}
	if len(rows) == 0 {
		out["note"] = "no opportunity matched. Try a broader filter, or call this " +
			"tool with no arguments to see everything in Job Radar."
	}
	return fit(out, "opportunities")
}

/* ── job_radar.opportunity.get ───────────────────────────────────────── */

type opportunityGet struct{ base }

// OpportunityGetTool is the capability that reads one opportunity.
//
// Named rather than typed inline because two things now depend on it being
// exactly this string: the tool's own Definition below, and the hydration
// policy in references.go, which uses it as the grant that authorizes
// reading an attached opportunity's present state. Two literals would be a
// rename that goes half-done and turns hydration permanently unauthorized
// without failing a build.
const OpportunityGetTool chatdomain.ToolName = "job_radar.opportunity.get"

func (opportunityGet) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   OpportunityGetTool,
		Title:  "Job Radar · Ver oportunidade",
		Effect: chatdomain.EffectRead,
		Description: "Reads one opportunity from Job Radar in full: its role, company, " +
			"stage, salary, location, stack, description and any notes. Use " +
			"job_radar.opportunity.list first to find the id.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"opportunity_id": opportunityIDProperty,
			},
			Required: []string{"opportunity_id"},
		},
	}
}

// opportunityIDProperty is the argument both id-taking tools share.
//
// ── Why the model is told where the id comes from ──────────────────────
// Because the alternative is a model that invents a plausible uuid. Saying
// it must come from a listing turns "I do not have one" into "call list
// first", which is a recoverable state, rather than into a fabricated
// argument that resolves to nothing — or, worse, to something.
var opportunityIDProperty = chatdomain.ToolProperty{
	Type: chatdomain.TypeString,
	Description: "The opportunity's id, exactly as returned by " +
		"job_radar.opportunity.list. Never invent one: if you do not have an " +
		"id, list first.",
	MaxLength: 36,
}

// parseID turns the model's string into a uuid.
//
// A malformed id is reported as an invalid argument rather than as a
// not-found, because they are different mistakes: the first means the model
// sent something that is not an id at all, and telling it "not found" would
// send it looking for a record instead of fixing its argument.
func parseID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return uuid.Nil, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
			"opportunity_id is not a valid id; use the id returned by job_radar.opportunity.list")
	}
	return id, nil
}

func (t opportunityGet) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID(argString(args, "opportunity_id"))
	if err != nil {
		return nil, err
	}
	o, err := t.svc.GetOpportunity(ctx, ws, id)
	if err != nil {
		return nil, toolError(err)
	}
	return fit(map[string]any{"opportunity": detail(o)}, "")
}

/* ── job_radar.opportunity.move ──────────────────────────────────────── */

type opportunityMove struct{ base }

func (opportunityMove) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:  "job_radar.opportunity.move",
		Title: "Job Radar · Mover oportunidade",
		// ── Why this is a write and is declared as one ──────────────────
		// It changes the user's real pipeline. Declaring it read to slip
		// past whatever an operator decides write tools deserve would be the
		// module lying about itself in the one field the interface uses to
		// warn a person before they authorize it.
		Effect: chatdomain.EffectWrite,
		Description: "Moves one opportunity to a different stage of the Job Radar " +
			"pipeline, and records the transition. Use it when the user says " +
			"something happened to an application — applied, got an interview, " +
			"was rejected. Find the id with job_radar.opportunity.list first. " +
			"If more than one opportunity could be the one they mean, ask them " +
			"which before moving anything: this changes real data, and moving " +
			"the wrong record is not something the user can see happening.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"opportunity_id": opportunityIDProperty,
				"stage": {
					Type:        chatdomain.TypeString,
					Description: stageDescription("The stage to move the opportunity to."),
					MaxLength:   20,
				},
			},
			Required: []string{"opportunity_id", "stage"},
		},
	}
}

func (t opportunityMove) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID(argString(args, "opportunity_id"))
	if err != nil {
		return nil, err
	}
	// Parsed by the domain, not by a list in this file.
	stage, err := domain.ParseStage(argString(args, "stage"))
	if err != nil {
		return nil, toolError(err)
	}

	result, err := t.svc.MoveOpportunity(ctx, ws, id, stage)
	if err != nil {
		return nil, toolError(err)
	}

	o := result.Opportunity
	out := map[string]any{
		"id":         o.ID.String(),
		"company":    o.CompanyName,
		"role":       o.Role,
		"stage":      string(stage),
		"updated_at": o.UpdatedAt.UTC().Format(timeFormat),
	}
	// previous_stage comes straight from the domain's MoveResult, which
	// knew both sides of the transition. Nothing was restructured to
	// produce it.
	if result.PreviousStage != nil {
		out["previous_stage"] = string(*result.PreviousStage)
	} else {
		// Entering the pipeline from Discover is a different event from a
		// move between stages, and saying so stops the model reporting a
		// transition that never had a starting point.
		out["previous_stage"] = discoverArgument
	}
	if result.Unchanged {
		out["unchanged"] = true
		out["note"] = "this opportunity was already at that stage; nothing was changed."
	}
	return out, nil
}

/* ── argument readers ────────────────────────────────────────────────── */

// The comma-ok form is kept even though the schema already validated the
// types: "safe because somewhere else checked" is exactly the assumption
// that stops being true after a refactor.
func argString(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

func argInt(args map[string]any, key string, fallback int) int {
	switch n := args[key].(type) {
	case int64:
		return int(n)
	case int:
		return n
	case float64:
		return int(n)
	default:
		return fallback
	}
}

/* ── job_radar.opportunity.create ────────────────────────────────────── */

// OpportunityCreateTool adds a record to the board.
const OpportunityCreateTool chatdomain.ToolName = "job_radar.opportunity.create"

type opportunityCreate struct{ base }

func (opportunityCreate) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:  OpportunityCreateTool,
		Title: "Job Radar · Criar oportunidade",
		// It puts a row in the user's real pipeline. Declaring it read to
		// slip past whatever an operator decides write tools deserve would
		// be the module lying about itself in the one field the interface
		// uses to warn a person before they authorize it.
		Effect: chatdomain.EffectWrite,
		// ── What this description has to get right ──────────────────────
		// Two opposite failures, and the wording is aimed at both. A model
		// that treats every mentioned job as a create fills the board with
		// things the user was only thinking about; a model that interrogates
		// for a URL and a stack before accepting "coloca o Safra no radar"
		// makes the feature slower than the form it replaces.
		//
		// The third failure is the quiet one: inventing a salary or a
		// location because the schema has a field for it. Saying so
		// explicitly is cheaper than any validation could be, because a
		// plausible invented value is indistinguishable from a real one
		// once it is stored.
		Description: "Adds a new opportunity to Job Radar. Use it when the user asks for " +
			"something to be put on their radar — \"coloca no meu radar\", \"adiciona " +
			"essa vaga\", \"cadastra aí\". Only the company and the role are required, " +
			"and everything else is optional: create with what the user actually told " +
			"you and leave the rest empty rather than filling a field with something " +
			"plausible. Never invent a salary, a location, a link or a stage — an " +
			"invented value is indistinguishable from a real one once it is stored, and " +
			"the user can add the rest later. Do NOT create just because a job came up " +
			"in conversation; wait for the user to ask. Before calling this a second " +
			"time in one conversation, check whether you already created that " +
			"opportunity — an acknowledgement like \"boa\" or \"perfeito\" is not a " +
			"second request.",
		Schema: chatdomain.ToolSchema{
			// ── Why these fields and not the whole record ───────────────
			// This is what a person says out loud when they ask for a job to
			// be tracked. Description, stack, match percent and the four
			// note channels are deliberately absent: they are things the
			// user writes later, in the form, looking at the record — and
			// every property here is one more field a model can be tempted
			// to fill in from nothing.
			Properties: map[string]chatdomain.ToolProperty{
				"company": {
					Type:        chatdomain.TypeString,
					Description: "The employer's name, as the user said it. Required.",
					MaxLength:   200,
				},
				"role": {
					Type:        chatdomain.TypeString,
					Description: "The job title. Required.",
					MaxLength:   200,
				},
				"stage": {
					Type: chatdomain.TypeString,
					Description: stageDescription("Optional. Only when the user said where in the "+
						"process this already is — \"já apliquei\", \"estou em entrevista\".") +
						" Leave it out for a job they have merely found: it then lands in " +
						"'discover', which is where a newly discovered posting belongs.",
					MaxLength: 20,
				},
				"location": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Where the role is, e.g. \"Remote (BR)\", \"Lisboa\".",
					MaxLength:   200,
				},
				"salary": {
					Type: chatdomain.TypeString,
					Description: "Optional. Free text, exactly as the user stated it: " +
						"\"R$ 11k\", \"USD 120-150k\". Do not convert, normalise or estimate.",
					MaxLength: 120,
				},
				"source_url": {
					Type:        chatdomain.TypeString,
					Description: "Optional. The link to the posting, when the user gave one.",
					MaxLength:   1000,
				},
				"next_action": {
					Type: chatdomain.TypeString,
					Description: "Optional. What the user said they still have to do, " +
						"e.g. \"mandar o CV até sexta\".",
					MaxLength: 500,
				},
			},
			Required: []string{"company", "role"},
		},
	}
}

func (t opportunityCreate) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}

	in := app.CreateInput{
		CompanyName: argString(args, "company"),
		Role:        argString(args, "role"),
		Location:    argString(args, "location"),
		Salary:      argString(args, "salary"),
		SourceURL:   argString(args, "source_url"),
		NextAction:  argString(args, "next_action"),
		// Source is left empty on purpose, so the application layer applies
		// the same default it applies to the form: "manual". A record typed
		// by a person and one dictated to an agent are the same kind of
		// record — both are the user telling the system about a job — and
		// giving the agent path its own label would be a distinction the
		// board would then have to explain.
	}
	// Absent and "discover" are the same input and both mean Discover, which
	// is the canonical default the HTTP handler already applies. The tool
	// does not get its own opinion about where a new record starts.
	if raw := strings.TrimSpace(strings.ToLower(argString(args, "stage"))); raw != "" && raw != discoverArgument {
		stage, err := domain.ParseStage(raw)
		if err != nil {
			return nil, toolError(err)
		}
		in.Stage = &stage
	}

	o, err := t.svc.CreateOpportunity(ctx, ws, in)
	if err != nil {
		return nil, toolError(err)
	}

	// The id first, because the next thing this model does is very likely
	// `move` or `get` on the record it just made, and it should not have to
	// call `list` to find out what it created.
	return map[string]any{
		"opportunity_id": o.ID.String(),
		"company":        o.CompanyName,
		"role":           o.Role,
		"stage":          stageLabel(o),
		"created":        true,
	}, nil
}

/* ── job_radar.opportunity.delete ────────────────────────────────────── */

// OpportunityDeleteTool removes a record from the board.
const OpportunityDeleteTool chatdomain.ToolName = "job_radar.opportunity.delete"

type opportunityDelete struct{ base }

func (opportunityDelete) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   OpportunityDeleteTool,
		Title:  "Job Radar · Remover oportunidade",
		Effect: chatdomain.EffectWrite,
		// ── The distinction this description exists to hold ─────────────
		// Removal and rejection are different events and a model will
		// happily conflate them, because in ordinary language "essa vaga já
		// era" covers both. Getting it wrong is asymmetric: a rejection
		// recorded as a deletion destroys the record of an application that
		// really happened, and the pipeline stops being able to answer "how
		// many did I apply to". The opposite mistake is merely untidy.
		//
		// So the description names the wrong readings explicitly rather than
		// describing the right one and hoping. A negative evaluation, a
		// decision to skip, a low tier and a rejection are the four things
		// that sound like removal and are not.
		Description: "Removes an opportunity from Job Radar. Use it ONLY when the user " +
			"explicitly asks for the record itself to be removed — \"apaga essa vaga\", " +
			"\"remove do radar\", \"cadastrei errado, pode excluir\". " +
			"This is NOT how a rejection is recorded: a rejected application stays on " +
			"the board at the 'rejected' stage, and job_radar.opportunity.move is what " +
			"records it. It is also not for a job the user decided to skip, rated " +
			"poorly, deprioritised or lost interest in — all of those stay on the " +
			"board. If more than one opportunity could be the one they mean, ask which " +
			"one before removing anything: this changes real data, and removing the " +
			"wrong record is not something the user can see happening.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"opportunity_id": opportunityIDProperty,
			},
			Required: []string{"opportunity_id"},
		},
	}
}

func (t opportunityDelete) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID(argString(args, "opportunity_id"))
	if err != nil {
		return nil, err
	}

	// Read before removing, for two reasons. The result can then name what
	// was removed, so the model acknowledges "removi a vaga do Safra"
	// instead of echoing a uuid at the user. And an id belonging to another
	// workspace fails here exactly as it fails in the delete itself — same
	// workspace-scoped query, same not-found — so nothing is learned from
	// the difference.
	o, err := t.svc.GetOpportunity(ctx, ws, id)
	if err != nil {
		return nil, toolError(err)
	}

	// The same application operation the web UI's DELETE route calls. It is
	// a SOFT delete: the row keeps its history and stops appearing on the
	// board. There is no second removal semantics for agents.
	if err := t.svc.DeleteOpportunity(ctx, ws, id); err != nil {
		return nil, toolError(err)
	}

	return map[string]any{
		"opportunity_id": o.ID.String(),
		"company":        o.CompanyName,
		"role":           o.Role,
		"removed":        true,
	}, nil
}
