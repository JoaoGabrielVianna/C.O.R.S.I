// Package tools exposes Threads as capabilities an agent can be authorized
// to use.
//
// ── Why this package imports the Agents module's ports ─────────────────
// It implements `chat/ports.Tool`, an interface Agents DECLARES and this
// package SATISFIES. That is dependency inversion, and it is the same
// arrangement GitHub and Job Radar already use:
//
//	ports.Tool — "An interface owned by Agents and satisfied elsewhere,
//	              wired at the composition root, is exactly how a module
//	              receives a capability without learning who provides it."
//
// The only chat symbols reachable from here are the vocabulary of the port
// itself — a name, a schema, an output map, an error code. Nothing here can
// observe which agent is calling, which conversation it belongs to, or
// whether the caller is an agent at all; and nothing in Agents can name
// Threads. There is no `if agent.name == "Content"` in this repository and
// there must never be one: capability comes from a grant, and a grant is a
// row an operator can see and revoke.
//
// ── Why it calls the application layer and not the repository ──────────
// Because the rules a write must pass are in `app`, not in SQL: the entity
// is loaded there, the domain applies the change there, validation happens
// there, and "this edit moved nothing" is decided there. A tool that held a
// repository would be a second writer with its own idea of what an update
// is. There is no SQL in this file, and no way to reach any from it.
//
// ── The status vocabulary is not repeated here ─────────────────────────
// Every tool that takes one parses with domain.ParseStatus and describes
// itself with domain.StatusNames(). Writing the five names into a schema
// string would be a copy that a sixth status would not update.
//
// ── Presentation is not decided here ───────────────────────────────────
// Every output below is a flat map of semantic fields: an id, a title, a
// status word, a timestamp. There is no markdown, no card shape, no colour
// and no component name. Web draws a card from this, a text channel draws a
// line, and neither is in the contract — which is what lets Threads be
// operated from somewhere React does not run.
package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
	chatports "github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/platform/workspace"
	"github.com/corsi/backend/internal/threads/app"
	"github.com/corsi/backend/internal/threads/domain"
	"github.com/corsi/backend/internal/threads/ports"
)

// New builds every Threads tool, ready for chat.Deps.Tools.
//
// Returned as the port type rather than as concrete values: the composition
// root's only job is to hand them over, and a caller that could reach a
// concrete method could reach past the contract.
func New(svc *app.Service) []chatports.Tool {
	b := base{svc: svc}
	return []chatports.Tool{
		threadList{b},
		threadGet{b},
		threadCreate{b},
		threadUpdate{b},
		threadDelete{b},
	}
}

type base struct{ svc *app.Service }

/* ── the tool names ──────────────────────────────────────────────────── */

// The five capabilities, named once.
//
// Exported and constant because more than one thing depends on each string
// being exactly this: the tool's own Definition, the hydration policy in
// references.go, and the tests. Two literals would be a rename that goes
// half-done — and a half-done rename of ThreadGetTool turns hydration
// permanently unauthorized without failing a build.
const (
	ThreadListTool   chatdomain.ToolName = "threads.thread.list"
	ThreadGetTool    chatdomain.ToolName = "threads.thread.get"
	ThreadCreateTool chatdomain.ToolName = "threads.thread.create"
	ThreadUpdateTool chatdomain.ToolName = "threads.thread.update"
	ThreadDeleteTool chatdomain.ToolName = "threads.thread.delete"
)

/* ── the workspace ───────────────────────────────────────────────────── */

// workspaceOf reads the workspace this call belongs to.
//
// Same mechanism, and the same reasoning, as the GitHub and Job Radar
// tools: the port's Execute takes a context and a validated argument map
// and nothing else, and the platform already puts the workspace on the
// context of every request. The chain is unbroken from the middleware
// through the SSE handler and the turn loop into the executor.
//
// The MODEL never supplies a workspace and there is no argument through
// which it could. A missing one is a refusal, never a default: guessing
// would mean reading — or rewriting — another workspace's content, and
// there is no fallback value that is not somebody's real work.
func workspaceOf(ctx context.Context) (uuid.UUID, error) {
	ws, ok := workspace.FromContext(ctx)
	if !ok || ws == uuid.Nil {
		return uuid.Nil, chatdomain.ToolError(chatdomain.ToolErrExecutionFailed,
			"this call arrived without a workspace, so no Threads data could be resolved")
	}
	return ws, nil
}

/* ── error translation ───────────────────────────────────────────────── */

// toolError turns a domain error into one the model can act on.
//
// The mapping is the whole reason the domain has a Kind: a missing thread
// must reach the model as something it can respond to by listing and
// retrying, not as `internal_error`, which teaches it only that the tool is
// broken.
func toolError(err error) error {
	var de *domain.Error
	if !errAs(err, &de) {
		return chatdomain.ToolError(chatdomain.ToolErrExecutionFailed, err.Error())
	}
	switch de.Kind {
	case domain.KindInvalid, domain.KindNotFound:
		// Both are facts about the request the model can fix by itself:
		// correct the status, or look the id up again. They go back as
		// recoverable failures, carrying the domain's own message so the
		// model reads "thread <id> was not found" and calls list instead of
		// inventing another id.
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

const timeFormat = "2006-01-02T15:04:05Z"

// statusDescription names the valid statuses from the domain enum, so the
// model is told the same list the parser enforces.
func statusDescription(prefix string) string {
	return prefix + " One of: " + strings.Join(domain.StatusNames(), ", ") + "."
}

// excerptRunes bounds the preview a listing carries per row.
//
// ── Why a listing carries any content at all ───────────────────────────
// Because disambiguation is the job. Three threads called "Microservices
// cedo demais", "Microservices e Kubernetes" and "Microservices em
// startups" are told apart by their titles; three drafts of the SAME piece
// are not, and the model resolving "pega o de microservices" has to be able
// to see the difference before it asks the user about it.
//
// ── Why it is 160 and not the whole thing ──────────────────────────────
// Every character here is a prompt token on the next provider call, times
// the number of rows. 160 is about a sentence — enough to recognise a
// piece, far too little to work from — and a model that needs the text is
// one call away from `get`, which returns all of it.
const excerptRunes = 160

// summary is one thread as a listing entry.
func summary(t *domain.Thread) map[string]any {
	m := map[string]any{
		"id":     t.ID.String(),
		"title":  t.Title,
		"status": string(t.Status),
		// The listing is ordered by this, and it is what the user means by
		// "aquele que eu mexi ontem".
		"updated_at": t.UpdatedAt.UTC().Format(timeFormat),
	}
	if ex, truncated := excerpt(t.Content); ex != "" {
		m["excerpt"] = ex
		// Said explicitly, because an excerpt that does not declare itself
		// is indistinguishable from a very short thread — and a model that
		// mistook one for the other would rewrite a draft from its first
		// sentence.
		m["excerpt_truncated"] = truncated
	}
	return m
}

// excerpt returns the opening of a thread's content, collapsed to one line,
// and whether it had to cut.
func excerpt(content string) (string, bool) {
	flat := strings.Join(strings.Fields(content), " ")
	if flat == "" {
		return "", false
	}
	runes := []rune(flat)
	if len(runes) <= excerptRunes {
		return flat, false
	}
	return strings.TrimSpace(string(runes[:excerptRunes])) + "…", true
}

// detail is one thread in full, for `get`.
//
// This is the whole record, and unlike a listing entry that is the right
// answer: `get` is called when the model has decided WHICH thread it is
// working on, and the next thing it does is rewrite the text. Handing back
// an abbreviated version would produce an edit that silently truncates the
// user's work.
func detail(t *domain.Thread) map[string]any {
	return map[string]any{
		"id":         t.ID.String(),
		"title":      t.Title,
		"status":     string(t.Status),
		"content":    t.Content,
		"created_at": t.CreatedAt.UTC().Format(timeFormat),
		"updated_at": t.UpdatedAt.UTC().Format(timeFormat),
	}
}

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
// complete one, and the model would answer "you have six drafts" when there
// are forty.
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
			"first entries are shown. Narrow the request with status or search."
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

/* ── the shared id argument ──────────────────────────────────────────── */

// threadIDProperty is the argument the three id-taking tools share.
//
// ── Why the model is told where the id comes from ──────────────────────
// Because the alternative is a model that invents a plausible uuid. Saying
// it must come from a listing turns "I do not have one" into "call list
// first", which is a recoverable state, rather than into a fabricated
// argument that resolves to nothing — or, worse, to something.
var threadIDProperty = chatdomain.ToolProperty{
	Type: chatdomain.TypeString,
	Description: "The thread's id, exactly as returned by threads.thread.list " +
		"or threads.thread.create. Never invent one: if you do not have an id, list first.",
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
			"thread_id is not a valid id; use the id returned by threads.thread.list")
	}
	return id, nil
}

/* ── argument readers ────────────────────────────────────────────────── */

// The comma-ok form is kept even though the schema already validated the
// types: "safe because somewhere else checked" is exactly the assumption
// that stops being true after a refactor.
func argString(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

// argStringPtr distinguishes an argument that was SENT from one that was
// not, which is the whole grammar of `update`. It returns nil for absent
// and a pointer for present — including present-and-empty, which the update
// tool then judges per field.
func argStringPtr(args map[string]any, key string) *string {
	raw, present := args[key]
	if !present {
		return nil
	}
	v, ok := raw.(string)
	if !ok {
		return nil
	}
	return &v
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

/* ── threads.thread.list ─────────────────────────────────────────────── */

type threadList struct{ base }

func (threadList) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   ThreadListTool,
		Title:  "Threads · Listar conteúdos",
		Effect: chatdomain.EffectRead,
		Description: "Lists the content threads stored in Threads, most recently worked " +
			"on first, with each one's title, status and a short excerpt. Call it " +
			"first when the user refers to a piece of content by name rather than " +
			"by id — it is how you find the id every other Threads tool needs. It " +
			"is also how you answer questions about the body of work: \"o que eu " +
			"tenho em revisão\", \"quais ideias estão paradas\". Filter by status or " +
			"free text instead of listing everything. If more than one result " +
			"plausibly matches what the user meant, ask them which one; do not guess.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"status": {
					Type:        chatdomain.TypeString,
					Description: statusDescription("Optional. Only threads in this state."),
					MaxLength:   20,
				},
				"search": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Matches the title or the body of the content.",
					MaxLength:   200,
				},
				"limit": {
					Type:        chatdomain.TypeInteger,
					Description: "How many to return, 1 to 100. Defaults to 25.",
				},
			},
		},
	}
}

func (t threadList) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}

	filter := ports.ThreadFilter{
		Search: argString(args, "search"),
		Limit:  argInt(args, "limit", 25),
	}
	if raw := strings.TrimSpace(argString(args, "status")); raw != "" {
		status, err := domain.ParseStatus(raw)
		if err != nil {
			return nil, toolError(err)
		}
		filter.Status = &status
	}

	items, total, err := t.svc.ListThreads(ctx, ws, filter)
	if err != nil {
		return nil, toolError(err)
	}

	rows := make([]map[string]any, 0, len(items))
	for _, item := range items {
		rows = append(rows, summary(item))
	}

	out := map[string]any{
		"threads": rows,
		// The unbounded count of what the same filter matches. Returned even
		// when it equals the number of rows, because a model that has to
		// infer completeness from a length will eventually infer wrong.
		"matching_total": total,
	}
	if len(rows) == 0 {
		out["note"] = "no thread matched. Try a broader filter, or call this tool " +
			"with no arguments to see everything in Threads."
	}
	return fit(out, "threads")
}

/* ── threads.thread.get ──────────────────────────────────────────────── */

type threadGet struct{ base }

func (threadGet) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   ThreadGetTool,
		Title:  "Threads · Ver conteúdo",
		Effect: chatdomain.EffectRead,
		Description: "Reads one content thread in full: its title, status and the " +
			"complete current text. Call it before rewriting anything — the stored " +
			"content is the current state of the work, and editing from a summary " +
			"or from what was said several turns ago would discard changes. Use " +
			"threads.thread.list first to find the id.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"thread_id": threadIDProperty,
			},
			Required: []string{"thread_id"},
		},
	}
}

func (t threadGet) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID(argString(args, "thread_id"))
	if err != nil {
		return nil, err
	}
	thread, err := t.svc.GetThread(ctx, ws, id)
	if err != nil {
		return nil, toolError(err)
	}
	return fit(map[string]any{"thread": detail(thread)}, "")
}

/* ── threads.thread.create ───────────────────────────────────────────── */

type threadCreate struct{ base }

func (threadCreate) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:  ThreadCreateTool,
		Title: "Threads · Criar conteúdo",
		// It puts a real row in the user's body of work. Declaring it read
		// to slip past whatever an operator decides write tools deserve
		// would be the module lying about itself in the one field the
		// interface uses to warn a person before they authorize it.
		Effect: chatdomain.EffectWrite,
		// ── What this description has to get right ──────────────────────
		// Three failures, and the wording aims at all of them. A model that
		// treats every passing thought as a create fills the workspace with
		// things the user was only saying out loud. A model that
		// interrogates for a channel and a format before accepting "guarda
		// essa ideia" makes the feature slower than a notes app. And the
		// quiet one: EMBELLISHING — turning one sentence into three
		// paragraphs of invented argument because the field looked empty.
		// A fabricated point is indistinguishable from the user's own once
		// it is stored, and they will read it back believing they wrote it.
		Description: "Creates a new content thread. Use it when the user asks for an " +
			"idea to be kept — \"guarda isso\", \"anota essa ideia\", \"tive uma ideia " +
			"de conteúdo\". Derive a short title from what they said; the title is a " +
			"handle for finding it again, not a headline. Store what the user " +
			"actually said as the content: do NOT add arguments, examples, numbers, " +
			"statistics or personal experiences they did not give you — an invented " +
			"point is indistinguishable from theirs once it is stored. Do NOT create " +
			"just because a topic came up in conversation; wait for the user to ask. " +
			"Before calling this a second time in one conversation, check whether you " +
			"already created that thread — an acknowledgement like \"boa\" or " +
			"\"perfeito\" is not a second request, and neither is a request to CHANGE " +
			"what you just saved, which is threads.thread.update.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"title": {
					Type: chatdomain.TypeString,
					Description: "A short handle for the piece, derived from what the user said: " +
						"\"Microservices cedo demais\". Required.",
					MaxLength: domain.MaxTitle,
				},
				"content": {
					Type: chatdomain.TypeString,
					Description: "The content itself, as it stands now — for a captured idea, " +
						"the user's own thought written plainly. Required.",
					MaxLength: domain.MaxContent,
				},
				"status": {
					Type: chatdomain.TypeString,
					Description: statusDescription("Optional. Only when the user said where this "+
						"already is — a finished post they are about to publish, or one they "+
						"already published.") +
						" Leave it out for something they merely thought of: it then lands in " +
						"'idea', which is where a captured thought belongs.",
					MaxLength: 20,
				},
			},
			Required: []string{"title", "content"},
		},
	}
}

func (t threadCreate) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}

	in := app.CreateInput{
		Title:   argString(args, "title"),
		Content: argString(args, "content"),
	}
	if raw := strings.TrimSpace(argString(args, "status")); raw != "" {
		status, err := domain.ParseStatus(raw)
		if err != nil {
			return nil, toolError(err)
		}
		in.Status = &status
	}

	thread, err := t.svc.CreateThread(ctx, ws, in)
	if err != nil {
		return nil, toolError(err)
	}

	// The id first, because the next thing this model does is very likely
	// `update` on the thread it just made, and it should not have to call
	// `list` to find out what it created.
	return map[string]any{
		"thread_id": thread.ID.String(),
		"title":     thread.Title,
		"status":    string(thread.Status),
		"created":   true,
	}, nil
}

/* ── threads.thread.update ───────────────────────────────────────────── */

type threadUpdate struct{ base }

func (threadUpdate) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   ThreadUpdateTool,
		Title:  "Threads · Atualizar conteúdo",
		Effect: chatdomain.EffectWrite,
		// ── The boundary this description exists to hold ────────────────
		// The user says "deixa mais provocativo". That sentence is an
		// INSTRUCTION TO THE MODEL, and the single most likely failure of
		// this tool is storing it as though it were the content — leaving a
		// thread whose text reads "deixa mais provocativo" where a draft
		// used to be. The work is destroyed and the tool reports success.
		//
		// So the rule is stated first and in the imperative: produce the new
		// version, then send the new version. The intelligence is the
		// agent's; this module stores state.
		//
		// The second failure is subtler: rewriting from memory. Content
		// changes every turn, and a model editing from what it wrote three
		// turns ago silently reverts everything since. Hence the instruction
		// to read first.
		Description: "Updates one content thread: its text, its title, its status, or any " +
			"combination. This is how content EVOLVES — an idea becoming a draft, a " +
			"hook being rewritten, a piece being marked for review. Send only the " +
			"fields you are changing; anything you leave out is kept exactly as it " +
			"is. " +
			"When the user asks for a textual change (\"muda o hook\", \"deixa mais " +
			"provocativo\", \"encurta isso\"), WRITE THE NEW VERSION YOURSELF and send " +
			"the finished text in `content`. Never send their instruction as the " +
			"content — that would replace the work with a note about the work. " +
			"Before rewriting, make sure you are working from the CURRENT stored text: " +
			"use threads.thread.get, because the thread may have moved since you last " +
			"read it. " +
			"Prefer updating the thread you are already working on to creating a new " +
			"one: a rewrite is a new version of the same piece, not a second piece.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"thread_id": threadIDProperty,
				"title": {
					Type: chatdomain.TypeString,
					Description: "Optional. A new handle for the piece. Only send it when the " +
						"subject genuinely changed — rewriting the text does not rename it.",
					MaxLength: domain.MaxTitle,
				},
				"content": {
					Type: chatdomain.TypeString,
					Description: "Optional. The COMPLETE new text, which replaces what is stored. " +
						"Not a diff, not a note about what to change, and not just the part " +
						"you rewrote: send the whole piece as it should now read.",
					MaxLength: domain.MaxContent,
				},
				"status": {
					Type:        chatdomain.TypeString,
					Description: statusDescription("Optional. The state to move this thread to."),
					MaxLength:   20,
				},
			},
			Required: []string{"thread_id"},
		},
	}
}

func (t threadUpdate) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID(argString(args, "thread_id"))
	if err != nil {
		return nil, err
	}

	change := domain.Change{
		Title:   argStringPtr(args, "title"),
		Content: argStringPtr(args, "content"),
	}

	// ── Why an empty content is refused rather than stored ──────────────
	// Because `content` REPLACES, and an empty replacement erases the piece
	// while reporting success. A model sends it by accident — a generation
	// that produced nothing, an argument built from a variable that was
	// never filled — far more often than a user asks for it, and the user
	// who genuinely wants a thread gone has delete and archive. The title
	// needs no such rule: the domain already refuses a blank one.
	if change.Content != nil && strings.TrimSpace(*change.Content) == "" {
		return nil, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
			"content cannot be emptied: sending an empty content would erase the "+
				"piece. Send the complete new text, or use threads.thread.delete if "+
				"the user asked for the thread to be removed.")
	}

	if raw := argStringPtr(args, "status"); raw != nil {
		status, err := domain.ParseStatus(*raw)
		if err != nil {
			return nil, toolError(err)
		}
		change.Status = &status
	}

	result, err := t.svc.UpdateThread(ctx, ws, id, change)
	if err != nil {
		return nil, toolError(err)
	}

	thread := result.Thread
	out := map[string]any{
		"thread_id":  thread.ID.String(),
		"title":      thread.Title,
		"status":     string(thread.Status),
		"updated_at": thread.UpdatedAt.UTC().Format(timeFormat),
		// What actually moved, so the model's confirmation to the user is
		// checkable rather than an assertion: "reescrevi o texto e marquei
		// como review" is verifiable against these three booleans.
		"title_changed":   result.TitleChanged,
		"content_changed": result.ContentChanged,
		"status_changed":  result.StatusChanged,
	}
	if result.StatusChanged {
		out["previous_status"] = string(result.PreviousStatus)
	}
	if result.Unchanged() {
		out["unchanged"] = true
		out["note"] = "this thread already held every value that was sent; nothing was changed."
	}
	return out, nil
}

/* ── threads.thread.delete ───────────────────────────────────────────── */

type threadDelete struct{ base }

func (threadDelete) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   ThreadDeleteTool,
		Title:  "Threads · Remover conteúdo",
		Effect: chatdomain.EffectWrite,
		// ── The distinction this description exists to hold ─────────────
		// Judging content and discarding it are different acts, and a model
		// will conflate them because ordinary language does: "esse texto
		// ficou ruim" sounds like a verdict on whether the row should
		// exist. It is not. It is feedback, and the response to feedback is
		// a rewrite.
		//
		// The asymmetry decides it. Deleting on a bad review destroys work
		// the user spent time on and did not ask to lose; leaving a bad
		// draft in place costs one line in a listing. So the description
		// names the wrong readings explicitly rather than describing the
		// right one and hoping — a negative judgement, a change of mind
		// about publishing, and a piece that went stale are the three
		// things that sound like removal and are not.
		Description: "Removes a content thread. Use it ONLY when the user explicitly asks " +
			"for the thread itself to be removed — \"apaga essa ideia\", \"pode " +
			"excluir\", \"era só um teste\". " +
			"This is NOT how criticism is recorded. \"Esse texto ficou ruim\", \"não " +
			"gostei\", \"está fraco\" and \"esse ângulo não funciona\" are feedback on " +
			"the writing: the answer is a new version through threads.thread.update, " +
			"never a removal. It is also not for a piece the user decided not to " +
			"publish or lost interest in — that is the 'archived' status, which keeps " +
			"the work. " +
			"If more than one thread could be the one they mean, ask which one before " +
			"removing anything: this destroys real work, and removing the wrong " +
			"thread is not something the user can see happening.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"thread_id": threadIDProperty,
			},
			Required: []string{"thread_id"},
		},
	}
}

func (t threadDelete) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID(argString(args, "thread_id"))
	if err != nil {
		return nil, err
	}

	// Read before removing, for two reasons. The result can then name what
	// was removed, so the model acknowledges "apaguei a ideia de
	// microservices" instead of echoing a uuid at the user. And an id
	// belonging to another workspace fails here exactly as it fails in the
	// delete itself — same workspace-scoped query, same not-found — so
	// nothing is learned from the difference.
	thread, err := t.svc.GetThread(ctx, ws, id)
	if err != nil {
		return nil, toolError(err)
	}

	// The same application operation any other surface would call. It is a
	// SOFT delete: the row stops appearing and the text is not destroyed.
	// There is no second removal semantics for agents.
	if err := t.svc.DeleteThread(ctx, ws, id); err != nil {
		return nil, toolError(err)
	}

	return map[string]any{
		"thread_id": thread.ID.String(),
		"title":     thread.Title,
		"deleted":   true,
	}, nil
}
